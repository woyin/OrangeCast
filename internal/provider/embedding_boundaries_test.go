package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddingDurableResultValidation(t *testing.T) {
	valid := func() *EmbeddingResult {
		return &EmbeddingResult{Model: "vector", Dimensions: 2, Vectors: [][]float32{{1, 0}}, InputTokens: 4, UsageKnown: true}
	}
	cases := []struct {
		name   string
		change func(*EmbeddingResult)
	}{
		{"model", func(r *EmbeddingResult) { r.Model = "other" }},
		{"unverified", func(r *EmbeddingResult) { r.UnverifiedModel = true }},
		{"negative usage", func(r *EmbeddingResult) { r.InputTokens = -1 }},
		{"shape", func(r *EmbeddingResult) { r.Vectors[0] = []float32{1} }},
		{"nan", func(r *EmbeddingResult) { r.Vectors[0][0] = float32(math.NaN()) }},
		{"infinite", func(r *EmbeddingResult) { r.Vectors[0][0] = float32(math.Inf(1)) }},
		{"zero", func(r *EmbeddingResult) { r.Vectors[0][0] = 0 }},
	}
	if err := ValidateEmbeddingResult(valid(), "vector", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEmbeddingResult(nil, "vector", 1, 2); err == nil {
		t.Fatal("nil checkpoint accepted")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid()
			tc.change(r)
			if err := ValidateEmbeddingResult(r, "vector", 1, 2); err == nil {
				t.Fatal("unsafe durable result accepted")
			}
		})
	}
}

func TestEmbeddingTransportAndCapacityBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, body, expected string }{
		{"oversized", strings.Repeat(" ", 4*1024*1024+1), "exceeds limit"},
		{"overflow norm", `{"model":"vector","data":[{"index":0,"embedding":[1.7e308,1.7e308]}]}`, "invalid embedding norm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			p, _ := NewEmbeddingClient("key", srv.URL, "vector", 0)
			r, err := p.Embed(t.Context(), []string{"a"})
			if r != nil || err == nil || !strings.Contains(err.Error(), tc.expected) || calls != 1 {
				t.Fatalf("result=%v err=%v calls=%d", r, err, calls)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { conn, _, _ := w.(http.Hijacker).Hijack(); conn.Close() }))
	defer srv.Close()
	p, _ := NewEmbeddingClient("key", srv.URL, "vector", 0)
	if _, err := p.Embed(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "remote result unknown") {
		t.Fatal(err)
	}
	cfg := p.Config()
	frozen, err := cfg.WithDimensions(2)
	if err != nil || frozen.ID == cfg.ID || frozen.ConnectionID != cfg.ConnectionID || frozen.Dimensions != 2 {
		t.Fatal(frozen, err)
	}
	for _, n := range []int{0, EmbeddingMaxDimensions + 1} {
		if _, err := cfg.WithDimensions(n); err == nil {
			t.Fatal("invalid dimension accepted")
		}
	}
	if _, err := EstimateEmbeddingInputs([]string{" "}); err == nil {
		t.Fatal("invalid estimate accepted")
	}
	wrapped := &EmbeddingResponseError{Cause: errors.New("supplier failure")}
	if !errors.Is(wrapped, wrapped.Cause) {
		t.Fatal("response error loses cause")
	}
}
