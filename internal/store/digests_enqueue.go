package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/woyin/orangecast/internal/models"
)

// EnqueueDigestJob 入队单集精读文生成（ADR-0023 T1）。
// 前提：该 Source 已有当前 Transcript 版本（无转录无素材）；已在处理中返回 nil+nil。
// 与 EnqueueJob 的乐观锁不同：digest 是转录后的衍生任务，source 通常已是 processed 状态，
// 因此按 job_type 判重而不是抢占 processing_status。
func (s *Store) EnqueueDigestJob(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ProcessingJob, error) {
	if sourceType != models.SourceDocument {
		if _, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("该内容尚未完成转录，无法生成精读文")
			}
			return nil, err
		}
	}
	var existing string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs WHERE source_type=? AND source_id=? AND job_type='episode_digest' AND status IN ('queued','running') LIMIT 1`,
		string(sourceType), sourceID).Scan(&existing)
	if err == nil {
		return nil, nil // 已有进行中的精读文任务
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("查询进行中精读文任务: %w", err)
	}
	job := &models.ProcessingJob{
		ID: uuid.NewString(), SourceType: sourceType, SourceID: sourceID,
		JobType: models.JobDigest, Status: models.StatusQueued,
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, is_automated)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		job.ID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status)); err != nil {
		return nil, fmt.Errorf("入队精读文任务: %w", err)
	}
	return job, nil
}
