package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// revisedWithFeedbackFixture 在 reviewedRevisionFixture 之上制造 durable 审校反馈
// （claim failed findings + style passed），返回 base revision。
func revisedWithFeedbackFixture(t *testing.T) (*Store, string, *models.ArticleRevision) {
	t.Helper()
	s, _, draftID, revID, _ := reviewedRevisionFixture(t)
	ctx := t.Context()
	claimJob, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindClaim)
	check(t, err)
	mustRunStoreJob(t, s, claimJob.ID)
	_, err = s.SaveClaimReviewOutput(ctx, claimJob.ID, &provider.ClaimReviewResult{
		Status:   provider.ClaimReviewFailed,
		Findings: []provider.ClaimReviewFinding{{Excerpt: "来源说过这句话。", IssueKind: "misattributed", Detail: "归因错误"}},
	}, "p", "m")
	check(t, err)
	styleJob, err := s.EnqueueRevisionReview(ctx, revID, ReviewKindStyle)
	check(t, err)
	mustRunStoreJob(t, s, styleJob.ID)
	_, err = s.SaveStyleReviewOutput(ctx, styleJob.ID, &stylePassResult, "", "")
	check(t, err)
	rev, err := s.GetArticleRevision(ctx, revID)
	check(t, err)
	return s, draftID, rev
}

