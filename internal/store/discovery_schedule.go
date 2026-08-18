package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/woyin/orangecast/internal/models"
)

// DiscoveryScheduleDecision explains whether automatic discovery may start now.
type DiscoveryScheduleDecision struct {
	Ready                            bool
	Reason                           string
	MaterialChangeCount, SourceCount int
	WindowStartAt                    string
}

const eligibleDiscoveryChangesSQL = `
	FROM material_changes mc
	JOIN keypoint_index ki ON ki.id=mc.keypoint_id
	WHERE mc.source_type='episode'
	  AND ki.quality_status IN ('ready','owner_confirmed')
	  AND ki.stale_at IS NULL
	  AND NOT EXISTS (
		SELECT 1 FROM editorial_relevance er
		WHERE er.editorial_profile_id=? AND er.keypoint_id=mc.keypoint_id
		  AND (er.assessment='irrelevant' OR er.owner_override='excluded')
	  )`

// EvaluateAutomaticDiscovery enforces ADR-0022's discovery window: only
// reviewed, non-stale Episode material that remains relevant to this profile
// may contribute. It never calls a provider or creates a batch.
func (s *Store) EvaluateAutomaticDiscovery(ctx context.Context, profileID string, now time.Time) (DiscoveryScheduleDecision, error) {
	settings, err := s.GetDiscoverySettings(ctx, profileID)
	if err == ErrNotFound {
		return DiscoveryScheduleDecision{Reason: "automatic discovery not enabled"}, nil
	}
	if err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	if !settings.Enabled {
		return DiscoveryScheduleDecision{Reason: "automatic discovery paused"}, nil
	}
	open, err := s.HasOpenProposalBatch(ctx, profileID)
	if err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	if open {
		return DiscoveryScheduleDecision{Reason: "unprocessed proposal batch exists"}, nil
	}
	var windowStart string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(created_at),'') FROM proposal_batches WHERE editorial_profile_id=?`, profileID).Scan(&windowStart); err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	where := eligibleDiscoveryChangesSQL
	args := []any{profileID}
	if windowStart != "" {
		where += ` AND mc.created_at>?`
		args = append(args, windowStart)
	}
	decision := DiscoveryScheduleDecision{WindowStartAt: windowStart}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT mc.source_id) `+where, args...).Scan(&decision.MaterialChangeCount, &decision.SourceCount); err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	if decision.MaterialChangeCount < 6 {
		decision.Reason = "fewer than six new reviewed material changes"
		return decision, nil
	}
	if decision.SourceCount < 2 {
		decision.Reason = "new material comes from fewer than two episodes"
		return decision, nil
	}
	var latestChange string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(mc.created_at),'') `+where, args...).Scan(&latestChange); err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	if latestChange != "" {
		changedAt, parseErr := time.Parse("2006-01-02 15:04:05", latestChange)
		if parseErr != nil {
			return DiscoveryScheduleDecision{}, fmt.Errorf("parse latest material change: %w", parseErr)
		}
		if now.UTC().Sub(changedAt) < time.Duration(settings.DebounceMinutes)*time.Minute {
			decision.Reason = "discovery debounce active"
			return decision, nil
		}
	}
	var today int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM proposal_batches WHERE editorial_profile_id=? AND date(created_at)=date(?)`, profileID, now.UTC().Format("2006-01-02 15:04:05")).Scan(&today); err != nil {
		return DiscoveryScheduleDecision{}, err
	}
	if today >= settings.DailyLimit {
		decision.Reason = "daily automatic batch limit reached"
		return decision, nil
	}
	decision.Ready = true
	return decision, nil
}

