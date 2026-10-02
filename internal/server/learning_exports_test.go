package server

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLearningExportsSSRAndCommands(t *testing.T) {
	srv := newTestServer(t)
	profile, e := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.DB.Exec(`INSERT INTO themes(id,editorial_profile_id,name)VALUES('owner-theme',?,'清晰的学习主题')`, profile.ID); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest(http.MethodGet, "/learning-exports?kind=theme&id=owner-theme&include_drafts=yes", nil)
	rec := httptest.NewRecorder()
	srv.handleLearningExports(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "owner-theme") || !strings.Contains(rec.Body.String(), "name=\"include_drafts\" value=\"yes\" checked") {
		t.Fatalf("SSR options missing: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="selection" required`) || !strings.Contains(rec.Body.String(), `value="theme:owner-theme" selected`) || !strings.Contains(rec.Body.String(), "主题：清晰的学习主题") || strings.Contains(rec.Body.String(), `<input name="id"`) || strings.Contains(rec.Body.String(), "问题或主题 ID") {
		t.Fatal("scope must use titles, not raw-ID input", rec.Body.String())
	}
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
		values  url.Values
		want    int
	}{
		{"/learning-exports/preview", srv.handleLearningExportPreview, url.Values{"kind": {"wrong"}, "id": {"none"}}, 400},
		{"/learning-exports/preview", srv.handleLearningExportPreview, url.Values{"selection": {"question"}, "kind": {"theme"}, "id": {"owner-theme"}}, 400},
		{"/learning-exports/create", srv.handleLearningExportCreate, url.Values{"preview_id": {"missing"}}, 400},
		{"/learning-exports/create", srv.handleLearningExportCreate, url.Values{"confirmed": {"yes"}, "preview_id": {"missing"}, "request_key": {"bad"}}, 400},
	} {
		req = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec = httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s: got %d want %d: %s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
	for _, suffix := range []string{"status", "download", "unknown"} {
		rec = httptest.NewRecorder()
		srv.handleLearningExportItem(rec, httptest.NewRequest(http.MethodGet, "/learning-exports/missing/"+suffix, nil))
		if rec.Code != 404 {
			t.Fatalf("%s: %d", suffix, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	srv.handleLearningExports(rec, httptest.NewRequest(http.MethodPost, "/learning-exports", nil))
	if rec.Code != 405 {
		t.Fatalf("method: %d", rec.Code)
	}
}
func TestLearningExportStatusPolling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	if out, err := exec.Command(node, filepath.Join("testdata", "learning-exports.cjs")).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestLearningExportAuthenticatedPreviewConfirmationAndPrivacy(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "exports@example.com", "password123")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "可导出的学习问题"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := doWithCookie(srv, nil, "GET", "/learning-exports"); rec.Code != 303 {
		t.Fatalf("auth: %d", rec.Code)
	}
	if _, err := srv.store.PreviewLearningExport(t.Context(), store.LearningExportScope{Kind: "question", ID: q.ID}); err != nil {
		t.Fatalf("store preview: %v", err)
	}
	selected := doWithCookie(srv, cookie, "GET", "/learning-exports?kind=question&id="+q.ID)
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `value="question:`+q.ID+`" selected`) || !strings.Contains(selected.Body.String(), "学习问题：可导出的学习问题") {
		t.Fatal("URL scope not preselected by title", selected.Body.String())
	}
	modern := postForm(t, srv, cookie, "/learning-exports/preview", url.Values{"selection": {"question:" + q.ID}, "include_excerpts": {"yes"}}.Encode())
	if modern.Code != 200 || !strings.Contains(modern.Body.String(), "范围：学习问题：可导出的学习问题") {
		t.Fatal("selection form preview failed", modern.Code, modern.Body.String())
	}
	if strings.Contains(modern.Body.String(), "范围：question / "+q.ID) {
		t.Fatal("preview exposed technical scope")
	}
	preview := postForm(t, srv, cookie, "/learning-exports/preview", url.Values{"kind": {"question"}, "id": {q.ID}}.Encode())
	if preview.Code != 200 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	field := func(name string) string {
		parts := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(preview.Body.String())
		if len(parts) != 2 {
			t.Fatalf("missing %s", name)
		}
		return parts[1]
	}
	values := url.Values{"confirmed": {"yes"}, "preview_id": {field("preview_id")}, "preview_hash": {field("preview_hash")}, "request_key": {uuid.NewString()}}
	created := postForm(t, srv, cookie, "/learning-exports/create", values.Encode())
	if created.Code != 303 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	replay := postForm(t, srv, cookie, "/learning-exports/create", values.Encode())
	if replay.Header().Get("Location") != created.Header().Get("Location") {
		t.Fatal("duplicate command created new export")
	}
	id := strings.TrimPrefix(created.Header().Get("Location"), "/learning-exports#export-")
	status := doWithCookie(srv, cookie, "GET", "/learning-exports/"+id+"/status")
	if status.Code != 200 || strings.Contains(status.Body.String(), "Path") || strings.Contains(status.Body.String(), srv.cfg.DataDir) || strings.Contains(status.Body.String(), q.Body) {
		t.Fatalf("status leaks: %d %s", status.Code, status.Body.String())
	}
	directory := filepath.Join(srv.cfg.DataDir, "learning-exports")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(directory, id+".zip")
	if err := os.WriteFile(archive, []byte("PK-local-export"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.PublishLearningExport(t.Context(), id, archive); err != nil {
		t.Fatal(err)
	}
	download := doWithCookie(srv, cookie, "GET", "/learning-exports/"+id+"/download")
	if download.Code != 200 || download.Body.String() != "PK-local-export" || download.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("download %d %s", download.Code, download.Body.String())
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	missing := doWithCookie(srv, cookie, "GET", "/learning-exports/"+id+"/download")
	if missing.Code != 404 {
		t.Fatalf("missing: %d", missing.Code)
	}
	invalid := doWithCookie(srv, cookie, "GET", "/learning-exports/"+id+"/status")
	if !strings.Contains(invalid.Body.String(), `"status":"failed"`) {
		t.Fatalf("invalid ready file: %s", invalid.Body.String())
	}
	values.Set("request_key", uuid.NewString())
	values.Set("preview_hash", "changed")
	conflict := postForm(t, srv, cookie, "/learning-exports/create", values.Encode())
	if conflict.Code != 409 {
		t.Fatalf("hash conflict: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestLearningExportJSONNavigationUsesExactReadOnlyPreview(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "export-navigation@example.com", "password123")
	q, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "播放中导出", Goal: "保留根播放器"})
	if err != nil {
		t.Fatal(err)
	}
	postJSON := func(path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		page := doWithCookie(srv, cookie, "GET", "/learning-exports")
		var csrf *http.Cookie
		for _, c := range page.Result().Cookies() {
			if c.Name == "cwp_csrf" {
				csrf = c
			}
		}
		if csrf == nil {
			t.Fatal("missing csrf")
		}
		values.Set("_csrf", csrf.Value)
		req := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.AddCookie(cookie)
		req.AddCookie(csrf)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	rec := postJSON("/learning-exports/preview", url.Values{"selection": {"question:" + q.ID}, "include_history": {"yes"}})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("json preview: %d %s", rec.Code, rec.Body.String())
	}
	var navigation struct{ State, Href string }
	if err := json.Unmarshal(rec.Body.Bytes(), &navigation); err != nil {
		t.Fatal(err)
	}
	if navigation.State != "saved" || !strings.HasPrefix(navigation.Href, "/learning-exports?preview_id=") {
		t.Fatal(navigation)
	}
	var before, after int
	if err := srv.store.DB.QueryRow("SELECT count(*) FROM learning_export_previews").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		page := doWithCookie(srv, cookie, "GET", navigation.Href)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `name="include_history" value="yes" checked`) || !strings.Contains(page.Body.String(), `class="learning-export-command"`) {
			t.Fatalf("confirmation page: %d %s", page.Code, page.Body.String())
		}
	}
	if err := srv.store.DB.QueryRow("SELECT count(*) FROM learning_export_previews").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("GET creates previews: %d→%d", before, after)
	}
	previewID := strings.TrimPrefix(navigation.Href, "/learning-exports?preview_id=")
	preview, err := srv.store.GetLearningExportPreview(t.Context(), previewID)
	if err != nil {
		t.Fatal(err)
	}
	rec = postJSON("/learning-exports/create", url.Values{"confirmed": {"yes"}, "preview_id": {preview.ID}, "preview_hash": {preview.Hash}, "request_key": {uuid.NewString()}})
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &navigation) != nil || navigation.State != "saved" || !strings.HasPrefix(navigation.Href, "/learning-exports#export-") {
		t.Fatalf("json create: %d %s", rec.Code, rec.Body.String())
	}
	if _, err = srv.store.DB.Exec("UPDATE learning_questions SET body='已修改' WHERE id=?", q.ID); err != nil {
		t.Fatal(err)
	}
	stale := doWithCookie(srv, cookie, "GET", "/learning-exports?preview_id="+preview.ID)
	if stale.Code != 409 {
		t.Fatalf("stale confirmation: %d %s", stale.Code, stale.Body.String())
	}
	if err := srv.store.DB.QueryRow("SELECT count(*) FROM learning_export_previews").Scan(&after); err != nil || before != after {
		t.Fatal("stale GET created preview", err, before, after)
	}
}
