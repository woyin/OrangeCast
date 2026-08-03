package provider

import (
	"strings"
	"testing"
	"time"
)

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

func TestGroqAnalyze_SplitsWindowsAndMergesCitations(t *testing.T) {
	segments := []Segment{
		{ID: "seg-0001", Text: strings.Repeat("a", 13000)},
		{ID: "seg-0002", Text: strings.Repeat("b", 13000)},
	}
	calls := 0
	g := NewGroqProvider("test")
	waits := 0
	g.sleepFn = func(time.Duration) { waits++ }
	g.chatCompleteFn = func(_ []map[string]string, _ string) (string, int, error) {
		calls++
		if calls == 1 {
			return `{"title":"First","summary":{"text":"summary one","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":["python"],"suggestedQuestions":[]}`, 200, nil
		}
		return `{"title":"Second","summary":{"text":"summary two","citations":["seg-0002"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":["python","ai"],"suggestedQuestions":[]}`, 200, nil
	}
	card, err := g.Analyze("ignored", segments)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 analysis windows, got %d", calls)
	}
	if waits != 1 {
		t.Fatalf("analysis windows should be rate paced, got %d waits", waits)
	}
	if card.Title != "First" || card.Summary.Text != "summary one\n\nsummary two" {
		t.Errorf("merged card mismatch: %+v", card)
	}
	if len(card.Summary.Citations) != 2 || card.Summary.Citations[0] != "seg-0001" || card.Summary.Citations[1] != "seg-0002" {
		t.Errorf("summary citations must preserve both windows: %+v", card.Summary.Citations)
	}
	if len(card.Tags) != 2 || card.Tags[0] != "python" || card.Tags[1] != "ai" {
		t.Errorf("tags should be stable and deduplicated: %+v", card.Tags)
	}
}

func TestSplitAnalysisWindows_DoesNotSplitSegment(t *testing.T) {
	segments := []Segment{{ID: "a", Text: "12345"}, {ID: "b", Text: "67890"}}
	windows := splitAnalysisWindows(segments, 10)
	if len(windows) != 2 || len(windows[0]) != 1 || windows[0][0].ID != "a" || windows[1][0].ID != "b" {
		t.Fatalf("segments must remain whole across windows: %+v", windows)
	}
}
