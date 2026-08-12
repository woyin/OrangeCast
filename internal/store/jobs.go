package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// EnqueueJob 创建任务并标记 source 为 queued（乐观锁：仅 unprocessed/failed 可入队）。
// 返回新 job；若 source 不可入队（已在处理中）返回 nil + nil（调用方视情况处理）。
func (s *Store) EnqueueJob(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType) (*models.ProcessingJob, error) {
	return s.enqueueJob(ctx, sourceType, sourceID, jobType, false)
}

// EnqueueIngestionJob creates a task discovered by an enabled podcast ingestion policy.
// The origin is preserved through its analyze continuation so automatic intake cannot
// silently create optional derivative media.
func (s *Store) EnqueueIngestionJob(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType) (*models.ProcessingJob, error) {
	return s.enqueueJob(ctx, sourceType, sourceID, jobType, true)
}

func (s *Store) enqueueJob(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType, automated bool) (*models.ProcessingJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 乐观锁 claim：只有 unprocessed 或 failed 的 source 才能入队，防重复处理。
	table := "episodes"
	if sourceType == models.SourceUpload {
		table = "uploads"
	}
	res, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET processing_status = ? WHERE id = ? AND processing_status IN ('unprocessed','failed')`, table),
		string(models.StatusQueuedEp), sourceID)
	if err != nil {
		return nil, fmt.Errorf("claim source: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, nil // 已在处理中，不重复入队
	}

	job := &models.ProcessingJob{
		ID:         uuid.NewString(),
		SourceType: sourceType, SourceID: sourceID, JobType: jobType,
		Status: models.StatusQueued, Automated: automated,
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, is_automated)
			 VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status), boolToInt(automated))
	if err != nil {
		return nil, fmt.Errorf("插入 job: %w", err)
	}
	return job, tx.Commit()
}

// MarkJobRunning 原子状态转换 queued→running，防止重复处理。返回是否成功 claim。
func (s *Store) MarkJobRunning(ctx context.Context, jobID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET status = 'running', updated_at = datetime('now')
		 WHERE id = ? AND status = 'queued'`, jobID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkJobSucceeded 幂等：仅 running 状态可置 succeeded，防 stale 覆盖。
func (s *Store) MarkJobSucceeded(ctx context.Context, jobID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET status = 'succeeded', updated_at = datetime('now')
		 WHERE id = ? AND status = 'running'`, jobID)
	return err
}

// MarkJobFailed 标记失败并记录错误，递增 attempt_count。
func (s *Store) MarkJobFailed(ctx context.Context, jobID, errMsg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET status = 'failed', last_error = ?, attempt_count = attempt_count + 1, updated_at = datetime('now')
		 WHERE id = ?`, errMsg, jobID)
	return err
}

// EnqueueAnalyze 创建分析任务（转录完成后的后续步骤）。
// 与 EnqueueJob 不同：此处 source 已处于 transcribed 状态，不触发 source claim，
// 仅创建 analyze job（若同 source 已有未完成 analyze 则不重复创建）。
func (s *Store) EnqueueAnalyze(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ProcessingJob, error) {
	return s.enqueueAnalyze(ctx, sourceType, sourceID, false)
}

// EnqueueAnalyzeForIngestion continues a transcript job while preserving its intake origin.
func (s *Store) EnqueueAnalyzeForIngestion(ctx context.Context, sourceType models.SourceType, sourceID string, automated bool) (*models.ProcessingJob, error) {
	return s.enqueueAnalyze(ctx, sourceType, sourceID, automated)
}

