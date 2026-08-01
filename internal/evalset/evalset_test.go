package evalset

import "testing"

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
