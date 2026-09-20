package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// ownerRevisionFixture 在 reviewedRevisionFixture 之上返回可直接手工修订的 draft/base。
func ownerRevisionFixture(t *testing.T) (*Store, string, *models.ArticleRevision) {
	t.Helper()
	s, _, draftID, revID, _ := reviewedRevisionFixture(t)
	rev, err := s.GetArticleRevision(t.Context(), revID)
	if err != nil {
		t.Fatal(err)
	}
	return s, draftID, rev
}

// TestOwnerRevisionUniqueLocationInheritance R21：唯一定位片段继承；
// 旧审校不复制；新修订 readiness 回到 blocked，重新审校后恢复导出。
func TestOwnerRevisionUniqueLocationInheritance(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	// 先完成 claim+style 审校（旧修订可导出）。
	claimJob, err := s.EnqueueRevisionReview(ctx, base.ID, ReviewKindClaim)
	check(t, err)
	mustRunStoreJob(t, s, claimJob.ID)
	_, err = s.SaveClaimReviewOutput(ctx, claimJob.ID, &claimPassResult, "", "")
	check(t, err)
	styleJob, err := s.EnqueueRevisionReview(ctx, base.ID, ReviewKindStyle)
	check(t, err)
	mustRunStoreJob(t, s, styleJob.ID)
	_, err = s.SaveStyleReviewOutput(ctx, styleJob.ID, &stylePassResult, "", "")
	check(t, err)
	ready, err := s.EvaluateArticlePublicationReadiness(ctx, base.ID)
	check(t, err)
	if !ready.Ready {
		t.Fatalf("旧修订应可导出: %+v", ready)
	}

	const newMarkdown = "# R21\n\n来源说过这句话。\n新增的 Owner 观点。"
	rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 修订", newMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Origin != "owner" || rev.Version != base.Version+1 {
		t.Fatalf("revision=%+v", rev)
	}
	// 唯一片段继承：source 片段在旧/新正文各出现一次。
	entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
	check(t, err)
	if len(entries) != 1 || entries[0].Excerpt != "来源说过这句话。" || entries[0].ClaimKind != "source_claim" {
		t.Fatalf("应只继承唯一定位片段: %+v", entries)
	}
	// Owner 片段被改写 → 不继承。
	// 旧审校不复制：新修订无任何审校行。
	var claimReviews, styleReviews int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_reviews WHERE work_revision_id=?`, rev.ID).Scan(&claimReviews))
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=? AND kind='style'`, rev.ID).Scan(&styleReviews))
	if claimReviews != 0 || styleReviews != 0 {
		t.Fatalf("旧审校不得复制到新修订: claim=%d style=%d", claimReviews, styleReviews)
	}
	// readiness 重新 blocked。
	notReady, err := s.EvaluateArticlePublicationReadiness(ctx, rev.ID)
	check(t, err)
	if notReady.Ready {
		t.Fatalf("新修订不得借旧通过状态导出: %+v", notReady)
	}
	// 旧修订历史保持可查且仍 ready。
	oldReady, err := s.EvaluateArticlePublicationReadiness(ctx, base.ID)
	check(t, err)
	if !oldReady.Ready {
		t.Fatalf("旧修订历史应保持: %+v", oldReady)
	}
	// 重新运行本 revision claim+style 后恢复导出。
	claimJob2, err := s.EnqueueRevisionReview(ctx, rev.ID, ReviewKindClaim)
	check(t, err)
	mustRunStoreJob(t, s, claimJob2.ID)
	_, err = s.SaveClaimReviewOutput(ctx, claimJob2.ID, &claimPassResult, "", "")
	check(t, err)
	styleJob2, err := s.EnqueueRevisionReview(ctx, rev.ID, ReviewKindStyle)
	check(t, err)
	mustRunStoreJob(t, s, styleJob2.ID)
	_, err = s.SaveStyleReviewOutput(ctx, styleJob2.ID, &stylePassResult, "", "")
	check(t, err)
	restored, err := s.EvaluateArticlePublicationReadiness(ctx, rev.ID)
	check(t, err)
	if !restored.Ready {
		t.Fatalf("重新审校后应恢复导出: %+v", restored)
	}
}

