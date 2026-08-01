package provider

import "testing"

func TestMapCitedToSources_OnlyCitedSegments(t *testing.T) {
	chunks := []Chunk{
		{Text: "A", Start: 0, End: 1, SegmentID: "seg-0001"},
		{Text: "B", Start: 1, End: 2, SegmentID: "seg-0002"},
		{Text: "C", Start: 2, End: 3, SegmentID: "seg-0003"},
	}
	// 只引用第 0 和第 2 个；越界索引忽略；重复去重
	out := MapCitedToSources(chunks, []int{0, 2, 9, 0})
	if len(out) != 2 {
		t.Fatalf("应只返回 2 个引用，实际 %d", len(out))
	}
	if out[0].SegmentID != "seg-0001" || out[1].SegmentID != "seg-0003" {
		t.Errorf("引用顺序/内容错误: %+v", out)
	}
}

func TestMapCitedToSources_NoFallback(t *testing.T) {
	chunks := []Chunk{{Text: "A", Start: 0, End: 1, SegmentID: "seg-0001"}}
	// 模型未给 cited → 绝不附加第一个片段（Phase 7 删除兜底）
	out := MapCitedToSources(chunks, nil)
	if len(out) != 0 {
		t.Fatalf("无 cited 时不应有 Sources，实际 %+v", out)
	}
	if HasReliableSources(&QAResult{Answer: "A", Sources: out}) {
		t.Error("无引用不应判定为可靠")
	}
	if HasReliableSources(&QAResult{Answer: "", Sources: []Source{{SegmentID: "seg-0001"}}}) {
		t.Error("空答案不应判定为可靠")
	}
	if !HasReliableSources(&QAResult{Answer: "A", Sources: []Source{{SegmentID: "seg-0001"}}}) {
		t.Error("有答案且有引用应判定为可靠")
	}
}
