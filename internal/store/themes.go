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

// CreateTheme records an Owner-created or Scout-suggested cross-episode topic.
func (s *Store) CreateTheme(ctx context.Context, theme models.Theme) (*models.Theme, error) {
	theme.ID = uuid.NewString()
	theme.Name = strings.TrimSpace(theme.Name)
	theme.Status = defaultString(theme.Status, "suggested")
	if theme.Name == "" || !validThemeStatus(theme.Status) {
		return nil, fmt.Errorf("%w: invalid theme", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, theme.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO themes (id, editorial_profile_id, name, description, status) VALUES (?, ?, ?, ?, ?)`, theme.ID, theme.EditorialProfileID, theme.Name, theme.Description, theme.Status)
	if err != nil {
		return nil, err
	}
	return s.GetTheme(ctx, theme.ID)
}

// GetTheme retrieves one theme by stable ID.
func (s *Store) GetTheme(ctx context.Context, id string) (*models.Theme, error) {
	theme := &models.Theme{}
	err := s.DB.QueryRowContext(ctx, `SELECT id, editorial_profile_id, name, description, status, created_at, updated_at FROM themes WHERE id=?`, id).Scan(&theme.ID, &theme.EditorialProfileID, &theme.Name, &theme.Description, &theme.Status, &theme.CreatedAt, &theme.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return theme, err
}

// ListThemes lists topics for one profile, with active themes first.
func (s *Store) ListThemes(ctx context.Context, profileID string) ([]*models.Theme, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, editorial_profile_id, name, description, status, created_at, updated_at FROM themes WHERE editorial_profile_id=? ORDER BY CASE status WHEN 'confirmed' THEN 0 WHEN 'suggested' THEN 1 ELSE 2 END, updated_at DESC, id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Theme
	for rows.Next() {
		theme := &models.Theme{}
		if err := rows.Scan(&theme.ID, &theme.EditorialProfileID, &theme.Name, &theme.Description, &theme.Status, &theme.CreatedAt, &theme.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, theme)
	}
	return out, rows.Err()
}

// SetThemeStatus records the Owner's decision to confirm or ignore a suggested theme.
func (s *Store) SetThemeStatus(ctx context.Context, themeID, status string) error {
	if !validThemeStatus(status) {
		return fmt.Errorf("%w: invalid theme status", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE themes SET status=?, updated_at=datetime('now') WHERE id=?`, status, themeID)
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

// AddKeyPointToTheme links a KeyPoint when its Source remains active. Themes
// organize material; they do not grant source-level creative authorization.
func (s *Store) AddKeyPointToTheme(ctx context.Context, themeID, keyPointID, relationship string) error {
	if !validThemeRelationship(relationship) {
		return fmt.Errorf("%w: invalid theme relationship", ErrInvalidEditorialState)
	}
	theme, err := s.GetTheme(ctx, themeID)
	if err != nil {
		return err
	}
	keyPoint, err := s.GetKeyPoint(ctx, keyPointID)
	if err != nil {
		return err
	}
	if keyPoint.QualityStatus != models.KeyPointReady && keyPoint.QualityStatus != models.KeyPointOwnerConfirmed || keyPoint.StaleAt != "" {
		return fmt.Errorf("%w: KeyPoint is not discovery-ready", ErrInvalidEditorialState)
	}
	eligible, err := s.IsKeyPointEligibleForProfile(ctx, theme.EditorialProfileID, keyPoint.ID)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf("%w: KeyPoint is excluded for this profile", ErrInvalidEditorialState)
	}
	usable, err := s.CanUseSourceForPublication(ctx, theme.EditorialProfileID, keyPoint.SourceType, keyPoint.SourceID)
	if err != nil {
		return err
	}
	if !usable {
		return fmt.Errorf("%w: KeyPoint source is archived or unavailable", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO theme_keypoints (theme_id, keypoint_id, relationship) VALUES (?, ?, ?) ON CONFLICT(theme_id, keypoint_id) DO UPDATE SET relationship=excluded.relationship`, themeID, keyPointID, relationship)
	return err
}

// ListThemeKeyPoints returns the durable relation records for a theme.
func (s *Store) ListThemeKeyPoints(ctx context.Context, themeID string) ([]*models.ThemeKeyPoint, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT theme_id, keypoint_id, relationship, created_at FROM theme_keypoints WHERE theme_id=? ORDER BY created_at, keypoint_id`, themeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ThemeKeyPoint
	for rows.Next() {
		relation := &models.ThemeKeyPoint{}
		if err := rows.Scan(&relation.ThemeID, &relation.KeyPointID, &relation.Relationship, &relation.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, relation)
	}
	return out, rows.Err()
}

func validThemeStatus(value string) bool {
	return value == "suggested" || value == "confirmed" || value == "ignored"
}
func validThemeRelationship(value string) bool {
	return value == "supports" || value == "complements" || value == "conflicts"
}
