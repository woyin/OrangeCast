package config

import "testing"

func TestKnowledgeModelsResolveEnvironmentAndFallback(t *testing.T) {
	t.Setenv("SESSION_SECRET", "test")
	t.Setenv("POD_BASE_URL", "https://example.test/v1")
	t.Setenv("POD_API_KEY", "test-key")
	t.Setenv("POD_MODEL", "base")
	for _, name := range []string{"POD_REVIEW_MODEL", "POD_DISCOVERY_MODEL", "POD_SELECTION_MODEL", "POD_WRITE_MODEL", "POD_LEARNING_REVIEW_MODEL"} {
		t.Setenv(name, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for stage, model := range c.KnowledgeStageModels() {
		if model != "base" {
			t.Fatalf("%s fallback %s", stage, model)
		}
	}
	t.Setenv("POD_DISCOVERY_MODEL", " discover ")
	t.Setenv("POD_SELECTION_MODEL", "select")
	t.Setenv("POD_WRITE_MODEL", "writer")
	t.Setenv("POD_LEARNING_REVIEW_MODEL", "learning")
	t.Setenv("POD_REVIEW_MODEL", "review")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"discover": "discover", "select": "select", "write": "writer", "revise": "writer", "review": "review", "review_final": "review", "weekly_review": "learning"}
	for stage, model := range c.KnowledgeStageModels() {
		if model != want[stage] {
			t.Fatalf("%s resolved %s", stage, model)
		}
	}
}
