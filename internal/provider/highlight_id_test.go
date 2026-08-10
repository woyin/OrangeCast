package provider

import "testing"

// TestStableHighlightID_StableAndDistinct (ADR-0019)
// 相同 Citation 集合（无论顺序）→ 相同 ID；不同 Citation 集合 → 不同 ID。
func TestStableHighlightID_StableAndDistinct(t *testing.T) {
	a := stableHighlightID([]string{"seg-0003", "seg-0001", "seg-0002"})
	b := stableHighlightID([]string{"seg-0001", "seg-0002", "seg-0003"}) // 顺序不同
	if a != b {
		t.Errorf("相同 Citation 集合（不同顺序）应得相同 ID：a=%s b=%s", a, b)
	}
	c := stableHighlightID([]string{"seg-0001", "seg-0002"})
	if a == c {
		t.Errorf("不同 Citation 集合应得不同 ID：a=%s c=%s", a, c)
	}
	if len(a) != 12 {
		t.Errorf("ID 应为 12 hex 字符，实际 %d", len(a))
	}
}

// TestValidateHighlightSet_AssignsStableIDs (ADR-0019)
// ValidateHighlightSet 必须为每个 Highlight 分配基于 Citation 集合的稳定 ID。
func TestValidateHighlightSet_AssignsStableIDs(t *testing.T) {
	segs := []Segment{
		{ID: "seg-0001"}, {ID: "seg-0002"}, {ID: "seg-0003"}, {ID: "seg-0004"},
	}
	hs := &HighlightSet{Highlights: []Highlight{
		{Gist: "g1", Citations: []string{"seg-0001", "seg-0002"}},
		{Gist: "g2", Citations: []string{"seg-0003", "seg-0004"}},
	}}
	cleaned, err := ValidateHighlightSet(hs, segs)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned.Highlights) != 2 {
		t.Fatalf("应保留 2 个，实际 %d", len(cleaned.Highlights))
	}
	if cleaned.Highlights[0].ID == "" || cleaned.Highlights[1].ID == "" {
		t.Error("每个 Highlight 必须有非空稳定 ID")
	}
	if cleaned.Highlights[0].ID == cleaned.Highlights[1].ID {
		t.Error("不同 Citation 集合的 Highlight 必须有不同 ID")
	}
	// 重新 Validate 相同输入，ID 应稳定不变。
	cleaned2, _ := ValidateHighlightSet(hs, segs)
	if cleaned.Highlights[0].ID != cleaned2.Highlights[0].ID {
		t.Error("相同 Citation 集合刷新后 ID 应稳定不变")
	}
}

// TestStableHighlightID_SkipsEmptyAndDuplicate 验证空字符串与重复 Citation 被忽略。
// 覆盖 stableHighlightID 中 c == "" || seen[c] → continue 分支。
func TestStableHighlightID_SkipsEmptyAndDuplicate(t *testing.T) {
	id := stableHighlightID([]string{"", "seg-0001", "seg-0001"})
	if len(id) != 12 {
		t.Fatalf("去重后应生成 12 字符 ID，实际 %q", id)
	}
	// 与仅含 seg-0001 的集合一致（空串与重复不影响）
	base := stableHighlightID([]string{"seg-0001"})
	if id != base {
		t.Errorf("空串与重复不应改变 ID：%q vs %q", id, base)
	}
	// 全部空/空白 → 仍生成确定性 ID（空集合哈希）
	empty := stableHighlightID([]string{"", "  "})
	if len(empty) != 12 {
		t.Fatalf("空集合也应生成 ID，实际 %q", empty)
	}
}
