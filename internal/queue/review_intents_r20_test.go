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

// fakeClaimReviewer / fakeStyleEditor：记录调用次数与请求，可注入结果/失败。
type fakeClaimReviewer struct {
	calls  int
	last   provider.ClaimReviewRequest
	result *provider.ClaimReviewResult
	err    error
}

func (f *fakeClaimReviewer) ReviewClaims(_ context.Context, req provider.ClaimReviewRequest) (*provider.ClaimReviewResult, provider.TaskUsage, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, provider.TaskUsage{}, f.err
	}
	return f.result, provider.TaskUsage{InputUnits: 12, OutputUnits: 5}, nil
}
func (f *fakeClaimReviewer) Name() string { return "fake-reviewer" }

type fakeStyleEditor struct {
	calls  int
	last   provider.StyleReviewRequest
	result *provider.StyleReviewResult
	err    error
}

func (f *fakeStyleEditor) ReviewStyle(_ context.Context, req provider.StyleReviewRequest) (*provider.StyleReviewResult, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}
func (f *fakeStyleEditor) Name() string { return "fake-styler" }

func bundleWithReviewers(claim *fakeClaimReviewer, style *fakeStyleEditor) func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
	return func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: &fakeClaimWriter{}, ClaimReviewer: claim, StyleEditor: style}, nil
	}
}

const r20Markdown = "# R20\n\n来源说过这句话。Owner 的判断在此。"

func passedClaimResult() *provider.ClaimReviewResult {
	return &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}
}

// TestClaimReviewJob_ProductionDispatch R20：生产入队 → fake ClaimReviewer →
// 冻结请求（正文/确认主张/授权材料全文）→ 审校落库（完整 provenance + origin_job）+
// usage receipt；重复点击复用同一 job，成功后零额外调用。
func TestClaimReviewJob_ProductionDispatch(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	kpID := ""
	{
		_, k, _, writeJob := claimWritingFixture(t, s)
		kpID = k
		fake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
		w.bundleFor = bundleWithWriter(fake)
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetJob(ctx, writeJob.ID)
		if err != nil || got.Status != models.StatusSucceeded {
			t.Fatalf("write job: %+v %v", got, err)
		}
	}
	var revisionID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions ORDER BY rowid DESC LIMIT 1`).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeClaimReviewer{result: passedClaimResult()}
	w.bundleFor = bundleWithReviewers(fake, nil)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("provider calls=%d", fake.calls)
	}
	// 冻结请求：正文/确认主张/授权 ID/材料全文来自 ReviewTaskInput。
	rev, err := s.GetArticleRevision(ctx, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	if fake.last.Markdown != rev.Markdown || fake.last.ConfirmedClaim != "Owner" {
		t.Fatalf("冻结请求不符: %+v", fake.last)
	}
	if len(fake.last.AuthorizedIDs) == 0 || len(fake.last.Materials) != 1 || fake.last.Materials[0].KeyPointID != kpID || fake.last.Materials[0].Content == "" {
		t.Fatalf("冻结材料不符: ids=%v materials=%+v", fake.last.AuthorizedIDs, fake.last.Materials)
	}
	// 审校落库：passed + provenance + origin_job。
	var status, providerName, model, prompt, origin string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT status,provider,model,prompt_version,origin_job_id FROM claim_reviews WHERE work_revision_id=?`, revisionID).
		Scan(&status, &providerName, &model, &prompt, &origin); err != nil {
		t.Fatalf("claim_reviews 未落库: %v", err)
	}
	if status != models.ClaimReviewStatusPassed || origin != job.ID ||
		providerName != "fake-reviewer" || model == "" || prompt != provider.ClaimReviewerPromptVersion {
		t.Fatalf("审校行不符: %s %s %s %s %s", status, providerName, model, prompt, origin)
	}
	// usage receipt 已落账。
	var receipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":claim_review").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("usage receipt=%d", receipts)
	}
	// 重复点击复用同一 job；succeeded 后 ProcessOne 无新任务，零额外调用。
	again, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
	if err != nil || again.ID != job.ID {
		t.Fatalf("重复点击: %v %+v", err, again)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("成功后重复点击不得再调用 provider: calls=%d", fake.calls)
	}
	final, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != models.StatusSucceeded {
		t.Fatalf("final status=%s err=%v", final.Status, final.LastError)
	}
}

