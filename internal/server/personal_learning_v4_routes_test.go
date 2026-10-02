package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPersonalLearningV4RouteBoundaries checks the assembled router rather than
// handlers alone: all new commands pass through the same Owner/CSRF boundary,
// while the offline shell is public static content with no personal projection.
func TestPersonalLearningV4RouteBoundaries(t *testing.T) {
	srv := newTestServer(t)
	paths := []string{
		"/questions/understanding-action", "/questions/example/gaps",
		"/api/learning-excerpts", "/quality-cases/action",
		"/learning-exports/preview", "/learning-exports/create",
		"/api/offline/enable", "/api/offline/sync", "/api/offline/revoke",
		"/api/study-chat", "/api/study-chat/retry",
	}
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		want := http.StatusSeeOther
		if strings.HasPrefix(path, "/api/") {
			want = http.StatusUnauthorized
		}
		if w.Code != want {
			t.Fatalf("unauthenticated %s: got %d want %d", path, w.Code, want)
		}
	}
	session := claimOwnerAndLogin(t, srv, "v4-boundaries@example.com", "password123")
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(session)
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF %s: got %d want 403", path, w.Code)
		}
	}
	for _, path := range []string{"/offline", "/sw.js"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("public static %s: status=%d cookies=%q", path, w.Code, w.Header().Get("Set-Cookie"))
		}
		if strings.Contains(w.Body.String(), session.Value) || strings.Contains(w.Body.String(), "v4-boundaries@example.com") {
			t.Fatalf("personal identity leaked into static %s", path)
		}
		if path == "/sw.js" && w.Header().Get("Service-Worker-Allowed") != "/" {
			t.Fatal("worker must have explicit root scope")
		}
	}
}
