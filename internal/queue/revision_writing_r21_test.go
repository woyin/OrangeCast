package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// fakeDualWriter 分别记录普通写作与修订调用（防止再次接错接口）。
type fakeDualWriter struct {
	writeCalls    int
	revisionCalls int
	lastRevision  provider.ClaimAwareWritingRequest
	result        *provider.ClaimAwareWritingResult
	err           error
}

func (f *fakeDualWriter) WriteArticle(ctx context.Context, req provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	f.writeCalls++
	return &provider.ArticleWritingResult{Title: "v1", Markdown: "# v1"}, nil
}

func (f *fakeDualWriter) WriteArticleWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	f.writeCalls++
	if f.err != nil {
		return nil, provider.TaskUsage{}, f.err
	}
	return f.result, provider.TaskUsage{InputUnits: 10, OutputUnits: 4}, nil
}

func (f *fakeDualWriter) WriteArticleRevisionWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	f.revisionCalls++
	f.lastRevision = req
	if f.err != nil {
		return nil, provider.TaskUsage{}, f.err
	}
	return f.result, provider.TaskUsage{InputUnits: 10, OutputUnits: 4}, nil
}

func (f *fakeDualWriter) Name() string { return "fake-dual-writer" }

// bundleWithWriterAny 注入任意 ArticleWriterProvider（修订测试用 dual fake）。
func bundleWithWriterAny(w provider.ArticleWriterProvider) func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
	return func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: w}, nil
	}
}

// revisionFixture 走生产链产出 base 修订并制造 durable 审校反馈（claim failed findings）。
func revisionFixture(t *testing.T, s *store.Store, w *Worker) (draftID string, baseRevisionID string) {
	t.Helper()
	ctx := context.Background()
	_, kpID, _, writeJob := claimWritingFixture(t, s)
	writerFake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
	w.bundleFor = bundleWithWriter(writerFake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	written, err := s.GetJob(ctx, writeJob.ID)
	if err != nil || written.Status != models.StatusSucceeded {
		t.Fatalf("write job: %+v %v", written, err)
	}
	var revisionID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, writeJob.SourceID, writeJob.ID).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	// durable 反馈：claim failed（findings）。
	claimJob, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, claimJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveClaimReviewOutput(ctx, claimJob.ID, &provider.ClaimReviewResult{
		Status:   provider.ClaimReviewFailed,
		Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
	}, "p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, claimJob.ID); err != nil {
		t.Fatal(err)
	}
	return writeJob.SourceID, revisionID
}

// revisionAIResult 依据 base 修订生成完整新正文 + 完整 ClaimMap 的合法输出。
func revisionAIResult() *provider.ClaimAwareWritingResult {
	return &provider.ClaimAwareWritingResult{
		Title:    "R21 AI 修订",
		Markdown: "# R21 AI\n\n来源说过这句话。（修订后）Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			// SourceTitle 与 fixture 原 ClaimMap 一致（身份保持校验要求）。
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{"kp"}, SourceTitle: "来源"}, // kp 由 caller 注入
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
}

