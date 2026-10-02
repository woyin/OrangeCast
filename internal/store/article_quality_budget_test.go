package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestArticleQualityBudgetPersistentDispatchAndRecovery(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	e := &provider.KnowledgeEstimate{InputTokens: 10, OutputTokens: 20, Method: "bounded"}
	budget := int64(100)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.AdmitArticleQualityStage(ctx, "unpriced", "contract", "writer", e); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatal(err)
	}
	e.PriceKnown = true
	e.InputCentsPerMillion = 1000000
	e.OutputCentsPerMillion = 1000000
	budget = 29
	s.SetOwnerMonthlyBudget(ctx, &budget)
	if err := s.AdmitArticleQualityStage(ctx, "exhausted", "contract", "writer", e); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatal(err)
	}
	budget = 100
	s.SetOwnerMonthlyBudget(ctx, &budget)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.AdmitArticleQualityStage(ctx, "same", "contract", "writer", e) == nil {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatal("dispatch must be unique", admitted.Load())
	}
	if err := s.AdmitArticleQualityStage(ctx, "same", "changed-price", "different", e); err == nil {
		t.Fatal("changed config bypassed pending")
	}
	receipt := models.UsageReceipt{Provider: "pod", Model: "writer", InputUnits: 5, OutputUnits: 4, CostKnown: true, CostCents: 9}
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_quality_usage BEFORE INSERT ON usage_records BEGIN SELECT RAISE(ABORT,'ledger unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishArticleQualityStage(ctx, "same", receipt); err == nil {
		t.Fatal("ledger failure lost")
	}
	res, _ := s.getReservationByJob(ctx, "same")
	if res.Status != models.BudgetPendingRemote {
		t.Fatal(res)
	}
	s.DB.Exec(`DROP TRIGGER reject_quality_usage`)
	for i := 0; i < 2; i++ {
		if err := s.FinishArticleQualityStage(ctx, "same", receipt); err != nil {
			t.Fatal(err)
		}
	}
	res, _ = s.getReservationByJob(ctx, "same")
	if res.Status != models.BudgetSettled {
		t.Fatal(res)
	}
	var count, total int
	s.DB.QueryRow(`SELECT COUNT(*),SUM(estimated_cost) FROM usage_records WHERE receipt_id='same'`).Scan(&count, &total)
	if count != 1 || total != 9 {
		t.Fatal(count, total)
	}
	if err := s.AdmitArticleQualityStage(ctx, "same", "contract", "writer", e); err == nil {
		t.Fatal("settled replay dispatched")
	}
	s.SetOwnerMonthlyBudget(ctx, nil)
	e.PriceKnown = false
	if err := s.AdmitArticleQualityStage(ctx, "unknown", "contract", "writer", e); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishArticleQualityStage(ctx, "unknown", models.UsageReceipt{Provider: "pod", Model: "writer"}); err != nil {
		t.Fatal(err)
	}
	var nullCost bool
	s.DB.QueryRow(`SELECT estimated_cost IS NULL FROM usage_records WHERE receipt_id='unknown'`).Scan(&nullCost)
	res, _ = s.getReservationByJob(ctx, "unknown")
	if !nullCost || res.Status != models.BudgetPendingRemote || res.ActualCostCents != nil {
		t.Fatal("unknown cost must remain pending/null", res)
	}
	if err := s.AdmitArticleQualityStage(ctx, "unknown", "new-contract", "writer", e); err == nil {
		t.Fatal("lost receipt retried unknown")
	}
}
