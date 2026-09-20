package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// readyNewContractFixture 产出 ready 的新契约修订（claim+style durable passed）。
func readyNewContractFixture(t *testing.T, srv *Server, guid string) (*models.ArticleRevision, string) {
	t.Helper()
	rev, draftID, _ := seedNewContractRevision(t, srv, guid)
	claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	mustRunJob(t, srv, claimJob.ID)
	_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
	check(t, err)
	styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
	check(t, err)
	mustRunJob(t, srv, styleJob.ID)
	_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "p", "m")
	check(t, err)
	ready, err := srv.store.EvaluateArticlePublicationReadiness(t.Context(), rev.ID)
	check(t, err)
	if !ready.Ready {
		t.Fatalf("fixture 应 ready: %+v", ready)
	}
	return rev, draftID
}

// csrfFor 登录并返回 session + csrf。
func csrfFor(t *testing.T, srv *Server, email string) (*http.Cookie, string) {
	t.Helper()
	session := claimOwnerAndLogin(t, srv, email, "password123")
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench")
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench GET 未设置 CSRF cookie")
	}
	return session, csrf
}

// historyRows 读取某修订的创作历史行数。
func historyRows(t *testing.T, srv *Server, revisionID string) int {
	t.Helper()
	var n int
	check(t, srv.store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM creation_history WHERE article_revision_id=?`, revisionID).Scan(&n))
	return n
}

// TestPackageActionsR22：GET 绝不登记；显式 POST 登记并回显；
// blocked/stale POST 409；unpublished→published 单行升级。
func TestPackageActionsR22(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID := readyNewContractFixture(t, srv, "r22a")
	session, csrf := csrfFor(t, srv, "r22a@example.com")

	// GET（markdown 形式同样）不登记。
	getRec := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package")
	if getRec.Code != http.StatusOK {
		t.Fatalf("package GET: %d %s", getRec.Code, getRec.Body.String())
	}
	if historyRows(t, srv, rev.ID) != 0 {
		t.Fatal("GET 不得登记创作历史")
	}
	mdRec := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package?format=markdown")
	if mdRec.Code != http.StatusOK || historyRows(t, srv, rev.ID) != 0 {
		t.Fatal("markdown GET 不得登记创作历史")
	}

	// 显式 POST：保存为未发布 → 单行 + 页面回显。
	rec := postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
		"_csrf="+csrf+"&revision_id="+rev.ID+"&status=unpublished")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unpublished POST: %d %s", rec.Code, rec.Body.String())
	}
	if historyRows(t, srv, rev.ID) != 1 {
		t.Fatalf("登记后应一行: %d", historyRows(t, srv, rev.ID))
	}
	// 升级为 published：仍单行。
	rec = postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
		"_csrf="+csrf+"&revision_id="+rev.ID+"&status=published")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("published POST: %d %s", rec.Code, rec.Body.String())
	}
	if historyRows(t, srv, rev.ID) != 1 {
		t.Fatalf("升级后仍应一行: %d", historyRows(t, srv, rev.ID))
	}
	// 内容包页显示已登记状态。
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "已登记创作历史") {
		t.Fatalf("内容包页应显示已登记状态: %d", page.Code)
	}
	_ = draftID
}

// TestPackageActionGateR22：blocked/stale/来源失效 POST 一律 409。
func TestPackageActionGateR22(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		srv := newTestServer(t)
		rev, _, _ := seedNewContractRevision(t, srv, "r22b")
		session, csrf := csrfFor(t, srv, "r22b@example.com")
		rec := postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
			"_csrf="+csrf+"&revision_id="+rev.ID+"&status=published")
		if rec.Code != http.StatusConflict {
			t.Fatalf("blocked 必须 409: %d %s", rec.Code, rec.Body.String())
		}
		if historyRows(t, srv, rev.ID) != 0 {
			t.Fatal("blocked 不得登记")
		}
	})
	t.Run("source archived after reviews", func(t *testing.T) {
		srv := newTestServer(t)
		rev, _, kpA := seedNewContractRevision(t, srv, "r22l")
		claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
		check(t, err)
		mustRunJob(t, srv, claimJob.ID)
		_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
		check(t, err)
		styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
		check(t, err)
		mustRunJob(t, srv, styleJob.ID)
		_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "p", "m")
		check(t, err)
		// 审校完成后素材来源被归档：来源有效性动态重验必须阻断登记。
		kpRow, err := srv.store.GetKeyPoint(t.Context(), kpA)
		check(t, err)
		if _, err := srv.store.DB.ExecContext(t.Context(),
			`UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, kpRow.SourceID); err != nil {
			t.Fatal(err)
		}
		session, csrf := csrfFor(t, srv, "r22l@example.com")
		rec := postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
			"_csrf="+csrf+"&revision_id="+rev.ID+"&status=published")
		if rec.Code != http.StatusConflict {
			t.Fatalf("来源归档后登记必须 409: %d %s", rec.Code, rec.Body.String())
		}
		if historyRows(t, srv, rev.ID) != 0 {
			t.Fatal("来源归档后不得登记")
		}
	})
	t.Run("stale", func(t *testing.T) {
		srv := newTestServer(t)
		rev, draftID, _ := seedNewContractRevision(t, srv, "r22c")
		claimJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
		check(t, err)
		mustRunJob(t, srv, claimJob.ID)
		_, err = srv.store.SaveClaimReviewOutput(t.Context(), claimJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "p", "m")
		check(t, err)
		styleJob, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindStyle)
		check(t, err)
		mustRunJob(t, srv, styleJob.ID)
		_, err = srv.store.SaveStyleReviewOutput(t.Context(), styleJob.ID, &provider.StyleReviewResult{Status: "passed"}, "p", "m")
		check(t, err)
		// Owner 推进 current → rev 变 stale。
		ownerRev, err := srv.store.SaveOwnerRevisionWithClaimLineage(t.Context(), draftID, rev.ID, "v2", "# v2\n\n全新。")
		check(t, err)
		session, csrf := csrfFor(t, srv, "r22c@example.com")
		rec := postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
			"_csrf="+csrf+"&revision_id="+rev.ID+"&status=published")
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale 必须 409: %d", rec.Code)
		}
		// 新 current 未审校 → 同样 409。
		rec = postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
			"_csrf="+csrf+"&revision_id="+ownerRev.ID+"&status=published")
		if rec.Code != http.StatusConflict {
			t.Fatalf("未审校 current 必须 409: %d", rec.Code)
		}
		if historyRows(t, srv, rev.ID) != 0 || historyRows(t, srv, ownerRev.ID) != 0 {
			t.Fatal("stale/未审校均不得登记")
		}
	})
}

