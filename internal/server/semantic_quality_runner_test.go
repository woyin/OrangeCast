package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// Exercise the external runner against actual authenticated API JSON. Synthetic
// fixtures only prove integration/read-only degradation, never real Recall.
func TestKnowledgeSemanticQualityRunnerReadonly(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "semantic-runner@example.com", "password123")
	srv.selector.WithEmbedding("unused-test-secret", "https://unused.example/v1", "vector", 2)
	client, err := srv.selector.Embedding()
	if err != nil {
		t.Fatal(err)
	}
	cfg := client.Config()
	if err = srv.store.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(srv.Router())
	defer remote.Close()
	queries := make([]map[string]any, 40)
	for i := range queries {
		group := "original"
		if i >= 20 {
			group = "rewrite"
		}
		queries[i] = map[string]any{"id": strconv.Itoa(i), "query": "测试查询 " + strconv.Itoa(i), "group": group, "relevant_keys": []string{"note:test-only"}}
	}
	dir := t.TempDir()
	manifest, output := filepath.Join(dir, "manifest.json"), filepath.Join(dir, "report.json")
	data, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), python, "../../scripts/semantic-quality-eval.py", "--base-url", remote.URL, "--config-id", cfg.ID, "--manifest", manifest, "--output", output)
	cmd.Env = append(os.Environ(), "CWP_SESSION_COOKIE="+cookie.Name+"="+cookie.Value)
	log, runErr := cmd.CombinedOutput()
	exit, ok := runErr.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("expected incomplete evaluation: %v %s", runErr, log)
	}
	result, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("%v %s", err, log)
	}
	var report struct {
		Status         string `json:"status"`
		IdentityStable bool   `json:"identity_stable"`
		Queries        []struct {
			SemanticAvailable bool     `json:"semantic_available"`
			HybridRecall      *float64 `json:"hybrid_recall_at_10"`
		} `json:"queries"`
	}
	if err = json.Unmarshal(result, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || !report.IdentityStable || len(report.Queries) != 40 {
		t.Fatalf("unexpected report: %s", result)
	}
	for _, row := range report.Queries {
		if row.SemanticAvailable || row.HybridRecall != nil {
			t.Fatal("FTS fallback became semantic recall")
		}
	}
	if err = srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("runner wrote database", before, after, err)
	}
}
