package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWritingModeCatalogFrozen(t *testing.T) {
	modes := WritingModes()
	if len(modes) != 4 {
		t.Fatal(modes)
	}
	for _, mode := range modes {
		first, err := FreezeWritingPurpose(mode.ID)
		if err != nil {
			t.Fatal(err)
		}
		second, _ := FreezeWritingPurpose(mode.ID)
		if first != second || ValidateWritingPurpose(first) != nil || len(first.Fingerprint) != 64 {
			t.Fatal(first)
		}
		broken := first
		broken.RuleVersion = "future"
		if ValidateWritingPurpose(broken) == nil {
			t.Fatal("unknown rule accepted")
		}
		broken = first
		broken.Fingerprint = "different"
		if ValidateWritingPurpose(broken) == nil {
			t.Fatal("invalid fingerprint accepted")
		}
	}
	modes[1].Sections[0] = "mutated"
	if WritingModes()[1].Sections[0] != "question" {
		t.Fatal("catalog mutable")
	}
	d, _ := FreezeWritingPurpose("")
	if d.Mode != "synthesis" {
		t.Fatal(d)
	}
	if _, err := FreezeWritingPurpose("unknown"); err == nil {
		t.Fatal("unknown accepted")
	}
	data, _ := json.Marshal(d)
	const golden = `{"mode":"synthesis","rule_version":"writing-purpose-v1","fingerprint":"b667350711d960350bf2911d13768c7965e28416a583093caddac44be072aa0c"}`
	if string(data) != golden {
		t.Fatal(string(data))
	}
}

func TestWritingModeRequestAndActualMessages(t *testing.T) {
	for _, mode := range WritingModes() {
		p, _ := FreezeWritingPurpose(mode.ID)
		for _, stage := range []string{"discover", "select", "write", "revise", "review", "review_final"} {
			req := KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePurposePromptVersion, Stage: stage, WritingPurpose: &p}
			if err := ValidateWritingPurposeRequest(req); err != nil {
				t.Fatal(err)
			}
			system, input, err := KnowledgeArticleMessages(req)
			if err != nil || !strings.Contains(system, mode.Rules) || !strings.Contains(input, p.Fingerprint) {
				t.Fatalf("%s %s: %v", mode.ID, stage, err)
			}
		}
	}
	if ValidateWritingPurposeRequest(KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePurposePromptVersion}) == nil {
		t.Fatal("missing purpose")
	}
	p, _ := FreezeWritingPurpose("synthesis")
	if ValidateWritingPurposeRequest(KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePromptVersion, WritingPurpose: &p}) == nil {
		t.Fatal("legacy purpose ignored")
	}
}

func TestWritingModeStructures(t *testing.T) {
	materials := []KnowledgeMaterial{{ID: "a", Kind: "keypoint", SourceType: "pod", SourceID: "a"}, {ID: "b", Kind: "keypoint", SourceType: "pod", SourceID: "b"}}
	for _, mode := range WritingModes() {
		p, _ := FreezeWritingPurpose(mode.ID)
		req := KnowledgeArticleRequest{Stage: "write", PromptVersion: KnowledgeArticlePurposePromptVersion, WritingPurpose: &p, Materials: materials}
		blocks := []KnowledgeBlock{}
		for _, section := range mode.Sections {
			blocks = append(blocks, KnowledgeBlock{PurposeSection: section, Kind: "synthesis", Text: "材料支持的解释，缺例子时明确材料不足", MaterialIDs: []string{"a", "b"}})
		}
		result := &KnowledgeArticleResult{Blocks: blocks}
		if err := ValidateWritingPurposeResult(req, result); err != nil {
			t.Fatalf("%s %v", mode.ID, err)
		}
		if len(blocks) == 0 {
			continue
		}
		result.Blocks = blocks[1:]
		if ValidateWritingPurposeResult(req, result) == nil {
			t.Fatal("missing structure accepted")
		}
		result.Blocks = blocks
		yes := true
		req.Stage = "review"
		req.Blocks = blocks[1:]
		if ValidateWritingPurposeResult(req, &KnowledgeArticleResult{Passed: &yes}) == nil {
			t.Fatal("passed missing structure")
		}
		no := false
		if ValidateWritingPurposeResult(req, &KnowledgeArticleResult{Passed: &no}) != nil {
			t.Fatal("failed review should retain diagnosis")
		}
	}
	p, _ := FreezeWritingPurpose("comparison")
	req := KnowledgeArticleRequest{Stage: "write", PromptVersion: KnowledgeArticlePurposePromptVersion, WritingPurpose: &p, Materials: materials[:1]}
	blocks := []KnowledgeBlock{}
	for _, s := range WritingModes()[2].Sections {
		blocks = append(blocks, KnowledgeBlock{PurposeSection: s, Text: "观点", MaterialIDs: []string{"a"}})
	}
	if ValidateWritingPurposeResult(req, &KnowledgeArticleResult{Blocks: blocks}) == nil {
		t.Fatal("one source comparison")
	}
	blocks[0].PurposeSection = "unknown"
	if ValidateWritingPurposeResult(req, &KnowledgeArticleResult{Blocks: blocks}) == nil {
		t.Fatal("unknown section")
	}
}

func TestKnowledgePromptCompatibilityWritingModes(t *testing.T) {
	for _, version := range []string{"knowledge-article-v1", "knowledge-article-v2", "knowledge-article-v3", "knowledge-article-v4"} {
		req := KnowledgeArticleRequest{Stage: "write", PromptVersion: version}
		system, input, err := KnowledgeArticleMessages(req)
		if err != nil || strings.Contains(input, "writing_purpose") || strings.Contains(system, "writing_purpose") {
			t.Fatalf("legacy changed %s %v", version, err)
		}
		if !KnowledgeArticlePromptSupported(version) {
			t.Fatal(version)
		}
	}
	if !KnowledgeArticlePromptSupported(KnowledgeArticlePurposePromptVersion) {
		t.Fatal("v5 unsupported")
	}
}

func TestWritingModeUnderstandingRemainsOwnerReflection(t *testing.T) {
	p, _ := FreezeWritingPurpose("synthesis")
	req := KnowledgeArticleRequest{Stage: "write", PromptVersion: KnowledgeArticlePurposePromptVersion, WritingPurpose: &p, Materials: []KnowledgeMaterial{{ID: "u", Kind: "understanding"}}}
	for _, kind := range []string{"source", "synthesis"} {
		result := &KnowledgeArticleResult{Blocks: []KnowledgeBlock{{Kind: kind, Text: "个人答案", MaterialIDs: []string{"u"}}}}
		if ValidateWritingPurposeResult(req, result) == nil {
			t.Fatalf("%s treated as fact", kind)
		}
	}
	result := &KnowledgeArticleResult{Blocks: []KnowledgeBlock{{Kind: "reflection", Text: "个人答案", MaterialIDs: []string{"u"}}}}
	if err := ValidateWritingPurposeResult(req, result); err != nil {
		t.Fatal(err)
	}
	result.Blocks[0].Quotes = []KnowledgeQuote{{MaterialID: "u", Text: "个人答案"}}
	if ValidateWritingPurposeResult(req, result) == nil {
		t.Fatal("understanding quoted")
	}
}