// TestRevisionAnchorAndHistoryLink R22：修订历史节点稳定 anchor；工作台历史
// 链接到确切 draft+revision。
func TestRevisionAnchorAndHistoryLink(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID := readyNewContractFixture(t, srv, "r22d")
	if _, err := srv.store.RecordArticleHistory(t.Context(), rev.ID, "published"); err != nil {
		t.Fatal(err)
	}
	session, _ := csrfFor(t, srv, "r22d@example.com")
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draftID)
	if page.Code != http.StatusOK {
		t.Fatalf("draft page: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), `id="revision-`+rev.ID+`"`) {
		t.Fatalf("修订历史节点缺少稳定 anchor revision-%s", rev.ID)
	}
	wb := doWithCookie(srv, session, http.MethodGet, "/workbench")
	if !strings.Contains(wb.Body.String(), "/workbench/drafts/"+draftID+"#revision-"+rev.ID) {
		t.Fatalf("工作台历史应链接确切修订 anchor")
	}
}

// TestDashboardNextStepsR22：继续听/继续写/失败任务精确 href；GET 只读。
func TestDashboardNextStepsR22(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID := readyNewContractFixture(t, srv, "r22e")
	// drafting/reviewing 草稿（fixture draft 状态 reviewing）。
	// 失败任务：claim review job 失败（source_id=draft）。
	job, err := srv.store.EnqueueRevisionReview(t.Context(), rev.ID, store.ReviewKindClaim)
	check(t, err)
	check(t, srv.store.MarkJobFailed(t.Context(), job.ID, "来源策略禁止外发"))
	// 继续听进度：真实 episode（无 DJ plan → 精确原音来源页 href）。
	ctx := t.Context()
	pod, err := srv.store.CreatePodcast(ctx, "https://r22e.example/feed", "R22E", "", "")
	check(t, err)
	if _, err := srv.store.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "r22e-ep", Title: "R22E 单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(ctx, pod.ID)
	check(t, err)
	listenEp := eps[0].ID
	check(t, srv.store.SaveListeningProgress(t.Context(), &models.ListeningProgress{
		SourceType: models.SourceEpisode, SourceID: listenEp, PlanID: "", ItemOffsetSeconds: 42,
	}))

	session := claimOwnerAndLogin(t, srv, "r22e@example.com", "password123")
	countsBefore := map[string]int{
		"processing_jobs_failed": countRows(t, srv, `SELECT COUNT(*) FROM processing_jobs WHERE status='failed'`),
		"history":                countRows(t, srv, `SELECT COUNT(*) FROM creation_history`),
		"listening":              countRows(t, srv, `SELECT COUNT(*) FROM listening_progress`),
	}
	page := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	if page.Code != http.StatusOK {
		t.Fatalf("dashboard: %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		"继续听", "继续写：", "/workbench/drafts/" + draftID,
		"失败任务：claim_review", "来源策略禁止外发",
		"/sources/episode/" + listenEp,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard 缺少 %q", want)
		}
	}
	countsAfter := map[string]int{
		"processing_jobs_failed": countRows(t, srv, `SELECT COUNT(*) FROM processing_jobs WHERE status='failed'`),
		"history":                countRows(t, srv, `SELECT COUNT(*) FROM creation_history`),
		"listening":              countRows(t, srv, `SELECT COUNT(*) FROM listening_progress`),
	}
	for k := range countsBefore {
		if countsBefore[k] != countsAfter[k] {
			t.Fatalf("GET 改变了 %s: %d → %d", k, countsBefore[k], countsAfter[k])
		}
	}
}

