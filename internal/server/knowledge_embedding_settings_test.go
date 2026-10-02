package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func embeddingSettingsPost(srv *Server, cookie *http.Cookie, value any, csrf bool) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(value)
	req := httptest.NewRequest("POST", "/api/knowledge-search-settings", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "settings-csrf"})
		req.Header.Set("X-CSRF-Token", "settings-csrf")
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}
func TestEmbeddingSettingsHTTPPermissionsCASAndNoPaidReads(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-edit@example.com", "password123")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer remote.Close()
	srv.selector.WithEmbedding("secret-sentinel", remote.URL, "vector", 2)
	client, _ := srv.selector.Embedding()
	cfg := client.Config()
	if err := srv.store.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(t.Context(), "个人资料", "我的私有原文不得出现在设置。")
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.SetSourceProductionPolicy(t.Context(), models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	command := map[string]any{"action": "settings", "config_id": cfg.ID, "expected_revision": 1, "index_authorized": true, "window_capacity": 123, "input_cents_per_million": 17, "sources": []store.EmbeddingSource{{SourceType: "document", SourceID: doc.ID}}}
	if rec := embeddingSettingsPost(srv, nil, command, true); rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	if rec := embeddingSettingsPost(srv, cookie, command, false); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if rec := embeddingSettingsPost(srv, cookie, command, true); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := embeddingSettingsPost(srv, cookie, command, true); rec.Code != 409 {
		t.Fatal("stale scope changed", rec.Code, rec.Body.String())
	}
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	page := doWithCookie(srv, cookie, "GET", "/search/settings")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "不会外发") || !strings.Contains(page.Body.String(), "尚无真实质量") || !strings.Contains(page.Body.String(), "disabled") {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, secret := range []string{"secret-sentinel", remote.URL, "我的私有原文不得出现在设置"} {
		if strings.Contains(page.Body.String(), secret) {
			t.Fatal("settings leaked", secret)
		}
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if calls != 0 || before != after {
		t.Fatal("GET paid or mutated", calls, before, after)
	}
	activate := map[string]any{"action": "activate", "config_id": cfg.ID, "expected_revision": 2, "enabled": true}
	if rec := embeddingSettingsPost(srv, cookie, activate, true); rec.Code != 400 {
		t.Fatal("no report enabled", rec.Code, rec.Body.String())
	}
	query := `{"action":"query","config_id":"` + cfg.ID + `","request_key":"00000000-0000-4000-8000-000000000001","query":"如何解释"}`
	if rec := semanticPost(srv, cookie, query, true); rec.Code != 409 {
		t.Fatal("paid query without admission", rec.Code, rec.Body.String())
	}
	var jobs int
	srv.store.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs)
	if jobs != 0 || calls != 0 {
		t.Fatal("gate scheduled paid jobs", jobs, calls)
	}
	command["action"] = "preview"
	rec := embeddingSettingsPost(srv, cookie, command, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"may_send":false`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	command["unknown_field"] = true
	if rec = embeddingSettingsPost(srv, cookie, command, true); rec.Code != 400 {
		t.Fatal("unknown fields accepted", rec.Code)
	}
}

func TestEmbeddingEvaluationExplicitPrepareWithoutActivatingSearch(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-eval@example.com", "password123")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"vector","data":[{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}))
	defer remote.Close()
	srv.selector.WithEmbedding("secret-sentinel", remote.URL, "vector", 2)
	client, _ := srv.selector.Embedding()
	cfg := client.Config()
	if err := srv.store.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(t.Context(), "评测原文", "主动回忆需要条件。")
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
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	path := "/api/knowledge-search-settings?config_id=" + cfg.ID + "&action=evaluate&q=主动回忆"
	page := doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `"read_only":true`) {
		t.Fatal(page.Code, page.Body.String())
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after || calls != 0 {
		t.Fatal("evaluation GET called or wrote", before, after, calls)
	}
	command := knowledgeSemanticCommand{Action: "evaluation_query", ConfigID: cfg.ID, ExpectedRevision: 2, Query: "主动回忆", RequestKey: uuid.NewString()}
	raw, _ := json.Marshal(command)
	for i := 0; i < 2; i++ {
		rec := semanticPost(srv, cookie, string(raw), true)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	changed := command
	changed.ExpectedRevision = 1
	changed.RequestKey = uuid.NewString()
	stale, _ := json.Marshal(changed)
	if rec := semanticPost(srv, cookie, string(stale), true); rec.Code != 409 {
		t.Fatal("stale evaluation scope admitted", rec.Code)
	}
	var jobs int
	srv.store.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs)
	if jobs != 1 || calls != 0 {
		t.Fatal("duplicate eval paid", jobs, calls)
	}
	if err = srv.worker.ProcessOne(t.Context()); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	page = doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `"Method":"rrf"`) {
		t.Fatal(page.Code, page.Body.String())
	}
	cfgAfter, err := srv.store.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if err != nil || cfgAfter.SemanticEnabled {
		t.Fatal("evaluation activated production", cfgAfter, err)
	}
	command.Action = "query"
	command.RequestKey = uuid.NewString()
	raw, _ = json.Marshal(command)
	if rec := semanticPost(srv, cookie, string(raw), true); rec.Code != 409 {
		t.Fatal("production query skipped gate", rec.Code)
	}
}

func TestEmbeddingSettingsControllerPreviewRemoveAndCASRetainsInputs(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for settings interaction validation")
	}
	output, err := exec.Command("node", "testdata/knowledge-search-settings.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func embeddingSettingsForm(srv *Server, cookie *http.Cookie, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/search/settings", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "settings-csrf"})
	req.Header.Set("X-CSRF-Token", "settings-csrf")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}
func embeddingSettingsFixture(t *testing.T) (*Server, *http.Cookie, provider.EmbeddingConfig, *models.Document, *int) {
	t.Helper()
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "embedding-contract@example.com", "password123")
	calls := new(int)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { *calls++ }))
	t.Cleanup(remote.Close)
	srv.selector.WithEmbedding("secret-sentinel", remote.URL, "vector", 2)
	client, _ := srv.selector.Embedding()
	cfg := client.Config()
	if err := srv.store.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.store.CreatePastedDocument(t.Context(), "明确范围", "不应出现在质量设置中的原文")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.ChangeKnowledgeEmbeddingScope(t.Context(), cfg.ID, 1, true, []store.EmbeddingSource{{SourceType: "document", SourceID: doc.ID}}); err != nil {
		t.Fatal(err)
	}
	return srv, cookie, cfg, doc, calls
}
func embeddingDeclaredReport(t *testing.T, srv *Server, id string) store.EmbeddingQualityReport {
	t.Helper()
	identity, err := srv.store.KnowledgeEmbeddingQualityIdentity(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return store.EmbeddingQualityReport{Identity: identity, QueryManifestSHA256: strings.Repeat("a", 64), CorpusManifestSHA256: strings.Repeat("b", 64), RelevanceManifestSHA256: strings.Repeat("c", 64), SourceRevision: "test-declaration-not-real-quality", Machine: "test-only", Method: "contract validation fixture", Fusion: "rrf-k60-lexical-semantic-v1", RealMeasurements: true, PerformanceDimensions: 2048, Windows10k: 10000, Windows50k: 50000, Queries: 40, Samples: 20, FTSRecall10: .5, HybridRecall10: .6, RewriteFTSRecall10: .4, RewriteHybridRecall10: .6, Cold10kP95MS: 100, Cold50kP95MS: 400, Warm10kP95MS: 50, Warm50kP95MS: 100, PeakRSSBytes: 1024, EstimatedCostKnown: true, ActualCostKnown: true}
}
func TestEmbeddingSettingsReportImportActivationAndSSRKeepScope(t *testing.T) {
	srv, cookie, cfg, doc, calls := embeddingSettingsFixture(t)
	report := embeddingDeclaredReport(t, srv, cfg.ID)
	report.Cold10kP95MS = 200
	report.Passed = true
	rec := embeddingSettingsPost(srv, cookie, map[string]any{"action": "report", "config_id": cfg.ID, "report": report}, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"passed":false`) {
		t.Fatal("caller report forced pass", rec.Code, rec.Body.String())
	}
	read := doWithCookie(srv, cookie, "GET", "/api/knowledge-search-settings?config_id="+cfg.ID)
	if read.Code != 200 || !strings.Contains(read.Body.String(), "未达到门槛") {
		t.Fatal(read.Code, read.Body.String())
	}
	report.Cold10kP95MS = 100
	payload, _ := json.Marshal(report)
	form := url.Values{"action": {"report"}, "config_id": {cfg.ID}, "report": {string(payload)}}
	if rec = embeddingSettingsForm(srv, cookie, form); rec.Code != 303 || rec.Header().Get("Location") != "/search/settings" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	current, _ := srv.store.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if current.SemanticEnabled {
		t.Fatal("report import activated without consent")
	}
	if rec = embeddingSettingsPost(srv, cookie, map[string]any{"action": "activate", "config_id": cfg.ID, "expected_revision": 2, "enabled": true}, true); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = embeddingSettingsForm(srv, cookie, url.Values{"action": {"activate"}, "config_id": {cfg.ID}, "expected_revision": {"2"}, "enabled": {"0"}}); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	before, _ := srv.store.KnowledgeEmbeddingQualityIdentity(t.Context(), cfg.ID)
	proposed := url.Values{"action": {"preview"}, "config_id": {cfg.ID}, "expected_revision": {"2"}, "index_authorized": {"1"}, "window_capacity": {"100"}, "input_cents_per_million": {"29"}, "sources": {`[{"source_type":"document","source_id":"` + doc.ID + `"}]`}, "understanding_snapshot_ids": {"[]"}}
	rec = embeddingSettingsForm(srv, cookie, proposed)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "没有保存范围") || !strings.Contains(rec.Body.String(), `value="29"`) {
		t.Fatal("SSR preview discarded proposed inputs", rec.Code, rec.Body.String())
	}
	after, _ := srv.store.KnowledgeEmbeddingQualityIdentity(t.Context(), cfg.ID)
	if before != after {
		t.Fatal("preview mutated index identity")
	}
	proposed.Set("action", "settings")
	if rec = embeddingSettingsForm(srv, cookie, proposed); rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	current, _ = srv.store.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	price, _ := srv.store.GetModelPrice(t.Context(), cfg.Provider, cfg.Model)
	if current.Revision != 3 || current.WindowCapacity != 100 || current.SemanticEnabled || price.InputCentsPerMillion != 29 || *calls != 0 {
		t.Fatal(current, price, *calls)
	}
}
func TestEmbeddingSettingsMalformedCommandsStayUnpaid(t *testing.T) {
	srv, cookie, cfg, doc, calls := embeddingSettingsFixture(t)
	cases := []url.Values{
		{"action": {"settings"}, "config_id": {cfg.ID}, "input_cents_per_million": {"not-price"}},
		{"action": {"settings"}, "config_id": {cfg.ID}, "sources": {"bad-json"}},
		{"action": {"settings"}, "config_id": {cfg.ID}, "sources": {"[]"}, "understanding_snapshot_ids": {"bad-json"}},
		{"action": {"report"}, "config_id": {cfg.ID}, "report": {"not-json"}},
		{"action": {"report"}, "config_id": {cfg.ID}, "report": {"{} {}"}},
		{"action": {"unknown"}, "config_id": {cfg.ID}},
	}
	for i, values := range cases {
		if rec := embeddingSettingsForm(srv, cookie, values); rec.Code != 400 {
			t.Fatalf("invalid form %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	request := func(raw string, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/knowledge-search-settings", strings.NewReader(raw))
		req.Header.Set("Content-Type", contentType)
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "settings-csrf"})
		req.Header.Set("X-CSRF-Token", "settings-csrf")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	for _, raw := range []string{`{} {}`, strings.Repeat(" ", 512<<10) + `{}`, `{"unknown_field":true}`, `{"action":"settings","config_id":"missing"}`} {
		rec := request(raw, "application/json")
		if rec.Code != 400 && rec.Code != 404 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	if rec := request("broken=%ZZ", "application/x-www-form-urlencoded"); rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	current, _ := srv.store.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if current.Revision != 2 || *calls != 0 {
		t.Fatal("invalid input modified config or paid", current, *calls)
	}
	original, _ := json.Marshal(map[string]any{"action": "preview", "config_id": cfg.ID, "sources": []store.EmbeddingSource{{SourceType: "document", SourceID: doc.ID}}})
	srv.selector.WithEmbedding("secret-sentinel", "https://changed.invalid/v1", "vector", 2)
	if rec := request(string(original), "application/json"); rec.Code != 409 {
		t.Fatal("changed route reused prior scope", rec.Code)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/search/settings", nil)
		rec := httptest.NewRecorder()
		srv.handleKnowledgeSearchSettings(rec, req)
		if rec.Code != 405 {
			t.Fatal(rec.Code)
		}
	}
}
func TestEmbeddingSettingsClosedDatabaseReturnsNoPrivateProjection(t *testing.T) {
	srv, _, cfg, _, calls := embeddingSettingsFixture(t)
	if err := srv.store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	// The handler unit covers storage disappearing after authentication; the Router's
	// authentication failures are tested separately and must not bypass authentication.
	for _, path := range []string{"/search/settings", "/api/knowledge-search-settings?config_id=" + cfg.ID} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		srv.handleKnowledgeSearchSettings(rec, req)
		if rec.Code < 400 || strings.Contains(rec.Body.String(), "secret-sentinel") || strings.Contains(rec.Body.String(), "不应出现在质量设置中的原文") {
			t.Fatal(path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/api/knowledge-search-settings", strings.NewReader(`{"action":"activate","config_id":"`+cfg.ID+`","expected_revision":2,"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleKnowledgeSearchSettings(rec, req)
	if rec.Code < 400 || *calls != 0 {
		t.Fatal(rec.Code, *calls)
	}
}
