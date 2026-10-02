package server

import (
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestKnowledgeRerankHTTPExplicitPreparation(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "rerank@example.com", "password123")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer remote.Close()
	srv.selector.WithRerank("secret", remote.URL, "rank")
	if _, err := srv.store.CreatePastedDocument(t.Context(), "主动回忆", "主动回忆是学习方法。"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/search?q=主动回忆&rerank=1", "/api/knowledge-rerank"} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(session)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if path == "/api/knowledge-rerank" && rec.Code != 405 {
			t.Fatal(rec.Code)
		}
		if path != "/api/knowledge-rerank" && (rec.Code != 200 || !strings.Contains(rec.Body.String(), "候选重排")) {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	bad := httptest.NewRequest("POST", "/api/knowledge-rerank?q=主动回忆", strings.NewReader("acknowledge=1"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, bad)
	if rec.Code != 403 {
		t.Fatal("missing csrf", rec.Code)
	}
	rec = writingCoveragePost(t, srv, session, "/api/knowledge-rerank?q=主动回忆", url.Values{"acknowledge": {"1"}}, false)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/automation/") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/knowledge-search-feedback", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"quality_gate_passed":false`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	hits, err := srv.store.SearchKnowledge(t.Context(), store.KnowledgeSearchQuery{Text: "主动回忆"})
	if err != nil || len(hits.Hits) != 1 {
		t.Fatal(hits, err)
	}
	hit := hits.Hits[0]
	rec = writingCoveragePost(t, srv, session, "/api/knowledge-search-feedback?q=主动回忆&kind=document", url.Values{"query": {"主动回忆"}, "key": {hit.Key}, "revision": {strconv.Itoa(hit.Revision)}, "label": {"relevant"}, "method": {"rerank_fts"}}, false)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "kind=document") {
		t.Fatal("feedback scope", rec.Code, rec.Header())
	}
	feedback, err := srv.store.ExportKnowledgeSearchFeedback(t.Context())
	if err != nil || len(feedback) != 1 || feedback[0].Method != "rerank_fts" {
		t.Fatal(feedback, err)
	}
	if calls != 0 {
		t.Fatal("HTTP performed paid call", calls)
	}
}