func (s *Store) enqueueAnalyze(ctx context.Context, sourceType models.SourceType, sourceID string, automated bool) (*models.ProcessingJob, error) {
	// 检查是否已有 queued/running 的 analyze job，避免重复
	var existing string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs WHERE source_type = ? AND source_id = ? AND job_type = 'analyze' AND status IN ('queued','running') LIMIT 1`,
		string(sourceType), sourceID).Scan(&existing)
	if err == nil {
		return nil, nil // 已存在进行中的分析任务
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	job := &models.ProcessingJob{
		ID:         uuid.NewString(),
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobAnalyze,
		Status: models.StatusQueued, Automated: automated,
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, is_automated)
			 VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status), boolToInt(automated))
	if err != nil {
		return nil, fmt.Errorf("插入 analyze job: %w", err)
	}
	return job, nil
}

// IndexSearch 写入/更新全文搜索索引（分段粒度，Roadmap Phase 5）。
// 每个 Transcript Segment 一行（可定位到时间点）；另写一行 Summary 供标题级检索。
func (s *Store) IndexSearch(ctx context.Context, sourceType models.SourceType, sourceID, title, summary string, segments []provider.Segment) error {
	// FTS5 无 ON CONFLICT，先删后插
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM search_index WHERE source_type = ? AND source_id = ?`,
		string(sourceType), sourceID); err != nil {
		return err
	}
	// Summary 行（segment_id=''）
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO search_index (source_type, source_id, segment_id, start, end, title, body)
		 VALUES (?, ?, '', 0, 0, ?, ?)`,
		string(sourceType), sourceID, title, summary); err != nil {
		return err
	}
	// 每个 Segment 一行
	for _, seg := range segments {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO search_index (source_type, source_id, segment_id, start, end, title, body)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			string(sourceType), sourceID, seg.ID, seg.Start, seg.End, title, seg.Text); err != nil {
			return err
		}
	}
	return nil
}

// SearchSource 全文搜索，返回实际命中的 Transcript Segment（含时间范围，可跳转 EvidenceAudio）。
func (s *Store) SearchSource(ctx context.Context, query string) ([]SearchHit, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT source_type, source_id, segment_id, start, end, title,
		        snippet(search_index, 6, '<mark>', '</mark>', '...', 12)
		 FROM search_index WHERE search_index MATCH ? ORDER BY rank LIMIT 50`,
		query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SourceType, &h.SourceID, &h.SegmentID, &h.Start, &h.End, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SearchHit 搜索结果条目（分段粒度）。
type SearchHit struct {
	SourceType models.SourceType
	SourceID   string
	SegmentID  string
	Start      float64
	End        float64
	Title      string
	Snippet    string
}

// GetJob 按 ID 查询单个处理任务。
func (s *Store) GetJob(ctx context.Context, jobID string) (*models.ProcessingJob, error) {
	j := &models.ProcessingJob{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, job_type, status, attempt_count, last_error, lease_until, heartbeat_at, is_automated, created_at, updated_at
		 FROM processing_jobs WHERE id = ?`, jobID).
		Scan(&j.ID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.LeaseUntil, &j.HeartbeatAt, &j.Automated, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

// RecordUsage 记录一次 AI 调用的用量与成本。
func (s *Store) RecordUsage(ctx context.Context, operation, provider, model string, inputUnits, outputUnits int, cost float64) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO usage_records (id, operation, provider, model, input_units, output_units, estimated_cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), operation, provider, model, inputUnits, outputUnits, cost)
	return err
}

// ---- SQLite 驱动 worker：lease / 心跳 / 启动恢复（ADR-0006）----

// ResetRunningOnStartup 启动时清空所有 running 任务的租约并置回 queued。
// 语义：旧进程已死（新进程刚启动），任何 running 任务都必须回收重跑（至少一次执行）。
// 事务性更新避免与旧进程残留并发写入。
func (s *Store) ResetRunningOnStartup(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET status='queued', lease_until=NULL, heartbeat_at=NULL
		 WHERE status='running'`)
	return err
}

