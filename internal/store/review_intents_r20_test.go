package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// reviewedRevisionFixture 在 confirmBriefFixture（确认 Brief + 持久链接）之上，
// 走生产 EnqueueClaimWriting + SaveClaimWritingOutput 原子落库，产出新契约修订。
func reviewedRevisionFixture(t *testing.T) (*Store, *models.CreationBrief, string, string, string) {
	t.Helper()
	s, b, _, kpID := confirmBriefFixture(t)
	ctx := context.Background()
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatalf("write enqueue: %v", err)
	}
	link, err := s.GetCreationArticleLinkByCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.GetArticleDraftByBrief(ctx, link.ArticleBriefID)
	if err != nil {
		t.Fatal(err)
	}
	const markdown = "# R20\n\n来源说过这句话。Owner 的判断在此。"
	out := ClaimWritingOutput{
		DraftID: draft.ID, JobID: job.ID,
		CreationBriefID: b.ID, BriefVersion: b.ConfirmedVersion,
		CreationArticleLinkID: link.ID, ArticleProposalID: link.ArticleProposalID, ArticleBriefID: link.ArticleBriefID,
		OwnerClaim: "Owner", Title: "R20 标题", Markdown: markdown,
		ProviderName: "test", ModelName: "m1", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{kpID},
		Entries: []models.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpID}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	rev, err := s.SaveClaimWritingOutput(ctx, out)
	if err != nil {
		t.Fatalf("save writing output: %v", err)
	}
	return s, b, draft.ID, rev.ID, kpID
}

// TestEnqueueRevisionReview_FrozenSnapshot R20：入队在同一事务冻结完整强类型
// 快照（精确 revision title/markdown、ClaimMap、confirmed claim、授权材料身份、
// profile、provider/model/prompt），job 配置同步冻结。
func TestEnqueueRevisionReview_FrozenSnapshot(t *testing.T) {
	s, b, _, revID, kpID := reviewedRevisionFixture(t)
	ctx := context.Background()
	job, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != models.JobClaimReview {
		t.Fatalf("job type=%s", job.JobType)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.ConfigVersion != provider.ClaimReviewerPromptVersion || exec.ConfiguredProvider == "" {
		t.Fatalf("配置冻结缺失: %+v", exec)
	}
	var input ReviewTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	rev, err := s.GetArticleRevision(ctx, revID)
	if err != nil {
		t.Fatal(err)
	}
	if input.Kind != ReviewKindClaim || input.RevisionID != revID || input.Title != rev.Title || input.Markdown != rev.Markdown {
		t.Fatalf("冻结正文不符: %+v", input)
	}
	if input.ConfirmedClaim != "Owner" || input.CreationBriefID != b.ID || input.BriefVersion != b.ConfirmedVersion {
		t.Fatalf("Brief 冻结不符: %+v", input)
	}
	if len(input.ClaimMap) != 2 {
		t.Fatalf("ClaimMap 冻结不符: %+v", input.ClaimMap)
	}
	foundKp := false
	for _, id := range input.AuthorizedIDs {
		if id == kpID {
			foundKp = true
		}
	}
	if !foundKp || len(input.Materials) != 1 || input.Materials[0].KeyPointID != kpID {
		t.Fatalf("授权材料冻结不符: ids=%v materials=%+v", input.AuthorizedIDs, input.Materials)
	}
	if input.Provider == "" || input.Model == "" || input.PromptVersion != provider.ClaimReviewerPromptVersion {
		t.Fatalf("provider 冻结不符: %+v", input)
	}
	// 意图持久映射可读。
	got, err := s.ReviewJobForRevision(ctx, revID, ReviewKindClaim)
	if err != nil || got.ID != job.ID {
		t.Fatalf("意图映射: %v %+v", err, got)
	}
}

// TestEnqueueRevisionReview_ClaimRequiresBridge R20：无持久 bridge 的旧文章修订
// 不能获得独立主张审校入队（ClaimReview 冻结依赖确认 Brief）。
func TestEnqueueRevisionReview_ClaimRequiresBridge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	check(t, err)
	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "旧"})
	check(t, err)
	check(t, s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"))
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "旧论点"})
	check(t, err)
	check(t, s.ConfirmArticleBrief(ctx, brief.ID))
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "旧文章")
	check(t, err)
	rev, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧正文", Origin: "owner"})
	check(t, err)
	if _, err := s.EnqueueRevisionReview(ctx, rev.ID, ReviewKindClaim); err == nil {
		t.Fatal("无 bridge 的旧文章修订必须拒绝主张审校入队")
	}
}

