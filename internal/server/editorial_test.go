package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestWorkbenchCreatesAndRendersEditorialProfile(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "editor@example.com", "password123")

	get := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), "内容工作台") {
		t.Fatalf("workbench should render: status=%d body=%q", getRec.Code, getRec.Body.String())
	}
	csrf := ""
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench GET should issue a CSRF cookie")
	}

	post := httptest.NewRequest(http.MethodPost, "/workbench/profiles",
		strings.NewReader("_csrf="+csrf+"&name=%E6%8A%80%E6%9C%AF%E5%91%A8%E5%88%8A&source_attribution=standard&monthly_budget_cents=1200"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther || !strings.HasPrefix(postRec.Header().Get("Location"), "/workbench?profile=") {
		t.Fatalf("profile creation should redirect to selected workbench: status=%d location=%q", postRec.Code, postRec.Header().Get("Location"))
	}

	profiles, err := srv.store.ListEditorialProfiles(post.Context())
	if err != nil || len(profiles) != 1 || profiles[0].Name != "技术周刊" || profiles[0].MonthlyBudgetCents == nil || *profiles[0].MonthlyBudgetCents != 1200 {
		t.Fatalf("profile must persist: profiles=%+v err=%v", profiles, err)
	}
}

func TestEditorialProfileCreationRejectsInvalidBudget(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "editor@example.com", "password123")
	get := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	var csrf string
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := httptest.NewRequest(http.MethodPost, "/workbench/profiles", strings.NewReader("_csrf="+csrf+"&name=x&monthly_budget_cents=-1"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("negative budget should be rejected: status=%d", postRec.Code)
	}
}

func TestKeyPointStatusAPIUpdatesInboxState(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "inbox@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "Tech Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode-1", Title: "Episode", AudioURL: "https://cdn.example.com/ep.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "Episode", 1,
		&provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "素材", Citations: []string{"seg-1"}}}},
		[]provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, err := srv.store.ListKeyPoints(t.Context(), 1, 10)
	if err != nil || len(keyPoints) != 1 {
		t.Fatalf("seed keypoint: keypoints=%+v err=%v", keyPoints, err)
	}

	get := httptest.NewRequest(http.MethodGet, "/keypoints", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	var csrf string
	for _, cookie := range getRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/keypoints/status", strings.NewReader("_csrf="+csrf+"&keypoint_id="+keyPoints[0].ID+"&status=shortlisted"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	request.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	recorder := httptest.NewRecorder()
	srv.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status update should succeed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated, err := srv.store.GetKeyPoint(t.Context(), keyPoints[0].ID)
	if err != nil || updated.ProductionStatus != models.KeyPointShortlisted {
		t.Fatalf("updated state should persist: keypoint=%+v err=%v", updated, err)
	}
}

func TestKeyPointStatusAPIRejectsWrongMethodAndInvalidState(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "inbox@example.com", "password123")
	get := httptest.NewRequest(http.MethodGet, "/api/keypoints/status", nil)
	get.AddCookie(session)
	getRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(getRec, get)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status API must reject GET: %d", getRec.Code)
	}

	page := httptest.NewRequest(http.MethodGet, "/keypoints", nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := httptest.NewRequest(http.MethodPost, "/api/keypoints/status", strings.NewReader("_csrf="+csrf+"&keypoint_id=missing&status=wrong"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status must be rejected: %d", postRec.Code)
	}
}

func TestWorkbenchSourceScopeGrantAndRevoke(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "scope@example.com", "password123")
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if err != nil {
		t.Fatal(err)
	}
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "Pod", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "Episode", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)

	page := httptest.NewRequest(http.MethodGet, "/workbench?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := httptest.NewRequest(http.MethodPost, "/workbench/sources", strings.NewReader("_csrf="+csrf+"&profile_id="+profile.ID+"&source_type=episode&source_id="+episodes[0].ID))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("scope grant should redirect: %d", postRec.Code)
	}
	inScope, err := srv.store.IsSourceInScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID)
	if err != nil || !inScope {
		t.Fatalf("scope grant should persist: inScope=%v err=%v", inScope, err)
	}
	post = httptest.NewRequest(http.MethodPost, "/workbench/sources", strings.NewReader("_csrf="+csrf+"&profile_id="+profile.ID+"&source_type=episode&source_id="+episodes[0].ID+"&action=revoke"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	postRec = httptest.NewRecorder()
	srv.Router().ServeHTTP(postRec, post)
	inScope, err = srv.store.IsSourceInScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID)
	if err != nil || inScope {
		t.Fatalf("scope revoke should persist: inScope=%v err=%v", inScope, err)
	}
}

