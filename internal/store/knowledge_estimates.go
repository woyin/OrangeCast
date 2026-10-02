package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/woyin/orangecast/internal/provider"
)

func ensureKnowledgeStageConfigs(req *provider.KnowledgeArticleRequest, model string) {
	if (req.PromptVersion != provider.KnowledgeArticlePromptVersion && req.PromptVersion != provider.KnowledgeArticlePurposePromptVersion) || len(req.StageConfigs) != 0 {
		return
	}
	models := map[string]string{}
	for _, stage := range []string{"discover", "select", "write", "revise", "review", "review_final", "weekly_review"} {
		models[stage] = model
	}
	if req.ReviewModel != "" {
		models["review"], models["review_final"] = req.ReviewModel, req.ReviewModel
	}
	req.StageConfigs = provider.FreezeKnowledgeStageConfigs(models)
}

func freezeKnowledgeEstimate(ctx context.Context, tx *sql.Tx, name, model string, req *provider.KnowledgeArticleRequest) (string, error) {
	cfg, err := provider.KnowledgeConfigForStage(*req, model)
	if err != nil {
		return "", err
	}
	if req.PromptVersion != provider.KnowledgeArticlePromptVersion && req.PromptVersion != provider.KnowledgeArticlePurposePromptVersion {
		return cfg.Model, nil
	}
	req.Estimate = nil // Each newly admitted stage estimates its own final messages.
	estimate, err := provider.EstimateKnowledgeRequest(*req, cfg.Model)
	if err != nil {
		return "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT input_cents_per_million,output_cents_per_million,updated_at FROM model_prices WHERE provider=? AND model=?`, name, cfg.Model).Scan(&estimate.InputCentsPerMillion, &estimate.OutputCentsPerMillion, &estimate.PriceUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	estimate.PriceKnown = err == nil
	if cost, known := estimate.CostForUnits(estimate.InputTokens, estimate.OutputTokens); known {
		estimate.CostCents = &cost
	}
	req.Estimate = estimate
	return cfg.Model, nil
}
