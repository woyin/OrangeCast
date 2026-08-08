package provider

import "testing"

// TestAppendUnique 验证 appendUnique 去重且过滤空串。
func TestAppendUnique(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "b", "c", "", "c", "a")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("appendUnique 结果不符: %v", got)
	}
	// 空输入
	if got := appendUnique(nil, "x"); len(got) != 1 || got[0] != "x" {
		t.Errorf("空输入应追加: %v", got)
	}
}

// TestMergeKnowledgeCards 验证多窗口知识卡片合并：主题/要点/章节/金句拼接、标签去重、摘要合并。
func TestMergeKnowledgeCards(t *testing.T) {
	cards := []*KnowledgeCard{
		{
			Title:   "通胀",
			Summary: CitedText{Text: "第一段总结", Citations: []string{"seg-0001"}},
			KeyPoints: []KeyPoint{
				{Content: "要点A", Citations: []string{"seg-0001"}},
			},
			Tags: []string{"经济", "通胀"},
		},
		{
			Title:   "", // 空标题不覆盖
			Summary: CitedText{Text: "第二段总结", Citations: []string{"seg-0002"}},
			KeyPoints: []KeyPoint{
				{Content: "要点B", Citations: []string{"seg-0002"}},
			},
			Tags: []string{"通胀", "央行"}, // 通胀已存在，去重
		},
		nil, // nil 卡片跳过
	}
	merged := mergeKnowledgeCards(cards)
	if merged.Title != "通胀" {
		t.Errorf("标题应为通胀，实际 %q", merged.Title)
	}
	if len(merged.KeyPoints) != 2 {
		t.Errorf("要点应合并为 2，实际 %d", len(merged.KeyPoints))
	}
	if len(merged.Tags) != 3 {
		t.Errorf("标签应去重为 3，实际 %v", merged.Tags)
	}
	if merged.Summary.Text != "第一段总结\n\n第二段总结" {
		t.Errorf("摘要应拼接，实际 %q", merged.Summary.Text)
	}
	if len(merged.Summary.Citations) != 2 {
		t.Errorf("摘要引用应合并为 2，实际 %v", merged.Summary.Citations)
	}
}

// TestValidReferenceIDs 验证只保留确实存在于检索结果的引用 ID（去重）。
func TestValidReferenceIDs(t *testing.T) {
	retrieved := []Chunk{
		{SegmentID: "seg-0001"},
		{SegmentID: "seg-0002"},
	}
	got := validReferenceIDs([]string{"seg-0001", "seg-9999", "seg-0002", "seg-0001"}, retrieved)
	if len(got) != 2 || got[0] != "seg-0001" || got[1] != "seg-0002" {
		t.Errorf("validReferenceIDs 结果不符: %v", got)
	}
	// 空检索 → 全过滤
	if got := validReferenceIDs([]string{"seg-0001"}, nil); len(got) != 0 {
		t.Errorf("空检索应全过滤，实际 %v", got)
	}
}