func TestWorkbenchProposalAndBriefAuthorizationFlow(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "proposal-flow@example.com", "password123")
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌", TargetAudience: "开发者"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("workbench should issue CSRF cookie")
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/workbench/proposals", "profile_id="+profile.ID+"&kind=evergreen&title=%E5%AE%A1%E6%9F%A5%E6%88%90%E6%9C%AC&thesis=%E6%95%88%E7%8E%87%E9%9C%80%E8%A6%81%E5%AE%A1%E6%9F%A5&candidate_keypoints=%5B%5D"); rec.Code != http.StatusSeeOther {
		t.Fatalf("proposal should be created: %d", rec.Code)
	}
	proposals, err := srv.store.ListArticleProposals(t.Context(), profile.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposal should persist: proposals=%+v err=%v", proposals, err)
	}
	proposal := proposals[0]
	if rec := post("/workbench/proposals/status", "profile_id="+profile.ID+"&proposal_id="+proposal.ID+"&status=accepted"); rec.Code != http.StatusSeeOther {
		t.Fatalf("proposal should be accepted: %d", rec.Code)
	}
	if rec := post("/workbench/briefs", "profile_id="+profile.ID+"&proposal_id="+proposal.ID+"&thesis=%E6%95%88%E7%8E%87%E9%9C%80%E8%A6%81%E5%AE%A1%E6%9F%A5&outline=%23+%E6%A0%87%E9%A2%98&material_plan=%5B%5D&conflict_plan=%5B%5D&target_length=1200"); rec.Code != http.StatusSeeOther {
		t.Fatalf("brief should be created: %d", rec.Code)
	}
	briefs, err := srv.store.ListArticleBriefs(t.Context(), profile.ID)
	if err != nil || len(briefs) != 1 || briefs[0].TargetLength == nil || *briefs[0].TargetLength != 1200 {
		t.Fatalf("brief should persist: briefs=%+v err=%v", briefs, err)
	}
	if rec := post("/workbench/briefs/confirm", "profile_id="+profile.ID+"&brief_id="+briefs[0].ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("brief should be confirmed: %d", rec.Code)
	}
	confirmed, err := srv.store.GetArticleBrief(t.Context(), briefs[0].ID)
	if err != nil || confirmed.Status != "confirmed" {
		t.Fatalf("brief confirmation should persist: brief=%+v err=%v", confirmed, err)
	}
}

func TestEditorialWorkflowHandlersRejectWrongMethodAndInvalidInput(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "proposal-errors@example.com", "password123")
	for _, path := range []string{"/workbench/proposals", "/workbench/proposals/status", "/workbench/briefs", "/workbench/briefs/confirm"} {
		if rec := doWithCookie(srv, session, http.MethodGet, path); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s should reject GET: %d", path, rec.Code)
		}
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	for _, request := range []struct{ path, body string }{
		{"/workbench/proposals", "profile_id=missing&kind=fresh&title=&candidate_keypoints=%5B%5D"},
		{"/workbench/proposals/status", "profile_id=missing&proposal_id=missing&status=wrong"},
		{"/workbench/briefs", "profile_id=missing&proposal_id=missing&target_length=0"},
		{"/workbench/briefs/confirm", "profile_id=missing&brief_id=missing"},
	} {
		if rec := post(request.path, request.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should reject invalid input: %d", request.path, rec.Code)
		}
	}
}

