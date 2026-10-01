package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func progressMode(p *models.ListeningProgress) string {
	if p.Mode != "" {
		return p.Mode
	}
	if p.PlanID != "" {
		return "dj"
	}
	return "original"
}

// SaveListeningProgress supports older seq-based clients. New clients use CAS.
func (s *Store) SaveListeningProgress(ctx context.Context, p *models.ListeningProgress) error {
	if p == nil || p.SourceID == "" || !validSourceType(p.SourceType) {
		return ErrInvalidEditorialState
	}
	// Legacy requests that omit plan_id still address their most recent row.
	if p.Mode == "" && p.PlanID == "" {
		old, err := s.GetListeningProgress(ctx, p.SourceType, p.SourceID)
		if err == nil {
			p.Mode = old.Mode
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	p.Mode = progressMode(p)
	if p.Mode != "original" && p.Mode != "dj" {
		return ErrInvalidEditorialState
	}
	if err := s.checkProgressAudio(ctx, p); err != nil {
		return err
	}
	if p.Seq <= 0 {
		p.Seq = 1
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO listening_progress
 (id,source_type,source_id,mode,plan_id,plan_version,item_position,highlight_id,item_offset_seconds,speed,seq,audio_sha256)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_type,source_id,mode) DO UPDATE SET
 plan_id=excluded.plan_id,plan_version=excluded.plan_version,item_position=excluded.item_position,
 highlight_id=excluded.highlight_id,item_offset_seconds=excluded.item_offset_seconds,speed=excluded.speed,
 seq=excluded.seq,audio_sha256=excluded.audio_sha256,revision=listening_progress.revision+1,updated_at=datetime('now')
 WHERE excluded.seq>listening_progress.seq AND (listening_progress.audio_sha256='' OR excluded.audio_sha256!='')`, p.ID, p.SourceType, p.SourceID, p.Mode, p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID, p.ItemOffsetSeconds, p.Speed, p.Seq, p.AudioSHA256)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 && p.AudioSHA256 == "" {
		old, e := s.GetListeningProgressMode(ctx, p.SourceType, p.SourceID, p.Mode)
		if e != nil {
			return e
		}
		if old.AudioSHA256 != "" {
			return ErrConflict
		}
	}
	return nil
}

// SaveListeningProgressCAS saves only the expected server revision (zero creates).
// Source and plan identities are authoritative; stale saves never overwrite.
func (s *Store) SaveListeningProgressCAS(ctx context.Context, p *models.ListeningProgress, expected int64) (*models.ListeningProgress, error) {
	if p == nil || p.SourceID == "" || !validSourceType(p.SourceType) || (p.Mode != "original" && p.Mode != "dj") || expected < 0 ||
		math.IsNaN(p.ItemOffsetSeconds) || math.IsInf(p.ItemOffsetSeconds, 0) || p.ItemOffsetSeconds < 0 || p.ItemPosition < 0 ||
		math.IsNaN(p.Speed) || math.IsInf(p.Speed, 0) || p.Speed < 0.75 || p.Speed > 2 {
		return nil, ErrInvalidEditorialState
	}
	exists, err := s.sourceExists(ctx, p.SourceType, p.SourceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	if err := s.checkProgressAudio(ctx, p); err != nil {
		return nil, err
	}
	if p.Mode == "dj" {
		plan, err := s.GetDJPlan(ctx, p.PlanID)
		if err != nil {
			return nil, err
		}
		if plan.SourceType != p.SourceType || plan.SourceID != p.SourceID || plan.Version != p.PlanVersion {
			return nil, ErrConflict
		}
		valid := false
		for _, item := range plan.Items {
			if item.Position == p.ItemPosition && item.Kind == models.DJItemEvidence && item.HighlightID == p.HighlightID && p.ItemOffsetSeconds >= item.Start && p.ItemOffsetSeconds <= item.End {
				valid = true
				break
			}
		}
		if !valid {
			return nil, ErrInvalidEditorialState
		}
	} else if p.PlanID != "" || p.PlanVersion != 0 {
		return nil, ErrInvalidEditorialState
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO listening_progress
 (id,source_type,source_id,mode,plan_id,plan_version,item_position,highlight_id,item_offset_seconds,speed,seq,revision,audio_sha256)
 SELECT ?,?,?,?,?,?,?,?,?,?,?,1,? WHERE ?=0
 ON CONFLICT(source_type,source_id,mode) DO UPDATE SET
 plan_id=excluded.plan_id,plan_version=excluded.plan_version,item_position=excluded.item_position,
 highlight_id=excluded.highlight_id,item_offset_seconds=excluded.item_offset_seconds,speed=excluded.speed,
 seq=excluded.seq,audio_sha256=excluded.audio_sha256,revision=listening_progress.revision+1,updated_at=datetime('now')
 WHERE listening_progress.revision=? AND (listening_progress.audio_sha256='' OR excluded.audio_sha256!='')`, p.ID, p.SourceType, p.SourceID, p.Mode, p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID, p.ItemOffsetSeconds, p.Speed, p.Seq, p.AudioSHA256, expected, expected)
	// INSERT SELECT cannot update an existing row for expected>0. Use a guarded UPDATE.
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if expected > 0 {
		res, err = s.DB.ExecContext(ctx, `UPDATE listening_progress SET plan_id=?,plan_version=?,item_position=?,highlight_id=?,item_offset_seconds=?,speed=?,seq=?,audio_sha256=?,revision=revision+1,updated_at=datetime('now') WHERE source_type=? AND source_id=? AND mode=? AND revision=? AND (audio_sha256='' OR ?!='')`, p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID, p.ItemOffsetSeconds, p.Speed, p.Seq, p.AudioSHA256, p.SourceType, p.SourceID, p.Mode, expected, p.AudioSHA256)
		if err != nil {
			return nil, err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return nil, err
		}
	}
	if n == 0 {
		return nil, ErrConflict
	}
	return s.GetListeningProgressMode(ctx, p.SourceType, p.SourceID, p.Mode)
}

// GetListeningProgress returns the most recently saved mode for legacy clients.
func (s *Store) GetListeningProgress(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ListeningProgress, error) {
	return s.getProgress(ctx, sourceType, sourceID, "")
}

// GetListeningProgressMode reads original or DJ progress independently.
func (s *Store) GetListeningProgressMode(ctx context.Context, sourceType models.SourceType, sourceID, mode string) (*models.ListeningProgress, error) {
	if mode != "original" && mode != "dj" {
		return nil, ErrInvalidEditorialState
	}
	return s.getProgress(ctx, sourceType, sourceID, mode)
}
func (s *Store) getProgress(ctx context.Context, sourceType models.SourceType, sourceID, mode string) (*models.ListeningProgress, error) {
	p := &models.ListeningProgress{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,mode,plan_id,plan_version,item_position,highlight_id,item_offset_seconds,speed,seq,revision,updated_at,audio_sha256 FROM listening_progress WHERE source_type=? AND source_id=? AND (?='' OR mode=?) ORDER BY updated_at DESC,seq DESC LIMIT 1`, sourceType, sourceID, mode, mode).Scan(&p.ID, &p.SourceType, &p.SourceID, &p.Mode, &p.PlanID, &p.PlanVersion, &p.ItemPosition, &p.HighlightID, &p.ItemOffsetSeconds, &p.Speed, &p.Seq, &p.Revision, &p.UpdatedAt, &p.AudioSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read listening progress: %w", err)
	}
	return p, nil
}

// DeleteListeningProgress removes both modes when the source is purged.
func (s *Store) DeleteListeningProgress(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM listening_progress WHERE source_type=? AND source_id=?`, sourceType, sourceID)
	return err
}

func (s *Store) checkProgressAudio(ctx context.Context, p *models.ListeningProgress) error {
	if p.AudioSHA256 == "" {
		return nil
	} // Old rows/clients explicitly remain unfrozen.
	ea, err := s.GetEvidenceAudio(ctx, p.SourceType, p.SourceID)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if ea.Status != "ready" || ea.SHA256 != p.AudioSHA256 {
		return fmt.Errorf("%w: 播放音频已变化或不可用", ErrConflict)
	}
	return nil
}
