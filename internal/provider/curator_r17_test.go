package provider

import "testing"

func TestValidateCuratorResultRejectsFabricatedDuplicateAndOverlap(t *testing.T) {
	req := CuratorRequest{Materials: []ArticleMaterial{{KeyPointID: "a"}, {KeyPointID: "b"}}}
	cases := []struct {
		name   string
		result *CuratorResult
	}{
		{"fabricated", &CuratorResult{Thesis: "t", Outline: "o", SelectedKeyPointIDs: []string{"x"}}},
		{"overlap", &CuratorResult{Thesis: "t", Outline: "o", SelectedKeyPointIDs: []string{"a"}, RejectedKeyPointIDs: []string{"a"}}},
		{"duplicate selected", &CuratorResult{Thesis: "t", Outline: "o", SelectedKeyPointIDs: []string{"a", "a"}}},
		{"duplicate rejected", &CuratorResult{Thesis: "t", Outline: "o", SelectedKeyPointIDs: []string{"a"}, RejectedKeyPointIDs: []string{"b", "b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateCuratorResult(tc.result, req); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