// TestEnqueueClaimRevision_FrozenSnapshot R21：入队冻结精确 base/ClaimMap/durable
// 反馈/确认 Brief 与原授权材料/Writer 配置；重复点击与 failed 重试复用同 job。
func TestEnqueueClaimRevision_FrozenSnapshot(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	if job.JobType != models.JobClaimRevision || job.Status != models.StatusQueued {
		t.Fatalf("job=%+v", job)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	check(t, err)
	if exec.ConfigVersion != provider.ClaimRevisionWriterPromptVersion || exec.ConfiguredProvider == "" {
		t.Fatalf("配置冻结缺失: %+v", exec)
	}
	var input RevisionWritingTaskInput
	check(t, json.Unmarshal([]byte(exec.InputSnapshotJSON), &input))
	if input.Kind != "ai_revision" || input.BaseRevisionID != base.ID || input.DraftID != draftID ||
		input.ExistingMarkdown != base.Markdown || input.Title != base.Title {
		t.Fatalf("base 冻结不符: %+v", input)
	}
	if len(input.ExistingClaimMap) != 2 {
		t.Fatalf("原 ClaimMap 冻结不符: %+v", input.ExistingClaimMap)
	}
	if len(input.ReviewFeedback) == 0 || !containsSubstring(input.ReviewFeedback, "归因错误") {
		t.Fatalf("durable 反馈冻结不符: %+v", input.ReviewFeedback)
	}
	if input.OwnerClaim != "Owner" || input.CreationBriefID == "" || input.BriefVersion <= 0 || len(input.Materials) == 0 {
		t.Fatalf("Brief/材料冻结不符: %+v", input)
	}
	if input.Provider == "" || input.Model == "" || input.PromptVersion != provider.ClaimRevisionWriterPromptVersion {
		t.Fatalf("writer 配置冻结不符: %+v", input)
	}
	// 重复点击复用同 job。
	again, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	if again.ID != job.ID {
		t.Fatalf("重复点击必须复用: %s vs %s", again.ID, job.ID)
	}
	// failed 重试同 job（reset queued，DB 状态断言）。
	check(t, s.MarkJobFailed(ctx, job.ID, "boom"))
	retried, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	if retried.ID != job.ID || retried.Status != models.StatusQueued {
		t.Fatalf("failed 重试: %+v", retried)
	}
	persisted, err := s.GetJob(ctx, job.ID)
	check(t, err)
	if persisted.Status != models.StatusQueued {
		t.Fatalf("DB 状态未 reset: %s", persisted.Status)
	}
}

func containsSubstring(list []string, want string) bool {
	for _, v := range list {
		if strings.Contains(v, want) {
			return true
		}
	}
	return false
}

// TestEnqueueClaimRevision_RequiresFeedbackAndBridge R21：无 durable 反馈或无
// bridge（旧文章）必须拒绝。
func TestEnqueueClaimRevision_RequiresFeedbackAndBridge(t *testing.T) {
	s, _, _, revID, _ := reviewedRevisionFixture(t)
	ctx := t.Context()
	if _, err := s.EnqueueClaimRevisionForRevision(ctx, revID); err == nil {
		t.Fatal("无 durable 反馈必须拒绝 AI 修订入队")
	}
	// 旧文章修订（无 bridge）。
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
	legacyRev, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧正文", Origin: "owner"})
	check(t, err)
	if _, err := s.EnqueueClaimRevisionForRevision(ctx, legacyRev.ID); err == nil {
		t.Fatal("无 bridge 的旧文章必须拒绝 durable AI 修订")
	}
}

// TestSaveClaimRevisionOutput_R21：CAS 原子落库 + origin 幂等重放 + base 过期不覆盖。
func TestSaveClaimRevisionOutput_R21(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	result := &provider.ClaimAwareWritingResult{
		Title:    "R21 AI 修订",
		Markdown: "# R21 AI\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{fixtureKpID(t, s, draftID)}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	mustRunStoreJob(t, s, job.ID)
	saved, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "rev-p", "rev-m")
	check(t, err)
	if saved.Origin != "ai_edit" || saved.OriginJobID != job.ID || saved.Version != base.Version+1 {
		t.Fatalf("saved=%+v", saved)
	}
	entries, err := s.ListClaimMap(ctx, draftID, saved.ID)
	check(t, err)
	if len(entries) != 2 {
		t.Fatalf("ClaimMap 未落库: %+v", entries)
	}
	// current 已切换为新修订，status reviewing。
	var current, status string
	check(t, s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,''),status FROM article_drafts WHERE id=?`, draftID).Scan(&current, &status))
	if current != saved.ID || status != "reviewing" {
		t.Fatalf("current=%s status=%s", current, status)
	}
	// job complete result。
	var state string
	check(t, s.DB.QueryRowContext(ctx, `SELECT COALESCE(result_state,'') FROM processing_jobs WHERE id=?`, job.ID).Scan(&state))
	if state != models.JobResultComplete {
		t.Fatalf("result_state=%s", state)
	}
	// 重放幂等：返回同一 revision，不产生第二条。
	replay, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
	check(t, err)
	if replay.ID != saved.ID {
		t.Fatalf("重放返回不同修订: %s vs %s", replay.ID, saved.ID)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&n))
	if n != 2 {
		t.Fatalf("重放不得新增修订: %d", n)
	}

	// base 过期场景由 TestSaveClaimRevisionOutput_StaleBaseConflict 独立覆盖；
	// 此处仅确认重放后未产生多余修订。
	var n2 int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n2))
	if n2 != 1 {
		t.Fatalf("AI 修订数异常: %d", n2)
	}
}

// TestSaveClaimRevisionOutput_StaleBaseConflict R21：入队后 base 被抢先推进，
// 保存返回 ErrConflict 且不产生修订。
func TestSaveClaimRevisionOutput_StaleBaseConflict(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	// 入队后 Owner 抢先保存新修订（current 离开 base）。
	_, err = s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v2", "# v2\n\n全新内容。")
	check(t, err)
	kp, sourceTitle, citations := fixtureKpIdentity(t, s, draftID)
	stale := &provider.ClaimAwareWritingResult{
		Title:    "过期 AI 修订",
		Markdown: "# 过期\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kp}, SourceTitle: sourceTitle, CitationRefs: citations},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, stale, "", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("base 过期必须 ErrConflict: %v", err)
	}
	var current string
	check(t, s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&current))
	if current == base.ID || strings.Contains(current, "job") {
		t.Fatalf("current 异常: %s", current)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin_job_id=?`, draftID, job.ID).Scan(&n))
	if n != 0 {
		t.Fatalf("冲突不得留下 AI 修订: %d", n)
	}
}

