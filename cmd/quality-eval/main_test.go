package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/woyin/orangecast/internal/evalset"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type manifestStore struct {
	manifest         *store.ArticleQualityManifest
	err              error
	closed           bool
	authorizations   int
	priceStore       *store.Store
	estimateErrStage string
}

func (s *manifestStore) AdmitArticleQualityStage(ctx context.Context, identity, contract, model string, estimate *provider.KnowledgeEstimate) error {
	if s.priceStore != nil {
		return s.priceStore.AdmitArticleQualityStage(ctx, identity, contract, model, estimate)
	}
	return nil
}
func (s *manifestStore) FinishArticleQualityStage(ctx context.Context, identity string, receipt models.UsageReceipt) error {
	if s.priceStore != nil {
		return s.priceStore.FinishArticleQualityStage(ctx, identity, receipt)
	}
	return nil
}
func (s *manifestStore) FreezeArticleQualityEstimate(ctx context.Context, name, model string, input *provider.KnowledgeArticleRequest) error {
	if input.Stage == s.estimateErrStage {
		return errors.New("price store unavailable")
	}
	if s.priceStore != nil {
		return s.priceStore.FreezeArticleQualityEstimate(ctx, name, model, input)
	}
	var err error
	input.Estimate, err = provider.EstimateKnowledgeRequest(*input, model)
	return err
}
func (s *manifestStore) Close() error { s.closed = true; return nil }
func (s *manifestStore) BuildArticleQualityManifest(_ context.Context, ids []string, name string) (*store.ArticleQualityManifest, error) {
	s.authorizations++
	if len(ids) != 1 || ids[0] != "case" || name != "pod" {
		return nil, errors.New("incorrect case authorization")
	}
	return s.manifest, s.err
}
func cliManifest(t *testing.T) *store.ArticleQualityManifest {
	t.Helper()
	input := provider.KnowledgeArticleRequest{Topic: &provider.KnowledgeTopic{Title: "自建", Question: "条件?", MaterialIDs: []string{"m"}}, Materials: []provider.KnowledgeMaterial{{ID: "m", Kind: "source_note", Content: "自建文本"}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return &store.ArticleQualityManifest{Version: 1, Provider: "pod", Fingerprint: "frozen", Cases: []*store.ArticleQualityCase{{ID: "case", State: "accepted", InputJSON: string(raw), Fingerprint: "case-frozen"}}}
}
func cliDeps(s *manifestStore) runDependencies {
	return runDependencies{open: func(string) (qualityStore, error) { return s, nil }, loadEnvironment: func(string) error { return nil }}
}
func cliArgs(out string, execute bool) []string {
	args := []string{"--db", "explicit.db", "--case", "case", "--output", out}
	if execute {
		args = append(args, "--run")
	}
	return args
}
func cliEnv(t *testing.T, base string) {
	t.Helper()
	for k, v := range map[string]string{"POD_BASE_URL": base, "POD_API_KEY": "private-key", "POD_MODEL": "model", "POD_WRITE_MODEL": "writer", "POD_REVIEW_MODEL": "reviewer", "POD_DISCOVERY_MODEL": "", "POD_SELECTION_MODEL": ""} {
		t.Setenv(k, v)
	}
}
func TestRunArgsValidationPreviewAndRepeatedParsing(t *testing.T) {
	s := &manifestStore{manifest: cliManifest(t)}
	deps := cliDeps(s)
	for _, args := range [][]string{nil, {"--bogus"}, {"positional"}, {"--db", "x", "--case", "case"}} {
		if err := runArgs(args, io.Discard, deps); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if s.authorizations != 0 {
		t.Fatal("invalid arguments opened source")
	}
	deps.open = func(string) (qualityStore, error) { return nil, errors.New("database unavailable") }
	if err := runArgs(cliArgs(t.TempDir(), false), io.Discard, deps); err == nil {
		t.Fatal("open error lost")
	}
	deps = cliDeps(s)
	s.err = errors.New("revoked")
	if err := runArgs(cliArgs(t.TempDir(), false), io.Discard, deps); err == nil || !s.closed {
		t.Fatal("authorization/close")
	}
	s.err = nil
	deps.loadEnvironment = func(string) error { t.Fatal("preview read .env"); return nil }
	out := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := runArgs(cliArgs(out, false), io.Discard, deps); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "preview-manifest.json"))
	if err != nil || !strings.Contains(string(raw), "case-frozen") {
		t.Fatalf("preview %s %v", raw, err)
	}
}
func TestRunArgsEnvironmentAndFrozenInputFailures(t *testing.T) {
	s := &manifestStore{manifest: cliManifest(t)}
	deps := cliDeps(s)
	deps.loadEnvironment = func(path string) error {
		if path != ".env" {
			t.Fatal(path)
		}
		return errors.New("environment unreadable")
	}
	if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, deps); err == nil {
		t.Fatal("env error lost")
	}
	deps = cliDeps(s)
	cliEnv(t, "")
	if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, deps); err == nil {
		t.Fatal("partial config accepted")
	}
	t.Setenv("POD_API_KEY", "")
	t.Setenv("POD_MODEL", "")
	if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, deps); err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Fatal(err)
	}
	cliEnv(t, "http://localhost")
	s.manifest.Cases[0].InputJSON = "bad-json"
	if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, deps); err == nil {
		t.Fatal("bad JSON accepted")
	}
	s.manifest.Cases[0].InputJSON = `{}`
	if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, deps); err == nil {
		t.Fatal("missing topic accepted")
	}
	s.manifest = cliManifest(t)
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), []byte("older"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runArgs(cliArgs(out, true), io.Discard, deps); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatal(err)
	}
}
func TestRunArgsPaidStagesFrozenRoutingReceiptsAndReplay(t *testing.T) {
	prices, err := store.Open(filepath.Join(t.TempDir(), "prices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer prices.Close()
	for _, price := range []models.ModelPrice{{Provider: "pod", Model: "writer", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 2000000}, {Provider: "pod", Model: "reviewer", InputCentsPerMillion: 3000000, OutputCentsPerMillion: 4000000}} {
		if err := prices.SetModelPrice(t.Context(), price); err != nil {
			t.Fatal(err)
		}
	}
	s := &manifestStore{manifest: cliManifest(t), priceStore: prices}
	s.manifest.Cases[0].Expected = "保留明确的材料边界"

	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 2 || strings.Count(body.Messages[1].Content, "Owner明确接纳的私有评测期望") != 1 {
			t.Errorf("exact expected instruction %#v", body.Messages)
		}
		content := `{"title":"自建素材","blocks":[{"kind":"synthesis","text":"保留边界","material_ids":["m"]},{"kind":"synthesis","text":"未知条件","material_ids":["m"]}]}`
		if calls == 1 && body.Model != "writer" {
			t.Error("write routing", body.Model)
		}
		if calls == 2 {
			if body.Model != "reviewer" {
				t.Error("review routing", body.Model)
			}
			content = `{"passed":true}`
		}
		json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 3}, "choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer remote.Close()
	cliEnv(t, remote.URL)
	out := t.TempDir()
	deps := cliDeps(s)
	if err := runArgs(cliArgs(out, true), io.Discard, deps); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || s.authorizations != 5 {
		t.Fatalf("calls %d authorizations %d", calls, s.authorizations)
	}
	if err := runArgs(cliArgs(out, true), io.Discard, deps); err != nil || calls != 2 {
		t.Fatalf("paid replay %v calls%d", err, calls)
	}
	for _, file := range []string{"manifest.json", "write.json", "review-manifest.json", "review.json", "report.json"} {
		info, err := os.Stat(filepath.Join(out, file))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private %s %v", file, err)
		}
	}
	for _, stage := range []struct {
		file                  string
		inputRate, outputRate int64
		actual                float64
	}{{"write.json", 1000000, 2000000, 13}, {"review.json", 3000000, 4000000, 33}} {
		raw, err := os.ReadFile(filepath.Join(out, stage.file))
		if err != nil {
			t.Fatal(err)
		}
		var receipt evalset.ArticleQualityRun
		if err := json.Unmarshal(raw, &receipt); err != nil {
			t.Fatal(err)
		}
		e := receipt.Input.Estimate
		if e == nil || !e.PriceKnown || e.PriceUpdatedAt == "" || e.InputCentsPerMillion != stage.inputRate || e.OutputCentsPerMillion != stage.outputRate || e.CostCents == nil || receipt.EstimatedCost == nil || receipt.ActualCost == nil || *receipt.ActualCost != stage.actual {
			t.Fatalf("wrong frozen stage price %+v receipt%+v", e, receipt)
		}
		exact, err := provider.EstimateKnowledgeRequest(receipt.Input, receipt.Model)
		if err != nil || exact.InputFingerprint != e.InputFingerprint {
			t.Fatalf("different dispatch messages %v", err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(out, "report.json"))
	if strings.Contains(string(raw), "private-key") || !strings.Contains(string(raw), "pending_human") {
		t.Fatal(string(raw))
	}
	if err := prices.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "writer", InputCentsPerMillion: 999999, OutputCentsPerMillion: 999999}); err != nil {
		t.Fatal(err)
	}
	if err := runArgs(cliArgs(out, true), io.Discard, deps); err == nil || !strings.Contains(err.Error(), "冲突") || calls != 2 {
		t.Fatalf("changed price silently reused receipts or dispatched %v calls%d", err, calls)
	}
}
func TestRunArgsFailureReportsNoHiddenRetry(t *testing.T) {
	for _, failReview := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "review"}[failReview], func(t *testing.T) {
			s := &manifestStore{manifest: cliManifest(t)}
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !failReview || calls == 2 {
					w.WriteHeader(503)
					io.WriteString(w, `{"model":"writer","usage":{"prompt_tokens":9,"completion_tokens":2}}`)
					return
				}
				io.WriteString(w, `{"model":"writer","usage":{"prompt_tokens":9,"completion_tokens":2},"choices":[{"message":{"content":"{\"title\":\"自建素材\",\"blocks\":[{\"kind\":\"synthesis\",\"text\":\"边界\",\"material_ids\":[\"m\"]},{\"kind\":\"synthesis\",\"text\":\"条件\",\"material_ids\":[\"m\"]}]}"}}]}`)
			}))
			defer remote.Close()
			cliEnv(t, remote.URL)
			out := t.TempDir()
			deps := cliDeps(s)
			if err := runArgs(cliArgs(out, true), io.Discard, deps); err == nil {
				t.Fatal("failure lost")
			}
			expected := 1
			if failReview {
				expected = 2
			}
			if calls != expected {
				t.Fatal("hidden retry", calls)
			}
			if err := runArgs(cliArgs(out, true), io.Discard, deps); err == nil || calls != expected {
				t.Fatal("failed receipt retry", err, calls)
			}
			raw, err := os.ReadFile(filepath.Join(out, "report.json"))
			if err != nil || !strings.Contains(string(raw), "failed_stage") || !strings.Contains(string(raw), "InputUnits") {
				t.Fatalf("report %s %v", raw, err)
			}
		})
	}
}
func TestRunActualArgumentMappingUsesExplicitDatabase(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"quality-eval", "--db", filepath.Join(t.TempDir(), "database.db"), "--case", "case", "--output", t.TempDir()}
	if err := run(); err == nil {
		t.Fatal("nonexistent accepted case unexpectedly available")
	}
}
func TestPrivateWritesAndFingerprint(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "blocking")
	if err := os.WriteFile(file, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := privateWrite(filepath.Join(file, "child"), nil); err == nil {
		t.Fatal("parent file accepted")
	}
	if err := privateWrite(dir, nil); err == nil {
		t.Fatal("directory accepted as file")
	}
	if err := frozenWrite(dir, nil); err == nil {
		t.Fatal("non-file accepted")
	}
	if got := priceFingerprint(provider.KnowledgeArticleRequest{Stage: "invalid", PromptVersion: provider.KnowledgeArticlePromptVersion}, "model"); got != "unknown-price" {
		t.Fatal(got)
	}
	if got := priceFingerprint(provider.KnowledgeArticleRequest{Stage: "write"}, "model"); len(got) != 64 {
		t.Fatal(got)
	}
}