// TestOwnerRevisionAmbiguityNotInherited R21：重复句（旧或新正文）、修改/删除/挪段
// 均不继承；EvidenceMap 兼容路径同一规则。
func TestOwnerRevisionAmbiguityNotInherited(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	cases := []struct {
		name        string
		newMarkdown string
		wantEntries int
	}{
		// base markdown: "# R20\n\n来源说过这句话。Owner 的判断在此。"
		// 新正文重复旧片段两次 → 歧义不继承。
		{"duplicate in new", "# R21\n\n来源说过这句话。来源说过这句话。", 0},
		// 删除片段 → 不继承。
		{"removed", "# R21\n\n全新内容。", 0},
		// 改写 source 片段 → source 不继承；未变的 Owner 片段仍唯一定位 → 继承。
		{"rewritten_source_only", "# R21\n\n来源说过这类话。Owner 的判断在此。", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21", tc.newMarkdown)
			check(t, err)
			entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
			check(t, err)
			if len(entries) != tc.wantEntries {
				t.Fatalf("entries=%d want %d: %+v", len(entries), tc.wantEntries, entries)
			}
			// EvidenceMap 兼容投影同样规则（本 fixture 无 evidence maps，确认零继承）。
			maps, err := s.ListEvidenceMaps(ctx, rev.ID)
			check(t, err)
			if len(maps) != 0 {
				t.Fatalf("maps=%+v", maps)
			}
			// 恢复 base 为 current，供下一子测试。
			check(t, s.restoreCurrent(ctx, draftID, base.ID))
		})
	}
	// 旧正文重复句场景：新正文把片段写两次等同于新正文不唯一（上面已覆盖）；
	// 这里构造“base 正文自身含重复片段”的修订：先保存一个含两次同一句的 Owner
	// 修订（无 ClaimMap 继承），再以该修订为 base 添加 ClaimMap 后编辑——
	// 唯一性在旧侧失效 → 不继承。
	dupBase, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "dup", "# dup\n\n重复句。重复句。")
	check(t, err)
	check(t, s.restoreCurrent(ctx, draftID, dupBase.ID))
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO claim_map_entries (id,draft_id,revision_id,excerpt,claim_kind,material_ids_json,source_title,citation_refs_json) VALUES (lower(hex(randomblob(16))),?,?,?,'source_claim','[]','来源','[]')`,
		draftID, dupBase.ID, "重复句。"); err != nil {
		t.Fatal(err)
	}
	rev2, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, dupBase.ID, "dup2", "# dup2\n\n重复句。")
	check(t, err)
	entries2, err := s.ListClaimMap(ctx, draftID, rev2.ID)
	check(t, err)
	if len(entries2) != 0 {
		t.Fatalf("旧正文重复片段不得继承: %+v", entries2)
	}
}

// TestOwnerRevisionDuplicateIdentityNotInherited R21：base ClaimMap 同 excerpt 两条
// 映射（身份歧义）→ 即使正文唯一定位也全部不继承；owner_claim 不得被 map 覆盖。
func TestOwnerRevisionDuplicateIdentityNotInherited(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	// 复制 source 条目：同 excerpt 两条映射，材料身份不同。
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO claim_map_entries (id,draft_id,revision_id,excerpt,claim_kind,material_ids_json,source_title,citation_refs_json)
		 SELECT lower(hex(randomblob(16))),draft_id,revision_id,excerpt,claim_kind,'["ghost"]',source_title,citation_refs_json
		 FROM claim_map_entries WHERE revision_id=? AND claim_kind='source_claim'`, base.ID); err != nil {
		t.Fatal(err)
	}
	rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 dup", "# R21\n\n来源说过这句话。")
	check(t, err)
	entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
	check(t, err)
	// 新正文只含 source 片段；其身份歧义 → 全部不继承（0 条）。
	if len(entries) != 0 {
		t.Fatalf("歧义 excerpt 全部不继承: %+v", entries)
	}
}