// TestSaveClaimRevisionOutput_RejectsInvalid R21：未授权材料/空结果/片段不匹配拒绝。
func TestSaveClaimRevisionOutput_RejectsInvalid(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	kp := fixtureKpID(t, s, draftID)
	cases := []struct {
		name string
		res  *provider.ClaimAwareWritingResult
	}{
		{"nil", nil},
		{"空正文", &provider.ClaimAwareWritingResult{Title: "T", Markdown: "", ClaimMap: []provider.ClaimMapEntry{{Excerpt: "x", ClaimKind: provider.ClaimOwner}}}},
		{"无 ClaimMap", &provider.ClaimAwareWritingResult{Title: "T", Markdown: "# T\n\n正文。", ClaimMap: nil}},
		{"越权材料", &provider.ClaimAwareWritingResult{Title: "T", Markdown: "# T\n\n正文。", ClaimMap: []provider.ClaimMapEntry{{Excerpt: "正文。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{"ghost"}}}}},
		{"片段不匹配", &provider.ClaimAwareWritingResult{Title: "T", Markdown: "# T\n\n正文。", ClaimMap: []provider.ClaimMapEntry{{Excerpt: "不在正文", ClaimKind: provider.ClaimOwner}}}},
	}
	for _, tc := range cases {
		if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, tc.res, "", ""); err == nil {
			t.Fatalf("%s 必须被拒绝", tc.name)
		}
	}
	_ = kp
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=? AND origin='ai_edit'`, draftID).Scan(&n))
	if n != 0 {
		t.Fatalf("被拒绝的保存不得产生修订: %d", n)
	}
}

// fixtureKpIdentity 读取 fixture 的 source claim 完整身份（测试工具）。
func fixtureKpIdentity(t *testing.T, s *Store, draftID string) (string, string, []string) {
	t.Helper()
	var raw, title, citations string
	check(t, s.DB.QueryRowContext(t.Context(),
		`SELECT material_ids_json, source_title, citation_refs_json FROM claim_map_entries WHERE draft_id=? AND claim_kind='source_claim' LIMIT 1`, draftID).Scan(&raw, &title, &citations))
	var ids, refs []string
	check(t, json.Unmarshal([]byte(raw), &ids))
	check(t, json.Unmarshal([]byte(citations), &refs))
	return ids[0], title, refs
}

// fixtureKpID 读取 fixture draft 授权材料 ID（测试工具）。
func fixtureKpID(t *testing.T, s *Store, draftID string) string {
	t.Helper()
	var kp string
	check(t, s.DB.QueryRowContext(t.Context(),
		`SELECT material_ids_json FROM claim_map_entries WHERE draft_id=? AND claim_kind='source_claim' LIMIT 1`, draftID).Scan(&kp))
	var ids []string
	check(t, json.Unmarshal([]byte(kp), &ids))
	if len(ids) == 0 {
		t.Fatal("fixture 缺少授权材料")
	}
	return ids[0]
}

// TestSaveClaimRevisionOutput_ConcurrentWithOwner R21：AI 保存与 Owner 保存同 base
// 并发 → 恰一成功一冲突；SQL 级守卫保证无孤儿 revision/映射。
func TestSaveClaimRevisionOutput_ConcurrentWithOwner(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	result := &provider.ClaimAwareWritingResult{
		Title:    "R21 AI 并发",
		Markdown: "# R21 AI\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{fixtureKpID(t, s, draftID)}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	var wg sync.WaitGroup
	type outcome struct {
		kind  string
		err   error
		revID string
	}
	results := make(chan outcome, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rev, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
		id := ""
		if rev != nil {
			id = rev.ID
		}
		results <- outcome{"ai", err, id}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v2", "# v2\n\nOwner 并发改写。")
		id := ""
		if rev != nil {
			id = rev.ID
		}
		results <- outcome{"owner", err, id}
	}()
	wg.Wait()
	close(results)
	ok := map[string]int{}
	conflict := map[string]int{}
	var winnerID string
	for r := range results {
		switch {
		case r.err == nil:
			ok[r.kind]++
			winnerID = r.revID
		case errors.Is(r.err, ErrConflict):
			conflict[r.kind]++
		default:
			t.Fatalf("%s 非预期错误: %v", r.kind, r.err)
		}
	}
	if ok["ai"]+ok["owner"] != 1 || conflict["ai"]+conflict["owner"] != 1 {
		t.Fatalf("恰一成功一冲突: ok=%v conflict=%v", ok, conflict)
	}
	var current string
	check(t, s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&current))
	if current != winnerID {
		t.Fatalf("current 与胜者不一致: %s vs %s", current, winnerID)
	}
	// 无孤儿：entries 与 canonical 只属于存在的修订。
	var orphanEntries, orphanCanonical int
	check(t, s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM claim_map_entries cme WHERE NOT EXISTS (SELECT 1 FROM article_revisions r WHERE r.id=cme.revision_id)`).Scan(&orphanEntries))
	check(t, s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM claim_maps cm WHERE NOT EXISTS (SELECT 1 FROM article_revisions r WHERE r.id=cm.work_revision_id)`).Scan(&orphanCanonical))
	if orphanEntries != 0 || orphanCanonical != 0 {
		t.Fatalf("孤儿 ClaimMap: entries=%d canonical=%d", orphanEntries, orphanCanonical)
	}
	// 新增修订恰好一个（base + 胜者）。
	var revTotal int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&revTotal))
	if revTotal != 2 {
		t.Fatalf("修订总数应恰为 base+胜者=2: %d", revTotal)
	}
}

// TestSaveClaimRevisionOutput_ReplayTamperedMap R21：同 title/body 但持久 ClaimMap
// 被篡改（或重放携带不同映射）→ ErrInvalidEditorialState，不得当作幂等成功。
func TestSaveClaimRevisionOutput_ReplayTamperedMap(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	result := &provider.ClaimAwareWritingResult{
		Title:    "R21 AI 重放",
		Markdown: "# R21 AI\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{fixtureKpID(t, s, draftID)}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	_, err = s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
	check(t, err)
	// 篡改持久映射的 source_title 后重放。
	_, err = s.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET source_title='篡改来源' WHERE draft_id=? AND excerpt='来源说过这句话。'`, draftID)
	check(t, err)
	if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", ""); err == nil || !strings.Contains(err.Error(), "身份与持久不一致") {
		t.Fatalf("篡改映射的重放必须拒绝: %v", err)
	}
	// 恢复后重放幂等成功。
	_, err = s.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET source_title='来源' WHERE draft_id=? AND excerpt='来源说过这句话。'`, draftID)
	check(t, err)
	replay, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
	check(t, err)
	if replay == nil {
		t.Fatal("恢复后重放应成功")
	}
}

// TestSaveClaimRevisionOutput_ReplayDuplicateEntries R21（第二轮审计 2）：
// 持久 claim_map_entries 出现重复 excerpt → 重放直接拒绝。
func TestSaveClaimRevisionOutput_ReplayDuplicateEntries(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	result := &provider.ClaimAwareWritingResult{
		Title:    "R21 AI 重放重复",
		Markdown: "# R21 AI\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{fixtureKpID(t, s, draftID)}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	_, err = s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
	check(t, err)
	// 注入同 excerpt 第二行（不同材料）。
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO claim_map_entries (id,draft_id,revision_id,excerpt,claim_kind,material_ids_json,source_title,citation_refs_json)
		 SELECT lower(hex(randomblob(16))),draft_id,revision_id,excerpt,'owner_claim','[]',source_title,citation_refs_json
		 FROM claim_map_entries WHERE draft_id=? AND excerpt='Owner 的判断在此。'`, draftID)
	check(t, err)
	if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", ""); err == nil || !strings.Contains(err.Error(), "重复 excerpt") {
		t.Fatalf("持久 entries 重复必须拒绝: %v", err)
	}
}