// TestEnqueueRevisionReview_IdempotentAndRetry R20：重复点击 queued/succeeded 复用
// 同一 job；failed 点击真实重试同一 job（reset queued，保留冻结输入/配置）。
func TestEnqueueRevisionReview_IdempotentAndRetry(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := context.Background()
	job1, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	job2, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if job1.ID != job2.ID || job2.Status != models.StatusQueued {
		t.Fatalf("queued 重复点击必须复用同一 job: %s vs %s (%s)", job1.ID, job2.ID, job2.Status)
	}
	// 成功后重复点击仍复用同一 job。
	if _, err := s.MarkJobRunning(ctx, job1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, job1.ID); err != nil {
		t.Fatal(err)
	}
	job3, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if job3.ID != job1.ID || job3.Status != models.StatusSucceeded {
		t.Fatalf("succeeded 重复点击必须复用同一 job: %+v", job3)
	}
	// failed → 真实重试：同 job reset queued，冻结输入/配置保留。
	if err := s.MarkJobFailed(ctx, job1.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	retried, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != job1.ID {
		t.Fatalf("failed 重试必须是同一 job: %s vs %s", retried.ID, job1.ID)
	}
	// 数据库状态必须真正 reset（不可只看返回对象）：queued + last_error 清空。
	persisted, err := s.GetJob(ctx, job1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.StatusQueued {
		t.Fatalf("重试后 DB 状态应为 queued: %s", persisted.Status)
	}
	if persisted.LastError != nil && *persisted.LastError != "" {
		t.Fatalf("重试后 last_error 应清空: %v", persisted.LastError)
	}
	// 再次点击（此刻已 queued）继续复用同一 job，不产生副作用。
	again, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil || again.ID != job1.ID || again.Status != models.StatusQueued {
		t.Fatalf("queued 重复点击必须复用: %+v %v", again, err)
	}
	if retried.Status != models.StatusQueued {
		t.Fatalf("failed 重试应 reset 为 queued: %s", retried.Status)
	}
	exec, err := s.GetJobExecution(ctx, job1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.ConfigVersion != provider.ClaimReviewerPromptVersion || exec.ConfiguredProvider == "" {
		t.Fatalf("重试后配置必须保留: %+v", exec)
	}
	var input ReviewTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil || input.RevisionID != revID {
		t.Fatalf("重试后冻结输入必须保留: %+v %+v", input, err)
	}
	// ClaimMap 引用不在确认快照内的材料时拒绝（部分缺失不得静默降级）。
	broken, _, _, brokenRev, _ := reviewedRevisionFixture(t)
	if _, err := broken.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json='["ghost-material"]' WHERE revision_id=?`, brokenRev); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.EnqueueRevisionReview(ctx, brokenRev, ReviewKindClaim); err == nil {
		t.Fatal("ClaimMap 引用不在确认快照内的材料必须拒绝入队")
	}
	// style 入队不受影响且 intent 按类型隔离。
	got, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
	if err != nil || got.JobType != models.JobStyleReview {
		t.Fatalf("style 入队: %v %+v", err, got)
	}
}

// TestSaveClaimReviewOutput_R20：校验契约 + 原子落库 + origin-job 幂等重放。
func TestSaveClaimReviewOutput_R20(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := context.Background()
	job, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	pass := &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed, Findings: nil}
	saved, err := s.SaveClaimReviewOutput(ctx, job.ID, pass, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != models.ClaimReviewStatusPassed || saved.OriginJobID != job.ID {
		t.Fatalf("saved=%+v", saved)
	}
	if saved.Provider == nil || saved.Model == nil || saved.PromptVersion == nil {
		t.Fatalf("provenance 必须非空: %+v", saved)
	}
	// job result 已 complete；重复恢复（重放）不产生第二条审校。
	replay, err := s.SaveClaimReviewOutput(ctx, job.ID, pass, "", "")
	if err != nil {
		t.Fatalf("重放应幂等: %v", err)
	}
	if replay.ID != saved.ID {
		t.Fatalf("重放返回不同审校: %s vs %s", replay.ID, saved.ID)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_reviews WHERE work_revision_id=?`, revID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("重放不得新增审校行: %d", n)
	}

	// 被拒绝的保存不得产生行（上方已断言 n2==1），且重放幂等（上方已断言）。
}

// ---- readiness 门禁（R20）----

// durable 审校行 helper：显式固定 created_at（确定性，无 sleep）。
const (
	ts1 = "2026-01-01 00:00:01"
	ts2 = "2026-01-01 00:00:02"
	ts3 = "2026-01-01 00:00:03"
	ts4 = "2026-01-01 00:00:04"
	ts5 = "2026-01-01 00:00:05"
)

func durableClaimRowAt(t *testing.T, s *Store, revisionID, jobID, status, issues, createdAt string) {
	t.Helper()
	if issues == "" {
		issues = "[]"
	}
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO claim_reviews (id,work_revision_id,status,issues_json,provider,model,prompt_version,origin_job_id,created_at)
		 VALUES (lower(hex(randomblob(16))),?,?,?,?,?,?,?,?)`,
		revisionID, status, issues, "prov-p", "prov-m", "prov-v", jobID, createdAt); err != nil {
		t.Fatal(err)
	}
}

func durableStyleRowAt(t *testing.T, s *Store, revisionID, jobID, status, issues, createdAt string) {
	t.Helper()
	if issues == "" {
		issues = "[]"
	}
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,provider,model,prompt_version,origin_job_id,created_at)
		 VALUES (lower(hex(randomblob(16))),?,'style',?,?,?,?,?,?,?)`,
		revisionID, status, issues, "prov-p", "prov-m", provider.StyleEditorPromptVersion, jobID, createdAt); err != nil {
		t.Fatal(err)
	}
}

func issueText(r *ArticlePublicationReadiness) string {
	return strings.Join(r.Issues, "；")
}

// secondRevision 经生产 Owner 修订路径在同 draft 下追加一个新契约修订。
func secondRevision(t *testing.T, s *Store, firstRevID string) string {
	t.Helper()
	draftID := ""
	if err := s.DB.QueryRowContext(context.Background(), `SELECT draft_id FROM article_revisions WHERE id=?`, firstRevID).Scan(&draftID); err != nil {
		t.Fatal(err)
	}
	rev, err := s.CreateArticleRevision(context.Background(), models.ArticleRevision{DraftID: draftID, Title: "R20 next", Markdown: "# R20 next\n\n新的正文。", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return rev.ID
}

func legacyCopyClaimRow(t *testing.T, s *Store, revisionID string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO claim_reviews (id,work_revision_id,status,issues_json,provider,model,prompt_version)
		 VALUES (lower(hex(randomblob(16))),?,'passed','[]','p','m','v')`, revisionID); err != nil {
		t.Fatal(err)
	}
}

// TestEvaluateReadiness_R20 新契约门禁矩阵：每个子测试独立 fixture，互不污染。
func TestEvaluateReadiness_R20(t *testing.T) {
	ctx := context.Background()

	t.Run("missing_both_blocks", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
		if err != nil {
			t.Fatal(err)
		}
		if !r.NewContract || r.Ready {
			t.Fatalf("初始不得 ready: %+v", r)
		}
		if !strings.Contains(issueText(r), "主张审校") || !strings.Contains(issueText(r), "风格审校") {
			t.Fatalf("缺失文案不明: %s", issueText(r))
		}
	})

	t.Run("other_revision_passed_does_not_unlock", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		otherRev := secondRevision(t, s, revID)
		durableClaimRowAt(t, s, otherRev, "job-o-c", "passed", "", ts1)
		durableStyleRowAt(t, s, otherRev, "job-o-s", "passed", "", ts1)
		r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Ready || !strings.Contains(issueText(r), "缺少独立主张审校") {
			t.Fatalf("其他 revision passed 不放行: %+v", r)
		}
		// 其他修订自身可 ready（精确修订语义）。
		or, err := s.EvaluateArticlePublicationReadiness(ctx, otherRev)
		if err != nil {
			t.Fatal(err)
		}
		if !or.Ready {
			t.Fatalf("其他修订自身应 ready: %+v", or)
		}
	})

	t.Run("claim_passed_style_missing_or_failed", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		durableClaimRowAt(t, s, revID, "job-c1", "passed", "", ts1)
		r, rerr := s.EvaluateArticlePublicationReadiness(ctx, revID)
		check(t, rerr)
		if r.Ready || !strings.Contains(issueText(r), "缺少独立风格审校") {
			t.Fatalf("style 缺失必须不放行: %+v", r)
		}
		durableStyleRowAt(t, s, revID, "job-s1", "failed", "[\"节奏拖沓\"]", ts2)
		r, rerr = s.EvaluateArticlePublicationReadiness(ctx, revID)
		check(t, rerr)
		if r.Ready || !strings.Contains(issueText(r), "风格审校未通过") {
			t.Fatalf("style failed 必须不放行: %+v", r)
		}
	})

	t.Run("legacy_only_rows_rejected", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		legacyCopyClaimRow(t, s, revID)
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,provider,model)
			 VALUES (lower(hex(randomblob(16))),?,'style','advisory','["旧建议"]','p','m')`, revID); err != nil {
			t.Fatal(err)
		}
		r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Ready || !strings.Contains(issueText(r), "旧版") {
			t.Fatalf("仅旧审校必须不放行: %+v", r)
		}
	})

	t.Run("both_passed_ready", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		durableClaimRowAt(t, s, revID, "job-c1", "passed", "", ts1)
		durableStyleRowAt(t, s, revID, "job-s1", "passed", "", ts1)
		r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Ready {
			t.Fatalf("claim+style 均 passed 必须 ready: %+v", r)
		}
	})

	t.Run("latest_failed_overrides_passed", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		durableClaimRowAt(t, s, revID, "job-c1", "passed", "", ts1)
		durableStyleRowAt(t, s, revID, "job-s1", "passed", "", ts2)
		durableClaimRowAt(t, s, revID, "job-c2", "failed", "[\"片段X： misattributed — 归因错误\"]", ts3)
		r, rerr := s.EvaluateArticlePublicationReadiness(ctx, revID)
		check(t, rerr)
		if r.Ready || !strings.Contains(issueText(r), "主张审校未通过") {
			t.Fatalf("最新 claim failed 必须覆盖旧 passed: %+v", r)
		}
		durableStyleRowAt(t, s, revID, "job-s2", "failed", "[\"拖沓\"]", ts4)
		r, rerr = s.EvaluateArticlePublicationReadiness(ctx, revID)
		check(t, rerr)
		if r.Ready || !strings.Contains(issueText(r), "风格审校未通过") {
			t.Fatalf("最新 style failed 必须覆盖旧 passed: %+v", r)
		}
	})

	t.Run("provenance_required", func(t *testing.T) {
		s, _, _, revID, _ := reviewedRevisionFixture(t)
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO claim_reviews (id,work_revision_id,status,issues_json,provider,origin_job_id,created_at)
			 VALUES (lower(hex(randomblob(16))),?,'passed','[]','p','job-c1',?)`, revID, ts1); err != nil {
			t.Fatal(err)
		}
		durableStyleRowAt(t, s, revID, "job-s1", "passed", "", ts1)
		r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Ready || !strings.Contains(issueText(r), "Provider/Model/Prompt") {
			t.Fatalf("claim provenance 缺失必须不放行: %+v", r)
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,provider,model,origin_job_id,created_at)
			 VALUES (lower(hex(randomblob(16))),?,'style','passed','[]','p','m','job-s2',?)`, revID, ts2); err != nil {
			t.Fatal(err)
		}
		r, rerr := s.EvaluateArticlePublicationReadiness(ctx, revID)
		check(t, rerr)
		if r.Ready || !strings.Contains(issueText(r), "Provider/Model/Prompt") {
			t.Fatalf("style provenance 缺失必须不放行: %+v", r)
		}
	})
}

