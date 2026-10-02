package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRerankProtocolAndValidation(t *testing.T) {
	cases := []struct {
		name, body  string
		status      int
		ok, receipt bool
	}{
		{"valid", `{"model":"rank","results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}],"usage":{"prompt_tokens":12,"total_tokens":12}}`, 200, true, false},
		{"unbounded-scores", `{"model":"rank","results":[{"index":0,"relevance_score":-2.5},{"index":1,"relevance_score":3.5}]}`, 200, true, false},
		{"no-usage", `{"model":"rank","results":[{"index":0,"relevance_score":0},{"index":1,"relevance_score":1}]}`, 200, true, false},
		{"model", `{"model":"other","results":[],"usage":{"prompt_tokens":12,"total_tokens":12}}`, 200, false, true},
		{"missing-score", `{"model":"rank","results":[{"index":0},{"index":1,"relevance_score":1}],"usage":{"prompt_tokens":12,"total_tokens":12}}`, 200, false, true},
		{"duplicate", `{"model":"rank","results":[{"index":0,"relevance_score":0},{"index":0,"relevance_score":1}]}`, 200, false, false},
		{"missing-index", `{"model":"rank","results":[{"relevance_score":0},{"index":1,"relevance_score":1}]}`, 200, false, false},
		{"outside", `{"model":"rank","results":[{"index":2,"relevance_score":0},{"index":1,"relevance_score":1}]}`, 200, false, false},
		{"score", `{"model":"rank","results":[{"index":0,"relevance_score":1e999},{"index":1,"relevance_score":1}]}`, 200, false, false},
		{"usage", `{"model":"rank","results":[{"index":0,"relevance_score":0},{"index":1,"relevance_score":1}],"usage":{"prompt_tokens":-1,"total_tokens":-1}}`, 200, false, false},
		{"json", `bad`, 200, false, false}, {"status", `secret-sentinel`, 429, false, false}, {"redirect", ``, 302, false, false}, {"bounded", strings.Repeat("x", 1048577), 200, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/rerank" || r.Header.Get("Authorization") != "Bearer secret-sentinel" {
					t.Error("route")
				}
				var v map[string]any
				json.NewDecoder(r.Body).Decode(&v)
				if v["return_documents"] != false || v["top_n"] != float64(2) {
					t.Error("bounded contract")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			p, err := NewRerankClient("secret-sentinel", srv.URL+"/v1", "rank")
			if err != nil || calls != 0 {
				t.Fatal(err)
			}
			cfg, _ := json.Marshal(p.Config())
			if strings.Contains(string(cfg), "secret-sentinel") || strings.Contains(string(cfg), srv.URL) {
				t.Fatal("secret provenance")
			}
			r, err := p.Rerank(context.Background(), "query", []string{"a", "b"})
			if (err == nil) != tc.ok || calls != 1 {
				t.Fatal(r, err, calls)
			}
			var re *RerankResponseError
			if errors.As(err, &re) != tc.receipt {
				t.Fatal("lost receipt", err)
			}
			if tc.receipt && (re.Receipt.InputTokens != 12 || re.Unwrap() == nil || re.Error() == "") {
				t.Fatal(re)
			}
			if tc.name == "valid" && (r.Scores[0] != .1 || r.Scores[1] != .9 || r.InputTokens != 12 || !r.UsageKnown) {
				t.Fatal(r)
			}
		})
	}
}
func TestRerankInputAndConfigBounds(t *testing.T) {
	for _, v := range []struct{ key, base, model string }{{"", "https://api.jina.ai/v1", "rank"}, {"key", "bad", "rank"}, {"key", "https://user:pass@api.jina.ai/v1", "rank"}, {"key", "https://api.jina.ai/v1?q=1", "rank"}, {"key", "https://api.jina.ai/v1", strings.Repeat("a", 201)}, {"ke\ny", "https://api.jina.ai/v1", "rank"}} {
		if _, err := NewRerankClient(v.key, v.base, v.model); err == nil {
			t.Fatal(v.base)
		}
	}
	for _, v := range []struct {
		q    string
		docs []string
	}{{"", []string{"a"}}, {strings.Repeat("a", 2049), []string{"b"}}, {"q", nil}, {"q", []string{""}}, {"q", []string{strings.Repeat("a", 8193)}}, {"q", []string{string([]byte{255})}}, {"q", make([]string, 31)}} {
		if _, err := EstimateRerankInputs(v.q, v.docs); err == nil {
			t.Fatal("unbounded request")
		}
	}
	docs := make([]string, 30)
	for i := range docs {
		docs[i] = strings.Repeat("a", 8192)
	}
	if _, err := EstimateRerankInputs("q", docs); err == nil {
		t.Fatal("total bound")
	}
	p, _ := NewRerankClient("key", "http://127.0.0.1:1", "rank")
	if _, err := p.Rerank(context.Background(), "q", []string{"a"}); err == nil {
		t.Fatal("transport")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Rerank(ctx, "q", []string{"a"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Rerank(ctx, "", nil); err == nil {
		t.Fatal("preflight")
	}
	sel := NewSelector("", "")
	if _, err := sel.Reranker(); err == nil {
		t.Fatal("text fallback")
	}
	sel.WithRerank("key", "https://api.jina.ai/v1", "rank")
	if _, err := sel.Reranker(); err != nil {
		t.Fatal(err)
	}
}
func TestEmbeddingRetrievalRolesAndIdentity(t *testing.T) {
	base, _ := NewEmbeddingClient("key", "https://api.jina.ai/v1", "jina-embeddings-v5-text-small", 2)
	prof, err := base.WithProfile("jina-retrieval-v1")
	if err != nil || prof.Config().ID == base.Config().ID || prof.Config().ConnectionID != base.Config().ConnectionID {
		t.Fatal(err)
	}
	fixed, err := prof.WithDimensions(4)
	if err != nil || fixed.Config().Profile != prof.Config().Profile {
		t.Fatal(err)
	}
	tasks := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		task := body["task"]
		if value, ok := task.(string); ok {
			tasks = append(tasks, value)
		}
		if task != "retrieval.query" && task != "retrieval.passage" {
			t.Error(task)
		}
		fmt.Fprint(w, `{"model":"jina-embeddings-v5-text-small","data":[{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`)
	}))
	defer srv.Close()
	prof.baseURL = srv.URL
	if _, err = prof.Embed(context.Background(), []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if _, err = prof.EmbedQuery(context.Background(), []string{"query"}); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0] != "retrieval.passage" || tasks[1] != "retrieval.query" {
		t.Fatal(tasks)
	}
	if _, err = base.WithProfile("unknown"); err == nil {
		t.Fatal("unknown profile")
	}
	other, _ := NewEmbeddingClient("key", "https://other.example/v1", "vector", 2)
	if _, err = other.WithProfile("jina-retrieval-v1"); err == nil {
		t.Fatal("unsupported route")
	}
	if _, err = base.WithProfile(""); err != nil {
		t.Fatal(err)
	}
}

