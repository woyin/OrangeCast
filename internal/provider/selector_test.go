package provider

import (
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

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

// TestSelector_ApplySettingsFrom 验证从 Settings 对象覆盖 key/URL（含 nil 指针安全）。
func TestSelector_ApplySettingsFrom(t *testing.T) {
	sel := NewSelector("", "")
	gk := "from-settings-groq"
	gurl := "https://custom.groq.com/v1"
	okey := "from-settings-openai"
	st := &models.Settings{
		GroqAPIKey: &gk, GroqBaseURL: &gurl, OpenAIAPIKey: &okey,
	}
	sel.ApplySettingsFrom(st)
	if !sel.HasGroq() || !sel.HasOpenAI() {
		t.Fatal("应从 settings 覆盖 key")
	}
	bundle, err := sel.Bundle("groq")
	if err != nil {
		t.Fatal(err)
	}
	g := bundle.Analysis.(*GroqProvider)
	if g.baseURL != gurl {
		t.Errorf("Groq baseURL 应覆盖，实际 %q", g.baseURL)
	}
	// nil Settings 安全
	sel2 := NewSelector("", "")
	sel2.ApplySettingsFrom(nil)
	if sel2.HasGroq() {
		t.Error("nil settings 不应覆盖 key")
	}
}

// TestSelector_BundleForTask_OpenAIModelOverride 验证 openai 模型名注入。
func TestSelector_BundleForTask_OpenAIModelOverride(t *testing.T) {
	sel := NewSelector("", "openai-key")
	bundle, err := sel.BundleForTask(TaskConfig{Provider: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	o, ok := bundle.Analysis.(*OpenAIProvider)
	if !ok {
		t.Fatal("Analysis 应为 *OpenAIProvider")
	}
	if o.analysisModel != "gpt-4o" {
		t.Errorf("model 应为 gpt-4o，实际 %s", o.analysisModel)
	}
}

// TestSelector_BundleForTask_GroqMissingKey 验证 groq 无 key 时报错。
func TestSelector_BundleForTask_GroqMissingKey(t *testing.T) {
	sel := NewSelector("", "")
	if _, err := sel.BundleForTask(TaskConfig{Provider: "groq"}); err == nil {
		t.Fatal("groq 无 key 应报错")
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

// fakeNarrationP minimal NarrationProvider for selector test.
type fakeNarrationP struct{}

func (fakeNarrationP) Synthesize(text, voice, outPath string) (*NarrationResult, error) {
	return nil, nil
}
func (fakeNarrationP) Available() bool { return true }
func (fakeNarrationP) Name() string    { return "fake-narration" }

// TestSelector_WithNarration_AttachesToBundles (ADR-0019)
// WithNarration 注入了 Narration provider，且不随 groq/openai 开关丢失。
func TestSelector_WithNarration_AttachesToBundles(t *testing.T) {
	sel := NewSelector("g", "o").WithNarration(fakeNarrationP{})
	// Groq bundle
	gb, err := sel.Bundle("groq")
	if err != nil {
		t.Fatal(err)
	}
	if gb.Narration == nil || gb.Narration.Name() != "fake-narration" {
		t.Error("Groq bundle 应携带注入的 Narration provider")
	}
	// OpenAI bundle
	ob, err := sel.Bundle("openai")
	if err != nil {
		t.Fatal(err)
	}
	if ob.Narration == nil || ob.Narration.Name() != "fake-narration" {
		t.Error("OpenAI bundle 应携带注入的 Narration provider")
	}
	// 未注入时 Narration 为 nil
	sel2 := NewSelector("g", "o")
	b2, _ := sel2.Bundle("groq")
	if b2.Narration != nil {
		t.Error("未注入 WithNarration 时 Narration 应为 nil")
	}
}
