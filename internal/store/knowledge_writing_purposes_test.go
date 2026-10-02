package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func TestWritingPurposeDefaultsAndDistinctDiscoveryIdentity(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	prefs, err := s.GetKnowledgeArticleSettings(ctx)
	if err != nil || prefs.WritingMode != "synthesis" || prefs.PreviewWritingPlan {
		t.Fatalf("defaults %+v %v", prefs, err)
	}
	req, last, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{}, false)
	if err != nil || req.WritingPurpose == nil || req.WritingPurpose.Mode != "synthesis" || req.PromptVersion != provider.KnowledgeArticlePurposePromptVersion {
		t.Fatalf("new request %+v %v", req, err)
	}
	plain, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "writer", req, last, false)
	if err != nil {
		t.Fatal(err)
	}
	preview := true
	second, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{WritingMode: "explanation", PreviewWritingPlan: &preview}, false)
	if err != nil {
		t.Fatal(err)
	}
	explained, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "writer", second, last, false)
	if err != nil {
		t.Fatal(err)
	}
	if plain.ID == explained.ID || plain.InputHash == explained.InputHash || second.WritingPurpose.Mode != "explanation" || !second.PreviewWritingPlan {
		t.Fatal("distinct purpose and preview not frozen")
	}
	prefs.WritingMode = "unknown"
	if s.SetKnowledgeArticleSettings(ctx, prefs) == nil {
		t.Fatal("unknown purpose persisted")
	}
	if _, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{WritingMode: "unknown"}, false); err == nil {
		t.Fatal("unknown scope purpose accepted")
	}
}

func TestWritingPurposeExplicitRevisionPreservesPassedVersionAndFrozenStages(t *testing.T) {
	s, article, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	original, err := s.GetKnowledgeRevision(ctx, article.ID, article.PassedRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueKnowledgePurposeRevision(ctx, article.ID, article.WorkingRevision, "explanation", "解释概念与边界", "review-separate"); err != nil {
		t.Fatal(err)
	}
	changed, err := s.GetKnowledgeArticle(ctx, article.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.PassedRevision != article.PassedRevision || changed.WorkingRevision != article.WorkingRevision+1 || changed.Status != "revise" {
		t.Fatalf("new revision %+v", changed)
	}
	old, err := s.GetKnowledgeRevision(ctx, article.ID, article.PassedRevision)
	if err != nil || old.InputJSON != original.InputJSON || old.ContentHash != original.ContentHash || old.BlocksJSON != original.BlocksJSON || !old.Passed {
		t.Fatalf("passed version rewritten %+v %v", old, err)
	}
	if err = s.QueueKnowledgePurposeRevision(ctx, article.ID, article.WorkingRevision, "practice", "", "review"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision %v", err)
	}
	job, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil || job == nil {
		t.Fatalf("claim %v %v", job, err)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input KnowledgeStageInput
	if err = json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	purpose := input.Request.WritingPurpose
	if purpose == nil || purpose.Mode != "explanation" || input.Request.Update != nil || input.Request.Estimate == nil || input.Request.PromptVersion != provider.KnowledgeArticlePurposePromptVersion {
		t.Fatalf("frozen revise %+v", input)
	}
	blocks := []provider.KnowledgeBlock{}
	for _, section := range []string{"question", "concept", "example", "boundary"} {
		blocks = append(blocks, provider.KnowledgeBlock{Kind: "synthesis", Text: "材料解释与来源边界", MaterialIDs: []string{input.Request.Materials[0].ID}, PurposeSection: section})
	}
	result := &provider.KnowledgeArticleResult{Title: "具体知识解释", Blocks: blocks}
	if err = provider.ValidateKnowledgeResult(input.Request, result); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitKnowledgeStage(ctx, job, input, result); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	review, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil || review == nil {
		t.Fatalf("review %v %v", review, err)
	}
	re, err := s.GetJobExecution(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed KnowledgeStageInput
	if err = json.Unmarshal([]byte(re.InputSnapshotJSON), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Stage != "review_final" || reviewed.Request.WritingPurpose == nil || *reviewed.Request.WritingPurpose != *purpose || reviewed.Request.Estimate.InputFingerprint == input.Request.Estimate.InputFingerprint {
		t.Fatalf("purpose/reestimate lost %+v", reviewed)
	}
	old, err = s.GetKnowledgeRevision(ctx, article.ID, article.PassedRevision)
	if err != nil || old.ContentHash != original.ContentHash || old.InputJSON != original.InputJSON || !old.Passed {
		t.Fatal("old passed version lost after AI draft")
	}
}

func TestWritingPurposeKnownLegacyStagePreserved(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	req.PromptVersion = provider.KnowledgeArticlePromptVersion
	req.WritingPurpose = nil
	a, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "legacy-model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	_, discover := finishKnowledgeTestStage(t, s, true)
	_, write := finishKnowledgeTestStage(t, s, true)
	if discover.Request.WritingPurpose != nil || write.Request.WritingPurpose != nil || write.Request.PromptVersion != provider.KnowledgeArticlePromptVersion {
		t.Fatal("legacy upgraded in place")
	}
	revision, err := s.GetKnowledgeRevision(ctx, a.ID, 1)
	if err != nil || revision.PromptVersion != provider.KnowledgeArticlePromptVersion {
		t.Fatalf("legacy revision %+v %v", revision, err)
	}
}