func countRows(t *testing.T, srv *Server, query string) int {
	t.Helper()
	var n int
	check(t, srv.store.DB.QueryRowContext(t.Context(), query).Scan(&n))
	return n
}

// TestSourcePageIsolationR22：真实 A/B 两来源（共享 Segment ID + 各自 digest +
// A 的 claim-map 文章修订），来源页分别只显示各自的精读/文章，B 不出现 A。
func TestSourcePageIsolationR22(t *testing.T) {
	srv := newTestServer(t)
	rev, draftIDA, kpA := seedNewContractRevision(t, srv, "r22g")
	ctx := t.Context()
	// 读取 A 的来源与真实 citation。
	kpRow, err := srv.store.GetKeyPoint(ctx, kpA)
	check(t, err)
	epA := kpRow.SourceID
	var sharedSegs []string
	check(t, json.Unmarshal([]byte(kpRow.CitationsJSON), &sharedSegs))
	sharedSeg := sharedSegs[0]
	// A 的 digest（引用共享 Segment）。
	_, err = srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epA, Title: "A 来源页精读",
	}, []models.DigestBlock{{Position: 0, Type: "summary", Text: "A 摘要", Citations: []string{sharedSeg}}})
	check(t, err)
	// 来源 B：同 Segment ID + 自己的 digest。
	pod, err := srv.store.CreatePodcast(ctx, "https://r22g-b.example/feed", "B", "", "")
	check(t, err)
	if _, err := srv.store.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "r22g-b", Title: "B", AudioURL: "https://b.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(ctx, pod.ID)
	check(t, err)
	epB := eps[0].ID
	tJob, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, epB, models.JobTranscribe)
	check(t, err)
	tv, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epB, store.KindTranscript, "t", "t", "1", tJob.ID,
		`{"language":"zh","text":"B","segments":[{"id":"`+sharedSeg+`","start":0,"end":1,"text":"B"}]}`)
	check(t, err)
	if _, err := srv.store.MarkJobRunning(ctx, tJob.ID); err != nil {
		t.Fatal(err)
	}
	check(t, srv.store.MarkJobSucceeded(ctx, tJob.ID))
	check(t, srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epB, store.KindTranscript, tv))
	card := &provider.KnowledgeCard{Title: "B", Summary: provider.CitedText{Text: "B", Citations: []string{sharedSeg}}, KeyPoints: []provider.KeyPoint{{Content: "B", Citations: []string{sharedSeg}}}}
	if _, err := srv.store.IndexKeyPoints(ctx, models.SourceEpisode, epB, "B 来源", 1, card, []provider.Segment{{ID: sharedSeg, Start: 0, End: 1, Text: "B"}}); err != nil {
		t.Fatal(err)
	}
	_, err = srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epB, Title: "B 来源页精读",
	}, []models.DigestBlock{{Position: 0, Type: "summary", Text: "B 摘要", Citations: []string{sharedSeg}}})
	check(t, err)

	session := claimOwnerAndLogin(t, srv, "r22g@example.com", "password123")
	exactALink := "/workbench/drafts/" + draftIDA + "#revision-" + rev.ID
	pageA := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+epA)
	if pageA.Code != http.StatusOK {
		t.Fatalf("A 来源页: %d", pageA.Code)
	}
	if !strings.Contains(pageA.Body.String(), "A 来源页精读") {
		t.Fatalf("A 来源页应显示 A digest")
	}
	if !strings.Contains(pageA.Body.String(), exactALink) {
		t.Fatalf("A 来源页应包含确切文章链接 %s", exactALink)
	}
	if strings.Contains(pageA.Body.String(), "B 来源页精读") {
		t.Fatal("A 来源页不得显示 B 的 digest")
	}
	pageB := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+epB)
	if pageB.Code != http.StatusOK {
		t.Fatalf("B 来源页: %d", pageB.Code)
	}
	if !strings.Contains(pageB.Body.String(), "B 来源页精读") {
		t.Fatalf("B 来源页应显示 B digest")
	}
	if strings.Contains(pageB.Body.String(), "A 来源页精读") {
		t.Fatal("B 来源页不得显示 A 的 digest")
	}
	if strings.Contains(pageB.Body.String(), exactALink) {
		t.Fatal("B 来源页不得包含 A 的确切文章链接")
	}
}

