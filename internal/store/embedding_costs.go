package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// FreezeEmbeddingEstimate copies this exact connection/model input-token price.
// An absent price remains unknown; it never borrows text-output or audio rates.
func (s *Store) FreezeEmbeddingEstimate(ctx context.Context, cfg provider.EmbeddingConfig, inputs []string) (*provider.KnowledgeEstimate, error) {
	if !strings.HasPrefix(cfg.Provider, "embedding-") || cfg.Model == "" || cfg.Unit != "input_tokens" {
		return nil, ErrInvalidEditorialState
	}
	estimate, err := provider.EstimateEmbeddingInputs(inputs)
	if err != nil {
		return nil, err
	}
	price, err := s.GetModelPrice(ctx, cfg.Provider, cfg.Model)
	if errors.Is(err, ErrNotFound) {
		return estimate, nil
	}
	if err != nil {
		return nil, err
	}
	// Guard arithmetic before using the shared frozen-price accounting contract.
	if price.InputCentsPerMillion > (math.MaxInt64-999999)/int64(estimate.InputTokens) {
		return nil, fmt.Errorf("%w: embedding price exceeds accounting capacity", ErrInvalidEditorialState)
	}
	estimate.PriceKnown = true
	estimate.InputCentsPerMillion = price.InputCentsPerMillion
	estimate.PriceUpdatedAt = price.UpdatedAt
	amount, _ := estimate.CostForUnits(estimate.InputTokens, 0)
	estimate.CostCents = &amount
	return estimate, nil
}

// HoldEmbeddingBudget accepts the input-only contract; text generation requires
// output capacity and continues to use HoldKnowledgeBudget unchanged.
func (s *Store) HoldEmbeddingBudget(ctx context.Context, jobID, operation string, cfg provider.EmbeddingConfig, e *provider.KnowledgeEstimate) (*models.BudgetReservation, error) {
	if e == nil || e.Method != "embedding-utf8-byte-bound-v1" || e.InputTokens <= 0 || e.OutputTokens != 0 || e.OutputCentsPerMillion != 0 || (operation != "embedding_content" && operation != "embedding_query" && operation != "embedding_preflight") || !strings.HasPrefix(cfg.Provider, "embedding-") {
		return nil, ErrBudgetIncomplete
	}
	return s.holdBudget(ctx, jobID, operation, false, cfg.Provider, cfg.Model, e.InputTokens, 0, e)
}
