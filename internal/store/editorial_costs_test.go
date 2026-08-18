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

// TestEditorialCostUnpricedModelIsZeroCost 验证未登记价格的模型按 0 成本记账、不报错
// （自定义兼容端点模型；预算由 CheckEditorialBudget 单独保证）。
func TestEditorialCostUnpricedModelIsZeroCost(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	cost, err := s.CalculateEditorialCost(ctx, "openai", "cmc/deepseek/deepseek-v4-flash", 1000, 500)
	if err != nil {
		t.Fatalf("未登记价格的模型不应报错: %v", err)
	}
	if cost != 0 {
		t.Fatalf("未登记价格的模型应按 0 成本记账，实际 %d", cost)
	}
	// 无预算画像仍可记账（审计行照写，成本为 0）
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "unpriced"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordEditorialUsage(ctx, models.EditorialUsageRecord{EditorialProfileID: profile.ID, TaskKind: "scout", EntityType: "profile", EntityID: profile.ID, Provider: "openai", Model: "cmc/deepseek/deepseek-v4-flash", PromptVersion: "v1", InputUnits: 1000, OutputUnits: 500, CostCents: cost}); err != nil {
		t.Fatalf("无价格模型也应能记录用量: %v", err)
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
