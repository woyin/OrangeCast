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

// fakeClaimWriter v2 写作 fake：记录调用次数，可注入失败与非法输出。
type fakeClaimWriter struct {
	calls  int
	last   provider.ClaimAwareWritingRequest
	result *provider.ClaimAwareWritingResult
	err    error
}

func (f *fakeClaimWriter) WriteArticleWithClaims(_ context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, provider.TaskUsage{}, f.err
	}
	return f.result, provider.TaskUsage{InputUnits: 10, OutputUnits: 4}, nil
}

// WriteArticle 是 bundle.Writer 字段要求的 v1 接口；v2 路径不应调用。
func (f *fakeClaimWriter) WriteArticle(context.Context, provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	return nil, fmt.Errorf("v1 WriteArticle 不应被 v2 写作任务调用")
}
func (f *fakeClaimWriter) Name() string { return "fake-writer" }

// claimWritingFixture 在 curatorFixture（真实 episode/keypoint/Brief 草案）之上，
// 走生产 CAS revision + 精确确认 + 持久链接路径，然后走生产入队，返回 job 身份。
func claimWritingFixture(t *testing.T, s *store.Store) (*models.CreationBrief, string, string, *models.ProcessingJob) {
	t.Helper()
	ctx := context.Background()
	b, p, kpid, sourceID := curatorFixture(t, s)
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID,
		Kind: "owner_reflection", Content: "这是 Owner 对材料的个人理解",
		CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.SaveCreationSelection(ctx, &models.CreationSelection{
		EditorialProfileID: p.EditorialProfileID, Title: "写作素材",
		MaterialIDs: []string{kpid}, NoteIDs: []string{note.ID}, Status: models.SelectionConfirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	selectionsJSON, _ := json.Marshal([]string{selection.ID})
	session, err := s.CreateIdeationSession(ctx, models.IdeationSession{
		EditorialProfileID: p.EditorialProfileID, Intent: "形成文章",
		ConstraintsJSON: "{}", SelectionsJSON: string(selectionsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE creation_proposals SET ideation_session_id=? WHERE id=?`, session.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	kp, err := s.GetKeyPoint(ctx, kpid)
	if err != nil {
		t.Fatal(err)
	}
	target := 1800
	snapshot, _ := json.Marshal(map[string]any{"provider": "test", "materials": []provider.ArticleMaterial{{
		SourceType: "episode", CardVersion: kp.CardVersion, KeyPointID: kp.ID, SourceID: kp.SourceID,
		SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description,
		Citations: []string{"seg-0001"},
	}}})
	base := b.CurrentVersion
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, base, models.CreationBriefRevision{
		OwnerClaim: p.OwnerClaim, Outline: "# 提纲", MaterialPlanJSON: `{"selected":["` + kpid + `"]}`,
		TargetLength:         &target,
		CuratorPromptVersion: "curator-v1", CuratorInputSnapshotJSON: string(snapshot),
	}); err != nil {
		t.Fatalf("CAS revision: %v", err)
	}
	if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(ctx, b.ID, base+1, "v2"); err != nil {
		t.Fatalf("confirm+link: %v", err)
	}
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return b, kpid, sourceID, job
}

func goodClaimWritingResult(markdown, kpid string) *provider.ClaimAwareWritingResult {
	return &provider.ClaimAwareWritingResult{
		Title:    "R19 标题",
		Markdown: markdown,
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpid}, SourceTitle: "来源", CitationRefs: []string{"seg-0001"}},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
}

func bundleWithWriter(w *fakeClaimWriter) func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
	return func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: w}, nil
	}
}

const claimWritingMarkdown = "# R19\n\n来源说过这句话。Owner 的判断在此。"

// TestClaimWritingJob_ProductionDispatch R19：真实入队 → fake Writer 非空正文 →
// 真实 revision + 双 ClaimMap 表（真 revision ID）+ job complete result 原子落库。
func TestClaimWritingJob_ProductionDispatch(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	b, kpid, _, job := claimWritingFixture(t, s)
	fake := &fakeClaimWriter{result: goodClaimWritingResult(claimWritingMarkdown, kpid)}
	w.bundleFor = bundleWithWriter(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("provider calls=%d", fake.calls)
	}
	// Gap4：冻结 target_length 必须传入 Provider 请求。
	if fake.last.TargetLength == nil || *fake.last.TargetLength != 1800 {
		t.Fatalf("TargetLength=%v", fake.last.TargetLength)
	}
	if len(fake.last.OwnerNotes) != 1 || fake.last.OwnerNotes[0].Content != "这是 Owner 对材料的个人理解" ||
		fake.last.OwnerNotes[0].Revision != 1 || fake.last.OwnerNotes[0].SourceID == "" {
		t.Fatalf("OwnerNote 冻结身份未送入 Writer: %+v", fake.last.OwnerNotes)
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusSucceeded {
		t.Fatalf("job status=%s err=%v", got.Status, got.LastError)
	}
	var revisionID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, job.SourceID, job.ID).Scan(&revisionID); err != nil {
		t.Fatalf("revision 未落库: %v", err)
	}
	rev, err := s.GetArticleRevision(ctx, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Version != 1 || rev.Markdown != claimWritingMarkdown || rev.Title != "R19 标题" || rev.Origin != "writer" {
		t.Fatalf("revision=%+v", rev)
	}
	draft, err := s.GetArticleDraft(ctx, job.SourceID)
	if err != nil || draft.CurrentRevisionID == nil || *draft.CurrentRevisionID != revisionID || draft.Status != "reviewing" {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
	var cmCount, entryCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_maps WHERE work_revision_id=?`, revisionID).Scan(&cmCount)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_map_entries WHERE revision_id=?`, revisionID).Scan(&entryCount)
	if cmCount != 2 || entryCount != 2 {
		t.Fatalf("双表映射: claim_maps=%d entries=%d", cmCount, entryCount)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("result_state=%q", exec.ResultState)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(exec.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result["revision_id"] != revisionID || result["draft_id"] != job.SourceID ||
		result["creation_brief_id"] != b.ID || result["claim_count"] != float64(2) ||
		result["url"] != "/workbench/drafts/"+job.SourceID || result["title"] != "R19 标题" {
		t.Fatalf("result=%+v", result)
	}
	// Gap2：result 桥接身份必须为非空字符串且与冻结 input 逐字段一致。
	var frozen store.ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &frozen); err != nil {
		t.Fatal(err)
	}
	assertNonEmptyString := func(key string) string {
		t.Helper()
		v, ok := result[key].(string)
		if !ok || v == "" {
			t.Fatalf("result[%s] 缺失或非非空字符串: %+v", key, result)
		}
		return v
	}
	if got := assertNonEmptyString("creation_article_link_id"); got != frozen.CreationArticleLinkID {
		t.Fatalf("link id 不符: %s != %s", got, frozen.CreationArticleLinkID)
	}
	if got := assertNonEmptyString("article_proposal_id"); got != frozen.ArticleProposalID {
		t.Fatalf("article proposal id 不符: %s != %s", got, frozen.ArticleProposalID)
	}
	if got := assertNonEmptyString("article_brief_id"); got != frozen.ArticleBriefID {
		t.Fatalf("article brief id 不符: %s != %s", got, frozen.ArticleBriefID)
	}
	// Gap1：持久 checkpoint 必须完整（stage/result/usage/调用身份）。
	execRaw, _ := s.GetJobExecution(ctx, job.ID)
	var cp claimWritingCheckpoint
	if err := json.Unmarshal([]byte(execRaw.CheckpointJSON), &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Stage != "written" || cp.Result == nil || cp.Provider != "fake-writer" ||
		cp.Model == "" || cp.PromptVersion != provider.ClaimWriterPromptVersion {
		t.Fatalf("checkpoint 元数据不完整: %+v", cp)
	}
	var receipts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":claim_writing").Scan(&receipts)
	if receipts != 1 {
		t.Fatalf("receipts=%d", receipts)
	}
	// 重复执行（恢复语义）：结果已持久 → 直接复用，不重复调用、不产生第二修订。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("恢复后 provider calls=%d", fake.calls)
	}
	var revCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, job.SourceID).Scan(&revCount)
	if revCount != 1 {
		t.Fatalf("修订数=%d", revCount)
	}
}

func TestClaimWritingJob_RejectsOwnerNoteEditedAfterEnqueue(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	_, kpid, _, job := claimWritingFixture(t, s)
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input store.ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.OwnerNotes) != 1 {
		t.Fatalf("OwnerNote 快照=%+v", input.OwnerNotes)
	}
	note := input.OwnerNotes[0]
	if _, err := s.UpdateOwnerNote(ctx, note.ID, "入队后被编辑", "[]", "[]", note.Revision); err != nil {
		t.Fatal(err)
	}
	fake := &fakeClaimWriter{result: goodClaimWritingResult(claimWritingMarkdown, kpid)}
	w.bundleFor = bundleWithWriter(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("笔记 revision 漂移必须在 Provider 前拦截: calls=%d", fake.calls)
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "笔记已修改") {
		t.Fatalf("job=%+v", got)
	}
}

// TestClaimWritingJob_CheckpointRecovery R19：checkpoint 已含完整 result/usage →
// 恢复不重复 Provider；重放 receipt/修订都唯一。
func TestClaimWritingJob_CheckpointRecovery(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	_, kpid, _, job := claimWritingFixture(t, s)
	result := goodClaimWritingResult(claimWritingMarkdown, kpid)
	cp, _ := json.Marshal(claimWritingCheckpoint{
		Stage: "written", Result: result, Usage: provider.TaskUsage{InputUnits: 10, OutputUnits: 4},
		Provider: "fake-writer", Model: provider.EffectiveModel("groq", "", string(models.JobClaimWriting)), PromptVersion: provider.ClaimWriterPromptVersion,
	})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cp)); err != nil {
		t.Fatal(err)
	}
	fake := &fakeClaimWriter{result: result}
	w.bundleFor = bundleWithWriter(fake)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("checkpoint 恢复不得调用 provider: %d", fake.calls)
	}
	var revCount, receipts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, job.SourceID, job.ID).Scan(&revCount)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":claim_writing").Scan(&receipts)
	if revCount != 1 || receipts != 1 {
		t.Fatalf("rev=%d receipts=%d", revCount, receipts)
	}
}

// TestClaimWritingJob_CheckpointThenBusinessFailureRecovery R19：checkpoint 后业务
// 事务失败（SQLite trigger 在 article_revisions INSERT 上 RAISE(FAIL) 注入）→
// 重启恢复/重试不重复 Provider 调用（calls=1），最终只有一个修订。
func TestClaimWritingJob_CheckpointThenBusinessFailureRecovery(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	_, kpid, _, job := claimWritingFixture(t, s)
	fake := &fakeClaimWriter{result: goodClaimWritingResult(claimWritingMarkdown, kpid)}
	w.bundleFor = bundleWithWriter(fake)

	// 第一轮：Provider 调用成功、checkpoint 已保存；用 trigger 让业务落库失败。
	trigger := fmt.Sprintf(`CREATE TEMP TRIGGER fail_claim_revision BEFORE INSERT ON article_revisions
	    WHEN NEW.origin_job_id = '%s' BEGIN SELECT RAISE(FAIL, 'injected business tx failure'); END`, job.ID)
	if _, err := s.DB.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || fake.calls != 1 {
		t.Fatalf("首轮应业务失败: status=%s calls=%d", got.Status, fake.calls)
	}
	var revCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions`).Scan(&revCount)
	if revCount != 0 {
		t.Fatalf("失败轮不得留下修订: %d", revCount)
	}
	// 清除注入；模拟重启恢复：running+过期租约 → ResetRunningOnStartup → 重新领取。
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_claim_revision`); err != nil {
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
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, job.SourceID, job.ID).Scan(&revCount)
	if revCount != 1 {
		t.Fatalf("恢复后修订数=%d", revCount)
	}
	final, _ := s.GetJob(ctx, job.ID)
	if final.Status != models.StatusSucceeded {
		t.Fatalf("恢复后 job status=%s err=%v", final.Status, final.LastError)
	}
}

// TestClaimWritingJob_InvalidOutputsRejected R19：nil/空 ClaimMap、片段不存在、
// 越权材料 → 无修订、无 result（Provider 可能已被调用，输出被程序拦截）。
func TestClaimWritingJob_InvalidOutputsRejected(t *testing.T) {
	cases := []struct {
		name   string
		result *provider.ClaimAwareWritingResult
	}{
		{"empty claimmap", &provider.ClaimAwareWritingResult{Title: "T", Markdown: claimWritingMarkdown, ClaimMap: nil}},
		{"excerpt missing", func() *provider.ClaimAwareWritingResult {
			r := goodClaimWritingResult(claimWritingMarkdown, "kp")
			r.ClaimMap[0].Excerpt = "正文里没有这句话"
			return r
		}()},
		{"unauthorized material", func() *provider.ClaimAwareWritingResult {
			r := goodClaimWritingResult(claimWritingMarkdown, "kp")
			r.ClaimMap[0].MaterialIDs = []string{"kp-fabricated"}
			return r
		}()},
		{"empty body", func() *provider.ClaimAwareWritingResult {
			r := goodClaimWritingResult(claimWritingMarkdown, "kp")
			r.Markdown = " "
			return r
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			_, _, _, job := claimWritingFixture(t, s)
			fake := &fakeClaimWriter{result: tc.result}
			w.bundleFor = bundleWithWriter(fake)
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetJob(ctx, job.ID)
			if got.Status != models.StatusFailed {
				t.Fatalf("status=%s", got.Status)
			}
			var revCount int
			s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions`).Scan(&revCount)
			exec, _ := s.GetJobExecution(ctx, job.ID)
			if revCount != 0 || exec.ResultState != "" {
				t.Fatalf("非法输出不得落库: rev=%d state=%q", revCount, exec.ResultState)
			}
		})
	}
}

