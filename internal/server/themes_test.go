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

func TestThemeBoardSurfacesProfileListFailure(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.store.DB.Exec(`DROP TABLE editorial_profiles`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/themes", nil)
	rec := httptest.NewRecorder()
	srv.handleThemes(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("profile list failure should be 500: %d", rec.Code)
	}
}

func TestScoutCreatesDeduplicatedCrossEpisodeProposal(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "scout@example.com", "password123")
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "one", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"}, {GUID: "two", Title: "第二集", AudioURL: "https://cdn.example.com/2.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	for i, episode := range episodes {
		if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episode.ID, episode.Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "观点" + string(rune('A'+i)), Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌", TargetAudience: "开发者"})
	for _, episode := range episodes {
		if err := srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episode.ID); err != nil {
			t.Fatal(err)
		}
	}
	theme, err := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "工程成本", Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, keyPoint := range keyPoints {
		if err := srv.store.AddKeyPointToTheme(t.Context(), theme.ID, keyPoint.ID, "supports"); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeScout{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Scout: fake}, nil
	}
	page := httptest.NewRequest(http.MethodGet, "/themes?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	run := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/themes/scout", strings.NewReader("_csrf="+csrf+"&profile_id="+profile.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := run(); rec.Code != http.StatusSeeOther || len(fake.requests) != 1 || len(fake.requests[0].Themes) != 1 || len(fake.requests[0].Themes[0].Materials) != 2 {
		t.Fatalf("Scout should receive one cross-episode Theme: code=%d request=%+v", rec.Code, fake.requests)
	}
	proposals, _ := srv.store.ListArticleProposals(t.Context(), profile.ID)
	if len(proposals) != 1 || proposals[0].Status != "proposed" {
		t.Fatalf("Scout proposal should persist as proposed: %+v", proposals)
	}
	if rec := run(); rec.Code != http.StatusSeeOther {
		t.Fatalf("duplicate scout run should redirect: %d", rec.Code)
	}
	proposals, _ = srv.store.ListArticleProposals(t.Context(), profile.ID)
	if len(proposals) != 1 {
		t.Fatalf("duplicate title must not create another proposal: %+v", proposals)
	}
	directRun := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/themes/scout", strings.NewReader("profile_id="+profile.ID))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.handleScoutRun(rec, req)
		return rec
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return nil, errors.New("unavailable") }
	if rec := directRun(); rec.Code != http.StatusBadRequest {
		t.Fatalf("unavailable Scout bundle should fail: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil }
	if rec := directRun(); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Scout provider should fail: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Scout: failingScout{}}, nil
	}
	if rec := directRun(); rec.Code != http.StatusBadRequest {
		t.Fatalf("Scout provider error should fail: %d", rec.Code)
	}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Scout: fake}, nil
	}
	if err := srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Scout must reject local-only material")
	}
	if err := srv.store.SetSourceProductionPolicy(t.Context(), models.SourceEpisode, episodes[0].ID, "public", models.ModelDataExternalAllowed); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='[]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Scout must reject KeyPoint without Citation")
	}
	if _, err := srv.store.DB.Exec(`UPDATE keypoint_index SET citations_json='["seg-1"]' WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	oneSourceTheme, err := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "单集主题", Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddKeyPointToTheme(t.Context(), oneSourceTheme.ID, keyPoints[0].ID, "supports"); err != nil {
		t.Fatal(err)
	}
	request, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile)
	if err != nil || len(request.Themes) != 1 {
		t.Fatalf("single-source Theme should be skipped, not included: request=%+v err=%v", request, err)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE article_proposals`); err != nil {
		t.Fatal(err)
	}
	if rec := directRun(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("proposal list failure should be 500: %d", rec.Code)
	}
	if _, err := srv.store.DB.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if rec := directRun(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("settings read failure should be 500: %d", rec.Code)
	}
}

func TestScoutSurfacesThemeMaterialAndSettingsFailures(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	theme, _ := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "主题", Status: "confirmed"})
	if _, err := srv.store.DB.Exec(`DROP TABLE theme_keypoints`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Theme material query failure should surface")
	}
	_ = theme
}

func TestScoutRequestRejectsArchivedAndMissingThemeMaterial(t *testing.T) {
	srv := newTestServer(t)
	podcast, _ := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/rss", "播客", "", "")
	srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, _ := srv.store.ListKeyPoints(t.Context(), 1, 10)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	srv.store.GrantSourceScope(t.Context(), profile.ID, models.SourceEpisode, episodes[0].ID)
	theme, _ := srv.store.CreateTheme(t.Context(), models.Theme{EditorialProfileID: profile.ID, Name: "主题", Status: "confirmed"})
	srv.store.AddKeyPointToTheme(t.Context(), theme.ID, keyPoints[0].ID, "supports")
	if err := srv.store.ArchiveSource(t.Context(), models.SourceEpisode, episodes[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Scout must reject archived source material")
	}
	if err := srv.store.ArchiveSource(t.Context(), models.SourceEpisode, episodes[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`DELETE FROM keypoint_index WHERE id=?`, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Scout must reject missing theme KeyPoint")
	}
}

func TestScoutRequestSurfacesThemeListFailure(t *testing.T) {
	srv := newTestServer(t)
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	if _, err := srv.store.DB.Exec(`DROP TABLE themes`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoutRequest(httptest.NewRequest(http.MethodPost, "/", nil), profile); err == nil {
		t.Fatal("Scout must surface Theme list query failures")
	}
}

type fakeScout struct{ requests []provider.ScoutRequest }

func (f *fakeScout) Scout(request provider.ScoutRequest) (*provider.ScoutResult, error) {
	f.requests = append(f.requests, request)
	materials := request.Themes[0].Materials
	return &provider.ScoutResult{Proposals: []provider.ScoutProposal{{Kind: "evergreen", Title: "跨集工程成本", Thesis: "论点", Audience: "开发者", Rationale: "跨集价值", CandidateKeyPointIDs: []string{materials[0].KeyPointID, materials[1].KeyPointID}}}}, nil
}

func (f *fakeScout) Name() string { return "fake-scout" }

type failingScout struct{}

func (failingScout) Scout(provider.ScoutRequest) (*provider.ScoutResult, error) {
	return nil, errors.New("scout failed")
}

func (failingScout) Name() string { return "failing-scout" }

func TestScoutRejectsWrongMethodMissingProfileAndNoCrossEpisodeTheme(t *testing.T) {
	srv := newTestServer(t)
	methodRec := httptest.NewRecorder()
	srv.handleScoutRun(methodRec, httptest.NewRequest(http.MethodGet, "/themes/scout", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Scout GET should be rejected: %d", methodRec.Code)
	}
	missing := httptest.NewRequest(http.MethodPost, "/themes/scout", strings.NewReader("profile_id=missing"))
	missing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingRec := httptest.NewRecorder()
	srv.handleScoutRun(missingRec, missing)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing profile should be rejected: %d", missingRec.Code)
	}
	profile, _ := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "品牌"})
	noTheme := httptest.NewRequest(http.MethodPost, "/themes/scout", strings.NewReader("profile_id="+profile.ID))
	noTheme.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	noThemeRec := httptest.NewRecorder()
	srv.handleScoutRun(noThemeRec, noTheme)
	if noThemeRec.Code != http.StatusBadRequest {
		t.Fatalf("profile without cross-episode Theme should be rejected: %d", noThemeRec.Code)
	}
}
