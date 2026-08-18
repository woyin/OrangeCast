package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"strings"
)

func validClaimKind(kind models.ClaimMapKind) bool {
	return kind == models.ClaimSource || kind == models.ClaimOwner || kind == models.ClaimSynthesis || kind == models.ClaimVerifiedFact
}

// CreateClaimMap persists the semantic responsibility of a work expression.
func (s *Store) CreateClaimMap(ctx context.Context, v models.ClaimMap) (*models.ClaimMap, error) {
	v.ID = uuid.NewString()
	v.Excerpt = strings.TrimSpace(v.Excerpt)
	if v.KeyPointIDsJSON == "" {
		v.KeyPointIDsJSON = "[]"
	}
	if v.VerifiedFactSourceIDsJSON == "" {
		v.VerifiedFactSourceIDsJSON = "[]"
	}
	if v.WorkRevisionID == "" || v.Excerpt == "" || !validClaimKind(v.Kind) {
		return nil, fmt.Errorf("%w: invalid claim map", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO claim_maps (id,work_revision_id,claim_kind,excerpt,keypoint_ids_json,owner_claim,verified_fact_source_ids_json) VALUES (?,?,?,?,?,?,?)`, v.ID, v.WorkRevisionID, v.Kind, v.Excerpt, v.KeyPointIDsJSON, v.OwnerClaim, v.VerifiedFactSourceIDsJSON)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM claim_maps WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// ListClaimMaps returns claims for one immutable work revision.
func (s *Store) ListClaimMaps(ctx context.Context, revisionID string) ([]*models.ClaimMap, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,work_revision_id,claim_kind,excerpt,keypoint_ids_json,owner_claim,verified_fact_source_ids_json,created_at FROM claim_maps WHERE work_revision_id=? ORDER BY rowid`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.ClaimMap{}
	for rows.Next() {
		v := &models.ClaimMap{}
		if err := rows.Scan(&v.ID, &v.WorkRevisionID, &v.Kind, &v.Excerpt, &v.KeyPointIDsJSON, &v.OwnerClaim, &v.VerifiedFactSourceIDsJSON, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}
	// Compatibility projection: old immutable EvidenceMaps remain readable as
	// semantic ClaimMaps while historical Article revisions are migrated.
	evidenceMaps, err := s.ListEvidenceMaps(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	for _, evidenceMap := range evidenceMaps {
		kind := models.ClaimSource
		switch evidenceMap.Kind {
		case models.EvidenceSynthesized:
			kind = models.ClaimSynthesis
		case models.EvidenceRhetorical:
			continue
		}
		out = append(out, &models.ClaimMap{ID: "compat-evidence-" + evidenceMap.ID, WorkRevisionID: evidenceMap.RevisionID, Kind: kind, Excerpt: evidenceMap.Excerpt, KeyPointIDsJSON: evidenceMap.KeyPointIDs, CreatedAt: evidenceMap.CreatedAt})
	}
	return out, nil
}

// CreateClaimReview writes an immutable review result for exactly one revision.
func (s *Store) CreateClaimReview(ctx context.Context, v models.ClaimReview) (*models.ClaimReview, error) {
	v.ID = uuid.NewString()
	if v.Status != "passed" && v.Status != "failed" && v.Status != "advisory" {
		return nil, fmt.Errorf("%w: invalid claim review", ErrInvalidEditorialState)
	}
	if v.IssuesJSON == "" {
		v.IssuesJSON = "[]"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO claim_reviews (id,work_revision_id,status,issues_json,provider,model,prompt_version,cost_cents) VALUES (?,?,?,?,?,?,?,?)`, v.ID, v.WorkRevisionID, v.Status, v.IssuesJSON, v.Provider, v.Model, v.PromptVersion, v.CostCents)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM claim_reviews WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// LatestClaimReview returns the most recent review, if any.
func (s *Store) LatestClaimReview(ctx context.Context, revisionID string) (*models.ClaimReview, error) {
	v := &models.ClaimReview{}
	var p, m, pv sql.NullString
	var cost sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id,work_revision_id,status,issues_json,provider,model,prompt_version,cost_cents,created_at FROM claim_reviews WHERE work_revision_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID).Scan(&v.ID, &v.WorkRevisionID, &v.Status, &v.IssuesJSON, &p, &m, &pv, &cost, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Valid {
		v.Provider = &p.String
	}
	if m.Valid {
		v.Model = &m.String
	}
	if pv.Valid {
		v.PromptVersion = &pv.String
	}
	if cost.Valid {
		x := cost.Int64
		v.CostCents = &x
	}
	return v, nil
}
