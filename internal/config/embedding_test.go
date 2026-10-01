package config

import (
	"strings"
	"testing"
)

func TestEmbeddingConfig(t *testing.T) {
	t.Setenv("SESSION_SECRET", "test-secret")
	for _, name := range []string{"LEARNING_EMBEDDING_BASE_URL", "LEARNING_EMBEDDING_API_KEY", "LEARNING_EMBEDDING_MODEL", "LEARNING_EMBEDDING_DIMENSIONS", "POD_BASE_URL", "POD_API_KEY", "POD_MODEL"} {
		t.Setenv(name, "")
	}
	t.Setenv("POD_BASE_URL", "https://text.example/v1")
	t.Setenv("POD_API_KEY", "pod-secret")
	t.Setenv("POD_MODEL", "text-only")
	c, err := Load()
	if err != nil || c.EmbeddingBaseURL != "" || c.EmbeddingDimensions != 0 {
		t.Fatalf("default must be independent: %v", err)
	}
	t.Setenv("LEARNING_EMBEDDING_API_KEY", "secret-sentinel")
	if _, err = Load(); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
		t.Fatal("partial configuration or secret disclosure")
	}
	t.Setenv("LEARNING_EMBEDDING_MODEL", "vector")
	t.Setenv("LEARNING_EMBEDDING_BASE_URL", "https://vector.example/v1")
	c, err = Load()
	if err != nil || c.EmbeddingModel != "vector" {
		t.Fatal(err)
	}
	for _, dim := range []string{"NaN", "0", "-1", "2049", "1.5"} {
		t.Setenv("LEARNING_EMBEDDING_DIMENSIONS", dim)
		if _, err = Load(); err == nil {
			t.Fatalf("accepted %s", dim)
		}
	}
	t.Setenv("LEARNING_EMBEDDING_DIMENSIONS", "2048")
	c, err = Load()
	if err != nil || c.EmbeddingDimensions != 2048 {
		t.Fatal(err)
	}
	for _, bad := range []string{"file:///etc/passwd", "https://u:secret-sentinel@host/v1", "https://host/v1?key=secret-sentinel", "https://host/v1#secret-sentinel"} {
		t.Setenv("LEARNING_EMBEDDING_BASE_URL", bad)
		if _, err = Load(); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("unsafe URL accepted or echoed")
		}
	}
	t.Setenv("LEARNING_EMBEDDING_BASE_URL", "https://vector.example/v1")
	t.Setenv("LEARNING_EMBEDDING_API_KEY", "bad\nsecret")
	if _, err = Load(); err == nil {
		t.Fatal("header injection")
	}
}
