package provider

import "testing"

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
