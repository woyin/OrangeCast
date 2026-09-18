// listening_progress.go 听播进度持久化（D07 / ADR-0024 §3 / R11）。
// seq 单调：单语句原子 UPSERT（UNIQUE(source_type,source_id) 冲突目标），
// 仅当新 seq 严格更大时更新——多标签并发、同时首次保存、倒序请求都不会
// 用旧状态覆盖新状态，也不需要应用层先读后写。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// SaveListeningProgress 原子保存/更新听播进度；seq 不大于已存值时忽略。
func (s *Store) SaveListeningProgress(ctx context.Context, p *models.ListeningProgress) error {
	if p == nil || p.SourceID == "" || !validSourceType(p.SourceType) {
		return fmt.Errorf("%w: invalid listening progress", ErrInvalidEditorialState)
	}
	if p.Seq <= 0 {
		p.Seq = 1
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO listening_progress (id, source_type, source_id, plan_id, plan_version, item_position, highlight_id, item_offset_seconds, speed, seq)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(source_type, source_id) DO UPDATE SET
		   plan_id=excluded.plan_id, plan_version=excluded.plan_version,
		   item_position=excluded.item_position, highlight_id=excluded.highlight_id,
		   item_offset_seconds=excluded.item_offset_seconds, speed=excluded.speed,
		   seq=excluded.seq, updated_at=datetime('now')
		 WHERE excluded.seq > listening_progress.seq`,
		p.ID, string(p.SourceType), p.SourceID, p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID,
		p.ItemOffsetSeconds, p.Speed, p.Seq)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // 旧序号：忽略，不覆盖新状态
	}
	return nil
}

// GetListeningProgress 读取听播进度；无记录返回 ErrNotFound。
func (s *Store) GetListeningProgress(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ListeningProgress, error) {
	p := &models.ListeningProgress{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, plan_id, plan_version, item_position, highlight_id,
		        item_offset_seconds, speed, seq, updated_at
		 FROM listening_progress WHERE source_type=? AND source_id=?`,
		string(sourceType), sourceID).
		Scan(&p.ID, &p.SourceType, &p.SourceID, &p.PlanID, &p.PlanVersion, &p.ItemPosition, &p.HighlightID,
			&p.ItemOffsetSeconds, &p.Speed, &p.Seq, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteListeningProgress 删除听播进度（Purge 级联：来源删除后进度无意义）。
func (s *Store) DeleteListeningProgress(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM listening_progress WHERE source_type=? AND source_id=?`,
		string(sourceType), sourceID)
	return err
}
