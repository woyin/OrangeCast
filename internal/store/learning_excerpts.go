package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"math"
)

// CreateLearningExcerpt accepts only a contiguous window from an existing,
// immutable audio transcript. It never infers segments or downloads audio.
func (s *Store) CreateLearningExcerpt(ctx context.Context, snapshotID string, segmentIDs []string) (*models.LearningExcerpt, error) {
	if snapshotID == "" || len(snapshotID) > 200 || len(segmentIDs) == 0 || len(segmentIDs) > 100 {
		return nil, ErrInvalidEditorialState
	}
	snap, segments, _, err := s.SnapshotContent(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if snap.Kind != models.SnapshotKindAudio || snap.AudioSHA256 == "" {
		return nil, ErrInvalidEditorialState
	}
	start := -1
	for i, seg := range segments {
		if seg.ID == segmentIDs[0] {
			start = i
			break
		}
	}
	if start < 0 || start+len(segmentIDs) > len(segments) {
		return nil, ErrInvalidEditorialState
	}
	seen := map[string]bool{}
	for i, id := range segmentIDs {
		seg := segments[start+i]
		if seen[id] || seg.ID != id || math.IsNaN(seg.Start) || math.IsInf(seg.Start, 0) || math.IsNaN(seg.End) || math.IsInf(seg.End, 0) || seg.Start < 0 || seg.End <= seg.Start {
			return nil, ErrInvalidEditorialState
		}
		seen[id] = true
		if i > 0 && seg.Start < segments[start+i-1].Start {
			return nil, ErrInvalidEditorialState
		}
	}
	excerpt := &models.LearningExcerpt{ID: uuid.NewString(), SourceType: snap.SourceType, SourceID: snap.SourceID, SnapshotID: snapshotID, AudioSHA256: snap.AudioSHA256, SegmentIDs: append([]string(nil), segmentIDs...), StartSeconds: segments[start].Start, EndSeconds: segments[start+len(segmentIDs)-1].End}
	if _, err = s.CheckLearningExcerpt(ctx, excerpt); err != nil {
		return nil, err
	}
	ids, _ := json.Marshal(segmentIDs)
	_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO learning_excerpts(id,source_type,source_id,snapshot_id,audio_sha256,segment_ids_json,start_seconds,end_seconds)VALUES(?,?,?,?,?,?,?,?)`, excerpt.ID, excerpt.SourceType, excerpt.SourceID, excerpt.SnapshotID, excerpt.AudioSHA256, string(ids), excerpt.StartSeconds, excerpt.EndSeconds)
	if err != nil {
		return nil, err
	}
	var id string
	if err = s.DB.QueryRowContext(ctx, `SELECT id FROM learning_excerpts WHERE snapshot_id=? AND start_seconds=? AND end_seconds=?`, snapshotID, excerpt.StartSeconds, excerpt.EndSeconds).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetLearningExcerpt(ctx, id)
}
// GetLearningExcerpt returns the immutable interval and its frozen source identity.
func (s *Store) GetLearningExcerpt(ctx context.Context, id string) (*models.LearningExcerpt, error) {
	e := &models.LearningExcerpt{}
	var ids string
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,snapshot_id,audio_sha256,segment_ids_json,start_seconds,end_seconds,created_at FROM learning_excerpts WHERE id=?`, id).Scan(&e.ID, &e.SourceType, &e.SourceID, &e.SnapshotID, &e.AudioSHA256, &ids, &e.StartSeconds, &e.EndSeconds, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(ids), &e.SegmentIDs); err != nil {
		return nil, err
	}
	return e, nil
}