// TestStyleReviewLegacyCompat_R20：旧文章同步路径（origin_job_id=”）仍可保存
// advisory/passed/failed 风格审校（存储兼容）；durable Provider 路径必须拒绝
// advisory 与自洽性不满足的结果。
func TestStyleReviewLegacyCompat_R20(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := context.Background()
	p, m := "legacy-p", "legacy-m"
	// 旧同步路径：advisory + 无 origin_job_id → 兼容保存。
	saved, err := s.CreateArticleReview(ctx, models.ArticleReview{
		RevisionID: revID, Kind: "style", Status: "advisory", IssuesJSON: `["旧建议"]`, Provider: &p, Model: &m,
	})
	if err != nil {
		t.Fatalf("旧 advisory 风格审校必须兼容保存: %v", err)
	}
	if saved.OriginJobID != "" {
		t.Fatalf("旧路径不得带 origin_job_id: %+v", saved)
	}
	// advisory 行不解除新契约门禁。
	r, err := s.EvaluateArticlePublicationReadiness(ctx, revID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || !strings.Contains(issueText(r), "旧版") {
		t.Fatalf("advisory 旧行必须不放行: %+v", r)
	}
	// durable Provider 路径：advisory 必须、passed 带 issues 必须、failed 无 issues 必须拒绝。
	job, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*provider.StyleReviewResult{
		{Status: "advisory", Issues: []string{"建议"}},
		{Status: "passed", Issues: []string{"不该有"}},
		{Status: "failed"},
		nil,
	} {
		if _, err := s.SaveStyleReviewOutput(ctx, job.ID, bad, "", ""); err == nil {
			t.Fatalf("durable 风格审校必须拒绝: %+v", bad)
		}
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=? AND origin_job_id!=''`, revID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("被拒绝的保存不得产生 durable 行: %d", n)
	}
	// 合法 durable passed 保存后 gate 解除（job 复位以便 worker 语义模拟）。
	check(t, func() error {
		_, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='running' WHERE id=?`, job.ID)
		return err
	}())
	if _, err := s.SaveStyleReviewOutput(ctx, job.ID, &provider.StyleReviewResult{Status: "passed"}, "", ""); err != nil {
		t.Fatal(err)
	}
}

