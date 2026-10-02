package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// AdmitArticleQualityStage reserves the frozen estimate and atomically claims one
// isolated dispatch. Identity is stable across price/config changes; contract
// captures the exact frozen request. A lost receipt never authorizes another call.
func (s *Store) AdmitArticleQualityStage(ctx context.Context, identity, contract, model string, estimate *provider.KnowledgeEstimate) error {
	if identity == "" || contract == "" {
		return ErrInvalidEditorialState
	}
	_, err := s.getReservationByJob(ctx, identity)
	if err == nil {
		return fmt.Errorf("%w: quality stage already has persistent dispatch identity", ErrConflict)
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := s.HoldKnowledgeBudget(ctx, identity, "quality_eval", false, "pod", model, estimate)
	if err != nil {
		return err
	}
	reason := contract
	if res == nil {
		// No monetary budget is configured. This row is a dispatch lock, not a
		// declaration of free usage: actual_cost_cents remains NULL until known.
		reason = "budget_not_configured:" + contract
		_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO budget_reservations(id,job_id,operation,estimated_cost_cents,status,reason) VALUES(?,?, 'quality_eval',0,'held',?)`, uuid.NewString(), identity, reason)
		if err != nil {
			return err
		}
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE budget_reservations SET status='pending_remote',reason=? WHERE job_id=? AND status='held'`, reason, identity)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// FinishArticleQualityStage writes an idempotent remote usage receipt to the
// normal monthly ledger. Unknown costs remain NULL and pending; ledger failures
// retain the dispatch reservation so receipt replay can settle without a call.
func (s *Store) FinishArticleQualityStage(ctx context.Context, identity string, receipt models.UsageReceipt) error {
	res, err := s.getReservationByJob(ctx, identity)
	if err != nil {
		return err
	}
	if res.Operation != "quality_eval" {
		return ErrInvalidEditorialState
	}
	if receipt.InputUnits < 0 || receipt.OutputUnits < 0 || receipt.CostKnown && receipt.CostCents < 0 {
		return ErrInvalidEditorialState
	}
	receipt.ReceiptID = identity
	receipt.AttemptID = identity
	if receipt.Operation == "" {
		receipt.Operation = "quality_eval"
	}
	if err = s.RecordUsageReceipt(ctx, receipt); err != nil {
		return err
	}
	if receipt.CostKnown {
		return s.SettleBudget(ctx, identity, receipt.CostCents)
	}
	return nil
}
