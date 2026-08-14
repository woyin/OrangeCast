package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// SetModelPrice records the Owner's current rate for an exact Provider/model pair.
func (s *Store) SetModelPrice(ctx context.Context, price models.ModelPrice) error {
	price.Provider = strings.ToLower(strings.TrimSpace(price.Provider))
	price.Model = strings.TrimSpace(price.Model)
	if price.Provider == "" || price.Model == "" || price.InputCentsPerMillion < 0 || price.OutputCentsPerMillion < 0 {
		return fmt.Errorf("%w: invalid model price", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO model_prices(provider,model,input_cents_per_million,output_cents_per_million)
		VALUES(?,?,?,?) ON CONFLICT(provider,model) DO UPDATE SET input_cents_per_million=excluded.input_cents_per_million,
		output_cents_per_million=excluded.output_cents_per_million,updated_at=datetime('now')`,
		price.Provider, price.Model, price.InputCentsPerMillion, price.OutputCentsPerMillion)
	return err
}

func (s *Store) GetModelPrice(ctx context.Context, providerName, model string) (*models.ModelPrice, error) {
	price := &models.ModelPrice{}
	err := s.DB.QueryRowContext(ctx, `SELECT provider,model,input_cents_per_million,output_cents_per_million,updated_at
		FROM model_prices WHERE provider=? AND model=?`, strings.ToLower(strings.TrimSpace(providerName)), strings.TrimSpace(model)).
		Scan(&price.Provider, &price.Model, &price.InputCentsPerMillion, &price.OutputCentsPerMillion, &price.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return price, err
}

func (s *Store) ListModelPrices(ctx context.Context) ([]models.ModelPrice, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT provider,model,input_cents_per_million,output_cents_per_million,updated_at FROM model_prices ORDER BY provider,model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prices []models.ModelPrice
	for rows.Next() {
		var p models.ModelPrice
		if err := rows.Scan(&p.Provider, &p.Model, &p.InputCentsPerMillion, &p.OutputCentsPerMillion, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prices = append(prices, p)
	}
	return prices, rows.Err()
}

func (s *Store) SetEditorialRoleFallback(ctx context.Context, route models.EditorialRoleFallback) error {
	route.Role = strings.TrimSpace(route.Role)
	route.Provider = strings.ToLower(strings.TrimSpace(route.Provider))
	route.Model = strings.TrimSpace(route.Model)
	if route.Role == "" {
		return fmt.Errorf("%w: role required", ErrInvalidEditorialState)
	}
	if route.Provider == "" || route.Model == "" {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM editorial_role_fallbacks WHERE role=?`, route.Role)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO editorial_role_fallbacks(role,provider,model) VALUES(?,?,?) ON CONFLICT(role) DO UPDATE SET provider=excluded.provider,model=excluded.model,updated_at=datetime('now')`, route.Role, route.Provider, route.Model)
	return err
}

func (s *Store) GetEditorialRoleFallback(ctx context.Context, role string) (*models.EditorialRoleFallback, error) {
	route := &models.EditorialRoleFallback{}
	err := s.DB.QueryRowContext(ctx, `SELECT role,provider,model,updated_at FROM editorial_role_fallbacks WHERE role=?`, role).Scan(&route.Role, &route.Provider, &route.Model, &route.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return route, err
}

// CalculateEditorialCost rounds any non-zero fractional cent up so spend is never understated.
func (s *Store) CalculateEditorialCost(ctx context.Context, providerName, model string, inputUnits, outputUnits int) (int64, error) {
	// Deterministic/fake providers may report no billable units. This is an exact
	// zero-cost call and does not require a price-table entry.
	if inputUnits == 0 && outputUnits == 0 {
		return 0, nil
	}
	price, err := s.GetModelPrice(ctx, providerName, model)
	if err != nil {
		return 0, err
	}
	numerator := int64(inputUnits)*price.InputCentsPerMillion + int64(outputUnits)*price.OutputCentsPerMillion
	if numerator == 0 {
		return 0, nil
	}
	return (numerator + 999999) / 1000000, nil
}

func (s *Store) RecordEditorialUsage(ctx context.Context, record models.EditorialUsageRecord) (*models.EditorialUsageRecord, error) {
	if record.EditorialProfileID == "" || record.TaskKind == "" || record.EntityID == "" || record.Provider == "" || record.Model == "" || record.PromptVersion == "" || record.InputUnits < 0 || record.OutputUnits < 0 || record.CostCents < 0 {
		return nil, fmt.Errorf("%w: incomplete editorial usage record", ErrInvalidEditorialState)
	}
	record.ID = uuid.NewString()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO editorial_usage_records(id,editorial_profile_id,article_draft_id,task_kind,entity_type,entity_id,provider,model,prompt_version,input_units,output_units,cost_cents,retry_count,fallback_from)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.ID, record.EditorialProfileID, record.ArticleDraftID, record.TaskKind, record.EntityType, record.EntityID, record.Provider, record.Model, record.PromptVersion, record.InputUnits, record.OutputUnits, record.CostCents, record.RetryCount, record.FallbackFrom)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// CheckEditorialBudget is a pre-dispatch gate. A configured budget requires a configured exact price.
func (s *Store) CheckEditorialBudget(ctx context.Context, profileID string, draftID *string, providerName, model string) error {
	profile, err := s.GetEditorialProfile(ctx, profileID)
	if err != nil {
		return err
	}
	if profile.MonthlyBudgetCents == nil && profile.PerArticleBudgetCents == nil {
		return nil
	}
	if _, err := s.GetModelPrice(ctx, providerName, model); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: missing price for %s/%s", ErrInvalidEditorialState, providerName, model)
		}
		return err
	}
	var monthly int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_cents),0) FROM editorial_usage_records WHERE editorial_profile_id=? AND created_at>=datetime('now','start of month')`, profileID).Scan(&monthly); err != nil {
		return err
	}
	if profile.MonthlyBudgetCents != nil && monthly >= *profile.MonthlyBudgetCents {
		return fmt.Errorf("%w: monthly editorial budget exhausted", ErrInvalidEditorialState)
	}
	if draftID != nil && profile.PerArticleBudgetCents != nil {
		var article int64
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_cents),0) FROM editorial_usage_records WHERE article_draft_id=?`, *draftID).Scan(&article); err != nil {
			return err
		}
		if article >= *profile.PerArticleBudgetCents {
			return fmt.Errorf("%w: per-article editorial budget exhausted", ErrInvalidEditorialState)
		}
	}
	return nil
}