func TestRunArgsArtifactFailuresPreserveReceipts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		writeFails bool
		block      string
	}{
		{"write-failure-report", true, "report.json"},
		{"review-manifest-conflict", false, "review-manifest.json"},
		{"successful-report-unwritable", false, "report.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &manifestStore{manifest: cliManifest(t)}
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.writeFails {
					w.WriteHeader(503)
					io.WriteString(w, `{}`)
					return
				}
				if calls == 2 {
					io.WriteString(w, `{"model":"reviewer","choices":[{"message":{"content":"{\"passed\":true}"}}]}`)
					return
				}
				io.WriteString(w, `{"model":"writer","choices":[{"message":{"content":"{\"title\":\"自建素材\",\"blocks\":[{\"kind\":\"synthesis\",\"text\":\"边界\",\"material_ids\":[\"m\"]},{\"kind\":\"synthesis\",\"text\":\"条件\",\"material_ids\":[\"m\"]}]}"}}]}`)
			}))
			defer remote.Close()
			cliEnv(t, remote.URL)
			out := t.TempDir()
			if tc.block == "report.json" {
				if err := os.Mkdir(filepath.Join(out, tc.block), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(out, tc.block), []byte("older manifest"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := runArgs(cliArgs(out, true), io.Discard, cliDeps(s)); err == nil {
				t.Fatal("artifact failure suppressed")
			}
			raw, err := os.ReadFile(filepath.Join(out, "write.json"))
			if err != nil || !strings.Contains(string(raw), "writer") {
				t.Fatalf("lost paid receipt %s %v", raw, err)
			}
			expected := 1
			if tc.name == "successful-report-unwritable" {
				expected = 2
			}
			if calls != expected {
				t.Fatalf("calls%d", calls)
			}
		})
	}
}

