package store

import (
	"context"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestEditorialCostLedgerAndBudgetGate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	monthly, article := int64(2), int64(1)
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "cost-profile", MonthlyBudgetCents: &monthly, PerArticleBudgetCents: &article})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "OpenAI", Model: "model-x", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 2000000}); err != nil {
		t.Fatal(err)
	}
	cost, err := s.CalculateEditorialCost(ctx, "openai", "model-x", 1, 0)
	if err != nil || cost != 1 {
		t.Fatalf("exact price should produce one cent: cost=%d err=%v", cost, err)
	}
	draftID := "draft-cost"
	// The optional draft foreign key must resolve, so a profile-only Scout call
	// exercises the monthly budget independently of article creation.
	if _, err := s.RecordEditorialUsage(ctx, models.EditorialUsageRecord{EditorialProfileID: profile.ID, TaskKind: "scout", EntityType: "profile", EntityID: profile.ID, Provider: "openai", Model: "model-x", PromptVersion: "v1", InputUnits: 1, CostCents: cost}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckEditorialBudget(ctx, profile.ID, nil, "openai", "model-x"); err != nil {
		t.Fatalf("one cent remains: %v", err)
	}
	if _, err := s.RecordEditorialUsage(ctx, models.EditorialUsageRecord{EditorialProfileID: profile.ID, TaskKind: "scout", EntityType: "profile", EntityID: draftID, Provider: "openai", Model: "model-x", PromptVersion: "v1", InputUnits: 1, CostCents: cost}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckEditorialBudget(ctx, profile.ID, nil, "openai", "model-x"); err == nil || !strings.Contains(err.Error(), "monthly") {
		t.Fatalf("exhausted monthly budget must block: %v", err)
	}
}

func TestEditorialBudgetRequiresConfiguredPrice(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	budget := int64(10)
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "priced", MonthlyBudgetCents: &budget})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckEditorialBudget(ctx, profile.ID, nil, "unknown", "model"); err == nil || !strings.Contains(err.Error(), "missing price") {
		t.Fatalf("configured budget cannot dispatch with unknown price: %v", err)
	}
}