// ListDiscoveryWindowChanges returns discovery-eligible changes after a prior
// proposal batch. The returned rows are the exact seed set that must be stored
// in the batch snapshot before a provider call.
func (s *Store) ListDiscoveryWindowChanges(ctx context.Context, profileID, after string) ([]*models.MaterialChange, error) {
	query := `SELECT mc.id,mc.keypoint_id,mc.source_type,mc.source_id,mc.change_kind,mc.snapshot_hash,mc.created_at ` + eligibleDiscoveryChangesSQL
	args := []any{profileID}
	if after != "" {
		query += ` AND mc.created_at>?`
		args = append(args, after)
	}
	query += ` ORDER BY mc.created_at,mc.id`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []*models.MaterialChange
	for rows.Next() {
		change := &models.MaterialChange{}
		if err := rows.Scan(&change.ID, &change.KeyPointID, &change.SourceType, &change.SourceID, &change.ChangeKind, &change.SnapshotHash, &change.CreatedAt); err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

// SetDiscoveryEnabled preserves the original small configuration API while
// applying ADR-0022's default guardrails.
func (s *Store) SetDiscoveryEnabled(ctx context.Context, profileID string, enabled bool, provider, model string, batchBudget *int64) error {
	return s.SetDiscoverySettings(ctx, models.DiscoverySettings{EditorialProfileID: profileID, Enabled: enabled, Provider: provider, Model: model, DailyLimit: 1, DebounceMinutes: 30, BatchBudgetCents: batchBudget})
}

// SetDiscoverySettings records explicit Owner authorization and every guardrail
// that determines when the service may spend money.
func (s *Store) SetDiscoverySettings(ctx context.Context, settings models.DiscoverySettings) error {
	if _, err := s.GetEditorialProfile(ctx, settings.EditorialProfileID); err != nil {
		return err
	}
	if settings.Enabled && (settings.Provider == "" || settings.Model == "") {
		return fmt.Errorf("%w: provider and model required", ErrInvalidEditorialState)
	}
	if settings.DailyLimit < 1 || settings.DebounceMinutes < 0 || settings.BatchBudgetCents != nil && *settings.BatchBudgetCents < 0 {
		return fmt.Errorf("%w: invalid discovery guardrails", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO discovery_settings (editorial_profile_id,enabled,provider,model,daily_limit,batch_budget_cents,debounce_minutes) VALUES (?,?,?,?,?,?,?) ON CONFLICT(editorial_profile_id) DO UPDATE SET enabled=excluded.enabled,provider=excluded.provider,model=excluded.model,daily_limit=excluded.daily_limit,batch_budget_cents=excluded.batch_budget_cents,debounce_minutes=excluded.debounce_minutes,updated_at=datetime('now')`, settings.EditorialProfileID, boolToInt(settings.Enabled), settings.Provider, settings.Model, settings.DailyLimit, settings.BatchBudgetCents, settings.DebounceMinutes)
	return err
}

// GetDiscoverySettings retrieves one profile's durable automatic-discovery authorization.
func (s *Store) GetDiscoverySettings(ctx context.Context, profileID string) (*models.DiscoverySettings, error) {
	settings := &models.DiscoverySettings{}
	var enabled int
	var budget sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT editorial_profile_id,enabled,provider,model,daily_limit,batch_budget_cents,debounce_minutes,updated_at FROM discovery_settings WHERE editorial_profile_id=?`, profileID).Scan(&settings.EditorialProfileID, &enabled, &settings.Provider, &settings.Model, &settings.DailyLimit, &budget, &settings.DebounceMinutes, &settings.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	settings.Enabled = enabled != 0
	if budget.Valid {
		value := budget.Int64
		settings.BatchBudgetCents = &value
	}
	return settings, nil
}

// ListEnabledDiscoverySettings returns only explicitly authorized profiles for
// the controlled scheduler to inspect.
func (s *Store) ListEnabledDiscoverySettings(ctx context.Context) ([]*models.DiscoverySettings, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT editorial_profile_id,enabled,provider,model,daily_limit,batch_budget_cents,debounce_minutes,updated_at FROM discovery_settings WHERE enabled=1 ORDER BY editorial_profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DiscoverySettings
	for rows.Next() {
		settings := &models.DiscoverySettings{}
		var enabled int
		var budget sql.NullInt64
		if err := rows.Scan(&settings.EditorialProfileID, &enabled, &settings.Provider, &settings.Model, &settings.DailyLimit, &budget, &settings.DebounceMinutes, &settings.UpdatedAt); err != nil {
			return nil, err
		}
		settings.Enabled = enabled != 0
		if budget.Valid {
			value := budget.Int64
			settings.BatchBudgetCents = &value
		}
		out = append(out, settings)
	}
	return out, rows.Err()
}
