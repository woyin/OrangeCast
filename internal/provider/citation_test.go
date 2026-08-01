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
