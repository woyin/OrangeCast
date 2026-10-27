package provider

import (
	"errors"
	"strings"
	"testing"
)

// errUpstream 用于模拟远端失败。
var errUpstream = errors.New("upstream failed")

// TestValidateTranslationSet_DropsInvalid 只保留真实片段与非空译文，并去重。
func TestValidateTranslationSet_DropsInvalid(t *testing.T) {
	segments := []Segment{{ID: "seg-0001", Text: "a"}, {ID: "seg-0002", Text: "b"}}
	set := &TranslationSet{Segments: []TranslatedSegment{
		{ID: "seg-0001", Text: "甲"},
		{ID: "seg-0001", Text: "重复"},
		{ID: "ghost", Text: "编造"},
		{ID: "seg-0002", Text: "   "},
	}}
	out := ValidateTranslationSet(set, segments)
	if len(out) != 1 || out[0].ID != "seg-0001" || out[0].Text != "甲" {
		t.Fatalf("应只保留一条有效译文: %+v", out)
	}
	if ValidateTranslationSet(nil, segments) != nil {
		t.Error("nil 结果应为空")
	}
}

// TestGroq_Translate 验证分段翻译与结果合并。
func TestGroq_Translate(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		if jsonMode != "object" {
			t.Errorf("翻译需要 JSON 对象模式，实际 %q", jsonMode)
		}
		if !strings.Contains(messages[1]["content"], "中文") {
			t.Error("提示应包含目标语言")
		}
		return `{"segments":[{"id":"seg-0001","text":"通胀是物价上升"}]}`, 200, nil
	}
	res, err := g.Translate([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "inflation"}}, "中文")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(res.Segments) != 1 || res.Segments[0].Text != "通胀是物价上升" {
		t.Fatalf("译文错误: %+v", res.Segments)
	}
}

// TestGroq_Translate_RejectsEmptyInput 无片段或无目标语言时直接报错。
func TestGroq_Translate_RejectsEmptyInput(t *testing.T) {
	g := NewGroqProvider("key")
	if _, err := g.Translate(nil, "中文"); err == nil {
		t.Fatal("无片段应报错")
	}
	if _, err := g.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "  "); err == nil {
		t.Fatal("无目标语言应报错")
	}
}

// TestGroq_Translate_AllInvalid 全部译文不可核验时显式报错，不写空集合。
func TestGroq_Translate_AllInvalid(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return `{"segments":[{"id":"ghost","text":"编造"}]}`, 200, nil
	}
	if _, err := g.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("全部无效应报错")
	}
}

// TestOpenAI_Translate 验证 OpenAI 路径解析对齐译文。
func TestOpenAI_Translate(t *testing.T) {
	srv := newOpenAITestServer(t, `{"segments":[{"id":"seg-0001","text":"第一段译文"}]}`)
	defer srv.Close()
	o := NewOpenAIProvider("key").WithBaseURL(srv.URL)
	res, err := o.Translate([]Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "first"}}, "中文")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(res.Segments) != 1 {
		t.Fatalf("应有 1 条译文: %+v", res.Segments)
	}
}

// TestSelector_BundleWiresTranslation 两个 Provider 都要装配翻译能力。
func TestSelector_BundleWiresTranslation(t *testing.T) {
	sel := NewSelector("groq-key", "openai-key")
	for _, name := range []string{"groq", "openai"} {
		bundle, err := sel.Bundle(name)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Translation == nil {
			t.Errorf("%s bundle 缺少 Translation", name)
		}
	}
}

// TestGroq_Translate_BadJSON 非法 JSON 显式报错，不返回空译文冒充成功。
func TestGroq_Translate_BadJSON(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return "not-json", 200, nil
	}
	if _, err := g.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestGroq_Translate_UpstreamError 远端错误向上返回，不静默返回部分结果。
func TestGroq_Translate_UpstreamError(t *testing.T) {
	g := NewGroqProvider("key")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		return "", 500, errUpstream
	}
	if _, err := g.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, "中文"); err == nil {
		t.Fatal("远端错误应报错")
	}
}

// TestOpenAI_Translate_RejectsEmptyInput OpenAI 路径同样拒绝空输入。
func TestOpenAI_Translate_RejectsEmptyInput(t *testing.T) {
	o := NewOpenAIProvider("key")
	if _, err := o.Translate(nil, "中文"); err == nil {
		t.Fatal("无片段应报错")
	}
	if _, err := o.Translate([]Segment{{ID: "seg-0001", Text: "a"}}, ""); err == nil {
		t.Fatal("无目标语言应报错")
	}
}