func TestRunArgsStagePriceFailuresNeverDispatchNextStage(t *testing.T) {
	for _, stage := range []string{"write", "review"} {
		t.Run(stage, func(t *testing.T) {
			s := &manifestStore{manifest: cliManifest(t), estimateErrStage: stage}
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				io.WriteString(w, `{"model":"writer","choices":[{"message":{"content":"{\"title\":\"自建素材\",\"blocks\":[{\"kind\":\"synthesis\",\"text\":\"边界\",\"material_ids\":[\"m\"]},{\"kind\":\"synthesis\",\"text\":\"条件\",\"material_ids\":[\"m\"]}]}"}}]}`)
			}))
			defer remote.Close()
			cliEnv(t, remote.URL)
			if err := runArgs(cliArgs(t.TempDir(), true), io.Discard, cliDeps(s)); err == nil || err.Error() != "price store unavailable" {
				t.Fatal(err)
			}
			expected := 0
			if stage == "review" {
				expected = 1
			}
			if calls != expected {
				t.Fatalf("dispatched unpriced stage calls%d", calls)
			}
		})
	}
}
func TestPriceFingerprintUsesFrozenRatesAndVersion(t *testing.T) {
	input := provider.KnowledgeArticleRequest{Stage: "write", Estimate: &provider.KnowledgeEstimate{PriceKnown: true, PriceUpdatedAt: "v1", InputCentsPerMillion: 100, OutputCentsPerMillion: 200}}
	original := priceFingerprint(input, "writer")
	input.Estimate.OutputCentsPerMillion++
	if priceFingerprint(input, "writer") == original {
		t.Fatal("rate changes did not change identity")
	}
	input.Estimate.OutputCentsPerMillion--
	input.Estimate.PriceUpdatedAt = "v2"
	if priceFingerprint(input, "writer") == original {
		t.Fatal("price version did not change identity")
	}
	input.Estimate.PriceUpdatedAt = "v1"
	input.Estimate.PriceKnown = false
	if priceFingerprint(input, "writer") == original {
		t.Fatal("unknown price treated as known")
	}
}

