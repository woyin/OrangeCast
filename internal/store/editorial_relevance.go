package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

const defaultEditorialProfileName = "默认创作画像"

// EnsureDefaultEditorialProfile returns the stable low-configuration profile used
// when an Owner has not created a specialised editorial identity.
func (s *Store) EnsureDefaultEditorialProfile(ctx context.Context) (*models.EditorialProfile, error) {
	profiles, err := s.ListEditorialProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if profile.Name == defaultEditorialProfileName {
			return profile, nil
		}
	}
	return s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: defaultEditorialProfileName, SourceAttribution: "standard"})
}

func validEditorialRelevance(assessment string) bool {
	switch assessment {
	case "relevant", "adjacent", "irrelevant":
		return true
	default:
		return false
	}
}

func validEditorialOverride(override string) bool {
	return override == "" || override == "included" || override == "excluded"
}

// SetEditorialRelevance updates an AI assessment or an Owner override. An Owner
// override is never replaced by an assessment-only write.
func (s *Store) SetEditorialRelevance(ctx context.Context, relevance models.EditorialRelevance) error {
	relevance.Assessment = strings.TrimSpace(relevance.Assessment)
	relevance.OwnerOverride = strings.TrimSpace(relevance.OwnerOverride)
	if !validEditorialRelevance(relevance.Assessment) || !validEditorialOverride(relevance.OwnerOverride) {
		return fmt.Errorf("%w: invalid editorial relevance", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, relevance.EditorialProfileID); err != nil {
		return err
	}
	if _, err := s.GetKeyPoint(ctx, relevance.KeyPointID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO editorial_relevance (editorial_profile_id,keypoint_id,assessment,owner_override,rationale)
		VALUES (?,?,?,?,?)
		ON CONFLICT(editorial_profile_id,keypoint_id) DO UPDATE SET
		assessment=excluded.assessment,
		owner_override=CASE WHEN excluded.owner_override IS NULL OR excluded.owner_override='' THEN editorial_relevance.owner_override ELSE excluded.owner_override END,
		rationale=excluded.rationale, updated_at=datetime('now')`, relevance.EditorialProfileID, relevance.KeyPointID, relevance.Assessment, nullEditorialOverride(relevance.OwnerOverride), relevance.Rationale)
	return err
}

func nullEditorialOverride(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// GetEditorialRelevance gets the durable relationship for one KeyPoint.
func (s *Store) GetEditorialRelevance(ctx context.Context, profileID, keyPointID string) (*models.EditorialRelevance, error) {
	r := &models.EditorialRelevance{}
	var override sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT editorial_profile_id,keypoint_id,assessment,owner_override,rationale,updated_at FROM editorial_relevance WHERE editorial_profile_id=? AND keypoint_id=?`, profileID, keyPointID).Scan(&r.EditorialProfileID, &r.KeyPointID, &r.Assessment, &override, &r.Rationale, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.OwnerOverride = override.String
	return r, nil
}

// IsKeyPointEligibleForProfile applies only relevance and Owner exclusion; it
// does not reintroduce SourceScope as a hidden creative authorization.
func (s *Store) IsKeyPointEligibleForProfile(ctx context.Context, profileID, keyPointID string) (bool, error) {
	r, err := s.GetEditorialRelevance(ctx, profileID, keyPointID)
	if err == ErrNotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return r.OwnerOverride != "excluded" && r.Assessment != "irrelevant", nil
}