// CheckLearningExcerpt rechecks current source and exact frozen content/audio.
func (s *Store) CheckLearningExcerpt(ctx context.Context, e *models.LearningExcerpt) (*models.LearningExcerpt, error) {
	if e == nil {
		return nil, ErrInvalidEditorialState
	}
	snap, segs, _, err := s.SnapshotContent(ctx, e.SnapshotID)
	if err != nil {
		return nil, err
	}
	if snap.SourceType != e.SourceType || snap.SourceID != e.SourceID || snap.AudioSHA256 != e.AudioSHA256 || snap.Kind != models.SnapshotKindAudio {
		return nil, ErrConflict
	}
	first := -1
	for i, seg := range segs {
		if len(e.SegmentIDs) > 0 && seg.ID == e.SegmentIDs[0] {
			first = i
			break
		}
	}
	if first < 0 || first+len(e.SegmentIDs) > len(segs) {
		return nil, ErrSnapshotInvalidated
	}
	for i, id := range e.SegmentIDs {
		if segs[first+i].ID != id {
			return nil, ErrSnapshotInvalidated
		}
	}
	if segs[first].Start != e.StartSeconds || segs[first+len(e.SegmentIDs)-1].End != e.EndSeconds {
		return nil, ErrSnapshotInvalidated
	}
	item, err := s.CheckListeningIdentity(ctx, models.ListeningQueueItem{SourceType: e.SourceType, SourceID: e.SourceID, Mode: "original", AudioSHA256: e.AudioSHA256})
	if err != nil {
		return nil, err
	}
	if !item.Available {
		return nil, fmt.Errorf("%w: %s", ErrConflict, item.Reason)
	}
	ea, err := s.GetEvidenceAudio(ctx, e.SourceType, e.SourceID)
	if err != nil {
		return nil, err
	}
	if ea.Status != "ready" || ea.SHA256 != e.AudioSHA256 {
		return nil, ErrConflict
	}
	return e, nil
}
func (s *Store) saveLearningExcerptProgress(ctx context.Context, p *models.ListeningProgress, expected int64, cas bool) (*models.ListeningProgress, error) {
	if p.ExcerptID == "" || len(p.ExcerptID) > 200 || p.PlanID != "" || p.PlanVersion != 0 || p.ItemPosition != 0 || p.HighlightID != "" || math.IsNaN(p.ItemOffsetSeconds) || math.IsInf(p.ItemOffsetSeconds, 0) || math.IsNaN(p.Speed) || math.IsInf(p.Speed, 0) || p.Speed < 0.75 || p.Speed > 2 || expected < 0 {
		return nil, ErrInvalidEditorialState
	}
	e, err := s.GetLearningExcerpt(ctx, p.ExcerptID)
	if err != nil {
		return nil, err
	}
	if e.SourceType != p.SourceType || e.SourceID != p.SourceID || p.AudioSHA256 != e.AudioSHA256 || (p.SnapshotID != "" && p.SnapshotID != e.SnapshotID) {
		return nil, ErrConflict
	}
	if p.ItemOffsetSeconds < e.StartSeconds || p.ItemOffsetSeconds > e.EndSeconds {
		return nil, ErrInvalidEditorialState
	}
	if _, err = s.CheckLearningExcerpt(ctx, e); err != nil {
		return nil, err
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Seq < 1 {
		p.Seq = 1
	}
	var res sql.Result
	if !cas {
		res, err = s.DB.ExecContext(ctx, `INSERT INTO learning_excerpt_progress(id,excerpt_id,item_offset_seconds,speed,seq)VALUES(?,?,?,?,?) ON CONFLICT(excerpt_id)DO UPDATE SET item_offset_seconds=excluded.item_offset_seconds,speed=excluded.speed,seq=excluded.seq,revision=learning_excerpt_progress.revision+1,updated_at=datetime('now') WHERE excluded.seq>learning_excerpt_progress.seq`, p.ID, p.ExcerptID, p.ItemOffsetSeconds, p.Speed, p.Seq)
	} else if expected == 0 {
		res, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO learning_excerpt_progress(id,excerpt_id,item_offset_seconds,speed,seq)VALUES(?,?,?,?,?)`, p.ID, p.ExcerptID, p.ItemOffsetSeconds, p.Speed, p.Seq)
	} else {
		res, err = s.DB.ExecContext(ctx, `UPDATE learning_excerpt_progress SET item_offset_seconds=?,speed=?,seq=?,revision=revision+1,updated_at=datetime('now')WHERE excerpt_id=? AND revision=?`, p.ItemOffsetSeconds, p.Speed, p.Seq, p.ExcerptID, expected)
	}
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if cas && n == 0 {
		return nil, ErrConflict
	}
	return s.GetLearningExcerptProgress(ctx, p.SourceType, p.SourceID, p.ExcerptID)
}
// GetLearningExcerptProgress returns progress scoped to one excerpt and its source.
func (s *Store) GetLearningExcerptProgress(ctx context.Context, sourceType models.SourceType, sourceID, excerptID string) (*models.ListeningProgress, error) {
	p := &models.ListeningProgress{Mode: "excerpt"}
	err := s.DB.QueryRowContext(ctx, `SELECT p.id,e.source_type,e.source_id,e.id,e.audio_sha256,p.item_offset_seconds,p.speed,p.seq,p.revision,p.updated_at FROM learning_excerpt_progress p JOIN learning_excerpts e ON e.id=p.excerpt_id WHERE e.id=? AND e.source_type=? AND e.source_id=?`, excerptID, sourceType, sourceID).Scan(&p.ID, &p.SourceType, &p.SourceID, &p.ExcerptID, &p.AudioSHA256, &p.ItemOffsetSeconds, &p.Speed, &p.Seq, &p.Revision, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e, err := s.GetLearningExcerpt(ctx, excerptID)
	if err != nil {
		return nil, err
	}
	snap, err := s.GetSourceSnapshot(ctx, e.SnapshotID)
	if err != nil {
		return nil, err
	}
	p.SnapshotVersion = snap.ContentVersion
	p.SnapshotID = e.SnapshotID
	p.SegmentIDs = e.SegmentIDs
	p.StartSeconds = e.StartSeconds
	p.EndSeconds = e.EndSeconds
	return p, nil
}
