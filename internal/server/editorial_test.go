package server

import (
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
		t.Fatalf("draft detail should render: %d", rec.Code)
	}
	if rec := post("/workbench/revisions", "draft_id="+draft.ID+"&title=%E6%96%87%E7%AB%A0&markdown=%23+%E7%AC%AC%E4%B8%80%E7%89%88"); rec.Code != http.StatusSeeOther {
		t.Fatalf("revision should be created: %d", rec.Code)
	}
	revisions, err := srv.store.ListArticleRevisions(t.Context(), draft.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Origin != "owner" {
		t.Fatalf("owner revision should persist: revisions=%+v err=%v", revisions, err)
	}
	if rec := post("/workbench/reviews", "revision_id="+revisions[0].ID+"&kind=evidence&status=passed&issues=%5B%5D"); rec.Code != http.StatusSeeOther {
		t.Fatalf("evidence review should be recorded: %d", rec.Code)
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
