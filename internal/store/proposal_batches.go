package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func validProposalBatchStatus(status string) bool {
	switch status {
	case "ready", "reviewing", "completed", "superseded", "failed", "stale":
		return true
	}
	return false
}

// CreateProposalBatch creates one idempotent, attention-bounded discovery batch.
func (s *Store) CreateProposalBatch(ctx context.Context, batch models.ProposalBatch) (*models.ProposalBatch, error) {
	batch.EditorialProfileID, batch.IdempotencyKey = strings.TrimSpace(batch.EditorialProfileID), strings.TrimSpace(batch.IdempotencyKey)
	if batch.Status == "" {
		batch.Status = "ready"
	}
	if batch.MaterialSnapshotJSON == "" {
		batch.MaterialSnapshotJSON = "[]"
	}
	if batch.EditorialProfileID == "" || batch.IdempotencyKey == "" || !validProposalBatchStatus(batch.Status) {
		return nil, fmt.Errorf("%w: invalid proposal batch", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, batch.EditorialProfileID); err != nil {
		return nil, err
	}
	batch.ID = uuid.NewString()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO proposal_batches (id,editorial_profile_id,status,window_start_at,material_snapshot_json,idempotency_key,shortage_reason,provider,model,cost_cents,failure_reason) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, batch.ID, batch.EditorialProfileID, batch.Status, optionalWorkspaceString(batch.WindowStartAt), batch.MaterialSnapshotJSON, batch.IdempotencyKey, batch.ShortageReason, batch.Provider, batch.Model, batch.CostCents, batch.FailureReason)
	if err != nil {
		return nil, err
	}
	return s.GetProposalBatchByIdempotencyKey(ctx, batch.IdempotencyKey)
}

