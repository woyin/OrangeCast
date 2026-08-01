package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// UpsertEvidenceAudio 记录/更新一个 Source 的 EvidenceAudio（ADR-0005）。
// 同一 Source 重复处理时覆盖 rel_path/sha256（幂等写入，不产生重复产物）。
func (s *Store) UpsertEvidenceAudio(ctx context.Context, sourceType models.SourceType, sourceID, relPath, format string, sizeBytes int64, sha256 string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO evidence_audio (id, source_type, source_id, rel_path, format, size_bytes, sha256, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'ready')
		 ON CONFLICT(source_type, source_id) DO UPDATE SET
		   rel_path = excluded.rel_path, format = excluded.format,
		   size_bytes = excluded.size_bytes, sha256 = excluded.sha256,
		   status = 'ready', updated_at = datetime('now')`,
		uuid.NewString(), string(sourceType), sourceID, relPath, format, sizeBytes, sha256)
	if err != nil {
		return fmt.Errorf("写入 evidence_audio: %w", err)
	}
	return nil
}

func (s *Store) GetEvidenceAudio(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.EvidenceAudio, error) {
	e := &models.EvidenceAudio{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, rel_path, format, size_bytes, sha256, status, created_at, updated_at
		 FROM evidence_audio WHERE source_type = ? AND source_id = ?`,
		string(sourceType), sourceID).
		Scan(&e.ID, &e.SourceType, &e.SourceID, &e.RelPath, &e.Format, &e.SizeBytes, &e.SHA256, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// MarkEvidenceMissing 记录 EvidenceAudio 文件缺失（恢复校验用）。
func (s *Store) MarkEvidenceMissing(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE evidence_audio SET status='missing', updated_at=datetime('now') WHERE source_type=? AND source_id=?`,
		string(sourceType), sourceID)
	return err
}

// ---- 可恢复 Purge（ADR-0012）----

// CreatePurgeIntent 记录一次 Purge 意图（两阶段删除的第一步）。
// 唯一约束 (source_type, source_id)：重复发起不产生重复记录。
func (s *Store) CreatePurgeIntent(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO purges (id, source_type, source_id, status) VALUES (?, ?, ?, 'pending')`,
		uuid.NewString(), string(sourceType), sourceID)
	if err != nil {
		return fmt.Errorf("记录 purge 意图: %w", err)
	}
	return nil
}

// ListPendingPurges 返回待完成的 purge 意图（重启后 Resume 用）。
func (s *Store) ListPendingPurges(ctx context.Context) ([]*models.Purge, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, status, created_at FROM purges WHERE status='pending' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Purge
	for rows.Next() {
		p := &models.Purge{}
		if err := rows.Scan(&p.ID, &p.SourceType, &p.SourceID, &p.Status, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPurgeDone 标记 purge 完成（DB 与文件都已删除）。
func (s *Store) MarkPurgeDone(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE purges SET status='done' WHERE id=?`, id)
	return err
}

// DeleteSourceRows 在单个事务内删除一个 Source 的全部 DB 行（两阶段 purge 的第二阶段）。
// 包括：source 本体、transcript、analysis、processing_jobs、evidence_audio、search_index。
func (s *Store) DeleteSourceRows(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		// artifact_versions.job_id 引用 processing_jobs，必须先删版本再删任务。
		`DELETE FROM artifact_versions WHERE source_type = ? AND source_id = ?`,
		`DELETE FROM transcripts WHERE source_type = ? AND source_id = ?`,
		`DELETE FROM analyses WHERE source_type = ? AND source_id = ?`,
		`DELETE FROM processing_jobs WHERE source_type = ? AND source_id = ?`,
		`DELETE FROM evidence_audio WHERE source_type = ? AND source_id = ?`,
		`DELETE FROM search_index WHERE source_type = ? AND source_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, string(sourceType), sourceID); err != nil {
			return fmt.Errorf("purge 级联删除: %w", err)
		}
	}
	switch sourceType {
	case models.SourceEpisode:
		_, err = tx.ExecContext(ctx, `DELETE FROM episodes WHERE id = ?`, sourceID)
	case models.SourceUpload:
		_, err = tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, sourceID)
	default:
		return fmt.Errorf("未知 source_type: %s", sourceType)
	}
	if err != nil {
		return fmt.Errorf("purge 删除 source 本体: %w", err)
	}
	return tx.Commit()
}

// DeleteSourceAndDependents 保留兼容：等同 DeleteSourceRows（两阶段 purge 内部复用）。
func (s *Store) DeleteSourceAndDependents(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	return s.DeleteSourceRows(ctx, sourceType, sourceID)
}
