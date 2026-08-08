package evalset

import (
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func TestEvalSet_AllSamplesPass(t *testing.T) {
	issues := Check()
	if len(issues) == 0 {
		return
	}
	for _, iss := range issues {
		t.Errorf("样本 %s [%s]: %s", iss.SampleID, iss.Kind, iss.Detail)
	}
	t.Fatalf("EvalSet 校验未通过（%d 个问题）", len(issues))
}

func TestEvalSet_Size(t *testing.T) {
	if len(Samples) < 5 || len(Samples) > 10 {
		t.Fatalf("EvalSet 应为 5-10 个样本，实际 %d", len(Samples))
	}
	hasZH, hasEN := false, false
	for _, s := range Samples {
		if s.Language == "zh" {
			hasZH = true
		}
		if s.Language == "en" {
			hasEN = true
		}
		if s.ID == "" || len(s.Segments) == 0 || s.Card == nil {
			t.Errorf("样本 %q 不完整", s.ID)
		}
	}
	if !hasZH || !hasEN {
		t.Error("EvalSet 应同时覆盖中英文样本")
	}
}

// TestQuoteInSegments 验证金句逐字匹配：命中与未命中。
func TestQuoteInSegments(t *testing.T) {
	segs := []provider.Segment{{ID: "s1", Start: 0, End: 5, Text: "主权财富基金改变全球投资格局"}}
	if !quoteInSegments("改变全球投资格局", []string{"s1"}, segs) {
		t.Error("子串金句应逐字命中")
	}
	if quoteInSegments("完全不同的内容", []string{"s1"}, segs) {
		t.Error("非子串应未命中")
	}
	// 引用不存在的段 → 未命中
	if quoteInSegments("改变全球投资", []string{"s2"}, segs) {
		t.Error("引用不存在段应未命中")
	}
}

// TestAllCitations 验证收集卡片全部 Citation。
func TestAllCitations(t *testing.T) {
	card := &provider.KnowledgeCard{
		Summary:   provider.CitedText{Citations: []string{"s1"}},
		KeyPoints: []provider.KeyPoint{{Citations: []string{"s2"}}},
		Chapters:  []provider.Chapter{{Citations: []string{"s3"}}},
		Quotes:    []provider.Quote{{Citations: []string{"s4"}}},
	}
	got := allCitations(card)
	if len(got) != 4 {
		t.Errorf("应收集 4 个 citation，实际 %v", got)
	}
}

// TestCheckSample_IssueBranches 用故意构造的非法样本覆盖 checkSample 的
// schema/time/citation/verbatim 四类问题分支。
func TestCheckSample_IssueBranches(t *testing.T) {
	smp := Sample{
		ID:       "bad-01",
		Language: "zh",
		Segments: []provider.Segment{
			// 时间非法：start=-1；另一个 start>=end
			{ID: "seg-0001", Start: -1, End: 5, Text: "非法时间片段"},
			{ID: "seg-0002", Start: 5, End: 5, Text: "零长度片段"},
		},
		Card: &provider.KnowledgeCard{
			Title: "",
			Summary: provider.CitedText{
				Text:       "无标题卡片且引用不存在的段",
				Citations:  []string{"seg-9999"},
			},
			Quotes: []provider.Quote{
				{Text: "完全不在片段中的金句", Citations: []string{"seg-0001"}},
			},
		},
	}
	issues := checkSample(smp)
	kinds := map[string]bool{}
	for _, iss := range issues {
		kinds[iss.Kind] = true
	}
	for _, want := range []string{"schema", "time", "citation", "verbatim"} {
		if !kinds[want] {
			t.Errorf("应产出 %s 类问题，实际 %v", want, issues)
		}
	}
}
