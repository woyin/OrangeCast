package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func optionalWorkspaceString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// CreateMaterialCandidate persists a learning insight before it becomes a KeyPoint.
func (s *Store) CreateMaterialCandidate(ctx context.Context, candidate models.MaterialCandidate) (*models.MaterialCandidate, error) {
	candidate.ID = uuid.NewString()
	candidate.SourceType, candidate.SourceID = strings.TrimSpace(candidate.SourceType), strings.TrimSpace(candidate.SourceID)
	candidate.OriginKind, candidate.Content = strings.TrimSpace(candidate.OriginKind), strings.TrimSpace(candidate.Content)
	if !validSourceType(models.SourceType(candidate.SourceType)) || candidate.SourceID == "" || candidate.OriginKind == "" || candidate.Content == "" {
		return nil, fmt.Errorf("%w: invalid material candidate", ErrInvalidEditorialState)
	}
	if candidate.CitationsJSON == "" {
		candidate.CitationsJSON = "[]"
	}
	var citationIDs []string
	if err := json.Unmarshal([]byte(candidate.CitationsJSON), &citationIDs); err != nil || len(citationIDs) == 0 {
		return nil, fmt.Errorf("%w: material candidate needs citation segment IDs", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, models.SourceType(candidate.SourceType), candidate.SourceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	valid, err := s.ValidateSourceCitations(ctx, models.SourceType(candidate.SourceType), candidate.SourceID, citationIDs)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("%w: citation does not resolve inside source", ErrInvalidEditorialState)
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	if candidate.Status != "pending" {
		return nil, fmt.Errorf("%w: material candidates must begin pending", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO material_candidates (id,source_type,source_id,origin_kind,origin_id,content,citations_json,status,rejection_reason) VALUES (?,?,?,?,?,?,?,?,?)`, candidate.ID, candidate.SourceType, candidate.SourceID, candidate.OriginKind, optionalWorkspaceString(candidate.OriginID), candidate.Content, candidate.CitationsJSON, candidate.Status, candidate.RejectionReason)
	if err != nil {
		return nil, err
	}
	return s.GetMaterialCandidate(ctx, candidate.ID)
}

// GetMaterialCandidate returns a candidate by stable identifier.
func (s *Store) GetMaterialCandidate(ctx context.Context, id string) (*models.MaterialCandidate, error) {
	c := &models.MaterialCandidate{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,origin_kind,COALESCE(origin_id,''),content,citations_json,status,rejection_reason,created_at,COALESCE(reviewed_at,'') FROM material_candidates WHERE id=?`, id).Scan(&c.ID, &c.SourceType, &c.SourceID, &c.OriginKind, &c.OriginID, &c.Content, &c.CitationsJSON, &c.Status, &c.RejectionReason, &c.CreatedAt, &c.ReviewedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return c, err
}

// ListMaterialCandidates lists candidates for one Source, newest first.
func (s *Store) ListMaterialCandidates(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.MaterialCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,origin_kind,COALESCE(origin_id,''),content,citations_json,status,rejection_reason,created_at,COALESCE(reviewed_at,'') FROM material_candidates WHERE source_type=? AND source_id=? ORDER BY created_at DESC,id DESC`, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.MaterialCandidate{}
	for rows.Next() {
		c := &models.MaterialCandidate{}
		if err := rows.Scan(&c.ID, &c.SourceType, &c.SourceID, &c.OriginKind, &c.OriginID, &c.Content, &c.CitationsJSON, &c.Status, &c.RejectionReason, &c.CreatedAt, &c.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetMaterialCandidateStatus records the quality-gate decision without deleting learning history.
func (s *Store) SetMaterialCandidateStatus(ctx context.Context, id, status, reason string) error {
	if status != "accepted" && status != "rejected" {
		return fmt.Errorf("%w: invalid material candidate decision", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status=?,rejection_reason=?,reviewed_at=datetime('now') WHERE id=?`, status, optionalWorkspaceString(reason), id)
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

// PromoteMaterialCandidate turns an Owner-accepted, cited learning candidate
// into a durable manual KeyPoint and one discovery change. The candidate is
// retained as auditable learning history rather than deleted.
func (s *Store) PromoteMaterialCandidate(ctx context.Context, id string) (*KeyPointRow, error) {
	candidate, err := s.GetMaterialCandidate(ctx, id)
	if err != nil {
		return nil, err
	}
	if candidate.Status != "accepted" {
		return nil, fmt.Errorf("%w: only accepted material candidates can become KeyPoints", ErrInvalidEditorialState)
	}
	claim, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='promoting' WHERE id=? AND status='accepted'`, id)
	if err != nil {
		return nil, err
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		return nil, ErrInvalidEditorialState
	}
	keyPoint, err := s.materialCandidateKeyPoint(ctx, candidate)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='accepted' WHERE id=? AND status='promoting'`, id)
		return nil, err
	}
	created, err := s.CreateManualKeyPoint(ctx, keyPoint)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='accepted' WHERE id=? AND status='promoting'`, id)
		return nil, err
	}
	if _, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: created.ID, SourceType: string(created.SourceType), SourceID: created.SourceID, ChangeKind: "candidate_promoted", SnapshotHash: candidate.ID}); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='promoted',reviewed_at=datetime('now') WHERE id=? AND status='promoting'`, id); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Store) materialCandidateKeyPoint(ctx context.Context, candidate *models.MaterialCandidate) (KeyPointRow, error) {
	var citations []string
	if err := json.Unmarshal([]byte(candidate.CitationsJSON), &citations); err != nil || len(citations) == 0 {
		return KeyPointRow{}, fmt.Errorf("%w: material candidate needs citations", ErrInvalidEditorialState)
	}
	keyPoint := KeyPointRow{SourceType: models.SourceType(candidate.SourceType), SourceID: candidate.SourceID, Content: candidate.Content, CitationsJSON: candidate.CitationsJSON}
	if keyPoint.SourceType == models.SourceDocument {
		document, err := s.GetDocument(ctx, candidate.SourceID)
		if err != nil {
			return KeyPointRow{}, err
		}
		positions := map[string]int{}
		for _, segment := range DocumentSegments(document) {
			positions[segment.ID] = segment.Position
		}
		start, end := 0, 0
		for _, citation := range citations {
			position := positions[citation]
			if position == 0 {
				return KeyPointRow{}, fmt.Errorf("%w: candidate citation does not resolve inside document", ErrInvalidEditorialState)
			}
			if start == 0 || position < start {
				start = position
			}
			if position > end {
				end = position
			}
		}
		keyPoint.SourceTitle, keyPoint.TimeStart, keyPoint.TimeEnd = document.Title, float64(start), float64(end)+.5
		return keyPoint, nil
	}
	version, err := s.GetCurrentVersion(ctx, keyPoint.SourceType, keyPoint.SourceID, KindTranscript)
	if err != nil {
		return KeyPointRow{}, err
	}
	var transcript provider.TranscriptPayload
	if err := json.Unmarshal([]byte(version.Payload), &transcript); err != nil {
		return KeyPointRow{}, err
	}
	segments := map[string]provider.Segment{}
	for _, segment := range transcript.Segments {
		segments[segment.ID] = segment
	}
	start, end := spanFromSegments(citations, segments)
	if end <= start {
		return KeyPointRow{}, fmt.Errorf("%w: candidate citation does not resolve inside transcript", ErrInvalidEditorialState)
	}
	keyPoint.SourceTitle, keyPoint.TimeStart, keyPoint.TimeEnd = candidate.SourceID, start, end
	if keyPoint.SourceType == models.SourceEpisode {
		if episode, err := s.GetEpisodeByID(ctx, keyPoint.SourceID); err == nil {
			keyPoint.SourceTitle = episode.Title
		}
	}
	return keyPoint, nil
}

// RecordMaterialChange writes one idempotent substantive change for discovery.
func (s *Store) RecordMaterialChange(ctx context.Context, change models.MaterialChange) (*models.MaterialChange, error) {
	change.ID = uuid.NewString()
	change.ChangeKind = strings.TrimSpace(change.ChangeKind)
	change.SnapshotHash = strings.TrimSpace(change.SnapshotHash)
	if change.KeyPointID == "" || change.ChangeKind == "" || change.SnapshotHash == "" || !validSourceType(models.SourceType(change.SourceType)) || strings.TrimSpace(change.SourceID) == "" {
		return nil, fmt.Errorf("%w: invalid material change", ErrInvalidEditorialState)
	}
	keyPoint, err := s.GetKeyPoint(ctx, change.KeyPointID)
	if err != nil {
		return nil, err
	}
	if keyPoint.SourceType != models.SourceType(change.SourceType) || keyPoint.SourceID != change.SourceID {
		return nil, fmt.Errorf("%w: material change source does not match KeyPoint", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO material_changes (id,keypoint_id,source_type,source_id,change_kind,snapshot_hash) VALUES (?,?,?,?,?,?) ON CONFLICT(keypoint_id,change_kind,snapshot_hash) DO NOTHING`, change.ID, change.KeyPointID, change.SourceType, change.SourceID, change.ChangeKind, change.SnapshotHash)
	if err != nil {
		return nil, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id,keypoint_id,source_type,source_id,change_kind,snapshot_hash,created_at FROM material_changes WHERE keypoint_id=? AND change_kind=? AND snapshot_hash=?`, change.KeyPointID, change.ChangeKind, change.SnapshotHash)
	if err := row.Scan(&change.ID, &change.KeyPointID, &change.SourceType, &change.SourceID, &change.ChangeKind, &change.SnapshotHash, &change.CreatedAt); err != nil {
		return nil, err
	}
	return &change, nil
}