func TestWorkbenchDraftRevisionAndEvidenceGateFlow(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "draft-flow@example.com", "password123")
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	brief, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ConfirmArticleBrief(t.Context(), brief.ID); err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/workbench/drafts", "brief_id="+brief.ID+"&title=%E6%96%87%E7%AB%A0"); rec.Code != http.StatusSeeOther {
		t.Fatalf("draft should be created: %d", rec.Code)
	}
	drafts, err := srv.store.ListArticleDrafts(t.Context(), profile.ID)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("draft should persist: drafts=%+v err=%v", drafts, err)
	}
	draft := drafts[0]
	if rec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID); rec.Code != http.StatusOK {
		t.Fatalf("empty draft detail should render: %d", rec.Code)
	}
	if rec := post("/workbench/revisions", "draft_id="+draft.ID+"&title=%E6%96%87%E7%AB%A0&markdown=%23+%E7%AC%AC%E4%B8%80%E7%89%88"); rec.Code != http.StatusSeeOther {
		t.Fatalf("revision should be created: %d", rec.Code)
	}
	revisions, err := srv.store.ListArticleRevisions(t.Context(), draft.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Origin != "owner" {
		t.Fatalf("owner revision should persist: revisions=%+v err=%v", revisions, err)
	}
	firstRevisionID := revisions[0].ID
	if rec := post("/workbench/revisions", "draft_id="+draft.ID+"&title=%E6%96%87%E7%AB%A0&markdown=%23+%E7%AC%AC%E4%BA%8C%E7%89%88"); rec.Code != http.StatusSeeOther {
		t.Fatalf("second revision should be created: %d", rec.Code)
	}
	revisions, err = srv.store.ListArticleRevisions(t.Context(), draft.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("two revisions should persist: revisions=%+v err=%v", revisions, err)
	}
	compareRec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID+"?compare_from="+firstRevisionID+"&compare_to="+revisions[0].ID)
	if compareRec.Code != http.StatusOK || !strings.Contains(compareRec.Body.String(), "修订对比") || !strings.Contains(compareRec.Body.String(), "v1 → v2") {
		t.Fatalf("draft should render a revision diff: status=%d body=%q", compareRec.Code, compareRec.Body.String())
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID+"?compare_from="+firstRevisionID); rec.Code != http.StatusBadRequest {
		t.Fatalf("incomplete comparison should reject: %d", rec.Code)
	}
	if rec := post("/workbench/reviews", "revision_id="+revisions[0].ID+"&kind=evidence&status=passed&issues=%5B%5D"); rec.Code != http.StatusSeeOther {
		t.Fatalf("evidence review should be recorded: %d", rec.Code)
	}
	if rec := post("/workbench/reviews", "revision_id="+revisions[0].ID+"&kind=style&status=advisory&issues=%5B%22%E5%87%8F%E5%B0%91%E5%A5%97%E8%AF%9D%22%5D"); rec.Code != http.StatusSeeOther {
		t.Fatalf("style review should be recorded: %d", rec.Code)
	}
	detailRec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "审校记录") || !strings.Contains(detailRec.Body.String(), "减少套话") || !strings.Contains(detailRec.Body.String(), "公众号预览") || !strings.Contains(detailRec.Body.String(), "打开公众号内容包") {
		t.Fatalf("draft should display review history: status=%d body=%q", detailRec.Code, detailRec.Body.String())
	}
	if _, err := srv.store.DB.Exec(`UPDATE article_reviews SET issues_json='not-json' WHERE revision_id=? AND kind='style'`, revisions[0].ID); err != nil {
		t.Fatal(err)
	}
	detailRec = doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"+draft.ID)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "审校记录格式异常") {
		t.Fatalf("draft should tolerate malformed historic issues: status=%d body=%q", detailRec.Code, detailRec.Body.String())
	}
	ready, err := srv.store.IsRevisionReadyForPublication(t.Context(), revisions[0].ID)
	if err != nil || !ready {
		t.Fatalf("passed evidence review should unlock revision: ready=%v err=%v", ready, err)
	}
}

func TestDraftWorkflowHandlersRejectWrongMethodAndInvalidInput(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "draft-errors@example.com", "password123")
	for _, path := range []string{"/workbench/drafts", "/workbench/revisions", "/workbench/reviews"} {
		if rec := doWithCookie(srv, session, http.MethodGet, path); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s should reject GET: %d", path, rec.Code)
		}
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/"); rec.Code != http.StatusNotFound {
		t.Fatalf("draft detail without an ID should be 404: %d", rec.Code)
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench", nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	for _, request := range []struct{ path, body string }{
		{"/workbench/drafts", "brief_id=missing&title=x"},
		{"/workbench/revisions", "draft_id=missing&title=x&markdown="},
		{"/workbench/reviews", "revision_id=missing&kind=wrong&status=passed&issues=%5B%5D"},
	} {
		if rec := post(request.path, request.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should reject invalid input: %d", request.path, rec.Code)
		}
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/workbench/drafts/missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing draft should be 404: %d", rec.Code)
	}
}