// TestClaimWritingJob_DynamicPolicyGateBeforeProvider R19：来源撤销（归档）或禁止
// 外发（LocalOnly）在 Provider 前拦截——零调用、零修订。
func TestClaimWritingJob_DynamicPolicyGateBeforeProvider(t *testing.T) {
	cases := []struct {
		name   string
		policy func(*store.Store, string)
	}{{
		name: "archived",
		policy: func(s *store.Store, sourceID string) {
			s.DB.ExecContext(context.Background(), `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, sourceID)
		},
	}, {
		name: "local only",
		policy: func(s *store.Store, sourceID string) {
			s.SetSourceProductionPolicy(context.Background(), models.SourceEpisode, sourceID, "internal", models.ModelDataLocalOnly)
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			_, kpid, sourceID, job := claimWritingFixture(t, s)
			tc.policy(s, sourceID)
			fake := &fakeClaimWriter{result: goodClaimWritingResult(claimWritingMarkdown, kpid)}
			w.bundleFor = bundleWithWriter(fake)
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if fake.calls != 0 {
				t.Fatalf("策略撤销必须零调用: %d", fake.calls)
			}
			got, _ := s.GetJob(ctx, job.ID)
			lastErr := ""
			if got.LastError != nil {
				lastErr = *got.LastError
			}
			if got.Status != models.StatusFailed || !strings.Contains(lastErr, "来源") {
				t.Fatalf("status=%s err=%v", got.Status, got.LastError)
			}
			var revCount int
			s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions`).Scan(&revCount)
			if revCount != 0 {
				t.Fatalf("rev=%d", revCount)
			}
		})
	}
}

