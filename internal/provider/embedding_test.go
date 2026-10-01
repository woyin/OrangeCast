package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmbeddingProtocol(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/embeddings" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret-sentinel" {
			t.Error("wrong independent transport")
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if string(body["encoding_format"]) != `"float"` || string(body["model"]) != `"vector"` || body["dimensions"] != nil {
			t.Error("autodetect must omit wire dimensions")
		}
		fmt.Fprint(w, `{"model":"vector","data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,4]}],"usage":{"prompt_tokens":9,"total_tokens":9}}`)
	}))
	defer srv.Close()
	sel := NewSelector("groq-secret", "text-secret").WithPod("pod-secret", srv.URL+"/text", "text")
	if _, err := sel.Embedding(); err != ErrEmbeddingUnavailable {
		t.Fatal("text fallback")
	}
	sel.WithEmbedding("secret-sentinel", srv.URL+"/v1", "vector", 0)
	p, err := sel.Embedding()
	if err != nil || calls.Load() != 0 {
		t.Fatal("construction did I/O")
	}
	cfg, _ := json.Marshal(p.Config())
	if strings.Contains(string(cfg), "secret-sentinel") || strings.Contains(string(cfg), srv.URL) {
		t.Fatal("unsafe provenance")
	}
	r, err := p.Embed(context.Background(), []string{"甲", "乙"})
	if err != nil || !r.UsageKnown || r.InputTokens != 9 || r.Dimensions != 2 || math.Abs(float64(r.Vectors[0][0])-.6) > .000001 || r.Vectors[1][1] != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	fixed, err := p.WithDimensions(2)
	if err != nil || fixed.Config().ID == p.Config().ID {
		t.Fatal("dimension identity")
	}
	if _, err = fixed.Embed(context.Background(), []string{"甲", "乙"}); err != nil {
		t.Fatal(err)
	}
	rotated, _ := NewEmbeddingClient("rotated-secret", srv.URL+"/v1", "vector", 0)
	if rotated.Config() != p.Config() {
		t.Fatal("credential rotation must preserve route identity")
	}
	e, err := EstimateEmbeddingInputs([]string{"你好"})
	if err != nil || e.OutputTokens != 0 || !e.Approximate || e.PriceKnown || e.CostCents != nil || e.InputTokens < 6 {
		t.Fatalf("bad estimate %+v %v", e, err)
	}
}

func TestEmbeddingRejectsInvalidResults(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		dimensions int
	}{
		{"duplicate", `{"model":"vector","data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[1,0]}]}`, 200, 0},
		{"missing", `{"model":"vector","data":[{"embedding":[1,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"negative", `{"model":"vector","data":[{"index":-1,"embedding":[1,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"mixed", `{"model":"vector","data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1]}]}`, 200, 0},
		{"changed", `{"model":"vector","data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1,0]}]}`, 200, 3},
		{"zero", `{"model":"vector","data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"infinite", `{"model":"vector","data":[{"index":0,"embedding":[1e999,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"nan", `{"model":"vector","data":[{"index":0,"embedding":[NaN,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"wrong_model", `{"model":"other","data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1,0]}]}`, 200, 0},
		{"usage", `{"model":"vector","data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1,0]}],"usage":{"prompt_tokens":2,"total_tokens":3}}`, 200, 0},
		{"http", "secret-sentinel", 429, 0}, {"redirect", "secret-sentinel", 302, 0}, {"truncated", `{"model":`, 200, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			p, _ := NewEmbeddingClient("secret-sentinel", srv.URL, "vector", tc.dimensions)
			r, err := p.Embed(context.Background(), []string{"a", "b"})
			if err == nil || r != nil || calls != 1 || strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatalf("invalid accepted/retried/leaked: %v", err)
			}
		})
	}
}

func TestEmbeddingBoundsAndUnknownUsage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if string(body["dimensions"]) != "2" {
			t.Error("explicit dimension not sent")
		}
		fmt.Fprint(w, `{"model":"vector","data":[{"index":0,"embedding":[1,0]}]}`)
	}))
	defer srv.Close()
	p, _ := NewEmbeddingClient("key", srv.URL, "vector", 2)
	for _, inputs := range [][]string{nil, {" "}, {strings.Repeat("a", 8193)}, make([]string, 17), {string([]byte{255})}} {
		if _, err := p.Embed(context.Background(), inputs); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid request called provider")
	}
	r, err := p.Embed(context.Background(), []string{"a"})
	if err != nil || r.UsageKnown {
		t.Fatal("missing usage must stay unknown")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Embed(ctx, []string{"a"}); err != context.Canceled {
		t.Fatal(err)
	}
	for _, n := range []int{0, 2049} {
		if _, err = p.WithDimensions(n); err == nil {
			t.Fatal("invalid frozen dimension")
		}
	}
	for _, url := range []string{"https://user:secret@host", "https://host?key=x", "file:///tmp"} {
		if _, err = NewEmbeddingClient("key", url, "vector", 1); err == nil {
			t.Fatal("unsafe endpoint")
		}
	}
}