func TestRerankRedirectDoesNotForwardCredentials(t *testing.T) {
	forwarded := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++ }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	p, err := NewRerankClient("private-key", origin.URL, "rank")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Rerank(t.Context(), "query", []string{"document"}); err == nil || forwarded != 0 {
		t.Fatal("redirect followed", err, forwarded)
	}
	if _, err = p.Rerank(nil, "query", []string{"document"}); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestSelectorIndependentRetrievalProfile(t *testing.T) {
	sel := NewSelector("", "").WithEmbedding("key", "https://api.jina.ai/v1", "jina-embeddings-v5-text-small", 1024).WithEmbeddingProfile("jina-retrieval-v1")
	p, err := sel.Embedding()
	if err != nil || p.Config().Profile != "jina-retrieval-v1" {
		t.Fatal(p, err)
	}
	sel.WithEmbeddingProfile("unsupported")
	if _, err = sel.Embedding(); err == nil {
		t.Fatal("invalid vector profile accepted")
	}
	for _, name := range []string{"openai", "groq"} {
		id := sel.TranscriptionConnectionID(name)
		if len(id) != 64 || id != TranscriptionConnectionFingerprint(name, "") || id != TranscriptionConnectionFingerprint(name, map[string]string{"openai": openaiBaseURL, "groq": groqBaseURL}[name]+"/") {
			t.Fatal("unstable route identity", name, id)
		}
	}
}