// TestClaimWritingJob_TamperedCheckpointNotReused R19：checkpoint 身份不匹配
// （provider/model/prompt_version 篡改）或结果本体非法（ClaimMap 越权/片段不匹配，
// metadata 正确）都不得被静默当作可用结果——重新调用 Provider 产出正确结果，
// 任务仍成功。
func TestClaimWritingJob_TamperedCheckpointNotReused(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*claimWritingCheckpoint)
	}{
		{"wrong provider", func(cp *claimWritingCheckpoint) { cp.Provider = "other-provider" }},
		{"wrong model", func(cp *claimWritingCheckpoint) { cp.Model = "other-model" }},
		{"wrong prompt", func(cp *claimWritingCheckpoint) { cp.PromptVersion = "claim-writer-v1" }},
		{"missing stage", func(cp *claimWritingCheckpoint) { cp.Stage = "" }},
		{"unauthorized claimmap", func(cp *claimWritingCheckpoint) {
			cp.Result.ClaimMap[0].MaterialIDs = []string{"kp-fabricated"}
		}},
		{"excerpt mismatch", func(cp *claimWritingCheckpoint) {
			cp.Result.ClaimMap[0].Excerpt = "checkpoint 里篡改的片段"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			_, kpid, _, job := claimWritingFixture(t, s)
			cp := claimWritingCheckpoint{
				Stage: "written", Result: goodClaimWritingResult(claimWritingMarkdown, kpid),
				Usage:    provider.TaskUsage{InputUnits: 10, OutputUnits: 4},
				Provider: "fake-writer", Model: provider.EffectiveModel("groq", "", string(models.JobClaimWriting)),
				PromptVersion: provider.ClaimWriterPromptVersion,
			}
			tc.tamper(&cp)
			cpJSON, _ := json.Marshal(cp)
			if err := s.SaveJobCheckpoint(ctx, job.ID, string(cpJSON)); err != nil {
				t.Fatal(err)
			}
			fake := &fakeClaimWriter{result: goodClaimWritingResult(claimWritingMarkdown, kpid)}
			w.bundleFor = bundleWithWriter(fake)
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if fake.calls != 1 {
				t.Fatalf("篡改 checkpoint 不得复用，应重新调用: calls=%d", fake.calls)
			}
			got, _ := s.GetJob(ctx, job.ID)
			if got.Status != models.StatusSucceeded {
				t.Fatalf("status=%s err=%v", got.Status, got.LastError)
			}
			var revCount int
			s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, job.SourceID, job.ID).Scan(&revCount)
			if revCount != 1 {
				t.Fatalf("rev=%d", revCount)
			}
		})
	}
}