// TestActualSourcesOnArticlePage R22（审计 8）：文章页"实际来源"精确链接——
// 新契约 claim_map 材料去重；OwnerNote ID 不得被当作 KeyPoint 伪造来源。
func TestActualSourcesOnArticlePage(t *testing.T) {
	srv := newTestServer(t)
	rev, draftID, kpA := seedNewContractRevision(t, srv, "r22h")
	ctx := t.Context()
	kpRow, err := srv.store.GetKeyPoint(ctx, kpA)
	check(t, err)
	// 数据库中真实存在的 OwnerNote + 重复 KeyPoint ID 一同写入 claim-map materials：
	// note ID 不得产生伪造来源链接，KeyPoint 来源去重为一个链接。
	note, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(kpRow.SourceType), SourceID: kpRow.SourceID,
		Kind: "owner_reflection", Content: "真实存在的个人笔记",
		CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	check(t, err)
	var sourceEntryID string
	check(t, srv.store.DB.QueryRowContext(ctx,
		`SELECT id FROM claim_map_entries WHERE revision_id=? AND claim_kind='source_claim'`, rev.ID).Scan(&sourceEntryID))
	materialsJSON, err := json.Marshal([]string{kpA, kpA, note.ID})
	check(t, err)
	_, err = srv.store.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json=? WHERE id=?`, string(materialsJSON), sourceEntryID)
	check(t, err)
	session := claimOwnerAndLogin(t, srv, "r22h@example.com", "password123")
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draftID)
	if page.Code != http.StatusOK {
		t.Fatalf("draft page: %d", page.Code)
	}
	body := page.Body.String()
	want := "/sources/" + string(kpRow.SourceType) + "/" + kpRow.SourceID
	if !strings.Contains(body, want) {
		t.Fatalf("文章页应显示实际来源链接 %s", want)
	}
	// 精确匹配完整 href（note 锚点链接带有 #note- 后缀，不算重复来源链接）。
	wantExact := want + "\""
	if strings.Count(body, wantExact) != 1 {
		t.Fatalf("同来源必须去重: %d", strings.Count(body, wantExact))
	}
	// note ID 必须以独立笔记锚点链接出现（不是伪造来源链接）。
	wantNoteLink := want + "#note-" + note.ID
	if !strings.Contains(body, wantNoteLink) {
		t.Fatalf("应显示笔记锚点链接 %s", wantNoteLink)
	}
	if strings.Contains(body, "/sources/"+string(kpRow.SourceType)+"/"+note.ID) ||
		strings.Contains(body, "/documents/"+note.ID) {
		t.Fatal("note ID 不得被拼成伪造来源链接")
	}
}

// TestActualSourcesLegacyEvidencePath R22：旧文章 evidence_map keypoint 精确链接。
func TestActualSourcesLegacyEvidencePath(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	check(t, err)
	pod, err := srv.store.CreatePodcast(ctx, "https://legacy.example/feed", "L", "", "")
	check(t, err)
	if _, err := srv.store.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "legacy", Title: "L", AudioURL: "https://l.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(ctx, pod.ID)
	check(t, err)
	epID := eps[0].ID
	proposal, err := srv.store.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Title: "旧"})
	check(t, err)
	check(t, srv.store.SetArticleProposalStatus(ctx, proposal.ID, "accepted"))
	brief, err := srv.store.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: "旧论点"})
	check(t, err)
	check(t, srv.store.ConfirmArticleBrief(ctx, brief.ID))
	draft, err := srv.store.CreateArticleDraft(ctx, brief.ID, "旧文章")
	check(t, err)
	// 一个真实 keypoint（evidence map 引用它）。
	job, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, epID, models.JobTranscribe)
	check(t, err)
	v, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, "t", "t", "1", job.ID,
		`{"language":"zh","text":"L","segments":[{"id":"seg-l","start":0,"end":1,"text":"L"}]}`)
	check(t, err)
	if _, err := srv.store.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	check(t, srv.store.MarkJobSucceeded(ctx, job.ID))
	check(t, srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, v))
	card := &provider.KnowledgeCard{Title: "L", Summary: provider.CitedText{Text: "L 内容", Citations: []string{"seg-l"}}, KeyPoints: []provider.KeyPoint{{Content: "L 内容", Citations: []string{"seg-l"}}}}
	if _, err := srv.store.IndexKeyPoints(ctx, models.SourceEpisode, epID, "L 来源", 1, card, []provider.Segment{{ID: "seg-l", Start: 0, End: 1, Text: "L"}}); err != nil {
		t.Fatal(err)
	}
	kps, _, err := srv.store.ListKeyPointsFiltered(ctx, store.KeyPointFilter{SourceID: epID}, 1, 10)
	check(t, err)
	kpID := kps[0].ID
	rev, err := srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "旧文章", Markdown: "# 旧\n\n保留句。", Origin: "owner"})
	check(t, err)
	_, err = srv.store.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: rev.ID, Kind: models.EvidenceRhetorical, Excerpt: "保留句。", KeyPointIDs: `["` + kpID + `"]`})
	check(t, err)
	session := claimOwnerAndLogin(t, srv, "r22i@example.com", "password123")
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID)
	if page.Code != http.StatusOK {
		t.Fatalf("legacy draft page: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "/sources/episode/"+epID) {
		t.Fatalf("旧文章页应显示 evidence 来源链接")
	}
}

// TestSourcePageUsageErrorR22（审计 4）：损坏的 claim-map JSON 触发 json_each
// 错误时，来源页必须 500，不得静默空状态。
func TestSourcePageUsageErrorR22(t *testing.T) {
	srv := newTestServer(t)
	rev, _, kpA := seedNewContractRevision(t, srv, "r22j")
	ctx := t.Context()
	kpRow, err := srv.store.GetKeyPoint(ctx, kpA)
	check(t, err)
	_, err = srv.store.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json='{broken' WHERE revision_id=?`, rev.ID)
	check(t, err)
	session := claimOwnerAndLogin(t, srv, "r22j@example.com", "password123")
	page := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+kpRow.SourceID)
	if page.Code != http.StatusInternalServerError {
		t.Fatalf("损坏 ClaimMap 必须显式 500: %d", page.Code)
	}
}

