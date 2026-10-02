package provider

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type jinaFixtureTransport func(*http.Request) (*http.Response, error)

func (f jinaFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbeddingJinaUsageCompatibility(t *testing.T) {
	const model = "jina-embeddings-v5-text-small"
	for _, tc := range []struct {
		name, base, usage, vector, returnedModel string
		known, failure, receipt                  bool
		tokens                                   int
	}{
		{"Jina total only", "https://api.jina.ai/v1", `{"total_tokens":35}`, `[1,0]`, model, true, false, false, 35},
		{"Jina zero known", "https://api.jina.ai/v1", `{"total_tokens":0}`, `[1,0]`, model, true, false, false, 0},
		{"Jina full usage", "https://api.jina.ai/v1", `{"prompt_tokens":7,"total_tokens":7}`, `[1,0]`, model, true, false, false, 7},
		{"Jina absent usage", "https://api.jina.ai/v1", `null`, `[1,0]`, model, false, false, false, 0},
		{"generic total stays unknown", "https://other.example/v1", `{"total_tokens":35}`, `[1,0]`, model, false, false, false, 0},
		{"lookalike host stays unknown", "https://api.jina.ai.other.example/v1", `{"total_tokens":35}`, `[1,0]`, model, false, false, false, 0},
		{"negative total rejected", "https://api.jina.ai/v1", `{"total_tokens":-1}`, `[1,0]`, model, false, true, false, 0},
		{"inconsistent fields rejected", "https://api.jina.ai/v1", `{"prompt_tokens":3,"total_tokens":4}`, `[1,0]`, model, false, true, false, 0},
		{"invalid vector retains usage", "https://api.jina.ai/v1", `{"total_tokens":35}`, `[1]`, model, true, true, true, 35},
		{"wrong model retains unpriced usage", "https://api.jina.ai/v1", `{"total_tokens":35}`, `[1,0]`, "wrong-model", true, true, true, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewEmbeddingClient("private-key", tc.base, model, 2)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client.client.Transport = jinaFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.String() != tc.base+"/embeddings" {
					t.Fatal("unexpected endpoint")
				}
				body := `{"model":"` + tc.returnedModel + `","data":[{"index":0,"embedding":` + tc.vector + `}],"usage":` + tc.usage + `}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			result, err := client.Embed(t.Context(), []string{"测试文本"})
			if calls != 1 || (err != nil) != tc.failure {
				t.Fatal(calls, result, err)
			}
			if tc.failure {
				if strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), tc.base) {
					t.Fatal("secret disclosure")
				}
				var paid *EmbeddingResponseError
				if errors.As(err, &paid) != tc.receipt {
					t.Fatal("incorrect usage receipt", err)
				}
				if tc.receipt {
					if paid.Receipt.InputTokens != tc.tokens || !paid.Receipt.UsageKnown || len(paid.Receipt.Vectors) != 0 {
						t.Fatal(paid.Receipt)
					}
					if tc.returnedModel != model && (!paid.Receipt.UnverifiedModel || paid.Receipt.Model != "unknown") {
						t.Fatal("wrong model was priced")
					}
				}
				return
			}
			if result.UsageKnown != tc.known || result.InputTokens != tc.tokens || result.Dimensions != 2 {
				t.Fatal(result)
			}
		})
	}
}