// ClaimNextJob 原子领取下一个可处理任务（queued 或租约过期的 running）。
// 返回 nil + nil 表示当前无任务。领取时设置 running + lease_until。
func (s *Store) ClaimNextJob(ctx context.Context, leaseDuration string) (*models.ProcessingJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 选择：queued 优先；其次 running 且 lease 已过期（stale，可回收）。
	var id string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs
		 WHERE status = 'queued'
		    OR (status = 'running' AND lease_until IS NOT NULL AND lease_until < datetime('now'))
		 ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 原子 claim：防止两个 worker 同时领取同一个 job。
	res, err := tx.ExecContext(ctx,
		`UPDATE processing_jobs SET status='running', lease_until=datetime('now', ?), heartbeat_at=datetime('now'), updated_at=datetime('now')
		 WHERE id = ? AND (status='queued' OR (status='running' AND lease_until IS NOT NULL AND lease_until < datetime('now')))`,
		leaseDuration, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil // 并发下被别人抢先领取
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// HeartbeatJob 刷新任务租约（长任务防止被其他 worker 回收）。
func (s *Store) HeartbeatJob(ctx context.Context, jobID, leaseDuration string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET lease_until=datetime('now', ?), heartbeat_at=datetime('now'), updated_at=datetime('now')
		 WHERE id = ? AND status='running'`,
		leaseDuration, jobID)
	return err
}

// ListQueuedOrRunning 列出全部未完成任务（用于测试与诊断）。
func (s *Store) ListQueuedOrRunning(ctx context.Context) ([]*models.ProcessingJob, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, job_type, status, attempt_count, last_error, lease_until, heartbeat_at, is_automated, created_at, updated_at
		 FROM processing_jobs WHERE status IN ('queued','running') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ProcessingJob
	for rows.Next() {
		j := &models.ProcessingJob{}
		if err := rows.Scan(&j.ID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.LeaseUntil, &j.HeartbeatAt, &j.Automated, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ProcessingProgress 描述全局处理进度（ADR-0015）。
type ProcessingProgress struct {
	Active *models.ProcessingJob   // 正在处理的任务（0 或 1 个，单 worker）
	Queued []*models.ProcessingJob // 排队中的任务
}

// GetProcessingProgress 返回当前处理进度。
func (s *Store) GetProcessingProgress(ctx context.Context) (*ProcessingProgress, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, job_type, status, attempt_count, last_error, lease_until, heartbeat_at, is_automated, created_at, updated_at, COALESCE(current_step,'')
		 FROM processing_jobs WHERE status IN ('running','queued') ORDER BY
		   CASE WHEN status='running' THEN 0 ELSE 1 END, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := &ProcessingProgress{}
	for rows.Next() {
		j := &models.ProcessingJob{}
		var step string
		if err := rows.Scan(&j.ID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.LeaseUntil, &j.HeartbeatAt, &j.Automated, &j.CreatedAt, &j.UpdatedAt, &step); err != nil {
			return nil, err
		}
		if j.Status == models.StatusRunning && p.Active == nil {
			p.Active = j
		} else {
			p.Queued = append(p.Queued, j)
		}
	}
	return p, rows.Err()
}

// SourceTitle 返回 Source 的标题（用于进度展示）。
func (s *Store) SourceTitle(ctx context.Context, sourceType models.SourceType, sourceID string) string {
	if sourceType == models.SourceEpisode {
		if ep, err := s.GetEpisodeByID(ctx, sourceID); err == nil {
			return ep.Title
		}
	} else {
		if up, err := s.GetUploadByID(ctx, sourceID); err == nil {
			return up.OriginalFilename
		}
	}
	return sourceID[:8] + "…"
}

// SourceStatus 返回 Source 的处理状态。
func (s *Store) SourceStatus(ctx context.Context, sourceType models.SourceType, sourceID string) models.EpisodeProcessingStatus {
	if sourceType == models.SourceEpisode {
		if ep, err := s.GetEpisodeByID(ctx, sourceID); err == nil {
			return ep.ProcessingStatus
		}
	} else {
		if up, err := s.GetUploadByID(ctx, sourceID); err == nil {
			return up.ProcessingStatus
		}
	}
	return models.StatusUnprocessed
}

// ListRecentCompleted 返回最近完成的 N 个任务（供进度页展示历史）。
func (s *Store) ListRecentCompleted(ctx context.Context, limit int) ([]*models.ProcessingJob, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, job_type, status, attempt_count, last_error, lease_until, heartbeat_at, is_automated, created_at, updated_at
		 FROM processing_jobs WHERE status IN ('succeeded','failed') ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ProcessingJob
	for rows.Next() {
		j := &models.ProcessingJob{}
		if err := rows.Scan(&j.ID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.LeaseUntil, &j.HeartbeatAt, &j.Automated, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