// TestClaimRevisionJob_ProductionDispatch R21：生产入队 → fake Writer 修订模式 →
// 冻结请求（ExistingMarkdown/反馈/原 ClaimMap/授权材料）→ 原子落库；重复点击同 job。
func TestClaimRevisionJob_ProductionDispatch(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	draftID, baseID := revisionFixture(t, s, w)
	_, kp, sourceTitle, citations, _ := fixtureSourceIdentity(t, s, draftID)
	job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDualWriter{result: withIdentity(revisionAIResult(), kp, sourceTitle, citations)}
	w.bundleFor = bundleWithWriterAny(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.revisionCalls != 1 {
		t.Fatalf("revision calls=%d", fake.revisionCalls)
	}
	if fake.writeCalls != 0 {
		t.Fatalf("普通写作不得被修订任务调用: %d", fake.writeCalls)
	}
	// 编译期断言：修订调用走专用接口（fake 实现两个方法，worker 只允许调修订方法）。
	var _ interface {
		WriteArticleRevisionWithClaims(context.Context, provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error)
		Name() string
	} = (*fakeDualWriter)(nil)
	// 冻结请求断言。
	if fake.lastRevision.ExistingMarkdown == "" || fake.lastRevision.ConfirmedClaim != "Owner" || len(fake.lastRevision.RevisionFeedback) == 0 || len(fake.lastRevision.ExistingClaimMap) != 2 {
		t.Fatalf("修订请求冻结不符: %+v", fake.lastRevision)
	}
	if len(fake.lastRevision.Materials) == 0 || fake.lastRevision.Materials[0].KeyPointID != kp {
		t.Fatalf("授权材料不符: %+v", fake.lastRevision.Materials)
	}
	// 新修订落库。
	rev, err := s.GetArticleRevision(ctx, func() string {
		var id string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}())
	if err != nil {
		t.Fatal(err)
	}
	if rev.Origin != "ai_edit" || rev.OriginJobID != job.ID {
		t.Fatalf("revision=%+v", rev)
	}
	entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ClaimMap: %v %+v", err, entries)
	}
	// job result complete + usage receipt。
	var state string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(result_state,'') FROM processing_jobs WHERE id=?`, job.ID).Scan(&state); err != nil || state != models.JobResultComplete {
		t.Fatalf("result_state=%s err=%v", state, err)
	}
	// 重复点击复用同 job，成功后零额外调用。
	again, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
	if err != nil || again.ID != job.ID {
		t.Fatalf("重复点击: %v %+v", err, again)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.revisionCalls != 1 {
		t.Fatalf("成功后零额外调用: %d", fake.revisionCalls)
	}
}

// TestClaimRevisionJob_CheckpointRecovery R21：checkpoint 后业务失败 → 重试+重启
// 恢复零额外 Provider 调用、单修订（子测试 A）；篡改 checkpoint 不静默复用（子测试 B）。
func TestClaimRevisionJob_CheckpointRecovery(t *testing.T) {
	t.Run("zero_extra_calls", func(t *testing.T) {
		s, w := newTestWorker(t)
		ctx := context.Background()
		draftID, baseID := revisionFixture(t, s, w)
		_, kp, sourceTitle, citations, _ := fixtureSourceIdentity(t, s, draftID)
		job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
		if err != nil {
			t.Fatal(err)
		}
		fake := &fakeDualWriter{result: withIdentity(revisionAIResult(), kp, sourceTitle, citations)}
		w.bundleFor = bundleWithWriterAny(fake)
		trigger := fmt.Sprintf(`CREATE TEMP TRIGGER fail_ai_rev BEFORE INSERT ON article_revisions
	        WHEN NEW.origin_job_id = '%s' BEGIN SELECT RAISE(FAIL, 'injected business tx failure'); END`, job.ID)
		if _, err := s.DB.ExecContext(ctx, trigger); err != nil {
			t.Fatal(err)
		}
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		failed, _ := s.GetJob(ctx, job.ID)
		if failed.Status != models.StatusFailed || fake.revisionCalls != 1 {
			t.Fatalf("首轮应业务失败: status=%s calls=%d", failed.Status, fake.revisionCalls)
		}
		exec, _ := s.GetJobExecution(ctx, job.ID)
		var cp revisionWritingCheckpoint
		if err := json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil {
			t.Fatal(err)
		}
		if cp.BaseRevisionID != baseID || cp.RequestHash == "" || cp.Stage != "written" || cp.Provider != "fake-dual-writer" {
			t.Fatalf("checkpoint 绑定不完整: %+v", cp)
		}
		if _, err := s.EnqueueClaimRevisionForRevision(ctx, baseID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_ai_rev`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='running', lease_until=datetime('now','-10 minutes') WHERE id=?`, job.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetRunningOnStartup(ctx); err != nil {
			t.Fatal(err)
		}
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if fake.revisionCalls != 1 {
			t.Fatalf("恢复重试不得重复 Provider: calls=%d", fake.revisionCalls)
		}
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("恢复后修订数=%d err=%v", n, err)
		}
	})

	t.Run("tampered_checkpoint_not_reused", func(t *testing.T) {
		s, w := newTestWorker(t)
		ctx := context.Background()
		draftID, baseID := revisionFixture(t, s, w)
		_, kp, sourceTitle, citations, _ := fixtureSourceIdentity(t, s, draftID)
		job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
		if err != nil {
			t.Fatal(err)
		}
		fake := &fakeDualWriter{result: withIdentity(revisionAIResult(), kp, sourceTitle, citations)}
		w.bundleFor = bundleWithWriterAny(fake)
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if fake.revisionCalls != 1 {
			t.Fatalf("首轮 calls=%d", fake.revisionCalls)
		}
		// 篡改 checkpoint 的 request hash 并再次调度（重放语义：同 job 再次执行）。
		if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json=json_set(checkpoint_json,'$.request_hash','tampered') WHERE id=?`, job.ID); err != nil {
			t.Fatal(err)
		}
		// 使业务落库失败一次以迫使 worker 重新走 checkpoint 判定：
		// 直接删除已落 ClaimMap 与修订后重置任务状态。
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM claim_map_entries WHERE revision_id=(SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?)`, draftID, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM claim_maps WHERE work_revision_id=(SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?)`, draftID, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE article_drafts SET current_revision_id=? WHERE id=?`, baseID, draftID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='running', lease_until=datetime('now','-10 minutes'), result_state='' WHERE id=?`, job.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetRunningOnStartup(ctx); err != nil {
			t.Fatal(err)
		}
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if fake.revisionCalls != 2 {
			t.Fatalf("篡改 checkpoint 必须重新调用: calls=%d", fake.revisionCalls)
		}
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("重新调用后修订数=%d err=%v", n, err)
		}
	})
}