func TestWriterCreatesEvidenceMappedImmutableRevision(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "writer-flow@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "效率也会带来审查成本", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if err := srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "审查成本", CandidateKeyPoints: "[\"" + keyPoints[0].ID + "\"]"})
	if err := srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	brief, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "效率也会带来审查成本", Outline: "# 结构", MaterialPlan: "[\"" + keyPoints[0].ID + "\"]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ConfirmArticleBrief(t.Context(), brief.ID); err != nil {
		t.Fatal(err)
	}
	writer := &fakeArticleWriter{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: writer}, nil
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/workbench/write", strings.NewReader("_csrf="+csrf+"&brief_id="+brief.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || len(writer.requests) != 1 || len(writer.requests[0].Materials) != 1 {
		t.Fatalf("writer should run with selected materials: code=%d requests=%+v", rec.Code, writer.requests)
	}
	drafts, _ := srv.store.ListArticleDrafts(t.Context(), profile.ID)
	if len(drafts) != 1 {
		t.Fatalf("writer should create one draft: %+v", drafts)
	}
	revisions, _ := srv.store.ListArticleRevisions(t.Context(), drafts[0].ID)
	if len(revisions) != 1 || revisions[0].Origin != "writer" || revisions[0].Provider == nil || *revisions[0].Provider != "fake-writer" {
		t.Fatalf("writer revision metadata should persist: %+v", revisions)
	}
	var maps int
	if err := srv.store.DB.QueryRow(`SELECT COUNT(*) FROM evidence_maps WHERE revision_id=?`, revisions[0].ID).Scan(&maps); err != nil || maps != 1 {
		t.Fatalf("writer evidence map should persist: count=%d err=%v", maps, err)
	}
	callRevise := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/workbench/revise", strings.NewReader("_csrf="+csrf+"&revision_id="+id))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	methodRec := httptest.NewRecorder()
	srv.handleArticleRevisionWriterRun(methodRec, httptest.NewRequest(http.MethodGet, "/workbench/revise", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should reject: %d", methodRec.Code)
	}
	if rec := callRevise("missing"); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing revision should reject: %d", rec.Code)
	}
	if rec := callRevise(revisions[0].ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("revision without feedback should reject: %d", rec.Code)
	}
	reviewer := &fakeEvidenceReviewer{status: "passed"}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{EvidenceReviewer: reviewer}, nil
	}
	reviewReq := httptest.NewRequest(http.MethodPost, "/workbench/reviews/evidence", strings.NewReader("_csrf="+csrf+"&revision_id="+revisions[0].ID))
	reviewReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reviewReq.AddCookie(session)
	reviewReq.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	reviewRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(reviewRec, reviewReq)
	if reviewRec.Code != http.StatusSeeOther || len(reviewer.requests) != 1 || len(reviewer.requests[0].Items) != 1 {
		t.Fatalf("independent evidence review should run against mappings: code=%d requests=%+v", reviewRec.Code, reviewer.requests)
	}
	styleEditor := &fakeStyleEditor{status: "advisory", issues: []string{"标题可以更具体"}}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{StyleEditor: styleEditor}, nil
	}
	styleReq := httptest.NewRequest(http.MethodPost, "/workbench/reviews/style", strings.NewReader("_csrf="+csrf+"&revision_id="+revisions[0].ID))
	styleReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	styleReq.AddCookie(session)
	styleReq.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	styleRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(styleRec, styleReq)
	if styleRec.Code != http.StatusSeeOther || len(styleEditor.requests) != 1 || styleEditor.requests[0].TargetAudience != "" {
		t.Fatalf("style review should receive exact revision/profile constraints: code=%d requests=%+v", styleRec.Code, styleEditor.requests)
	}
	firstRevisionID := revisions[0].ID
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: writer}, nil
	}
	reviseRec := callRevise(revisions[0].ID)
	if reviseRec.Code != http.StatusSeeOther || len(writer.requests) != 2 || writer.requests[1].ExistingMarkdown == "" || len(writer.requests[1].RevisionFeedback) != 1 {
		t.Fatalf("reviewed revision should create an AI edit request: code=%d request=%+v", reviseRec.Code, writer.requests)
	}
	revisions, _ = srv.store.ListArticleRevisions(t.Context(), drafts[0].ID)
	if len(revisions) != 2 || revisions[0].Origin != "ai_edit" {
		t.Fatalf("Writer revision should be immutable ai_edit: %+v", revisions)
	}
	currentRevisionID := revisions[0].ID
	// The generated edit is now current and needs its own evidence pass; an
	// approval on the preceding snapshot must not unlock this replacement.
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{EvidenceReviewer: reviewer}, nil
	}
	currentReviewReq := httptest.NewRequest(http.MethodPost, "/workbench/reviews/evidence", strings.NewReader("_csrf="+csrf+"&revision_id="+currentRevisionID))
	currentReviewReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	currentReviewReq.AddCookie(session)
	currentReviewReq.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	currentReviewRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(currentReviewRec, currentReviewReq)
	if currentReviewRec.Code != http.StatusSeeOther {
		t.Fatalf("current AI edit should be independently reviewable: %d %s", currentReviewRec.Code, currentReviewRec.Body.String())
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: failingArticleWriter{}}, nil
	}
	if rec := callRevise(firstRevisionID); rec.Code != http.StatusBadRequest {
		t.Fatalf("failing Writer should reject: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil }
	if rec := callRevise(firstRevisionID); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Writer should reject: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: writer}, nil
	}
	if _, err := srv.store.DB.Exec(`CREATE TRIGGER abort_edit_map BEFORE INSERT ON evidence_maps BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := callRevise(firstRevisionID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("map persistence should fail: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TRIGGER abort_edit_map`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`CREATE TRIGGER abort_edit_revision BEFORE INSERT ON article_revisions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := callRevise(firstRevisionID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("revision persistence should fail: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TRIGGER abort_edit_revision`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if rec := callRevise(firstRevisionID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings lookup should fail: %d", rec.Code)
	}
	packagePage := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+currentRevisionID+"/package")
	if packagePage.Code != http.StatusOK || !strings.Contains(packagePage.Body.String(), "单集") || !strings.Contains(packagePage.Body.String(), "可复制富文本") {
		t.Fatalf("ready revision should render publication package: status=%d body=%s", packagePage.Code, packagePage.Body.String())
	}
	download := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+currentRevisionID+"/package?format=markdown")
	if download.Code != http.StatusOK || !strings.Contains(download.Body.String(), "## 来源") || !strings.Contains(download.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("package download should contain Markdown and sources: status=%d header=%q body=%s", download.Code, download.Header().Get("Content-Type"), download.Body.String())
	}
	if missing := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/missing/package"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing revision must not produce package: %d", missing.Code)
	}
	malformedReq := httptest.NewRequest(http.MethodGet, "/workbench/revisions//package", nil)
	malformedRec := httptest.NewRecorder()
	srv.handlePublicationPackage(malformedRec, malformedReq)
	if malformedRec.Code != http.StatusNotFound {
		t.Fatalf("malformed package path must be 404: %d", malformedRec.Code)
	}
	if _, err := srv.store.DB.Exec(`UPDATE evidence_maps SET keypoint_ids_json='broken' WHERE revision_id=?`, currentRevisionID); err != nil {
		t.Fatal(err)
	}
	if invalidMap := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+currentRevisionID+"/package"); invalidMap.Code != http.StatusInternalServerError {
		t.Fatalf("invalid evidence map must block package: %d", invalidMap.Code)
	}
	if _, err := srv.store.DB.Exec(`UPDATE evidence_maps SET keypoint_ids_json=? WHERE revision_id=?`, "[\""+keyPoints[0].ID+"\"]", currentRevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DELETE FROM keypoint_index WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if missingSource := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+currentRevisionID+"/package"); missingSource.Code != http.StatusInternalServerError {
		t.Fatalf("missing evidence source must block package: %d", missingSource.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE evidence_maps`); err != nil {
		t.Fatal(err)
	}
	if missingMaps := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+currentRevisionID+"/package"); missingMaps.Code != http.StatusInternalServerError {
		t.Fatalf("missing evidence mappings must block package: %d", missingMaps.Code)
	}
	blocked, err := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: drafts[0].ID, Title: "未审校", Markdown: "# 未审校", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	blockedPage := doWithCookie(srv, session, http.MethodGet, "/workbench/revisions/"+blocked.ID+"/package")
	if blockedPage.Code != http.StatusConflict {
		t.Fatalf("unreviewed revision must not produce package: %d", blockedPage.Code)
	}
}

func TestWriterRejectsUnconfirmedMaterialsAndProviderFailures(t *testing.T) {
	srv := newTestServer(t)
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "有证据的观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[\"" + keyPoints[0].ID + "\"]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/workbench/write", nil),
		httptest.NewRequest(http.MethodPost, "/workbench/write", nil),
	} {
		rec := httptest.NewRecorder()
		srv.handleArticleWriterRun(rec, request)
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusBadRequest {
			t.Fatalf("writer should reject method or missing brief: %d", rec.Code)
		}
	}
	unconfirmed, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "未确认", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/workbench/write", strings.NewReader("brief_id="+unconfirmed.ID))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	srv.handleArticleWriterRun(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed brief should be rejected: %d", recorder.Code)
	}
	badBrief := *brief
	badBrief.MaterialPlan = "not-json"
	if _, err := srv.writerRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile, &badBrief, proposal); err == nil {
		t.Fatal("writer must reject malformed material plan")
	}
	missingMaterial := *brief
	missingMaterial.MaterialPlan = `["missing"]`
	if _, err := srv.writerRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile, &missingMaterial, proposal); err == nil {
		t.Fatal("writer must reject missing KeyPoint")
	}
	if _, err := srv.writerRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile, brief, proposal); err == nil {
		t.Fatal("writer must reject a Source outside profile scope")
	}
	if err := srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.writerRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile, brief, proposal); err == nil {
		t.Fatal("writer must reject local-only material for external providers")
	}
	if err := srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataExternalAllowed); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='[]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.writerRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile, brief, proposal); err == nil {
		t.Fatal("writer must reject KeyPoint without Citation")
	}
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='["seg-1"]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}

	post := httptest.NewRequest(http.MethodPost, "/workbench/write", strings.NewReader("brief_id="+brief.ID))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return nil, errors.New("provider unavailable")
	}
	rec := httptest.NewRecorder()
	srv.handleArticleWriterRun(rec, post)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unavailable provider should be rejected: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: failingArticleWriter{}}, nil
	}
	rec = httptest.NewRecorder()
	srv.handleArticleWriterRun(rec, post)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("writer failure should be reported: %d", rec.Code)
	}
	run := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/workbench/write", strings.NewReader("brief_id="+brief.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		srv.handleArticleWriterRun(recorder, req)
		return recorder
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil }
	if rec := run(); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Writer implementation should be rejected: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Writer: &fakeArticleWriter{}}, nil
	}
	for _, tc := range []struct{ table, name string }{{"article_drafts", "block_draft"}, {"article_revisions", "block_revision"}, {"evidence_maps", "block_evidence"}} {
		if _, err := srv.store.DB.Exec(`CREATE TRIGGER ` + tc.name + ` BEFORE INSERT ON ` + tc.table + ` BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
			t.Fatal(err)
		}
		if rec := run(); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s persistence failure should be 500: %d", tc.table, rec.Code)
		}
		if _, err := srv.store.DB.Exec(`DROP TRIGGER ` + tc.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if rec := run(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings read failure should be 500: %d", rec.Code)
	}
}

