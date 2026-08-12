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
