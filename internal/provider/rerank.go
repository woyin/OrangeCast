package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// RerankConfig is credential-free provenance for a bounded candidate ranking.
type RerankConfig struct{ ID, ConnectionID, Provider, Model string }

// RerankResult preserves scores in input order and supplier-reported usage.
type RerankResult struct {
	Model                       string
	Scores                      []float64
	InputTokens                 int
	UsageKnown, UnverifiedModel bool
}

// RerankClient performs only explicit paid calls, without redirects or retries.
type RerankClient struct {
	key, baseURL, model string
	client              *http.Client
}

// RerankResponseError retains paid usage even if the response cannot be applied.
type RerankResponseError struct {
	Receipt *RerankResult
	Cause   error
}

func (e *RerankResponseError) Error() string { return e.Cause.Error() }
func (e *RerankResponseError) Unwrap() error { return e.Cause }

// NewRerankClient constructs a separately configured endpoint without I/O.
func NewRerankClient(key, base, model string) (*RerankClient, error) {
	key, base, model = strings.TrimSpace(key), strings.TrimRight(strings.TrimSpace(base), "/"), strings.TrimSpace(model)
	u, e := url.Parse(base)
	if key == "" || model == "" || len(model) > 200 || e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(key+model, "\r\n") {
		return nil, errors.New("invalid independent rerank configuration")
	}
	return &RerankClient{key: key, baseURL: base, model: model, client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Config returns a stable route/model identity, never credentials.
func (p *RerankClient) Config() RerankConfig {
	connection := fmt.Sprintf("%x", sha256.Sum256([]byte("embedding\x00"+p.baseURL)))
	return RerankConfig{ID: fmt.Sprintf("%x", sha256.Sum256([]byte("rerank-v1\x00"+connection+"\x00"+p.model))), ConnectionID: connection, Provider: "embedding-" + connection[:16], Model: p.model}
}

// EstimateRerankInputs provides a complete UTF-8 byte bound, not measured usage.
func EstimateRerankInputs(query string, docs []string) (*KnowledgeEstimate, error) {
	if strings.TrimSpace(query) == "" || !utf8.ValidString(query) || len(query) > 2048 || len(docs) < 1 || len(docs) > 30 {
		return nil, errors.New("rerank requires a bounded query and 1..30 candidates")
	}
	units := 32 + len(query)
	for _, d := range docs {
		if strings.TrimSpace(d) == "" || !utf8.ValidString(d) || len(d) > 8192 {
			return nil, errors.New("rerank candidate exceeds complete input limit")
		}
		units += len(d)
	}
	if units > 98304 {
		return nil, errors.New("rerank total input exceeds limit")
	}
	raw, _ := json.Marshal([]any{query, docs})
	hash := sha256.Sum256(raw)
	return &KnowledgeEstimate{Method: "embedding-utf8-byte-bound-v1", InputFingerprint: fmt.Sprintf("%x", hash), InputTokens: units, Approximate: true}, nil
}

// Rerank ranks the exact authorized candidate inputs, recording no response text.
func (p *RerankClient) Rerank(ctx context.Context, query string, docs []string) (out *RerankResult, err error) {
	if _, err = EstimateRerankInputs(query, docs); err != nil {
		return nil, err
	}
	var receipt *RerankResult
	defer func() {
		if err != nil && receipt != nil {
			err = &RerankResponseError{Receipt: receipt, Cause: err}
		}
	}()
	raw, _ := json.Marshal(map[string]any{"model": p.model, "query": query, "documents": docs, "top_n": len(docs), "return_documents": false})
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/rerank", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("rerank request construction failed")
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("rerank transport failed; remote result unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rerank HTTP status %d; response body omitted", resp.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 1048577))
	if err != nil || len(raw) > 1048576 {
		return nil, errors.New("rerank response incomplete or exceeds limit")
	}
	var wire struct {
		Model   string
		Results []struct {
			Index *int
			Score *float64 `json:"relevance_score"`
		}
		Usage *embeddingWireUsage
	}
	if json.Unmarshal(raw, &wire) != nil {
		return nil, errors.New("invalid rerank response JSON")
	}
	units, known, usageErr := (&EmbeddingClient{baseURL: p.baseURL}).embeddingInputUsage(wire.Usage)
	if known {
		m := p.model
		if wire.Model != m {
			m = "unknown"
		}
		receipt = &RerankResult{Model: m, InputTokens: units, UsageKnown: true, UnverifiedModel: wire.Model != p.model}
	}
	if wire.Model != p.model || len(wire.Results) != len(docs) {
		return nil, errors.New("invalid rerank response identity or count")
	}
	result := &RerankResult{Model: wire.Model, Scores: make([]float64, len(docs)), InputTokens: units, UsageKnown: known}
	seen := make([]bool, len(docs))
	for _, r := range wire.Results {
		if r.Index == nil || *r.Index < 0 || *r.Index >= len(docs) || seen[*r.Index] || r.Score == nil || math.IsNaN(*r.Score) || math.IsInf(*r.Score, 0) {
			return nil, errors.New("invalid rerank result index or score")
		}
		seen[*r.Index] = true
		result.Scores[*r.Index] = *r.Score
	}
	if usageErr != nil {
		return nil, usageErr
	}
	return result, nil
}
