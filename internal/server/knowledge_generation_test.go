package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeGenerationJSONAdmissionRetainsSSRAndIdempotency(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "generation@example.com", "password123")
	seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "fixture"
	post := func() *httptest.ResponseRecorder {
		csrf := journeyCSRF(t, srv, session)
		req := httptest.NewRequest("POST", "/knowledge-articles/generate", strings.NewReader("theme="))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		req.AddCookie(session)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	first := post()
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var result map[string]string
	if e := json.Unmarshal(first.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(result["redirect"], "/knowledge-articles/") {
		t.Fatal(result)
	}
	again := post()
	if again.Code != 200 || again.Body.String() != first.Body.String() {
		t.Fatal(again.Code, again.Body.String())
	}
	jobs, e := srv.store.ListQueuedOrRunning(t.Context())
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	if ex, e := srv.store.GetJobExecution(t.Context(), jobs[0].ID); e != nil || ex.RemoteCallStarted {
		t.Fatal("admission made paid call", ex, e)
	}
	ssr := postForm(t, srv, session, "/knowledge-articles/generate", "theme=")
	if ssr.Code != 303 || ssr.Header().Get("Location") != result["redirect"] {
		t.Fatal(ssr.Code, ssr.Body.String())
	}
	srv.cfg.PodAPIKey = ""
	bad := post()
	if bad.Code != 400 || strings.Contains(bad.Body.String(), "<html") {
		t.Fatal(bad.Code, bad.Body.String())
	}
}