func TestRunArgsConfiguredBudgetRejectsUnpricedAndExhaustedWithoutHTTP(t *testing.T) {
	for _, priced := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpriced", true: "exhausted"}[priced], func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "budget.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			budget := int64(1)
			if err := db.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
				t.Fatal(err)
			}
			if priced {
				if err := db.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "writer", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 1000000}); err != nil {
					t.Fatal(err)
				}
			}
			s := &manifestStore{manifest: cliManifest(t), priceStore: db}
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("budget rejection called remote") }))
			defer remote.Close()
			cliEnv(t, remote.URL)
			err = runArgs(cliArgs(t.TempDir(), true), io.Discard, cliDeps(s))
			wanted := store.ErrBudgetUnpriced
			if priced {
				wanted = store.ErrBudgetExhausted
			}
			if !errors.Is(err, wanted) || calls != 0 {
				t.Fatalf("wrong admission %v calls%d", err, calls)
			}
			for _, table := range []string{"budget_reservations", "usage_records"} {
				var count int
				if err := db.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("spurious %s count%d err%v", table, count, err)
				}
			}
		})
	}
}
func TestRunArgsUnknownOutcomePendingAndLostReceiptNeverRedispatches(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &manifestStore{manifest: cliManifest(t), priceStore: db}
	var calls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer remote.Close()
	cliEnv(t, remote.URL)
	out := t.TempDir()
	args := cliArgs(out, true)
	deps := cliDeps(s)
	for i := 0; i < 2; i++ {
		if err := runArgs(args, io.Discard, deps); err == nil {
			t.Fatal("unknown outcome passed")
		}
		if calls.Load() != 1 {
			t.Fatal("unknown outcome retried", calls.Load())
		}
	}
	var pending, unknown int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM budget_reservations WHERE status='pending_remote' AND actual_cost_cents IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("unknown reservation%d %v", pending, err)
	}
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE estimated_cost IS NULL`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("unknown usage%d %v", unknown, err)
	}
	if err := os.Remove(filepath.Join(out, "write.json")); err != nil {
		t.Fatal(err)
	}
	if err := runArgs(args, io.Discard, deps); err == nil || calls.Load() != 1 {
		t.Fatalf("lost receipt redispatched %v calls%d", err, calls.Load())
	}
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("double ledger%d %v", unknown, err)
	}
}
func TestRunArgsLedgerFailureRecoversReceiptWithoutRepeatingPaidWrite(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, model := range []string{"writer", "reviewer"} {
		if err := db.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: model, InputCentsPerMillion: 1000000, OutputCentsPerMillion: 1000000}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.Exec(`CREATE TRIGGER reject_quality_cli_usage BEFORE INSERT ON usage_records BEGIN SELECT RAISE(ABORT,'ledger unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	s := &manifestStore{manifest: cliManifest(t), priceStore: db}
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		content := `{"title":"自建素材","blocks":[{"kind":"synthesis","text":"边界","material_ids":["m"]},{"kind":"synthesis","text":"条件","material_ids":["m"]}]}`
		if body.Model == "reviewer" {
			content = `{"passed":true}`
		}
		json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 3}, "choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer remote.Close()
	cliEnv(t, remote.URL)
	out := t.TempDir()
	args := cliArgs(out, true)
	deps := cliDeps(s)
	if err := runArgs(args, io.Discard, deps); err == nil || calls != 1 {
		t.Fatalf("ledger fault %v calls%d", err, calls)
	}
	raw, err := os.ReadFile(filepath.Join(out, "write.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt evalset.ArticleQualityRun
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.Status != "succeeded" || !receipt.UsageKnown || receipt.Usage.InputUnits != 7 {
		t.Fatalf("lost metered receipt %+v %v", receipt, err)
	}
	if _, err := db.DB.Exec(`DROP TRIGGER reject_quality_cli_usage`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runArgs(args, io.Discard, deps); err != nil || calls != 2 {
			t.Fatalf("ledger recovery/replay %v calls%d", err, calls)
		}
	}
	var count, cost, settled int
	if err := db.DB.QueryRow(`SELECT COUNT(*),SUM(estimated_cost) FROM usage_records`).Scan(&count, &cost); err != nil || count != 2 || cost != 20 {
		t.Fatalf("double-cost count%d cost%d %v", count, cost, err)
	}
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM budget_reservations WHERE status='settled'`).Scan(&settled); err != nil || settled != 2 {
		t.Fatalf("settled%d %v", settled, err)
	}
}