func TestWriterSurfacesProfileLookupFailure(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
	if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`UPDATE article_proposals SET editorial_profile_id='missing' WHERE id=?`, proposal.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workbench/write", strings.NewReader("brief_id="+brief.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleArticleWriterRun(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("profile lookup failure should be 500: %d", rec.Code)
	}
}

func TestEvidenceReviewerRejectsInvalidEvidenceAndProviderFailures(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
	draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
	revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/workbench/reviews/evidence", strings.NewReader("revision_id="+revision.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.handleEvidenceReviewRun(rec, req)
		return rec
	}
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("revision without EvidenceMap should be rejected: %d", rec.Code)
	}
	if _, err := srv.store.CreateEvidenceMap(t.Context(), models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceRhetorical, Excerpt: "不存在", KeyPointIDs: "[]"}); err != nil {
		t.Fatal(err)
	}
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("EvidenceMap excerpt outside markdown should be rejected: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`UPDATE evidence_maps SET excerpt='正文' WHERE revision_id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return nil, errors.New("unavailable") }
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("unavailable reviewer should be rejected: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil }
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reviewer should be rejected: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{EvidenceReviewer: failingEvidenceReviewer{}}, nil
	}
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("reviewer failure should be rejected: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if rec := call(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings failure should be 500: %d", rec.Code)
	}
}

