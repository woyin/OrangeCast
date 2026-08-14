package provider

import "testing"

func TestValidateCuratorResultRejectsUnapprovedMaterial(t *testing.T) {
	request := CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1"}}}
	valid := &CuratorResult{Thesis: "论点", Outline: "# 结构", SelectedKeyPointIDs: []string{"kp-1"}}
	if _, err := validateCuratorResult(valid, request); err != nil {
		t.Fatalf("authorized brief should validate: %v", err)
	}
	invalid := &CuratorResult{Thesis: "论点", Outline: "# 结构", SelectedKeyPointIDs: []string{"invented"}}
	if _, err := validateCuratorResult(invalid, request); err == nil {
		t.Fatal("Curator must not introduce material outside the accepted proposal")
	}
}
