package provider

import "testing"

func TestSelector_DefaultGroq(t *testing.T) {
	sel := NewSelector("groq-key", "openai-key")
	b, err := sel.Bundle("")
	if err != nil {
		t.Fatal(err)
	}
	if b.Transcription.Name() != "groq" {
		t.Errorf("空 provider 应默认 groq，实际 %s", b.Transcription.Name())
	}
}

func TestSelector_OpenAI(t *testing.T) {
	sel := NewSelector("g", "o-key")
	b, err := sel.Bundle("openai")
	if err != nil {
		t.Fatal(err)
	}
	if b.Analysis.Name() != "openai" {
		t.Errorf("应返回 openai bundle，实际 %s", b.Analysis.Name())
	}
}

func TestSelector_GroqMissingKey(t *testing.T) {
	sel := NewSelector("", "o-key")
	if _, err := sel.Bundle("groq"); err == nil {
		t.Error("groq 缺 key 应报错（生产不静默回退）")
	}
}

func TestSelector_OpenAIMissingKey(t *testing.T) {
	sel := NewSelector("g-key", "")
	if _, err := sel.Bundle("openai"); err == nil {
		t.Error("openai 缺 key 应报错")
	}
}

func TestSelector_UnknownProvider(t *testing.T) {
	sel := NewSelector("g", "o")
	if _, err := sel.Bundle("claude"); err == nil {
		t.Error("未知 provider 应报错")
	}
}

func TestHasKeys(t *testing.T) {
	sel := NewSelector("g", "")
	if !sel.HasGroq() || sel.HasOpenAI() {
		t.Error("HasGroq/HasOpenAI 判断错误")
	}
}