func TestEvidenceReviewRequestEnforcesMappedSourcePolicy(t *testing.T) {
	srv := newTestServer(t)
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}})
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
	draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
	revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
	srv.store.CreateEvidenceMap(t.Context(), models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceParaphrased, Excerpt: "正文", KeyPointIDs: "[\"" + keyPoints[0].ID + "\"]"})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := srv.evidenceReviewRequest(request, revision); err == nil {
		t.Fatal("review must reject unscoped evidence KeyPoint")
	}
	srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID)
	if err := srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(request, revision); err == nil {
		t.Fatal("review must reject local-only evidence KeyPoint")
	}
	srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataExternalAllowed)
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='[]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(request, revision); err == nil {
		t.Fatal("review must reject keypoint without Citation")
	}
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='["seg-1"]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, err := srv.evidenceReviewRequest(request, revision); err != nil || len(got.Items) != 1 || len(got.Items[0].Materials) != 1 {
		t.Fatalf("eligible mapped evidence should become review request: request=%+v err=%v", got, err)
	}
}

func TestEvidenceReviewerSurfacesLookupAndPersistenceFailures(t *testing.T) {
	newRevision := func(t *testing.T) (*Server, *models.ArticleRevision) {
		t.Helper()
		srv := newTestServer(t)
		profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
		proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
		srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
		brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
		srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
		draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
		revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
		srv.store.CreateEvidenceMap(t.Context(), models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceRhetorical, Excerpt: "正文", KeyPointIDs: "[]"})
		return srv, revision
	}
	srv, revision := newRevision(t)
	methodRec := httptest.NewRecorder()
	srv.handleEvidenceReviewRun(methodRec, httptest.NewRequest(http.MethodGet, "/workbench/reviews/evidence", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should be rejected: %d", methodRec.Code)
	}
	missingRec := httptest.NewRecorder()
	srv.handleEvidenceReviewRun(missingRec, httptest.NewRequest(http.MethodPost, "/workbench/reviews/evidence", strings.NewReader("revision_id=missing")))
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing revision should be rejected: %d", missingRec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{EvidenceReviewer: &fakeEvidenceReviewer{status: "passed"}}, nil
	}
	if _, err := srv.store.DB.Exec(`CREATE TRIGGER abort_review BEFORE INSERT ON article_reviews BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workbench/reviews/evidence", strings.NewReader("revision_id="+revision.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleEvidenceReviewRun(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("review persistence failure should be 500: %d", rec.Code)
	}

	srv, revision = newRevision(t)
	if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DELETE FROM article_drafts WHERE id=?`, revision.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
		t.Fatal("draft lookup failure should surface")
	}
	srv, revision = newRevision(t)
	if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE article_proposals`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
		t.Fatal("proposal lookup failure should surface")
	}
	srv, revision = newRevision(t)
	if _, err := srv.store.DB.Exec(`DROP TABLE evidence_maps`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
		t.Fatal("evidence map query failure should surface")
	}
	srv, revision = newRevision(t)
	if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE article_briefs`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.evidenceReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
		t.Fatal("brief lookup failure should surface")
	}
}

