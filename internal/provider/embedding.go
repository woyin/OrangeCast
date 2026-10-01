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

const EmbeddingMaxDimensions = 2048
const EmbeddingMaxBatch = 16
const EmbeddingMaxInputBytes = 8192

var ErrEmbeddingUnavailable = errors.New("independent embedding connection is not configured")

// EmbeddingConfig contains only safe provenance, never an endpoint or credential.
// Dimensions=0 is usable for explicit preflight only. Jobs freeze its measured value.
type EmbeddingConfig struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connection_id"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Dimensions   int    `json:"dimensions"`
	Unit         string `json:"unit"`
}

type EmbeddingResult struct {
	Vectors     [][]float32 `json:"vectors"`
	Model       string      `json:"model"`
	Dimensions  int         `json:"dimensions"`
	InputTokens int         `json:"input_tokens"`
	UsageKnown  bool        `json:"usage_known"`
}

// EmbeddingClient deliberately has no automatic retry: an unknown remote result
// must be resolved by the durable task, rather than silently billed a second time.
type EmbeddingClient struct {
	key, baseURL, model string
	dimensions          int
	requestDimensions   int
	client              *http.Client
}

func NewEmbeddingClient(key, baseURL, model string, dimensions int) (*EmbeddingClient, error) {
	key, baseURL, model = strings.TrimSpace(key), strings.TrimRight(strings.TrimSpace(baseURL), "/"), strings.TrimSpace(model)
	if key == "" || baseURL == "" || model == "" {
		return nil, ErrEmbeddingUnavailable
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(key+model, "\r\n") || len(model) > 200 || dimensions < 0 || dimensions > EmbeddingMaxDimensions {
		return nil, errors.New("invalid independent embedding configuration")
	}
	return &EmbeddingClient{key: key, baseURL: baseURL, model: model, dimensions: dimensions, requestDimensions: dimensions, client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (p *EmbeddingClient) Config() EmbeddingConfig {
	connection := fmt.Sprintf("%x", sha256.Sum256([]byte("embedding\x00"+p.baseURL)))
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", connection, p.model, p.dimensions))))
	return EmbeddingConfig{ID: id, ConnectionID: connection, Provider: "embedding-" + connection[:16], Model: p.model, Dimensions: p.dimensions, Unit: "input_tokens"}
}

// WithDimensions constructs a client for a measured, fixed dimension. When the
// original route omitted dimensions, do not add an unsupported wire parameter.
// WithDimensions freezes a measured result in safe provenance without any I/O.
func (c EmbeddingConfig) WithDimensions(n int) (EmbeddingConfig, error) {
	if n < 1 || n > EmbeddingMaxDimensions {
		return c, errors.New("embedding dimension outside capacity")
	}
	c.Dimensions = n
	c.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", c.ConnectionID, c.Model, n))))
	return c, nil
}

func (p *EmbeddingClient) WithDimensions(n int) (*EmbeddingClient, error) {
	if n < 1 || n > EmbeddingMaxDimensions {
		return nil, errors.New("embedding dimension outside capacity")
	}
	copy := *p
	copy.dimensions = n
	return &copy, nil
}

func embeddingInputs(inputs []string) error {
	if len(inputs) < 1 || len(inputs) > EmbeddingMaxBatch {
		return errors.New("embedding batch must contain 1..16 inputs")
	}
	for _, v := range inputs {
		if strings.TrimSpace(v) == "" || !utf8.ValidString(v) || len(v) > EmbeddingMaxInputBytes {
			return errors.New("embedding input must be nonempty UTF-8 and at most 8KiB; split complete windows explicitly")
		}
	}
	return nil
}

// Embed only runs when explicitly called by preflight or an admitted persistent job.
// EmbeddingResponseError preserves supplier-reported numeric usage when the
// returned vectors are unusable. It never retains response bodies or vectors.
type EmbeddingResponseError struct {
	Cause   error
	Receipt *EmbeddingResult
}

func (e *EmbeddingResponseError) Error() string { return e.Cause.Error() }
func (e *EmbeddingResponseError) Unwrap() error { return e.Cause }

func (p *EmbeddingClient) Embed(ctx context.Context, inputs []string) (out *EmbeddingResult, err error) {
	var receipt *EmbeddingResult
	defer func() {
		if err != nil && receipt != nil {
			err = &EmbeddingResponseError{Cause: err, Receipt: receipt}
		}
	}()
	if err := embeddingInputs(inputs); err != nil {
		return nil, err
	}
	body := struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Encoding   string   `json:"encoding_format"`
		Dimensions int      `json:"dimensions,omitempty"`
	}{p.model, inputs, "float", p.requestDimensions}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("embedding request construction failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.key)
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("embedding transport failed; remote result unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding HTTP status %d; response body omitted", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return nil, errors.New("embedding response incomplete or exceeds limit")
	}
	var wire struct {
		Data []struct {
			Index     *int      `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage *struct {
			Prompt *int `json:"prompt_tokens"`
			Total  *int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return nil, errors.New("invalid embedding response JSON")
	}
	if wire.Usage != nil && wire.Usage.Prompt != nil && wire.Usage.Total != nil && *wire.Usage.Prompt >= 0 && *wire.Usage.Total == *wire.Usage.Prompt {
		model := p.model
		if wire.Model != p.model {
			model = "unknown"
		}
		receipt = &EmbeddingResult{Model: model, InputTokens: *wire.Usage.Prompt, UsageKnown: true}
	}
	if len(wire.Data) != len(inputs) || wire.Model != p.model {
		return nil, errors.New("invalid embedding response identity or batch")
	}
	result := &EmbeddingResult{Vectors: make([][]float32, len(inputs)), Model: wire.Model}
	for _, item := range wire.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(inputs) || result.Vectors[*item.Index] != nil {
			return nil, errors.New("invalid embedding response index")
		}
		n := len(item.Embedding)
		if n < 1 || n > EmbeddingMaxDimensions || (p.dimensions != 0 && n != p.dimensions) || (result.Dimensions != 0 && result.Dimensions != n) {
			return nil, errors.New("embedding dimension mismatch or exceeds capacity")
		}
		result.Dimensions = n
		var norm float64
		for _, v := range item.Embedding {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, errors.New("nonfinite embedding")
			}
			norm = math.Hypot(norm, v)
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, errors.New("invalid embedding norm")
		}
		vec := make([]float32, n)
		for j, v := range item.Embedding {
			vec[j] = float32(v / norm)
		}
		result.Vectors[*item.Index] = vec
	}
	if wire.Usage != nil && wire.Usage.Prompt != nil && wire.Usage.Total != nil {
		if *wire.Usage.Prompt < 0 || *wire.Usage.Total != *wire.Usage.Prompt {
			return nil, errors.New("invalid embedding token usage")
		}
		result.InputTokens = *wire.Usage.Prompt
		result.UsageKnown = true
	}
	return result, nil
}

// EstimateEmbeddingInputs is a labeled admission heuristic over all untruncated
// inputs. It is not provider-reported usage or a tokenizer. No output price applies.
func EstimateEmbeddingInputs(inputs []string) (*KnowledgeEstimate, error) {
	if err := embeddingInputs(inputs); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(inputs)
	tokens := 32
	for _, v := range inputs {
		tokens += len(v) + 8
	} // conservative byte bound, plus per-input framing
	return &KnowledgeEstimate{Method: "embedding-utf8-byte-bound-v1", InputFingerprint: fmt.Sprintf("%x", sha256.Sum256(payload)), InputTokens: tokens, Approximate: true}, nil
}

// ValidateEmbeddingResult is also used when restoring a durable response. A
// checkpoint is not an exemption from vector shape, identity, or unit checks.
func ValidateEmbeddingResult(r *EmbeddingResult, model string, count, dimensions int) error {
	if r == nil || r.Model != model || len(r.Vectors) != count || r.Dimensions < 1 || r.Dimensions > EmbeddingMaxDimensions || (dimensions != 0 && r.Dimensions != dimensions) || r.InputTokens < 0 {
		return errors.New("invalid embedding result identity")
	}
	for _, vec := range r.Vectors {
		if len(vec) != r.Dimensions {
			return errors.New("invalid embedding vector shape")
		}
		var norm float64
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return errors.New("nonfinite embedding vector")
			}
			norm += float64(v) * float64(v)
		}
		if math.Abs(norm-1) > .0001 {
			return errors.New("invalid embedding vector norm")
		}
	}
	return nil
}