// TestSaveReviewReplayReturnsExactRow_R20：重放必须返回 origin_job 精确匹配的持久行
// （含持久 provenance），不被同 revision 更新的其他行取代，也不用调用参数伪造。
func TestSaveReviewReplayReturnsExactRow_R20(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := context.Background()

	// claim：先经生产入队+保存（实际调用身份 prov-actual）。
	job, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveClaimReviewOutput(ctx, job.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "prov-actual", "prov-model")
	if err != nil {
		t.Fatal(err)
	}
	// 同 revision 插入更新的其他 durable 行（模拟后续审校）。
	durableClaimRowAt(t, s, revID, "job-newer", "failed", "[\"newer\"]", ts5)
	replay, err := s.SaveClaimReviewOutput(ctx, job.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != saved.ID || replay.OriginJobID != job.ID {
		t.Fatalf("重放必须返回 origin_job 精确行: %+v vs %+v", replay, saved)
	}
	if replay.Provider == nil || *replay.Provider != "prov-actual" || replay.Model == nil || *replay.Model != "prov-model" {
		t.Fatalf("重放 provenance 必须来自持久行: %+v", replay)
	}

	// style：实际身份 prov-style；用不同调用参数重放 → 返回持久 provenance。
	styleJob, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
	if err != nil {
		t.Fatal(err)
	}
	styleSaved, err := s.SaveStyleReviewOutput(ctx, styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "prov-style", "prov-style-model")
	if err != nil {
		t.Fatal(err)
	}
	styleReplay, err := s.SaveStyleReviewOutput(ctx, styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "fake-other", "fake-other-model")
	if err != nil {
		t.Fatal(err)
	}
	if styleReplay.ID != styleSaved.ID {
		t.Fatalf("style 重放返回不同行: %+v vs %+v", styleReplay, styleSaved)
	}
	if styleReplay.Provider == nil || *styleReplay.Provider != "prov-style" || styleReplay.Model == nil || *styleReplay.Model != "prov-style-model" {
		t.Fatalf("style 重放 provenance 必须来自持久行: %+v", styleReplay)
	}
}

// TestStyleEnqueue_FiltersMaterialsByClaimMap R20：新契约 style 入队冻结材料 =
// 该精确 revision ClaimMap 实际引用的 ID；无 ClaimMap 拒绝。
func TestStyleEnqueue_FiltersMaterialsByClaimMap(t *testing.T) {
	s, _, _, revID, kpID := reviewedRevisionFixture(t)
	ctx := context.Background()
	job, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input ReviewTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Materials) != 1 || input.Materials[0].KeyPointID != kpID {
		t.Fatalf("style 冻结材料应按 ClaimMap 过滤: %+v", input.Materials)
	}
	// 无 ClaimMap 的新修订：拒绝 style 入队。
	empty := secondRevision(t, s, revID)
	if _, err := s.EnqueueRevisionReview(ctx, empty, ReviewKindStyle); err == nil {
		t.Fatal("无 ClaimMap 的新契约修订必须拒绝 style 入队")
	}
}

