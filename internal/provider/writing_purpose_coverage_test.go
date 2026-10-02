package provider

import (
	"math"
	"strings"
	"testing"
)

func TestWritingPurposeAdmissionRejectsMalformedFrozenProtocol(t *testing.T) {
	valid, _ := FreezeWritingPurpose("explanation")
	for _, tc := range []struct {
		name      string
		purpose   *FrozenWritingPurpose
		version   string
		materials []KnowledgeMaterial
	}{
		{"missing", nil, KnowledgeArticlePurposePromptVersion, nil},
		{"unknown-mode", &FrozenWritingPurpose{Mode: "alien", RuleVersion: WritingPurposeRuleVersion}, KnowledgeArticlePurposePromptVersion, nil},
		{"changed-rule", &FrozenWritingPurpose{Mode: valid.Mode, RuleVersion: "future", Fingerprint: valid.Fingerprint}, KnowledgeArticlePurposePromptVersion, nil},
		{"changed-fingerprint", &FrozenWritingPurpose{Mode: valid.Mode, RuleVersion: valid.RuleVersion, Fingerprint: "forged"}, KnowledgeArticlePurposePromptVersion, nil},
		{"legacy-understanding", nil, "knowledge-article-v3", []KnowledgeMaterial{{Kind: "understanding"}}},
		{"legacy-reference", nil, "knowledge-article-v4", []KnowledgeMaterial{{UnderstandingReferences: []KnowledgeUnderstandingReference{{Kind: "article", ObjectID: "a"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := KnowledgeArticleRequest{PromptVersion: tc.version, WritingPurpose: tc.purpose, Stage: "write", Materials: tc.materials}
			if _, _, err := KnowledgeArticleMessages(req); err == nil {
				t.Fatal("invalid protocol produced messages")
			}
			if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{}); err == nil {
				t.Fatal("invalid admission advanced")
			}
			if _, usage, err := NewOpenAIProvider("unused").KnowledgeArticleStep(t.Context(), req); err == nil || usage.InputUnits != 0 {
				t.Fatal("invalid protocol contacted provider", usage, err)
			}
		})
	}
}

func TestWritingPurposeFinalReviewChecksActualDraftAndSourceIdentity(t *testing.T) {
	purpose, _ := FreezeWritingPurpose("comparison")
	req := KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePurposePromptVersion, WritingPurpose: &purpose, Stage: "review_final", Materials: []KnowledgeMaterial{{ID: "a", Kind: "keypoint", SourceType: "episode", SourceID: "same"}, {ID: "b", Kind: "source_note", SourceType: "episode", SourceID: "same"}}}
	for _, section := range WritingModes()[2].Sections {
		req.Blocks = append(req.Blocks, KnowledgeBlock{PurposeSection: section, Kind: "synthesis", Text: "资料支持的比较", MaterialIDs: []string{"a", "b"}})
	}
	yes := true
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Passed: &yes}); err == nil || !strings.Contains(err.Error(), "不同来源") {
		t.Fatal("two materials counted as independent sources", err)
	}
	req.Materials[1].SourceID = "other"
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Passed: &yes}); err != nil {
		t.Fatal(err)
	}
	req.WritingPurpose = &FrozenWritingPurpose{Mode: "unknown"}
	if err := ValidateWritingPurposeResult(req, &KnowledgeArticleResult{Passed: &yes}); err == nil {
		t.Fatal("unknown mode review passed")
	}
	req.WritingPurpose = &purpose
	if err := ValidateWritingPurposeResult(req, &KnowledgeArticleResult{}); err != nil {
		t.Fatal("missing verdict should be checked by verdict validation", err)
	}
	req.Stage = "update_propose"
	if err := ValidateKnowledgeResult(req, nil); err == nil {
		t.Fatal("empty update accepted")
	}
}

func TestWritingPurposeUnderstandingNeverDirectSourceQuote(t *testing.T) {
	materials := []KnowledgeMaterial{{ID: "u", Kind: "understanding", Evidence: "[u-1] 个人答案", Citations: []string{"u-1"}}}
	blocks := []KnowledgeBlock{{Kind: "reflection", Text: "个人答案", MaterialIDs: []string{"u"}, Quotes: []KnowledgeQuote{{MaterialID: "u", Text: "个人答案"}}}, {Kind: "reflection", Text: "另一理解", MaterialIDs: []string{"u"}}}
	if ValidateKnowledgeBlocks("个人理解", blocks, materials) == nil {
		t.Fatal("understanding became source quote")
	}
}

func TestWritingPurposeEstimateCostRejectsOverflowAndNegativeReceipts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		e       *KnowledgeEstimate
		in, out int
		cost    int64
		known   bool
	}{
		{"negative-input", nil, -1, 0, 0, false},
		{"negative-output", nil, 0, -1, 0, false},
		{"free-empty", nil, 0, 0, 0, true},
		{"missing-price", nil, 1, 1, 0, false},
		{"negative-price", &KnowledgeEstimate{PriceKnown: true, InputCentsPerMillion: -1}, 1, 0, 0, false},
		{"multiply-overflow", &KnowledgeEstimate{PriceKnown: true, InputCentsPerMillion: math.MaxInt64}, 2, 0, 0, false},
		{"output-overflow", &KnowledgeEstimate{PriceKnown: true, OutputCentsPerMillion: math.MaxInt64}, 0, 2, 0, false},
		{"sum-overflow", &KnowledgeEstimate{PriceKnown: true, InputCentsPerMillion: math.MaxInt64 / 2, OutputCentsPerMillion: math.MaxInt64 / 2}, 1, 1, 0, false},
		{"round-up", &KnowledgeEstimate{PriceKnown: true, InputCentsPerMillion: 1}, 1, 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, known := tc.e.CostForUnits(tc.in, tc.out)
			if cost != tc.cost || known != tc.known {
				t.Fatal(cost, known)
			}
		})
	}
}