// TestPackageDownloadNoHistoryAndFlashGuard R22（审计 5）：
//   - plain/cover 下载同样不写历史；
//   - ?history= 不得伪造"刚刚完成登记"（参数必须等于数据库真实状态）；
//   - published 行 POST unpublished：仍显示 published，不显示降级 flash。
func TestPackageDownloadNoHistoryAndFlashGuard(t *testing.T) {
	srv := newTestServer(t)
	rev, _ := readyNewContractFixture(t, srv, "r22k")
	session, csrf := csrfFor(t, srv, "r22k@example.com")

	// plain / cover 下载不写历史。
	for _, suffix := range []string{"?format=plain", "?format=cover-svg"} {
		rec := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package"+suffix)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s download: %d", suffix, rec.Code)
		}
	}
	if historyRows(t, srv, rev.ID) != 0 {
		t.Fatal("plain/cover 下载不得写历史")
	}

	// 显式登记 published。
	rec := postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
		"_csrf="+csrf+"&revision_id="+rev.ID+"&status=published")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("published POST: %d", rec.Code)
	}
	// published 行 POST unpublished：不被降级。
	rec = postReviewForm(t, srv, session, csrf, "/workbench/record-article-history",
		"_csrf="+csrf+"&revision_id="+rev.ID+"&status=unpublished")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unpublished POST: %d", rec.Code)
	}
	var status string
	check(t, srv.store.DB.QueryRowContext(t.Context(),
		`SELECT status FROM creation_history WHERE article_revision_id=?`, rev.ID).Scan(&status))
	if status != "published" {
		t.Fatalf("published 不得被降级: %s", status)
	}
	// 重定向携带 ?history=unpublished，但页面不得伪造降级 flash，仍显示真实 published。
	page := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package?history=unpublished")
	body := page.Body.String()
	if strings.Contains(body, "刚刚完成登记：unpublished") {
		t.Fatal("不得伪造降级 flash")
	}
	if !strings.Contains(body, "已登记创作历史：<strong>published</strong>") {
		t.Fatalf("页面必须显示数据库真实状态 published")
	}
	// 合法 flash：参数与真实状态一致时显示。
	page2 := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+rev.ID+"/package?history=published")
	if !strings.Contains(page2.Body.String(), "刚刚完成登记：published") {
		t.Fatal("真实状态一致的 flash 应显示")
	}
}

