package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// NarrationRow narrations 表的行（GeneratedDerivative 的音频形态，ADR-0019）。
type NarrationRow struct {
	ID              string
	SourceType      models.SourceType
	SourceID        string
	HighlightID     string
	Version         int
	Voice           string
	Model           string
	RelPath         string // 相对 NarrationDir
	DurationSeconds float64
	CharCount       int
	Provider        string
	CreatedAt       string
}

// CreateNarration 写入一条 Narration 版本，返回版本号。
// 版本号 = 该 (source, highlight_id) 下已有最大版本 + 1；并发下由 UNIQUE 兜底。
func (s *Store) CreateNarration(ctx context.Context, sourceType models.SourceType, sourceID, highlightID, voice, model, relpath string, durationSeconds float64, charCount int, providerName string) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var maxVer int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM narrations WHERE source_type=? AND source_id=? AND highlight_id=?`,
		string(sourceType), sourceID, highlightID).Scan(&maxVer); err != nil {
		return 0, err
	}
	version := maxVer + 1
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO narrations (id, source_type, source_id, highlight_id, version, voice, model, relpath, duration_seconds, char_count, provider)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(sourceType), sourceID, highlightID, version, voice, model, relpath, durationSeconds, charCount, providerName); err != nil {
		return 0, fmt.Errorf("写入 narration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

// GetCurrentNarration 取某 Highlight 当前采用的 Narration（MAX(version) per highlight_id，ADR-0019 R4）。
// 无版本返回 ErrNotFound。
func (s *Store) GetCurrentNarration(ctx context.Context, sourceType models.SourceType, sourceID, highlightID string) (*NarrationRow, error) {
	r := &NarrationRow{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, highlight_id, version, voice, model, relpath, duration_seconds, char_count, provider, created_at
		 FROM narrations
		 WHERE source_type=? AND source_id=? AND highlight_id=?
		 ORDER BY version DESC LIMIT 1`,
		string(sourceType), sourceID, highlightID).
		Scan(&r.ID, &r.SourceType, &r.SourceID, &r.HighlightID, &r.Version, &r.Voice, &r.Model, &r.RelPath, &r.DurationSeconds, &r.CharCount, &r.Provider, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListCurrentNarrationsForSource 列出某 Source 下每个 Highlight 的当前 Narration。
// 返回 highlight_id → NarrationRow 映射，供 DJ 页一次性取全部。
func (s *Store) ListCurrentNarrationsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) (map[string]*NarrationRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT n.id, n.source_type, n.source_id, n.highlight_id, n.version, n.voice, n.model, n.relpath, n.duration_seconds, n.char_count, n.provider, n.created_at
		 FROM narrations n
		 INNER JOIN (
		   SELECT highlight_id, MAX(version) AS maxv
		   FROM narrations WHERE source_type=? AND source_id=?
		   GROUP BY highlight_id
		 ) m ON n.highlight_id = m.highlight_id AND n.version = m.maxv
		 WHERE n.source_type=? AND n.source_id=?`,
		string(sourceType), sourceID, string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*NarrationRow{}
	for rows.Next() {
		r := &NarrationRow{}
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.HighlightID, &r.Version, &r.Voice, &r.Model, &r.RelPath, &r.DurationSeconds, &r.CharCount, &r.Provider, &r.CreatedAt); err != nil {
			return nil, err
		}
		out[r.HighlightID] = r
	}
	return out, rows.Err()
}

// DeleteNarrationsForSource Purge 时删除该 Source 的全部 Narration（ADR-0019）。
func (s *Store) DeleteNarrationsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM narrations WHERE source_type=? AND source_id=?`, string(sourceType), sourceID)
	return err
}
