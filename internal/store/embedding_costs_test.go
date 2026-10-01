package store

import (
	"errors"
	"math"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestEmbeddingConfigPriceAndBudget(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, _ := provider.NewEmbeddingClient("secret", "https://embedding.example/v1", "vector", 2)
	cfg := p.Config()
	// A same-model text price does not authorize this independent connection.
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "vector", InputCentsPerMillion: 10, OutputCentsPerMillion: 10}); err != nil {
		t.Fatal(err)
	}
	e, err := s.FreezeEmbeddingEstimate(ctx, cfg, []string{"你好"})
	if err != nil || e.PriceKnown || e.CostCents != nil {
		t.Fatal("unknown silently priced")
	}
	if hold, err := s.HoldEmbeddingBudget(ctx, "embedding-preflight", "embedding_preflight", cfg, e); err != nil || hold != nil {
		t.Fatal(err)
	}
	budget := int64(100)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HoldEmbeddingBudget(ctx, "embedding-preflight", "embedding_preflight", cfg, e); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatal(err)
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000, OutputCentsPerMillion: 100}); err == nil {
		t.Fatal("embedding output tariff accepted")
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000}); err != nil {
		t.Fatal(err)
	}
	e, err = s.FreezeEmbeddingEstimate(ctx, cfg, []string{"你好"})
	if err != nil || !e.PriceKnown || e.CostCents == nil || *e.CostCents != 1 || e.OutputTokens != 0 || e.OutputCentsPerMillion != 0 {
		t.Fatalf("%+v %v", e, err)
	}
	// Changing the table cannot mutate an admitted estimate.
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 2000}); err != nil {
		t.Fatal(err)
	}
	held, err := s.HoldEmbeddingBudget(ctx, "embedding-preflight", "embedding_preflight", cfg, e)
	if err != nil || held.EstimatedCostCents != 1 {
		t.Fatal(err)
	}
	repeated, err := s.HoldEmbeddingBudget(ctx, "embedding-preflight", "embedding_preflight", cfg, e)
	if err != nil || repeated.ID != held.ID {
		t.Fatal("not idempotent")
	}
	actual, known := e.CostForUnits(2000, 0)
	if !known || actual != 2 {
		t.Fatal("actual must use frozen input-only tariff")
	}
	if _, err = s.HoldEmbeddingBudget(ctx, "bad", "embedding_query", cfg, nil); !errors.Is(err, ErrBudgetIncomplete) {
		t.Fatal(err)
	}
	other := cfg
	other.Provider = "pod"
	if _, err = s.FreezeEmbeddingEstimate(ctx, other, []string{"a"}); err == nil {
		t.Fatal("wrong unit route")
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezeEmbeddingEstimate(ctx, cfg, []string{"a"}); err == nil {
		t.Fatal("overflow price accepted")
	}
}

func TestEmbeddingUnverifiedModelUsageIsNeverPriced(t *testing.T) {
	s := newTestStore(t)
	client, err := provider.NewEmbeddingClient("secret", "https://embedding.example/v1", "unknown", 2)
	if err != nil {
		t.Fatal(err)
	}
	cfg := client.Config()
	if err = s.SetModelPrice(t.Context(), models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000000}); err != nil {
		t.Fatal(err)
	}
	estimate, err := s.FreezeEmbeddingEstimate(t.Context(), cfg, []string{"材料"})
	if err != nil || !estimate.PriceKnown {
		t.Fatal(estimate, err)
	}
	input := KnowledgeEmbeddingJobInput{Kind: "preflight", Config: cfg, Estimate: estimate}
	reported := &provider.EmbeddingResult{Model: "unknown", InputTokens: 31, UsageKnown: true, UnverifiedModel: true}
	if err = s.RecordEmbeddingReceipt(t.Context(), "unverified-response", input, reported); err != nil {
		t.Fatal(err)
	}
	usage, err := s.ListRunUsage(t.Context(), "unverified-response")
	if err != nil || len(usage) != 1 || usage[0].CostKnown || !usage[0].UnitsKnown || usage[0].InputUnits != 31 {
		t.Fatal(usage, err)
	}
	if err = provider.ValidateEmbeddingResult(reported, "unknown", 1, 2); err == nil {
		t.Fatal("unverified identity accepted")
	}
}
