package provider

import (
	"strings"
	"testing"
)

func updateProviderInput() KnowledgeArticleRequest {
	return KnowledgeArticleRequest{Stage: "update_propose", PromptVersion: KnowledgeArticlePromptVersion, Materials: knowledgeTestMaterials(), Blocks: []KnowledgeBlock{{ID: "b0", Kind: "source", Text: "旧表达", MaterialIDs: []string{"source"}}, {ID: "b1", Kind: "reflection", Text: "旧理解", MaterialIDs: []string{"note"}}}, Update: &KnowledgeUpdateContext{ParentRevision: 1, ParentHash: "hash"}}
}
func updateProviderAnalysis() *KnowledgeUpdateAnalysis {
	return &KnowledgeUpdateAnalysis{Decision: "update", Reason: "新证据补充条件", Changes: []KnowledgeUpdateChange{{Action: "add", BlockID: "b0", Reason: "补充限制", MaterialIDs: []string{"source"}}}}
}
func TestKnowledgeUpdateValidationAndCurrentAttribution(t *testing.T) {
	for _, action := range []string{"add", "remove", "refute"} {
		req := updateProviderInput()
		a := updateProviderAnalysis()
		a.Changes[0].Action = action
		if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Update: a}); err != nil {
			t.Fatal(action, err)
		}
	}
	for _, mutate := range []func(*KnowledgeArticleRequest, *KnowledgeUpdateAnalysis){
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { r.Update = nil },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { r.Update.ParentRevision = 0 },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { r.Update.ParentHash = "" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Reason = "" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Reason = strings.Repeat("界", 4001) },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Decision = "invented" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Changes[0].Action = "invented" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Changes[0].Reason = "" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes[0].Reason = strings.Repeat("界", 4001)
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes[0].MaterialIDs = []string{"invented"}
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Changes[0].MaterialIDs = nil },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Changes[0].BlockID = "unknown" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes[0].Action = "remove"
			a.Changes[0].BlockID = "unknown"
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes[0].Action = "refute"
			a.Changes[0].MaterialIDs = []string{"note"}
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Changes[0].Action = "keep" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Decision = "no_change" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Decision = "new_direction" },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Decision = "new_direction"
			a.NewDirection = &KnowledgeTopic{Title: "不足"}
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) { a.Missing = make([]string, 21) },
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes = make([]KnowledgeUpdateChange, 121)
		},
		func(r *KnowledgeArticleRequest, a *KnowledgeUpdateAnalysis) {
			a.Changes[0].MaterialIDs = make([]string, 21)
		},
	} {
		r, a := updateProviderInput(), updateProviderAnalysis()
		mutate(&r, a)
		if ValidateKnowledgeUpdate(r, a) == nil {
			t.Fatal("invalid update admitted", a)
		}
	}
	if ValidateKnowledgeUpdate(updateProviderInput(), nil) == nil {
		t.Fatal("nil analysis")
	}
	for _, decision := range []string{"no_change", "insufficient"} {
		a := updateProviderAnalysis()
		a.Decision = decision
		a.Changes[0].Action = "keep"
		if err := ValidateKnowledgeUpdate(updateProviderInput(), a); err != nil {
			t.Fatal(err)
		}
	}
	a := updateProviderAnalysis()
	a.Decision = "new_direction"
	a.Changes = nil
	a.NewDirection = &KnowledgeTopic{Title: "新的用途", Question: "如何建立材料目录？", Thesis: "目录按场景组织", Outline: "分类", Score: 90, Sufficient: true, MaterialIDs: []string{"source", "note"}}
	if err := ValidateKnowledgeUpdate(updateProviderInput(), a); err != nil {
		t.Fatal(err)
	}
}
func TestKnowledgeUpdateMessagesKeepParentSeparateFromEvidence(t *testing.T) {
	req := updateProviderInput()
	system, input, err := KnowledgeArticleMessages(req)
	if err != nil || !strings.Contains(system, "不是事实证据") || !strings.Contains(system, "update") || !strings.Contains(input, "旧表达") {
		t.Fatal(system, input, err)
	}
	req.Stage = "review_final"
	system, _, err = KnowledgeArticleMessages(req)
	if err != nil || !strings.Contains(system, "当前") {
		t.Fatal(system, err)
	}
	req.Update = nil
	req.Stage = "discover"
	system, _, err = KnowledgeArticleMessages(req)
	if err != nil || strings.Contains(system, "update_propose") {
		t.Fatal("update instructions leaked into old contract", system, err)
	}
}
