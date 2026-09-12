// listening_progress.go 听播进度持久化（D07 / ADR-0024 §3）。
// seq 单调：保存时仅接受比已存序号更大的请求（多标签/倒序请求不会用旧状态
// 覆盖新状态）；节流由客户端负责，暂停/离开页面时补存。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// SaveListeningProgress 保存/更新听播进度；seq 不大于已存值时忽略（防旧覆盖新）。
func (s *Store) SaveListeningProgress(ctx context.Context, p *models.ListeningProgress) error {
	if p == nil || p.SourceID == "" || !validSourceType(p.SourceType) {
		return fmt.Errorf("%w: invalid listening progress", ErrInvalidEditorialState)
	}
	if p.Seq <= 0 {
		p.Seq = 1
	}
	var existingSeq int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT seq FROM listening_progress WHERE source_type=? AND source_id=?`,
		string(p.SourceType), p.SourceID).Scan(&existingSeq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO listening_progress (id, source_type, source_id, plan_id, plan_version, item_position, highlight_id, item_offset_seconds, speed, seq)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			p.ID, string(p.SourceType), p.SourceID, p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID,
			p.ItemOffsetSeconds, p.Speed, p.Seq)
		return err
	case err != nil:
		return err
	}
	if p.Seq <= existingSeq {
		return nil // 旧序号：忽略，不覆盖新状态
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE listening_progress SET plan_id=?, plan_version=?, item_position=?, highlight_id=?,
		        item_offset_seconds=?, speed=?, seq=?, updated_at=datetime('now')
		 WHERE source_type=? AND source_id=?`,
		p.PlanID, p.PlanVersion, p.ItemPosition, p.HighlightID, p.ItemOffsetSeconds, p.Speed, p.Seq,
		string(p.SourceType), p.SourceID)
	return err
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
