package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/google/uuid"
)

// UpsertTranscript 写入或更新转录结果（同 source 唯一）。
func (s *Store) UpsertTranscript(ctx context.Context, userID string, sourceType models.SourceType, sourceID, language, plainText, segmentsJSON string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO transcripts (id, user_id, source_type, source_id, language, plain_text, segments_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, source_type, source_id) DO UPDATE SET
		   language = excluded.language, plain_text = excluded.plain_text,
		   segments_json = excluded.segments_json`,
		uuid.NewString(), userID, string(sourceType), sourceID, language, plainText, segmentsJSON)
	if err != nil {
		return fmt.Errorf("写入 transcript: %w", err)
	}
	return nil
}

func (s *Store) GetTranscript(ctx context.Context, userID string, sourceType models.SourceType, sourceID string) (*models.Transcript, error) {
	t := &models.Transcript{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, source_type, source_id, language, plain_text, segments_json, created_at
		 FROM transcripts WHERE user_id = ? AND source_type = ? AND source_id = ?`,
		userID, string(sourceType), sourceID).
		Scan(&t.ID, &t.UserID, &t.SourceType, &t.SourceID, &t.Language, &t.PlainText, &t.SegmentsJSON, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// UpsertAnalysis 写入或更新分析结果（KnowledgeCard）。
func (s *Store) UpsertAnalysis(ctx context.Context, userID string, sourceType models.SourceType, sourceID, title, summary, contentJSON string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO analyses (id, user_id, source_type, source_id, title, summary, content_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, source_type, source_id) DO UPDATE SET
		   title = excluded.title, summary = excluded.summary, content_json = excluded.content_json`,
		uuid.NewString(), userID, string(sourceType), sourceID, title, summary, contentJSON)
	if err != nil {
		return fmt.Errorf("写入 analysis: %w", err)
	}
	return nil
}

func (s *Store) GetAnalysis(ctx context.Context, userID string, sourceType models.SourceType, sourceID string) (*models.Analysis, error) {
	a := &models.Analysis{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, source_type, source_id, title, summary, content_json, created_at
		 FROM analyses WHERE user_id = ? AND source_type = ? AND source_id = ?`,
		userID, string(sourceType), sourceID).
		Scan(&a.ID, &a.UserID, &a.SourceType, &a.SourceID, &a.Title, &a.Summary, &a.ContentJSON, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// DeleteSourceAndDependents 删除一个多态 source 及其全部关联数据（孤儿数据级联清理）。
// 在单个 DB 事务内删除 episode/upload 记录及其 transcript/analysis/processing_job。
// 音频临时文件本就不持久化，无需文件系统清理。
// 这是第 1+5 题决定的级联删除策略：删 source 即删其所有衍生数据。
func (s *Store) DeleteSourceAndDependents(ctx context.Context, userID string, sourceType models.SourceType, sourceID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除关联的转录、分析、任务
	for _, stmt := range []string{
		`DELETE FROM transcripts WHERE user_id = ? AND source_type = ? AND source_id = ?`,
		`DELETE FROM analyses WHERE user_id = ? AND source_type = ? AND source_id = ?`,
		`DELETE FROM processing_jobs WHERE user_id = ? AND source_type = ? AND source_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, userID, string(sourceType), sourceID); err != nil {
			return fmt.Errorf("级联删除: %w", err)
		}
	}

	// 删除 source 本体
	switch sourceType {
	case models.SourceEpisode:
		_, err = tx.ExecContext(ctx, `DELETE FROM episodes WHERE id = ? AND user_id = ?`, sourceID, userID)
	case models.SourceUpload:
		_, err = tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND user_id = ?`, sourceID, userID)
	default:
		return fmt.Errorf("未知 source_type: %s", sourceType)
	}
	if err != nil {
		return fmt.Errorf("删除 source 本体: %w", err)
	}
	return tx.Commit()
}

// UpdateEpisodeStatus 更新单集处理状态。
func (s *Store) UpdateEpisodeStatus(ctx context.Context, id string, status models.EpisodeProcessingStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE episodes SET processing_status = ? WHERE id = ?`, string(status), id)
	return err
}

func (s *Store) UpdateUploadStatus(ctx context.Context, id string, status models.EpisodeProcessingStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE uploads SET processing_status = ? WHERE id = ?`, string(status), id)
	return err
}