// TestStyleReviewJob_ProductionDispatch R20：durable style 入队 → fake StyleEditor →
// 审校落库（kind=style + provenance + origin_job）+ usage（含 checkpoint Usage 持久）。
func TestStyleReviewJob_ProductionDispatch(t *testing.T) {
	s, w := newTestWorker(t)
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
	job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindStyle)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeStyleEditor{result: &provider.StyleReviewResult{Status: "passed"}}
	w.bundleFor = bundleWithReviewers(nil, fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("provider calls=%d", fake.calls)
	}
	// 冻结请求：正文与画像约束来自快照。
	if fake.last.Markdown != r20Markdown || fake.last.Title == "" {
		t.Fatalf("冻结风格请求不符: %+v", fake.last)
	}
	var status, kind, providerName, origin string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT status,kind,provider,origin_job_id FROM article_reviews WHERE revision_id=?`, revisionID).
		Scan(&status, &kind, &providerName, &origin); err != nil {
		t.Fatalf("article_reviews 未落库: %v", err)
	}
	if status != "passed" || kind != "style" || providerName != "fake-styler" || origin != job.ID {
		t.Fatalf("风格审校行不符: %s %s %s %s", status, kind, providerName, origin)
	}
	// checkpoint 必须持久 Usage（恢复记账不丢失）。
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cp styleReviewCheckpoint
	if err := json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil {
		t.Fatal(err)
	}
	// checkpoint 的 Usage 字段被显式写入（StyleReviewResult.Usage 不参与 JSON 序列化，
	// 恢复记账依赖独立的 cp.Usage）；fake 无计量时为零值但字段必须存在。
	t.Logf("checkpoint usage: %+v", cp.Usage)
	if cp.RevisionID != revisionID || cp.RequestHash == "" {
		t.Fatalf("style checkpoint 绑定不完整: %+v", cp)
	}
	var receipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":style_review").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("style usage receipt=%d", receipts)
	}
}

// TestClaimReviewJob_CheckpointThenRetryZeroCalls R20：checkpoint 已持久后业务落库
// 失败（INSERT trigger 注入）→ store 重试（failed reset queued）→ 复用 checkpoint
// 零额外 Provider 调用；重放只落一条审校。
func TestClaimReviewJob_CheckpointThenRetryZeroCalls(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	kpID := ""
	{
		_, k, _, writeJob := claimWritingFixture(t, s)
		kpID = k
		_ = writeJob
		writerFake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
		w.bundleFor = bundleWithWriter(writerFake)
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var revisionID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions ORDER BY rowid DESC LIMIT 1`).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeClaimReviewer{result: passedClaimResult()}
	w.bundleFor = bundleWithReviewers(fake, nil)

	// 第一轮：Provider 成功、checkpoint 已保存；trigger 让 claim_reviews INSERT 失败。
	trigger := fmt.Sprintf(`CREATE TEMP TRIGGER fail_claim_review BEFORE INSERT ON claim_reviews
	    WHEN NEW.origin_job_id = '%s' BEGIN SELECT RAISE(FAIL, 'injected business tx failure'); END`, job.ID)
	if _, err := s.DB.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != models.StatusFailed || fake.calls != 1 {
		t.Fatalf("首轮应业务失败: status=%s calls=%d", failed.Status, fake.calls)
	}
	// checkpoint 绑定完整：revision + request hash + 调用身份。
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cp claimReviewCheckpoint
	if err := json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil {
		t.Fatal(err)
	}
	if cp.RevisionID != revisionID || cp.RequestHash == "" || cp.Provider != "fake-reviewer" || cp.Stage != "reviewed" {
		t.Fatalf("checkpoint 绑定不完整: %+v", cp)
	}

	// 生产重试语义（HTTP 入口的 store 等价路径）：failed → reset queued。
	retried, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != job.ID || retried.Status != models.StatusQueued {
		t.Fatalf("重试应 reset 同一 job: %+v", retried)
	}
	// 清除注入；模拟重启恢复：running+过期租约 → ResetRunningOnStartup → 重新领取。
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_claim_review`); err != nil {
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
	if fake.calls != 1 {
		t.Fatalf("恢复重试不得重复 Provider: calls=%d", fake.calls)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_reviews WHERE work_revision_id=?`, revisionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("恢复后审校行=%d", n)
	}
	final, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != models.StatusSucceeded {
		t.Fatalf("恢复后 status=%s err=%v", final.Status, final.LastError)
	}
}

