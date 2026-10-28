package provider

import (
	"fmt"
	"testing"
)

// TestGroq_Translate_ChunksLongTranscript 超过单批上限时按 40 段分批请求，
// 合并结果且不重复、不丢段。
func TestGroq_Translate_ChunksLongTranscript(t *testing.T) {
	segments := make([]Segment, 0, 45)
	for i := 1; i <= 45; i++ {
		segments = append(segments, Segment{ID: fmt.Sprintf("seg-%04d", i), Start: float64(i - 1), End: float64(i), Text: "text"})
	}
	calls := 0
	g := NewGroqProvider("key")
	g.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		calls++
		if calls == 1 {
			return `{"segments":[{"id":"seg-0001","text":"一"}]}`, 200, nil
		}
		return `{"segments":[{"id":"seg-0041","text":"四十一"}]}`, 200, nil
	}
	res, err := g.Translate(segments, "中文")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if calls != 2 {
		t.Fatalf("45 段应分 2 批，实际 %d 批", calls)
	}
	if len(res.Segments) != 2 {
		t.Fatalf("应合并且只保留返回的片段，实际 %+v", res.Segments)
	}
}
