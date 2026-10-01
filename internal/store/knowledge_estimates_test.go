package store

import (
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"testing"
)

func TestKnowledgeStageFreezesRoutingPriceAndMessages(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	req.StageConfigs = provider.FreezeKnowledgeStageConfigs(map[string]string{"discover": "cheap", "write": "writer", "review": "reviewer", "revise": "writer", "review_final": "reviewer", "select": "selector", "weekly_review": "learning"})
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "cheap", InputCentsPerMillion: 100, OutputCentsPerMillion: 400}); err != nil {
		t.Fatal(err)
	}
	article, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "base", req, false)
	if err != nil {
		t.Fatal(err)
	}
	job, input := finishKnowledgeTestStage(t, s, true)
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil || exec.ConfiguredModel != "cheap" {
		t.Fatal(exec, err)
	}
	if input.Request.Estimate == nil || !input.Request.Estimate.PriceKnown || input.Request.Estimate.OutputTokens != 2048 || input.Request.Estimate.CostCents == nil {
		t.Fatal(input.Request.Estimate)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "cheap", InputCentsPerMillion: 999, OutputCentsPerMillion: 999}); err != nil {
		t.Fatal(err)
	}
	budget := int64(10000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.HoldKnowledgeBudget(ctx, job.ID, "knowledge_article", false, "pod", "cheap", input.Request.Estimate)
	if err != nil || reservation.EstimatedCostCents != *input.Request.Estimate.CostCents {
		t.Fatal(reservation, err)
	}
	job, input = finishKnowledgeTestStage(t, s, true)
	exec, err = s.GetJobExecution(ctx, job.ID)
	if err != nil || exec.ConfiguredModel != "writer" || input.Request.Estimate.OutputTokens != 8192 {
		t.Fatal(exec, input, err)
	}
	if input.Request.Estimate.PriceKnown || input.Request.Estimate.CostCents != nil {
		t.Fatal("missing price presented as free")
	}
	if _, err := s.HoldKnowledgeBudget(ctx, job.ID, "knowledge_article", false, "pod", "writer", input.Request.Estimate); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatal(err)
	}
	if _, err := s.HoldKnowledgeBudget(ctx, job.ID, "knowledge_article", false, "pod", "writer", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	job, input = finishKnowledgeTestStage(t, s, true)
	exec, err = s.GetJobExecution(ctx, job.ID)
	if err != nil || exec.ConfiguredModel != "reviewer" || input.Request.Estimate.OutputTokens != 4096 {
		t.Fatal(exec, err)
	}
	runs, err := s.ListKnowledgeExecutions(ctx, article.ID)
	if err != nil || len(runs) != 3 {
		t.Fatal(runs, err)
	}
	for _, run := range runs {
		if run.Estimate == nil || run.Estimate.InputFingerprint == "" {
			t.Fatal("estimate absent from receipt view", run)
		}
	}
}

func TestKnowledgeInvalidConfigRollsBackAdmissionAndLegacyRemains(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	req.StageConfigs = map[string]provider.KnowledgeStageConfig{"discover": {Model: "broken", MaxOutputTokens: 1}}
	if _, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false); err == nil {
		t.Fatal("bad output contract admitted")
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial article persisted", n, err)
	}
	req.StageConfigs = nil
	req.PromptVersion = "knowledge-article-v3"
	req.ReviewModel = "legacy-review"
	_, _, err = s.ReserveKnowledgeArticle(ctx, profile, "pod", "legacy", req, false)
	if err != nil {
		t.Fatal(err)
	}
	job, input := finishKnowledgeTestStage(t, s, true)
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil || exec.ConfigVersion != "knowledge-article-v3" || input.Request.Estimate != nil {
		t.Fatal(exec, err)
	}
	job, input = finishKnowledgeTestStage(t, s, true)
	job, input = finishKnowledgeTestStage(t, s, true)
	exec, err = s.GetJobExecution(ctx, job.ID)
	if err != nil || exec.ConfiguredModel != "legacy-review" || input.Request.Estimate != nil {
		t.Fatal(exec, err)
	}
	var frozen provider.KnowledgeArticleRequest
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &struct {
		Request *provider.KnowledgeArticleRequest `json:"request"`
	}{Request: &frozen}); err != nil {
		t.Fatal(err)
	}
	if !provider.KnowledgeArticlePromptSupported(frozen.PromptVersion) {
		t.Fatal(frozen.PromptVersion)
	}
}