// TestOwnerRevisionStaleBaseConflict R21：过期 base 返回 ErrConflict 且不产生孤儿 revision。
func TestOwnerRevisionStaleBaseConflict(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	first, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v2", "# v2\n\n全新内容。")
	check(t, err)
	_, err = s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v3", "# v3\n\n另一个内容。")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("过期 base 必须 ErrConflict: %v", err)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&n))
	if n != 2 {
		t.Fatalf("冲突不得产生孤儿 revision: %d", n)
	}
	var current string
	check(t, s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_revision_id,'') FROM article_drafts WHERE id=?`, draftID).Scan(&current))
	if current != first.ID {
		t.Fatalf("current 被覆盖: %s", current)
	}
}

// TestOwnerRevisionConcurrentSameBase R21：两个手工请求同 base 恰一成功。
func TestOwnerRevisionConcurrentSameBase(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			markdown := "# v2\n\n并发内容 A。"
			if i == 1 {
				markdown = "# v2\n\n并发内容 B。"
			}
			_, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "v2", markdown)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("非预期错误: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("恰一成功一冲突: ok=%d conflict=%d", successes, conflicts)
	}
	var n int
	check(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&n))
	if n != 2 {
		t.Fatalf("只应有一个新 revision: %d", n)
	}
}

// TestFirstOwnerRevisionEmptyBaseRace R21：空 draft 首次修订（空基准）并发只一个成功。
func TestFirstOwnerRevisionEmptyBaseRace(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	check(t, err)
	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "空"})
	check(t, err)
	check(t, s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"))
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点"})
	check(t, err)
	check(t, s.ConfirmArticleBrief(ctx, brief.ID))
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "空草稿")
	check(t, err)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draft.ID, "", "第一版", "# 第一版")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("非预期错误: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("首次并发只应一个成功: %d", successes)
	}
}

// mustRunStoreJob / 共享结果（测试内部工具）。
func mustRunStoreJob(t *testing.T, s *Store, jobID string) {
	t.Helper()
	ok, err := s.MarkJobRunning(t.Context(), jobID)
	if err != nil || !ok {
		t.Fatalf("MarkJobRunning: %v %v", ok, err)
	}
}

// TestOwnerRevisionCanonicalDuplicateNotInherited R21：canonical claim_maps 同
// excerpt 重复（身份歧义）→ 该 excerpt 全部不继承，owner_claim 不得被任意覆盖。
func TestOwnerRevisionCanonicalDuplicateNotInherited(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	// 复制 canonical source 行（同 excerpt、不同 owner_claim）。
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO claim_maps (id,work_revision_id,claim_kind,excerpt,keypoint_ids_json,owner_claim,verified_fact_source_ids_json)
		 SELECT lower(hex(randomblob(16))),work_revision_id,claim_kind,excerpt,keypoint_ids_json,'伪造主张',verified_fact_source_ids_json
		 FROM claim_maps WHERE work_revision_id=? AND claim_kind='source_claim'`, base.ID); err != nil {
		t.Fatal(err)
	}
	rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 canonical dup", "# R21\n\n来源说过这句话。")
	check(t, err)
	entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
	check(t, err)
	// 新正文只有 source 片段（身份歧义→不继承）；owner 片段不在新正文 → 0 条。
	if len(entries) != 0 {
		t.Fatalf("canonical 歧义 excerpt 全部不继承: %+v", entries)
	}
	// 对照：移除重复 canonical 行后，唯一 canonical 的 owner_claim 血缘保留（非任意覆盖）。
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM claim_maps WHERE work_revision_id=? AND owner_claim='伪造主张'`, base.ID); err != nil {
		t.Fatal(err)
	}
	check(t, s.restoreCurrent(ctx, draftID, base.ID))
	rev2, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 clean", "# R21 clean\n\n来源说过这句话。Owner 的判断在此。")
	check(t, err)
	var ownerClaim string
	check(t, s.DB.QueryRowContext(ctx,
		`SELECT owner_claim FROM claim_maps WHERE work_revision_id=? AND excerpt='来源说过这句话。'`, rev2.ID).Scan(&ownerClaim))
	if ownerClaim != "Owner" {
		t.Fatalf("owner_claim 血缘应保留为确认值: %q", ownerClaim)
	}
}

// TestOwnerRevisionEvidenceDuplicateNotInherited R21：EvidenceMap 同 excerpt 重复
// （身份歧义）→ 全部不继承；唯一 excerpt 正常继承。
func TestOwnerRevisionEvidenceDuplicateNotInherited(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
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
	base, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧\n\n保留句。唯一句。", Origin: "owner"})
	check(t, err)
	// 同 excerpt 两条 EvidenceMap（身份歧义）。
	for i := 0; i < 2; i++ {
		_, err = s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: base.ID, Kind: models.EvidenceRhetorical, Excerpt: "保留句。", KeyPointIDs: "[]"})
		check(t, err)
	}
	// 唯一 excerpt 对照行。
	_, err = s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: base.ID, Kind: models.EvidenceRhetorical, Excerpt: "唯一句。", KeyPointIDs: "[]"})
	check(t, err)
	rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draft.ID, base.ID, "新", "# 新\n\n保留句。唯一句。")
	check(t, err)
	maps, err := s.ListEvidenceMaps(ctx, rev.ID)
	check(t, err)
	if len(maps) != 1 || maps[0].Excerpt != "唯一句。" {
		t.Fatalf("歧义 excerpt 全部不继承（唯一对照正常继承）: %+v", maps)
	}
}

// TestOwnerRevisionCanonicalIdentityDriftNotInherited R21（第二轮审计 1）：
// canonical 唯一但 kind/materials 身份与 entries 不一致 → 该 excerpt 不继承。
func TestOwnerRevisionCanonicalIdentityDriftNotInherited(t *testing.T) {
	s, draftID, base := ownerRevisionFixture(t)
	ctx := t.Context()
	// 篡改 canonical source 行的 claim_kind（与 entries 的 source_claim 漂移）。
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE claim_maps SET claim_kind='owner_claim' WHERE work_revision_id=? AND excerpt='来源说过这句话。'`, base.ID); err != nil {
		t.Fatal(err)
	}
	rev, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 drift", "# R21\n\n来源说过这句话。")
	check(t, err)
	entries, err := s.ListClaimMap(ctx, draftID, rev.ID)
	check(t, err)
	// 新正文只含 source 片段，其 canonical 身份漂移 → 不继承（owner 片段不在新正文）。
	if len(entries) != 0 {
		t.Fatalf("canonical 身份漂移 excerpt 不得继承: %+v", entries)
	}
	// 对照：materials 漂移同样阻断。
	check(t, s.restoreCurrent(ctx, draftID, base.ID))
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE claim_maps SET claim_kind='source_claim', keypoint_ids_json='["ghost"]' WHERE work_revision_id=? AND excerpt='来源说过这句话。'`, base.ID); err != nil {
		t.Fatal(err)
	}
	rev2, err := s.SaveOwnerRevisionWithClaimLineage(ctx, draftID, base.ID, "R21 drift2", "# R21b\n\n来源说过这句话。")
	check(t, err)
	entries2, err := s.ListClaimMap(ctx, draftID, rev2.ID)
	check(t, err)
	if len(entries2) != 0 {
		t.Fatalf("canonical materials 漂移 excerpt 不得继承: %+v", entries2)
	}
}
