package config

import "testing"

func TestRetrievalProfileAndRerankConfig(t *testing.T) {
	good := Config{EmbeddingAPIKey: "key", EmbeddingBaseURL: "https://api.jina.ai/v1", EmbeddingModel: "jina-embeddings-v5-text-small", EmbeddingDimensions: 1024, EmbeddingProfile: "jina-retrieval-v1"}
	if err := good.ValidateEmbedding(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.EmbeddingProfile = "bad" }, func(c *Config) { c.EmbeddingModel = "other" }, func(c *Config) { c.EmbeddingBaseURL = "https://other.example/v1" }, func(c *Config) { c.EmbeddingBaseURL = "http://api.jina.ai/v1" }} {
		c := good
		change(&c)
		if c.ValidateEmbedding() == nil {
			t.Fatal("incompatible profile")
		}
	}
	if (&Config{}).ValidateRerank() != nil {
		t.Fatal("optional rerank")
	}
	for _, c := range []Config{{RerankAPIKey: "key"}, {RerankAPIKey: "key", RerankBaseURL: "https://api.jina.ai/v1?key=x", RerankModel: "rank"}, {RerankAPIKey: "ke\ny", RerankBaseURL: "https://api.jina.ai/v1", RerankModel: "rank"}} {
		if c.ValidateRerank() == nil {
			t.Fatal("bad rerank")
		}
	}
	c := Config{RerankAPIKey: "key", RerankBaseURL: "https://api.jina.ai/v1", RerankModel: "rank"}
	if err := c.ValidateRerank(); err != nil {
		t.Fatal(err)
	}
}
