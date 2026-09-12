package provider

import (
	"strings"
	"testing"
)

func TestValidateHighlightSet_Valid(t *testing.T) {
	hs := &HighlightSet{
		Highlights: []Highlight{
			{Gist: "重要观点", Citations: []string{"seg-0001", "seg-0002"}},
			{Gist: "实用技巧", Citations: []string{"seg-0003"}},
		},
	}
	segs := []Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "A"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "B"},
		{ID: "seg-0003", Start: 10, End: 15, Text: "C"},
	}
	got, err := ValidateHighlightSet(hs, segs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Highlights) != 2 {
		t.Errorf("应保留 2 个高光，实际 %d", len(got.Highlights))
	}
}

func TestValidateHighlightSet_DropsInvalidCitations(t *testing.T) {
	hs := &HighlightSet{
		Highlights: []Highlight{
			{Gist: "有效", Citations: []string{"seg-0001"}},
			{Gist: "无效引用", Citations: []string{"seg-9999"}}, // 不存在的 segment → 省略
			{Gist: "", Citations: []string{"seg-0001"}},     // 空 Gist → 省略
			{Gist: "无引用", Citations: nil},                   // 无 Citation → 省略
		},
	}
	segs := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "A"}}
	got, err := ValidateHighlightSet(hs, segs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Highlights) != 1 {
		t.Errorf("应只保留 1 个有效高光，实际 %d", len(got.Highlights))
	}
	if got.Highlights[0].Gist != "有效" {
		t.Errorf("保留的高光应是'有效'，实际 %s", got.Highlights[0].Gist)
	}
}

func TestValidateHighlightSet_AllInvalid(t *testing.T) {
	hs := &HighlightSet{
		Highlights: []Highlight{{Gist: "x", Citations: []string{"missing"}}},
	}
	segs := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "A"}}
	_, err := ValidateHighlightSet(hs, segs)
	if err == nil {
		t.Error("全部无效应报错")
	}
}

func TestValidateHighlightSet_Nil(t *testing.T) {
	_, err := ValidateHighlightSet(nil, nil)
	if err == nil {
		t.Error("nil 应报错")
	}
}

// TestValidateHighlightSet_D01Constraints D01：连续区间保留；乱序/跨空洞/重叠/零时长显式拒绝。
func TestValidateHighlightSet_D01Constraints(t *testing.T) {
	segs := []Segment{
		{ID: "seg-0001", Start: 0, End: 10, Text: "开场"},
		{ID: "seg-0002", Start: 10, End: 20, Text: "广告"},
		{ID: "seg-0003", Start: 20, End: 30, Text: "观点一"},
		{ID: "seg-0004", Start: 30, End: 40, Text: "观点二"},
		{ID: "seg-0005", Start: 40, End: 50, Text: "收尾"},
	}
	// 正常连续区间保留；引用乱序输入会被规范化（稳定 ID 语义）。
	ok := &HighlightSet{Highlights: []Highlight{
		{Gist: "g1", Citations: []string{"seg-0003", "seg-0004"}},
		{Gist: "g2", Citations: []string{"seg-0001"}},
		{Gist: "g3", Citations: []string{"seg-0005"}}, // 尾段不遗漏
	}}
	cleaned, err := ValidateHighlightSet(ok, segs)
	if err != nil {
		t.Fatalf("正常连续区间应保留: %v", err)
	}
	if len(cleaned.Highlights) != 3 {
		t.Fatalf("应保留 3 个高光: %+v", cleaned)
	}

	// 跨广告空洞（seg-0001 与 seg-0003 跨过被跳过的广告段）→ 显式拒绝。
	gap := &HighlightSet{Highlights: []Highlight{{Gist: "跨广告", Citations: []string{"seg-0001", "seg-0003"}}}}
	if _, err := ValidateHighlightSet(gap, segs); err == nil || !strings.Contains(err.Error(), "不连续") {
		t.Fatalf("跨空洞应显式拒绝: %v", err)
	}
	// 重叠 → 显式拒绝。
	overlap := &HighlightSet{Highlights: []Highlight{
		{Gist: "a", Citations: []string{"seg-0003"}},
		{Gist: "b", Citations: []string{"seg-0003", "seg-0004"}},
	}}
	if _, err := ValidateHighlightSet(overlap, segs); err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("重叠应显式拒绝: %v", err)
	}
	// 零时长 → 显式拒绝。
	zero := []Segment{{ID: "seg-0001", Start: 5, End: 5, Text: "零时长"}}
	zeroHS := &HighlightSet{Highlights: []Highlight{{Gist: "z", Citations: []string{"seg-0001"}}}}
	if _, err := ValidateHighlightSet(zeroHS, zero); err == nil || !strings.Contains(err.Error(), "零时长") {
		t.Fatalf("零时长应显式拒绝: %v", err)
	}
	// 完全相同的引用集合 → 去重不报错。
	dup := &HighlightSet{Highlights: []Highlight{
		{Gist: "first", Citations: []string{"seg-0003"}},
		{Gist: "dup", Citations: []string{"seg-0003"}},
	}}
	cleaned, err = ValidateHighlightSet(dup, segs)
	if err != nil || len(cleaned.Highlights) != 1 {
		t.Fatalf("重复集合应去重: %v %+v", err, cleaned)
	}
}
