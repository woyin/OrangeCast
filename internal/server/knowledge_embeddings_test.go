package server

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeEmbeddingJobAutomationProjectionIsReadonly(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-projection@example.com", "password123")
	client, err := provider.NewEmbeddingClient("secret-sentinel", "https://private-endpoint.example/v1", "vector", 0)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := srv.store.ReserveKnowledgeEmbeddingPreflight(t.Context(), uuid.NewString(), client.Config())
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	page := doWithCookie(srv, cookie, "GET", "/automation/"+job.ID)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "embedding-utf8-byte-bound-v1") || !strings.Contains(page.Body.String(), "冻结价格未知") || strings.Contains(page.Body.String(), "secret-sentinel") || strings.Contains(page.Body.String(), "private-endpoint") {
		t.Fatal(page.Code, page.Body.String())
	}
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("GET changed index", before, after, err)
	}
}

func semanticPost(srv *Server, cookie *http.Cookie, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/knowledge-semantic", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "semantic-csrf"})
		req.Header.Set("X-CSRF-Token", "semantic-csrf")
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}
func TestKnowledgeQueryEmbeddingHTTPGetDoesNotCallOrWrite(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "semantic-http@example.com", "password123")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		data := make([]map[string]any, len(payload.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float64{1, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "vector", "data": data, "usage": map[string]int{"prompt_tokens": 4, "total_tokens": 4}})
	}))
	defer remote.Close()
	srv.selector.WithEmbedding("secret-sentinel", remote.URL, "vector", 2)
	client, _ := srv.selector.Embedding()
	cfg := client.Config()
	if err := srv.store.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(t.Context(), "学习原文", "主动回忆保留完整语境。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.ChangeKnowledgeEmbeddingScope(t.Context(), cfg.ID, 1, true, []store.EmbeddingSource{{SourceType: "document", SourceID: doc.ID}}); err != nil {
		t.Fatal(err)
	}
	windows, err := srv.store.PrepareKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	vectors := make([][]float32, len(windows))
	for i := range vectors {
		vectors[i] = []float32{1, 0}
	}
	if _, err = srv.store.AdoptKnowledgeEmbeddings(t.Context(), cfg.ID, windows, &provider.EmbeddingResult{Model: "vector", Dimensions: 2, Vectors: vectors}); err != nil {
		t.Fatal(err)
	}
	// Gate fixture only: these declared values do not establish real model quality.
	identity, e := srv.store.KnowledgeEmbeddingQualityIdentity(t.Context(), cfg.ID)
	if e != nil {
		t.Fatal(e)
	}
	report := store.EmbeddingQualityReport{Identity: identity, QueryManifestSHA256: strings.Repeat("a", 64), CorpusManifestSHA256: strings.Repeat("b", 64), RelevanceManifestSHA256: strings.Repeat("c", 64), SourceRevision: "test-fixture", Machine: "test-fixture", Method: "test-fixture-not-real-quality", Fusion: store.KnowledgeFusionVersion, RealMeasurements: true, Queries: 40, Samples: 20, PerformanceDimensions: 2048, Windows10k: 10000, Windows50k: 50000, FTSRecall10: .5, HybridRecall10: .6, RewriteFTSRecall10: .4, RewriteHybridRecall10: .6, Cold10kP95MS: 100, Cold50kP95MS: 400, Warm10kP95MS: 50, Warm50kP95MS: 100, EstimatedCostKnown: true, ActualCostKnown: true, PeakRSSBytes: 1024}
	if _, e = srv.store.SaveKnowledgeEmbeddingQualityReport(t.Context(), cfg.ID, report); e != nil {
		t.Fatal(e)
	}
	if e = srv.store.SetKnowledgeSemanticEnabled(t.Context(), cfg.ID, 2, true); e != nil {
		t.Fatal(e)
	}
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	page := doWithCookie(srv, cookie, "GET", "/search?q=主动回忆&semantic=1")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "semantic-query-form") || !strings.Contains(page.Body.String(), "本次使用FTS") {
		t.Fatal(page.Code, page.Body.String())
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if calls != 0 || before != after {
		t.Fatal("GET paid or wrote", calls, before, after)
	}
	command := knowledgeSemanticCommand{Action: "query", ConfigID: cfg.ID, Query: "如何应用", RequestKey: uuid.NewString()}
	raw, _ := json.Marshal(command)
	if rec := semanticPost(srv, nil, string(raw), true); rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	if rec := semanticPost(srv, cookie, string(raw), false); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	rec := semanticPost(srv, cookie, string(raw), true)
	if rec.Code != 200 || calls != 0 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var state knowledgeSemanticJobState
	json.Unmarshal(rec.Body.Bytes(), &state)
	if state.JobID == "" || state.QueryReady {
		t.Fatal(state)
	}
	duplicate := semanticPost(srv, cookie, string(raw), true)
	if duplicate.Code != 200 || !strings.Contains(duplicate.Body.String(), state.JobID) {
		t.Fatal(duplicate.Code, duplicate.Body.String())
	}
	changed := command
	changed.Query = "不同问题"
	changedRaw, _ := json.Marshal(changed)
	if rec := semanticPost(srv, cookie, string(changedRaw), true); rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	for _, path := range []string{"/api/knowledge-semantic?job_id=" + state.JobID, "/api/knowledge-semantic?request_action=query&request_key=" + command.RequestKey, "/api/knowledge-semantic?config_id=" + cfg.ID + "&query=如何应用", "/search?q=如何应用&semantic=1"} {
		page = doWithCookie(srv, cookie, "GET", path)
		if page.Code != 200 || strings.Contains(page.Body.String(), "secret-sentinel") || strings.Contains(page.Body.String(), remote.URL) {
			t.Fatal(path, page.Code, page.Body.String())
		}
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after || calls != 0 {
		t.Fatal("poll wrote/called")
	}
	if err = srv.worker.ProcessOne(t.Context()); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	ready := doWithCookie(srv, cookie, "GET", "/api/knowledge-semantic?job_id="+state.JobID)
	json.Unmarshal(ready.Body.Bytes(), &state)
	if !state.QueryReady || state.Status != "succeeded" {
		t.Fatal(state)
	}
	if rec := semanticPost(srv, cookie, strings.Repeat("x", 65537), true); rec.Code != 400 {
		t.Fatal("unbounded request", rec.Code)
	}
	for _, bad := range []string{`{"action":"query","secret":"x"}`, `{} {}`, `{"action":"unknown"}`} {
		if rec := semanticPost(srv, cookie, bad, true); rec.Code != 400 {
			t.Fatal(rec.Code)
		}
	}
}

func TestKnowledgeSearchSettingsReadonlyAndExplicitPreflight(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-settings@example.com", "password123")
	page := doWithCookie(srv, cookie, "GET", "/search/settings")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "独立 embedding 连接尚未配置") {
		t.Fatal(page.Code, page.Body.String())
	}
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer remote.Close()
	srv.selector.WithEmbedding("secret-sentinel", remote.URL, "vector", 0)
	var before, after int
	if err := srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	page = doWithCookie(srv, cookie, "GET", "/search/settings")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "明确预检连接") || strings.Contains(page.Body.String(), remote.URL) || strings.Contains(page.Body.String(), "secret-sentinel") {
		t.Fatal(page.Code, page.Body.String())
	}
	if err := srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after || calls != 0 {
		t.Fatal("settings GET mutated or called provider", before, after, calls, err)
	}
	key := uuid.NewString()
	body := `{"action":"preflight","request_key":"` + key + `"}`
	for _, csrf := range []bool{false, true, true} {
		result := semanticPost(srv, cookie, body, csrf)
		if !csrf {
			if result.Code != http.StatusForbidden {
				t.Fatal(result.Code)
			}
			continue
		}
		if result.Code != 200 {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	result := doWithCookie(srv, cookie, "GET", "/api/knowledge-semantic?request_action=preflight&request_key="+key)
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	var count int
	if err := srv.store.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&count); err != nil || count != 1 || calls != 0 {
		t.Fatal(count, calls, err)
	}
}

func TestQuestionStudyGenerationAutomationProjectionIsReadonly(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "study-automation@example.com", "password123")
	client, err := provider.NewSelector("", "").WithPod("secret-sentinel", "https://private-endpoint.example/v1", "generate").QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(t.Context(), "条件来源", "检查条件后再解释。")
	if err != nil {
		t.Fatal(err)
	}
	question, err := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "检查哪些条件？"})
	if err != nil {
		t.Fatal(err)
	}
	question, err = srv.store.ChangeLearningQuestion(t.Context(), question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: doc.ID}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := srv.store.StartQuestionStudySession(t.Context(), question.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, job, _, err := srv.store.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "解释条件", uuid.NewString(), nil, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	page := doWithCookie(srv, cookie, "GET", "/automation/"+job.ID)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "question-study-serialized-byte-bound-v1") || strings.Contains(page.Body.String(), "secret-sentinel") || strings.Contains(page.Body.String(), "private-endpoint") {
		t.Fatal(page.Code, page.Body.String())
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("GET admitted work", before, after)
	}
}