// TestSaveClaimRevisionOutput_ReplayCanonicalDrift R21（第二轮审计 2）：
// canonical claim_maps 投影与 result 漂移（kind/materials）→ 重放拒绝。
func TestSaveClaimRevisionOutput_ReplayCanonicalDrift(t *testing.T) {
	s, draftID, base := revisedWithFeedbackFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimRevisionForRevision(ctx, base.ID)
	check(t, err)
	mustRunStoreJob(t, s, job.ID)
	result := &provider.ClaimAwareWritingResult{
		Title:    "R21 AI canonical",
		Markdown: "# R21 AI\n\n来源说过这句话。Owner 的判断在此。",
		ClaimMap: []provider.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{fixtureKpID(t, s, draftID)}, SourceTitle: "来源"},
			{Excerpt: "Owner 的判断在此。", ClaimKind: provider.ClaimOwner},
		},
	}
	_, err = s.SaveClaimRevisionOutput(ctx, job.ID, result, "", "")
	check(t, err)
	// 漂移 canonical 的 kind。
	_, err = s.DB.ExecContext(ctx,
		`UPDATE claim_maps SET claim_kind='owner_claim' WHERE work_revision_id=(SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?) AND excerpt='来源说过这句话。'`, draftID, job.ID)
	check(t, err)
	if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", ""); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("canonical 漂移必须拒绝: %v", err)
	}
	// materials 漂移同样拒绝。
	_, err = s.DB.ExecContext(ctx,
		`UPDATE claim_maps SET claim_kind='source_claim', keypoint_ids_json='["ghost"]' WHERE work_revision_id=(SELECT id FROM article_revisions WHERE draft_id=? AND origin_job_id=?) AND excerpt='来源说过这句话。'`, draftID, job.ID)
	check(t, err)
	if _, err := s.SaveClaimRevisionOutput(ctx, job.ID, result, "", ""); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("canonical materials 漂移必须拒绝: %v", err)
	}
}