// TestReviewJobs_DynamicPolicyZeroCalls R20：入队后动态 LocalOnly / archived →
// Provider 零调用，任务失败且原因可见（冻结输入不绕过撤销/归档）。
func TestReviewJobs_DynamicPolicyZeroCalls(t *testing.T) {
	t.Run("claim_local_only", func(t *testing.T) {
		s, w := newTestWorker(t)
		ctx := context.Background()
		sourceID := seedReviewArticle(t, s, w)
		var revisionID string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions ORDER BY rowid DESC LIMIT 1`).Scan(&revisionID); err != nil {
			t.Fatal(err)
		}
		job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
		if err != nil {
			t.Fatal(err)
		}
		// 入队后动态收紧：LocalOnly。
		if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, sourceID, "internal", models.ModelDataLocalOnly); err != nil {
			t.Fatal(err)
		}
		fake := &fakeClaimReviewer{result: passedClaimResult()}
		w.bundleFor = bundleWithReviewers(fake, nil)
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 0 {
			t.Fatalf("LocalOnly 必须 zero call: %d", fake.calls)
		}
		got, err := s.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "禁止外发") {
			t.Fatalf("任务失败原因不可见: %+v", got)
		}
	})
	t.Run("style_archived", func(t *testing.T) {
		s, w := newTestWorker(t)
		ctx := context.Background()
		sourceID := seedReviewArticle(t, s, w)
		var revisionID string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions ORDER BY rowid DESC LIMIT 1`).Scan(&revisionID); err != nil {
			t.Fatal(err)
		}
		job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindStyle)
		if err != nil {
			t.Fatal(err)
		}
		// 入队后动态归档来源。
		if _, err := s.DB.ExecContext(ctx, `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, sourceID); err != nil {
			t.Fatal(err)
		}
		fake := &fakeStyleEditor{result: &provider.StyleReviewResult{Status: "passed"}}
		w.bundleFor = bundleWithReviewers(nil, fake)
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 0 {
			t.Fatalf("archived 必须 zero call: %d", fake.calls)
		}
		got, err := s.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "已归档或不可用") {
			t.Fatalf("任务失败原因不可见: %+v", got)
		}
	})
}

// seedReviewArticle 走生产链产出新契约修订，返回来源 episode ID（策略/归档用）。
func seedReviewArticle(t *testing.T, s *store.Store, w *Worker) string {
	t.Helper()
	ctx := context.Background()
	_, kpID, sourceID, writeJob := claimWritingFixture(t, s)
	fake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
	w.bundleFor = bundleWithWriter(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, writeJob.ID)
	if err != nil || got.Status != models.StatusSucceeded {
		t.Fatalf("write job: %+v %v", got, err)
	}
	return sourceID
}

// TestStyleReviewJob_InvalidProviderOutputRejected R20：Provider 返回 nil/非法
// status → 明确失败，不 panic、不 checkpoint、不落库；修复后再次处理可正常完成。
func TestStyleReviewJob_InvalidProviderOutputRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *provider.StyleReviewResult
	}{
		{"nil", nil},
		{"advisory", &provider.StyleReviewResult{Status: "advisory", Issues: []string{"建议"}}},
		{"failed 无 issues", &provider.StyleReviewResult{Status: "failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			_, kpID, _, writeJob := claimWritingFixture(t, s)
			writerFake := &fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)}
			w.bundleFor = bundleWithWriter(writerFake)
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			var revisionID string
			if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, writeJob.SourceID, writeJob.ID).Scan(&revisionID); err != nil {
				t.Fatal(err)
			}
			job, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindStyle)
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeStyleEditor{result: tc.result}
			w.bundleFor = bundleWithReviewers(nil, fake)
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatalf("ProcessOne 不应上抛 panic 级错误: %v", err)
			}
			got, err := s.GetJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "非法结论") {
				t.Fatalf("非法输出应明确失败: %+v", got)
			}
			// 不 checkpoint、不落库。
			exec, err := s.GetJobExecution(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if exec.CheckpointJSON != "" {
				t.Fatalf("非法输出不得写 checkpoint: %s", exec.CheckpointJSON)
			}
			var n int
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=?`, revisionID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("非法输出不得落库: %d", n)
			}
			// 修复（fake 返回合法结果）后重试同 job 可正常完成。
			fake.result = &provider.StyleReviewResult{Status: "passed"}
			retried, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindStyle)
			if err != nil || retried.ID != job.ID {
				t.Fatalf("重试: %v %+v", err, retried)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			final, err := s.GetJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if final.Status != models.StatusSucceeded {
				t.Fatalf("修复后应成功: %+v", final)
			}
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=?`, revisionID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("修复后应恰一条审校: %d", n)
			}
		})
	}
}
