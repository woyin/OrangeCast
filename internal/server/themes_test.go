package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestThemeBoardCreateConfirmAndLinkKeyPoint(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "theme-board@example.com", "password123")
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "审查成本", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if err := srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest(http.MethodGet, "/themes?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK || !strings.Contains(pageRec.Body.String(), "跨 Episode 主题") {
		t.Fatalf("theme board should render: %d %s", pageRec.Code, pageRec.Body.String())
	}
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
	if rec := post("/themes/create", "profile_id="+profile.ID+"&name=%E5%AE%A1%E6%9F%A5%E6%88%90%E6%9C%AC&description=%E8%B7%A8%E9%9B%86%E6%9D%90%E6%96%99"); rec.Code != http.StatusSeeOther {
		t.Fatalf("theme creation should redirect: %d", rec.Code)
	}
	themes, _ := srv.store.ListThemes(t.Context(), profile.ID)
	if len(themes) != 1 {
		t.Fatalf("theme should persist: %+v", themes)
	}
	if rec := post("/themes/keypoints", "profile_id="+profile.ID+"&theme_id="+themes[0].ID+"&keypoint_id="+keyPoints[0].ID+"&relationship=conflicts"); rec.Code != http.StatusSeeOther {
		t.Fatalf("keypoint relation should persist: %d", rec.Code)
	}
	if rec := post("/themes/status", "profile_id="+profile.ID+"&theme_id="+themes[0].ID+"&status=confirmed"); rec.Code != http.StatusSeeOther {
		t.Fatalf("theme confirmation should redirect: %d", rec.Code)
	}
	relations, _ := srv.store.ListThemeKeyPoints(t.Context(), themes[0].ID)
	updated, _ := srv.store.GetTheme(t.Context(), themes[0].ID)
	if len(relations) != 1 || relations[0].Relationship != "conflicts" || updated.Status != "confirmed" {
		t.Fatalf("theme lifecycle should persist: relations=%+v theme=%+v", relations, updated)
	}
}

func TestThemeHandlersRejectWrongMethodAndInvalidInput(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "theme-errors@example.com", "password123")
	for _, path := range []string{"/themes/create", "/themes/status", "/themes/keypoints"} {
		if rec := doWithCookie(srv, session, http.MethodGet, path); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s should reject GET: %d", path, rec.Code)
		}
	}
	page := httptest.NewRequest(http.MethodGet, "/themes", nil)
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
		{"/themes/create", "profile_id=missing&name="},
		{"/themes/status", "profile_id=missing&theme_id=missing&status=wrong"},
		{"/themes/keypoints", "profile_id=missing&theme_id=missing&keypoint_id=missing&relationship=wrong"},
	} {
		if rec := post(request.path, request.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should reject invalid input: %d", request.path, rec.Code)
		}
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/themes?profile=missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown profile should be 404: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE editorial_profiles`); err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/themes"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("profile query failure should be 500: %d", rec.Code)
	}
}

func TestThemeBoardSurfacesThemeListFailure(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "theme-list-error@example.com", "password123")
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if _, err := srv.store.DB.Exec(`DROP TABLE themes`); err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, session, http.MethodGet, "/themes?profile="+profile.ID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("theme list failure should be 500: %d", rec.Code)
	}
}

func TestThemeBoardSurfacesTemplateFailure(t *testing.T) {
	srv := newTestServer(t)
	srv.tmpl = &Templates{}
	req := httptest.NewRequest(http.MethodGet, "/themes", nil)
	rec := httptest.NewRecorder()
	srv.handleThemes(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("template failure should be 500: %d", rec.Code)
	}
}
