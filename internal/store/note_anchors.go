package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/woyin/orangecast/internal/provider"
	"math"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

func (s *Store) prepareNoteAnchor(ctx context.Context, note *models.OwnerNote, citations, references []string) (string, error) {
	a := models.NoteAnchor{}
	if note.AnchorJSON != "" && json.Unmarshal([]byte(note.AnchorJSON), &a) != nil {
		return "", ErrInvalidEditorialState
	}
	if math.IsNaN(a.Position) || math.IsInf(a.Position, 0) || a.Position < 0 {
		return "", ErrInvalidEditorialState
	}
	ids := citations
	if note.Kind == "owner_reflection" {
		ids = references
	}
	if a.NoPosition && (len(ids) > 0 || a.Position != 0 || a.Mode != "" || a.PlanID != "" || a.PlanVersion != 0 || a.AudioSHA256 != "") {
		return "", ErrInvalidEditorialState
	}
	if (a.Mode != "" && a.Mode != "original" && a.Mode != "dj" && a.Mode != "excerpt") || len(a.AudioSHA256) > 128 || len(a.PlanID) > 200 || a.PlanVersion < 0 || (a.Mode != "dj" && (a.PlanID != "" || a.PlanVersion != 0)) || (a.Mode == "dj" && (a.PlanID == "" || a.PlanVersion < 1)) {
		return "", ErrInvalidEditorialState
	}
	if len(ids) == 0 && (note.AnchorJSON == "" || strings.TrimSpace(note.AnchorJSON) == "{}") {
		a.NoPosition = true
	}
	var snap *models.SourceSnapshot
	var audio []provider.Segment
	var docs []models.DocumentSegment
	var err error
	if a.SnapshotID != "" && note.Kind == "owner_reflection" {
		snap, audio, docs, err = s.SnapshotContent(ctx, a.SnapshotID)
		if err != nil {
			return "", err
		}
		if string(snap.SourceType) != note.SourceType || snap.SourceID != note.SourceID || snap.ContentVersion != a.Version {
			return "", ErrInvalidEditorialState
		}
	} else {
		snap, err = s.FreezeSourceSnapshot(ctx, models.SourceType(note.SourceType), note.SourceID)
		if err == nil {
			if a.SnapshotID != "" && (a.SnapshotID != snap.ID || a.Version != snap.ContentVersion) {
				return "", fmt.Errorf("%w: 来源版本已改变，保留草稿并重新选择依据", ErrConflict)
			}
			_, audio, docs, err = s.SnapshotContent(ctx, snap.ID)
		}
	}
	if err != nil {
		if len(ids) > 0 || a.SnapshotID != "" || (!errors.Is(err, ErrInvalidEditorialState) && !errors.Is(err, ErrNotFound)) {
			return "", err
		}
	} else {
		a.SnapshotID, a.Version, a.SegmentIDs = snap.ID, snap.ContentVersion, ids
		if a.AudioSHA256 != "" && (snap.AudioSHA256 == "" || a.AudioSHA256 != snap.AudioSHA256) {
			return "", fmt.Errorf("%w: 笔记原音身份与冻结快照不符", ErrInvalidEditorialState)
		}
		if a.Mode == "dj" {
			plan, e := s.GetDJPlan(ctx, a.PlanID)
			if e != nil {
				return "", e
			}
			var frozen struct {
				ID string `json:"source_snapshot_id"`
			}
			if json.Unmarshal([]byte(plan.InputSnapshotJSON), &frozen) != nil || plan.SourceType != snap.SourceType || plan.SourceID != snap.SourceID || plan.Version != a.PlanVersion || frozen.ID != snap.ID {
				return "", ErrInvalidEditorialState
			}
		}
		if len(ids) > 0 && note.AnchorJSON == "" {
			for _, seg := range audio {
				if seg.ID == ids[0] {
					a.Position = seg.Start
					break
				}
			}
			for _, seg := range docs {
				if seg.ID == ids[0] {
					a.Position = float64(seg.Position)
					break
				}
			}
		}
	}

	raw, err := json.Marshal(a)
	return string(raw), err
}
func (s *Store) validateNoteReferences(ctx context.Context, note *models.OwnerNote, ids []string) (bool, error) {
	var a models.NoteAnchor
	if json.Unmarshal([]byte(note.AnchorJSON), &a) != nil || a.SnapshotID == "" {
		return s.ValidateSourceCitations(ctx, models.SourceType(note.SourceType), note.SourceID, ids)
	}
	snap, audio, docs, err := s.SnapshotContent(ctx, a.SnapshotID)
	if err != nil {
		return false, err
	}
	if snap.SourceID != note.SourceID || string(snap.SourceType) != note.SourceType || snap.ContentVersion != a.Version {
		return false, ErrInvalidEditorialState
	}
	valid := map[string]bool{}
	for _, seg := range audio {
		valid[seg.ID] = true
	}
	for _, seg := range docs {
		valid[seg.ID] = true
	}
	for _, id := range ids {
		if !valid[id] {
			return false, nil
		}
	}
	return true, nil
}

// ListOwnerNoteRevisions returns only the content versions actually preserved.
func (s *Store) ListOwnerNoteRevisions(ctx context.Context, noteID string) ([]*models.OwnerNote, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT note_id,revision,source_type,source_id,kind,content,citations_json,references_json,anchor_json,created_at FROM owner_note_revisions WHERE note_id=? ORDER BY revision DESC`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.OwnerNote
	for rows.Next() {
		n := &models.OwnerNote{}
		if err := rows.Scan(&n.ID, &n.Revision, &n.SourceType, &n.SourceID, &n.Kind, &n.Content, &n.CitationsJSON, &n.ReferencesJSON, &n.AnchorJSON, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
