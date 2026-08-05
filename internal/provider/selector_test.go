package provider

import "testing"

func TestSelector_Bundle_GroqDefault(t *testing.T) {
	sel := NewSelector("groq-key", "openai-key")
	bundle, err := sel.Bundle("groq")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Transcription.Name() != "groq" {
		t.Errorf("Groq bundle 应返回 groq provider，实际 %s", bundle.Transcription.Name())
	}
}

func TestSelector_Bundle_MissingGroqKey(t *testing.T) {
	sel := NewSelector("", "")
	_, err := sel.Bundle("groq")
	if err == nil {
		t.Error("缺少 Groq key 应报错")
	}
}

func TestSelector_Bundle_MissingOpenAIKey(t *testing.T) {
	sel := NewSelector("g", "")
	_, err := sel.Bundle("openai")
	if err == nil {
		t.Error("缺少 OpenAI key 应报错")
	}
}

func TestSelector_Bundle_UnknownProvider(t *testing.T) {
	sel := NewSelector("g", "o")
	_, err := sel.Bundle("claude")
	if err == nil {
		t.Error("未知 provider 应报错")
	}
}

func TestSelector_BundleForTask_ModelOverride(t *testing.T) {
	sel := NewSelector("groq-key", "")
	bundle, err := sel.BundleForTask(TaskConfig{Provider: "groq", Model: "custom-model"})
	if err != nil {
		t.Fatal(err)
	}
	g, ok := bundle.Analysis.(*GroqProvider)
	if !ok {
		t.Fatal("Analysis 应为 *GroqProvider")
	}
	if g.model != "custom-model" {
		t.Errorf("model 应为 custom-model，实际 %s", g.model)
	}
}

func TestSelector_ApplySettings(t *testing.T) {
	sel := NewSelector("", "")
	if sel.HasGroq() {
		t.Error("初始应无 Groq key")
	}
	sel.ApplySettings("new-groq-key", "https://custom.groq.com/v1", "", "")
	if !sel.HasGroq() {
		t.Error("ApplySettings 后应有 Groq key")
	}
	bundle, err := sel.Bundle("groq")
	if err != nil {
		t.Fatal(err)
	}
	g := bundle.Analysis.(*GroqProvider)
	if g.baseURL != "https://custom.groq.com/v1" {
		t.Errorf("baseURL 应被覆盖，实际 %s", g.baseURL)
	}
}

func TestSelector_HasGroq_HasOpenAI(t *testing.T) {
	sel := NewSelector("g", "")
	if !sel.HasGroq() || sel.HasOpenAI() {
		t.Error("HasGroq/HasOpenAI 返回不正确")
	}
	sel2 := NewSelector("", "o")
	if sel2.HasGroq() || !sel2.HasOpenAI() {
		t.Error("HasGroq/HasOpenAI 返回不正确")
	}
}