// TestPackageHistoryReadErrorR22（审计 3）：readiness/来源检查通过后，读取
// creation_history 发生真实数据库错误（删表）→ 内容包页必须 500，不得静默
// 显示为未登记。
func TestPackageHistoryReadErrorR22(t *testing.T) {
	srv := newTestServer(t)
	rev, _ := readyNewContractFixture(t, srv, "r22m")
	// 请求会通过 readiness/source checks，随后读 creation_history 时表不存在 → 500。
	if _, err := srv.store.DB.ExecContext(t.Context(), `DROP TABLE creation_history`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/workbench/revisions/"+rev.ID+"/package", nil)
	rec := httptest.NewRecorder()
	srv.handlePublicationPackage(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("历史读取错误必须 500: %d %s", rec.Code, rec.Body.String())
	}
}

// TestActualSourcesDocumentNotePath R22（审计 2）：document 笔记锚点链接路径
// （/documents/<docID>#note-<noteID>），直接验证 helper。
func TestActualSourcesDocumentNotePath(t *testing.T) {
	srv := newTestServer(t)
	rev, _, _ := seedNewContractRevision(t, srv, "r22n")
	ctx := t.Context()
	doc, err := srv.store.CreatePDFDocument(ctx, "R22N 文档", "r22n.pdf", "文档内容")
	check(t, err)
	note, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceDocument), SourceID: doc.ID,
		Kind: "owner_reflection", Content: "文档笔记",
		CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	check(t, err)
	if _, err := srv.store.DB.ExecContext(ctx,
		`UPDATE claim_map_entries SET material_ids_json=? WHERE revision_id=?`,
		`["`+note.ID+`"]`, rev.ID); err != nil {
		t.Fatal(err)
	}
	srcs, err := srv.actualSourcesForRevision(ctx, rev)
	check(t, err)
	if len(srcs) != 1 || srcs[0].Href != "/documents/"+doc.ID+"#note-"+note.ID {
		t.Fatalf("document 笔记锚点链接不符: %+v", srcs)
	}
}