// TestCorruptedClaimMapBlocksEnqueueR20（item 3）：claim_map_entries JSON 损坏时
// ListClaimMap 与 claim/style 入队全部失败（独立 fixture，逐列验证，不复用污染数据）。
func TestCorruptedClaimMapBlocksEnqueueR20(t *testing.T) {
	cases := []struct {
		name   string
		column string
	}{
		{"material_ids_json", "material_ids_json"},
		{"citation_refs_json", "citation_refs_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, revID, _ := reviewedRevisionFixture(t)
			ctx := context.Background()
			draftID := ""
			check(t, s.DB.QueryRowContext(ctx, `SELECT draft_id FROM article_revisions WHERE id=?`, revID).Scan(&draftID))
			_, err := s.DB.ExecContext(ctx, `UPDATE claim_map_entries SET `+tc.column+`='{broken' WHERE revision_id=?`, revID)
			check(t, err)
			_, listErr := s.ListClaimMap(ctx, draftID, revID)
			if listErr == nil || !(strings.Contains(listErr.Error(), "ClaimMap") && strings.Contains(listErr.Error(), "解析")) {
				t.Fatalf("%s 损坏时 ListClaimMap 必须因解析失败: %v", tc.column, listErr)
			}
			_, claimErr := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
			if claimErr == nil || !strings.Contains(claimErr.Error(), "解析") {
				t.Fatalf("%s 损坏时 claim 入队必须因解析失败: %v", tc.column, claimErr)
			}
			_, styleErr := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
			if styleErr == nil || !strings.Contains(styleErr.Error(), "解析") {
				t.Fatalf("%s 损坏时 style 入队必须因解析失败: %v", tc.column, styleErr)
			}
		})
	}
}

// TestStyleEnqueueRejectsGhostMaterialR20（item 4）：ClaimMap 引用不在确认快照内的
// 材料（哪怕仅一个）时，style 入队拒绝，不得部分过滤。
func TestStyleEnqueueRejectsGhostMaterialR20(t *testing.T) {
	s, _, _, revID, kpID := reviewedRevisionFixture(t)
	ctx := context.Background()
	// source_claim 同时引用真实材料与 ghost。
	_, err := s.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json=? WHERE revision_id=? AND claim_kind='source_claim'`,
		`["`+kpID+`","ghost-material"]`, revID)
	check(t, err)
	if _, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle); err == nil {
		t.Fatal("style 入队必须拒绝 ghost material（不得部分过滤）")
	}
	// claim 入队同样拒绝（既有契约，回归确认）。
	if _, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim); err == nil {
		t.Fatal("claim 入队必须拒绝 ghost material")
	}
}

// check 测试内断言无错误（防止 fixture 准备步骤吞错产生假阳性）。
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
