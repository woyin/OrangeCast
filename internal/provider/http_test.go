package provider

import "testing"

func TestParseJSONLoose_PlainJSON(t *testing.T) {
	raw := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[]}`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Title != "T" {
		t.Errorf("title=%q want T", c.Title)
	}
	if c.Summary.Text != "S" || len(c.Summary.Citations) != 1 {
		t.Errorf("summary 解析错误: %+v", c.Summary)
	}
}

func TestParseJSONLoose_StripsCodeBlock(t *testing.T) {
	// Groq 常见的 markdown 代码块包裹（第 10 题：容错剥离脏标记）
	raw := "```json\n{\"title\":\"X\",\"summary\":{\"text\":\"\",\"citations\":[]},\"keyPoints\":[],\"chapters\":[],\"quotes\":[],\"tags\":[],\"suggestedQuestions\":[]}\n```"
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatalf("应剥离代码块标记: %v", err)
	}
	if c.Title != "X" {
		t.Errorf("title=%q want X", c.Title)
	}
}

func TestParseJSONLoose_StripsSurroundingNoise(t *testing.T) {
	// LLM 偶尔在 JSON 前后加解释文本
	raw := `这是知识卡片：{"title":"Y","summary":{"text":"","citations":[]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[]} 希望对你有帮助`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Fatalf("应剥离前后噪声: %v", err)
	}
	if c.Title != "Y" {
		t.Errorf("title=%q want Y", c.Title)
	}
}

func TestParseJSONLoose_IgnoresUnknownField(t *testing.T) {
	// json_object 模式不强制 schema，LLM 可能输出额外字段，应忽略而非拒绝（容错）
	raw := `{"title":"Z","summary":{"text":"","citations":[]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[],"suggestedQuestions":[],"bogusField":1}`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err != nil {
		t.Errorf("应忽略未知字段，却报错: %v", err)
	}
	if c.Title != "Z" {
		t.Errorf("title=%q want Z", c.Title)
	}
}

func TestParseJSONLoose_RejectsMalformed(t *testing.T) {
	raw := `not json at all`
	var c KnowledgeCard
	if err := parseJSONLoose(raw, &c); err == nil {
		t.Error("非 JSON 应解析失败")
	}
}