// ReserveAutomaticProposalBatch durably claims one exact material snapshot before
// any provider call. A concurrent server observing the same idempotency key
// receives the existing batch with created=false and must not spend again.
func (s *Store) ReserveAutomaticProposalBatch(ctx context.Context, batch models.ProposalBatch) (*models.ProposalBatch, bool, error) {
	batch.EditorialProfileID, batch.IdempotencyKey = strings.TrimSpace(batch.EditorialProfileID), strings.TrimSpace(batch.IdempotencyKey)
	if batch.MaterialSnapshotJSON == "" {
		batch.MaterialSnapshotJSON = "[]"
	}
	if batch.EditorialProfileID == "" || batch.IdempotencyKey == "" {
		return nil, false, fmt.Errorf("%w: invalid automatic proposal batch", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, batch.EditorialProfileID); err != nil {
		return nil, false, err
	}
	batch.ID, batch.Status = uuid.NewString(), "reviewing"
	result, err := s.DB.ExecContext(ctx, `INSERT INTO proposal_batches (id,editorial_profile_id,status,window_start_at,material_snapshot_json,idempotency_key,shortage_reason,provider,model,cost_cents,failure_reason) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, batch.ID, batch.EditorialProfileID, batch.Status, optionalWorkspaceString(batch.WindowStartAt), batch.MaterialSnapshotJSON, batch.IdempotencyKey, batch.ShortageReason, batch.Provider, batch.Model, batch.CostCents, batch.FailureReason)
	if err != nil {
		return nil, false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	persisted, err := s.GetProposalBatchByIdempotencyKey(ctx, batch.IdempotencyKey)
	return persisted, created == 1, err
}

// ListProposalBatches returns a profile's automatic discovery history, newest first.
func (s *Store) ListProposalBatches(ctx context.Context, profileID string) ([]*models.ProposalBatch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,status,COALESCE(window_start_at,''),material_snapshot_json,idempotency_key,shortage_reason,provider,model,cost_cents,failure_reason,created_at,COALESCE(completed_at,'') FROM proposal_batches WHERE editorial_profile_id=? ORDER BY created_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ProposalBatch
	for rows.Next() {
		batch := &models.ProposalBatch{}
		var provider, model, failure sql.NullString
		var cost sql.NullInt64
		if err := rows.Scan(&batch.ID, &batch.EditorialProfileID, &batch.Status, &batch.WindowStartAt, &batch.MaterialSnapshotJSON, &batch.IdempotencyKey, &batch.ShortageReason, &provider, &model, &cost, &failure, &batch.CreatedAt, &batch.CompletedAt); err != nil {
			return nil, err
		}
		if provider.Valid {
			batch.Provider = &provider.String
		}
		if model.Valid {
			batch.Model = &model.String
		}
		if cost.Valid {
			value := cost.Int64
			batch.CostCents = &value
		}
		if failure.Valid {
			batch.FailureReason = &failure.String
		}
		out = append(out, batch)
	}
	return out, rows.Err()
}

// GetProposalBatchByIdempotencyKey retrieves the batch generated for one material snapshot.
func (s *Store) GetProposalBatchByIdempotencyKey(ctx context.Context, key string) (*models.ProposalBatch, error) {
	b := &models.ProposalBatch{}
	var provider, model, failure sql.NullString
	var cost sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,status,COALESCE(window_start_at,''),material_snapshot_json,idempotency_key,shortage_reason,provider,model,cost_cents,failure_reason,created_at,COALESCE(completed_at,'') FROM proposal_batches WHERE idempotency_key=?`, key).Scan(&b.ID, &b.EditorialProfileID, &b.Status, &b.WindowStartAt, &b.MaterialSnapshotJSON, &b.IdempotencyKey, &b.ShortageReason, &provider, &model, &cost, &failure, &b.CreatedAt, &b.CompletedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if provider.Valid {
		b.Provider = &provider.String
	}
	if model.Valid {
		b.Model = &model.String
	}
	if cost.Valid {
		cents := cost.Int64
		b.CostCents = &cents
	}
	if failure.Valid {
		b.FailureReason = &failure.String
	}
	return b, nil
}

// HasOpenProposalBatch implements attention backpressure for automatic discovery.
func (s *Store) HasOpenProposalBatch(ctx context.Context, profileID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM proposal_batches WHERE editorial_profile_id=? AND status IN ('ready','reviewing')`, profileID).Scan(&n)
	return n > 0, err
}

// FinalizeAutomaticProposalBatch atomically exposes a completed provider result
// and its CreationProposals to the Owner attention queue.
func (s *Store) FinalizeAutomaticProposalBatch(ctx context.Context, batchID, providerName, modelName, shortageReason string, cost *int64, proposals []models.CreationProposal) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var profileID, status string
	if err := tx.QueryRowContext(ctx, `SELECT editorial_profile_id,status FROM proposal_batches WHERE id=?`, batchID).Scan(&profileID, &status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "reviewing" {
		return fmt.Errorf("%w: automatic batch is not claimed", ErrInvalidEditorialState)
	}
	for _, proposal := range proposals {
		proposal.ID = uuid.NewString()
		proposal.WorkingTitle = strings.TrimSpace(proposal.WorkingTitle)
		proposal.ProposedClaim = strings.TrimSpace(proposal.ProposedClaim)
		if proposal.WorkingTitle == "" || proposal.ProposedClaim == "" {
			return fmt.Errorf("%w: invalid automatic creation proposal", ErrInvalidEditorialState)
		}
		if proposal.CreationForm == "" {
			proposal.CreationForm = "article"
		}
		if proposal.MaterialIDsJSON == "" {
			proposal.MaterialIDsJSON = "[]"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO creation_proposals (id,editorial_profile_id,proposal_batch_id,ideation_session_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, proposal.ID, profileID, batchID, optionalWorkspaceString(proposal.IdeationSessionID), "proposed", proposal.CreationForm, proposal.WorkingTitle, proposal.ProposedClaim, nil, proposal.Audience, proposal.Rationale, proposal.MaterialIDsJSON, proposal.HistoryRelationship); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE proposal_batches SET status='ready',shortage_reason=?,provider=?,model=?,cost_cents=?,failure_reason=NULL,completed_at=datetime('now') WHERE id=? AND status='reviewing'`, shortageReason, providerName, modelName, cost, batchID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return tx.Commit()
}

// FailAutomaticProposalBatch preserves a visible failure rather than silently
// clearing a paid discovery attempt.
func (s *Store) FailAutomaticProposalBatch(ctx context.Context, batchID, providerName, modelName, reason string, cost *int64) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE proposal_batches SET status='failed',provider=?,model=?,cost_cents=?,failure_reason=?,completed_at=datetime('now') WHERE id=? AND status='reviewing'`, optionalWorkspaceString(providerName), optionalWorkspaceString(modelName), cost, optionalWorkspaceString(reason), batchID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// SetProposalBatchStatus records visible completion, failure, or staleness.
func (s *Store) SetProposalBatchStatus(ctx context.Context, id, status, reason string) error {
	if !validProposalBatchStatus(status) {
		return fmt.Errorf("%w: invalid proposal batch status", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE proposal_batches SET status=?,failure_reason=?,completed_at=CASE WHEN ? IN ('completed','failed','superseded') THEN datetime('now') ELSE completed_at END WHERE id=?`, status, optionalWorkspaceString(reason), status, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
