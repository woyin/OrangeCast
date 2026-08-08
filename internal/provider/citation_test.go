package provider

import (
	"testing"
)

func testSegments() []Segment {
	return []Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "主权财富基金正在改变全球投资格局"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "新加坡政府投资公司是典型的长期投资者"},
		{ID: "seg-0003", Start: 10, End: 15, Text: "The future of AI depends on data governance."},
	}
}

func TestValidateCard_ValidCitations(t *testing.T) {
	card := &KnowledgeCard{
		Title:     "主权财富基金",
		Summary:   CitedText{Text: "概述", Citations: []string{"seg-0001", "seg-0002"}},
		KeyPoints: []KeyPoint{{Content: "要点", Description: "d", Citations: []string{"seg-0001"}}},
		Chapters:  []Chapter{{Title: "章", Gist: "g", Citations: []string{"seg-0002"}}},
		Quotes:    []Quote{{Text: "主权财富基金正在改变全球投资格局", Citations: []string{"seg-0001"}}},
	}
	got, err := ValidateCard(card, testSegments())
	if err != nil {
		t.Fatalf("有效卡片不应报错: %v", err)
	}
	if got.Title != "主权财富基金" || len(got.Quotes) != 1 {
		t.Errorf("清洗结果不符: %+v", got)
	}
}

func TestValidateCard_RejectsUnknownCitation(t *testing.T) {
	card := &KnowledgeCard{
		Title:   "T",
		Summary: CitedText{Text: "S", Citations: []string{"seg-9999"}}, // 不存在
	}
	_, err := ValidateCard(card, testSegments())
	if err == nil {
		t.Fatal("引用不存在的 Segment 应被拒绝")
	}
	if _, ok := err.(*ErrNoValidCitations); !ok {
		t.Errorf("应返回 ErrNoValidCitations，实际 %T", err)
	}
}

func TestValidateCard_DropsNonVerbatimQuote(t *testing.T) {
	card := &KnowledgeCard{
		Title:     "T",
		Summary:   CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []KeyPoint{{Content: "KP", Description: "d", Citations: []string{"seg-0001"}}},
		Chapters:  []Chapter{{Title: "CH", Gist: "g", Citations: []string{"seg-0001"}}},
		Quotes: []Quote{
			{Text: "改变全球投资格局", Citations: []string{"seg-0001"}},             // 子串，逐字
			{Text: "投资格局正在改变全球", Citations: []string{"seg-0001"}},           // 乱序，应省略
			{Text: "主权财富基金正在改变全球投资格局而且很长", Citations: []string{"seg-0001"}}, // 超范围，应省略
		},
	}
	got, err := ValidateCard(card, testSegments())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotes) != 1 || got.Quotes[0].Text != "改变全球投资格局" {
		t.Errorf("应只保留逐字子串金句，实际 %+v", got.Quotes)
	}
}

func TestValidateCard_EnglishVerbatim(t *testing.T) {
	card := &KnowledgeCard{
		Title:     "AI",
		Summary:   CitedText{Text: "S", Citations: []string{"seg-0003"}},
		KeyPoints: []KeyPoint{{Content: "KP", Description: "d", Citations: []string{"seg-0003"}}},
		Chapters:  []Chapter{{Title: "CH", Gist: "g", Citations: []string{"seg-0003"}}},
		Quotes:    []Quote{{Text: "The future of AI depends on data governance.", Citations: []string{"seg-0003"}}},
	}
	got, err := ValidateCard(card, testSegments())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Quotes) != 1 {
		t.Error("英文逐字金句应保留")
	}
}

func TestResolveCitationRange(t *testing.T) {
	start, end, ok := ResolveCitationRange("seg-0002", testSegments())
	if !ok || start != 5 || end != 10 {
		t.Errorf("解析时间范围错误: %v %v %v", start, end, ok)
	}
	if _, _, ok := ResolveCitationRange("nope", testSegments()); ok {
		t.Error("未知 citation 不应解析成功")
	}
}

// TestResolveCitationSpan 验证多 Citation 聚合时间范围：min(start)–max(end)。
func TestResolveCitationSpan(t *testing.T) {
	segs := testSegments()
	// 跨 seg-0001(0-5) 与 seg-0003(10-15) → 0-15
	start, end, ok := ResolveCitationSpan([]string{"seg-0001", "seg-0003"}, segs)
	if !ok || start != 0 || end != 15 {
		t.Errorf("跨段聚合错误: %v %v %v", start, end, ok)
	}
	// 空引用 → ok=false
	if _, _, ok := ResolveCitationSpan(nil, segs); ok {
		t.Error("空引用应 ok=false")
	}
	// 含未知引用 → 忽略未知，已知仍解析
	start, end, ok = ResolveCitationSpan([]string{"seg-9999", "seg-0002"}, segs)
	if !ok || start != 5 || end != 10 {
		t.Errorf("应忽略未知引用并解析已知: %v %v %v", start, end, ok)
	}
	// 全部未知 → ok=false
	if _, _, ok := ResolveCitationSpan([]string{"seg-x", "seg-y"}, segs); ok {
		t.Error("全部未知引用应 ok=false")
	}
}

// TestResolveReferenceRange 验证 Reference 聚合时间范围（程序计算，不声称逐字）。
func TestResolveReferenceRange(t *testing.T) {
	segs := testSegments()
	start, end := ResolveReferenceRange([]string{"seg-0001", "seg-0003"}, segs)
	if start != 0 || end != 15 {
		t.Errorf("Reference 聚合错误: %v %v", start, end)
	}
	// 空引用 → 0,0
	start, end = ResolveReferenceRange(nil, segs)
	if start != 0 || end != 0 {
		t.Errorf("空引用应 0,0，实际 %v %v", start, end)
	}
	// 未知引用 → 0,0
	start, end = ResolveReferenceRange([]string{"seg-x"}, segs)
	if start != 0 || end != 0 {
		t.Errorf("未知引用应 0,0，实际 %v %v", start, end)
	}
}

// TestTruncate 验证 truncate 截断逻辑：短于等于 n 原样返回，超长截断加省略号。
func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("短文本应原样，实际 %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Errorf("超长应截断加省略号，实际 %q", got)
	}
	if got := truncate("", 3); got != "" {
		t.Errorf("空串应原样，实际 %q", got)
	}
}

// TestErrNoValidCitations_Error 验证 ErrNoValidCitations 的错误消息格式。
func TestErrNoValidCitations_Error(t *testing.T) {
	e := &ErrNoValidCitations{Detail: "无有效引用"}
	want := "证据校验失败：无有效引用"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