// TestClaimRevisionJob_StaleBaseNoOverwrite R21：base 被抢先推进 → 保存冲突、
// 不覆盖 current、不留 AI 修订。
func TestClaimRevisionJob_StaleBaseNoOverwrite(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	draftID, baseID := revisionFixture(t, s, w)
	_, kp, sourceTitle, citations, _ := fixtureSourceIdentity(t, s, draftID)
	job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
	if err != nil {
		t.Fatal(err)
	}
	// 入队后 Owner 抢先推进 current。
	if _, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, baseID, "v2", "# v2\n\nOwner 抢先改写。"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDualWriter{result: withIdentity(revisionAIResult(), kp, sourceTitle, citations)}
	w.bundleFor = bundleWithWriterAny(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "过期") {
		t.Fatalf("base 过期必须可见失败: %+v", got)
	}
	var current string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current == job.ID {
		t.Fatal("current 不得指向 AI 修订")
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("冲突不得留下 AI 修订: %d %v", n, err)
	}
}

// TestClaimRevisionJob_DynamicPolicyZeroCalls R21：入队后 LocalOnly → 零调用。
func TestClaimRevisionJob_DynamicPolicyZeroCalls(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	_, kpID, sourceID, writeJob := claimWritingFixture(t, s)
	writerFake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
	w.bundleFor = bundleWithWriter(writerFake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var baseID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, writeJob.SourceID, writeJob.ID).Scan(&baseID); err != nil {
		t.Fatal(err)
	}
	claimJob, err := s.EnqueueRevisionReview(ctx, baseID, store.ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, claimJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveClaimReviewOutput(ctx, claimJob.ID, &provider.ClaimReviewResult{
		Status:   provider.ClaimReviewFailed,
		Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
	}, "p", "m"); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, sourceID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	_, kp, sourceTitle, citations, _ := fixtureSourceIdentity(t, s, writeJob.SourceID)
	fake := &fakeDualWriter{result: withIdentity(revisionAIResult(), kp, sourceTitle, citations)}
	w.bundleFor = bundleWithWriterAny(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.revisionCalls != 0 {
		t.Fatalf("LocalOnly 必须 zero call: %d", fake.revisionCalls)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "禁止外发") {
		t.Fatalf("失败原因不可见: %+v", got)
	}
}

// TestClaimRevisionJob_InvalidOutputRejected R21：未授权材料输出拒绝且不留修订。
func TestClaimRevisionJob_InvalidOutputRejected(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	draftID, baseID := revisionFixture(t, s, w)
	job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
	if err != nil {
		t.Fatal(err)
	}
	bad := revisionAIResult()
	bad.ClaimMap[0].MaterialIDs = []string{"ghost-kp"}
	fake := &fakeClaimWriter{result: bad}
	w.bundleFor = bundleWithWriterAny(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("非法输出应失败: %+v", got)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("非法输出不得落库: %d %v", n, err)
	}
}

func storeKpID(t *testing.T, s *store.Store, draftID string) string {
	t.Helper()
	_, kp, _, _, _ := fixtureSourceIdentity(t, s, draftID)
	return kp
}

// fixtureSourceIdentity 读取 fixture 的 source claim 身份（kp/materials/title/citations）。
func fixtureSourceIdentity(t *testing.T, s *store.Store, draftID string) (string, string, string, []string, []string) {
	t.Helper()
	var raw, title, citations string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT material_ids_json, source_title, citation_refs_json FROM claim_map_entries WHERE draft_id=? AND claim_kind='source_claim' LIMIT 1`, draftID).Scan(&raw, &title, &citations); err != nil {
		t.Fatal(err)
	}
	var ids, refs []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(citations), &refs)
	if len(ids) == 0 {
		t.Fatal("fixture 缺少授权材料")
	}
	return ids[0], ids[0], title, refs, nil
}

// withIdentity 用 fixture 原 source 身份填充修订输出（身份保持校验要求完整一致）。
func withIdentity(r *provider.ClaimAwareWritingResult, kp, sourceTitle string, citations []string) *provider.ClaimAwareWritingResult {
	for i := range r.ClaimMap {
		if r.ClaimMap[i].ClaimKind == provider.ClaimSource {
			r.ClaimMap[i].MaterialIDs = []string{kp}
			r.ClaimMap[i].SourceTitle = sourceTitle
			r.ClaimMap[i].CitationRefs = citations
		}
	}
	return r
}

// withKp 兼容旧调用（仅材料 ID）。
func withKp(r *provider.ClaimAwareWritingResult, kp string) *provider.ClaimAwareWritingResult {
	for i := range r.ClaimMap {
		if r.ClaimMap[i].ClaimKind == provider.ClaimSource {
			r.ClaimMap[i].MaterialIDs = []string{kp}
		}
	}
	return r
}