type failingEvidenceReviewer struct{}

func (failingEvidenceReviewer) ReviewEvidence(provider.EvidenceReviewRequest) (*provider.EvidenceReviewResult, error) {
	return nil, errors.New("reviewer failed")
}

func (failingEvidenceReviewer) Name() string { return "failing-reviewer" }

type fakeArticleWriter struct {
	requests []provider.ArticleWritingRequest
}

func (f *fakeArticleWriter) WriteArticle(request provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	f.requests = append(f.requests, request)
	return &provider.ArticleWritingResult{Title: "审查成本", Markdown: "# 审查成本\n效率也会带来审查成本。", EvidenceMaps: []provider.ArticleEvidence{{Kind: "paraphrased", Excerpt: "效率也会带来审查成本。", KeyPointIDs: []string{request.Materials[0].KeyPointID}}}}, nil
}

func (f *fakeArticleWriter) Name() string { return "fake-writer" }

type failingArticleWriter struct{}

func (failingArticleWriter) WriteArticle(provider.ArticleWritingRequest) (*provider.ArticleWritingResult, error) {
	return nil, errors.New("writer failed")
}

func (failingArticleWriter) Name() string { return "failing-writer" }

type fakeEvidenceReviewer struct {
	status   string
	requests []provider.EvidenceReviewRequest
}

func (f *fakeEvidenceReviewer) ReviewEvidence(request provider.EvidenceReviewRequest) (*provider.EvidenceReviewResult, error) {
	f.requests = append(f.requests, request)
	return &provider.EvidenceReviewResult{Status: f.status}, nil
}

func (f *fakeEvidenceReviewer) Name() string { return "fake-evidence-reviewer" }

type fakeStyleEditor struct {
	status   string
	issues   []string
	requests []provider.StyleReviewRequest
}

func (f *fakeStyleEditor) ReviewStyle(request provider.StyleReviewRequest) (*provider.StyleReviewResult, error) {
	f.requests = append(f.requests, request)
	return &provider.StyleReviewResult{Status: f.status, Issues: f.issues}, nil
}

func (f *fakeStyleEditor) Name() string { return "fake-style-editor" }

type failingStyleEditor struct{}

func (failingStyleEditor) ReviewStyle(provider.StyleReviewRequest) (*provider.StyleReviewResult, error) {
	return nil, errors.New("style failed")
}

func (failingStyleEditor) Name() string { return "failing-style-editor" }

func TestStyleEditorRejectsInvalidAndProviderFailures(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌", Voice: "清晰"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
	draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
	revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/workbench/reviews/style", strings.NewReader("revision_id="+revision.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.handleStyleReviewRun(rec, req)
		return rec
	}
	methodRec := httptest.NewRecorder()
	srv.handleStyleReviewRun(methodRec, httptest.NewRequest(http.MethodGet, "/workbench/reviews/style", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should reject: %d", methodRec.Code)
	}
	missingReq := httptest.NewRequest(http.MethodPost, "/workbench/reviews/style", strings.NewReader("revision_id=missing"))
	missingReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingRec := httptest.NewRecorder()
	srv.handleStyleReviewRun(missingRec, missingReq)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing revision should reject: %d", missingRec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil }
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider should reject: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return nil, errors.New("unavailable") }
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("unavailable provider should reject: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{StyleEditor: failingStyleEditor{}}, nil
	}
	if rec := call(); rec.Code != http.StatusBadRequest {
		t.Fatalf("failed editor should reject: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{StyleEditor: &fakeStyleEditor{status: "passed"}}, nil
	}
	if _, err := srv.store.DB.Exec(`CREATE TRIGGER abort_style BEFORE INSERT ON article_reviews BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if rec := call(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("style persistence failure should be 500: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if rec := call(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings failure should be 500: %d", rec.Code)
	}
}

func TestStyleReviewRequestSurfacesRevisionLineageFailures(t *testing.T) {
	newRevision := func(t *testing.T) (*Server, *models.ArticleRevision) {
		t.Helper()
		srv := newTestServer(t)
		profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌", TargetAudience: "创作者", Voice: "清晰", StyleGuide: "短句"})
		if err != nil {
			t.Fatal(err)
		}
		proposal, err := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted"); err != nil {
			t.Fatal(err)
		}
		targetLength := 1200
		brief, err := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]", TargetLength: &targetLength})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.store.ConfirmArticleBrief(t.Context(), brief.ID); err != nil {
			t.Fatal(err)
		}
		draft, err := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
		if err != nil {
			t.Fatal(err)
		}
		revision, err := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
		if err != nil {
			t.Fatal(err)
		}
		return srv, revision
	}

	srv, revision := newRevision(t)
	request, err := srv.styleReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision)
	if err != nil || request.TargetAudience != "创作者" || request.Voice != "清晰" || request.StyleGuide != "短句" || request.TargetLength == nil || *request.TargetLength != 1200 {
		t.Fatalf("unexpected style request: %#v, %v", request, err)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE article_drafts`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.styleReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
		t.Fatal("missing draft should fail")
	}

	for _, table := range []string{"article_briefs", "article_proposals", "editorial_profiles"} {
		srv, revision = newRevision(t)
		if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.store.DB.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.styleReviewRequest(httptest.NewRequest(http.MethodPost, "/", nil), revision); err == nil {
			t.Fatalf("missing %s should fail", table)
		}
	}
}

func TestStyleEditorRejectsBrokenRevisionLineage(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
	srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
	brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
	srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
	draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
	revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
	if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DELETE FROM article_drafts WHERE id=?`, draft.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workbench/reviews/style", strings.NewReader("revision_id="+revision.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleStyleReviewRun(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("broken lineage should be rejected: %d", rec.Code)
	}
}

func TestRevisionWriterRejectsBrokenLineage(t *testing.T) {
	newRevision := func(t *testing.T) (*Server, *models.ArticleRevision) {
		t.Helper()
		srv := newTestServer(t)
		profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
		proposal, _ := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Title: "选题", CandidateKeyPoints: "[]"})
		srv.store.SetArticleProposalStatus(t.Context(), proposal.ID, "accepted")
		brief, _ := srv.store.CreateArticleBrief(t.Context(), models.ArticleBrief{ProposalID: proposal.ID, Thesis: "论点", Outline: "# 结构", MaterialPlan: "[]", ConflictPlan: "[]"})
		srv.store.ConfirmArticleBrief(t.Context(), brief.ID)
		draft, _ := srv.store.CreateArticleDraft(t.Context(), brief.ID, "文章")
		revision, _ := srv.store.CreateArticleRevision(t.Context(), models.ArticleRevision{DraftID: draft.ID, Title: "文章", Markdown: "正文", Origin: "owner"})
		return srv, revision
	}
	for _, table := range []string{"article_drafts", "article_briefs", "article_proposals", "editorial_profiles"} {
		srv, revision := newRevision(t)
		if _, err := srv.store.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.store.DB.Exec(`DELETE FROM ` + table + ` WHERE 1=1`); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/workbench/revise", strings.NewReader("revision_id="+revision.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.handleArticleRevisionWriterRun(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("missing %s should reject: %d", table, rec.Code)
		}
	}
}
