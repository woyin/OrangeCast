package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/google/uuid"
)

// EnqueueJob 创建任务并标记 source 为 queued（乐观锁：仅 unprocessed/failed 可入队）。
// 返回新 job；若 source 不可入队（已在处理中）返回 nil + nil（调用方视情况处理）。
func (s *Store) EnqueueJob(ctx context.Context, userID string, sourceType models.SourceType, sourceID string, jobType models.JobType) (*models.ProcessingJob, error) {
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
		fmt.Sprintf(`UPDATE %s SET processing_status = ? WHERE id = ? AND user_id = ? AND processing_status IN ('unprocessed','failed')`, table),
		string(models.StatusQueuedEp), sourceID, userID)
	if err != nil {
		return nil, fmt.Errorf("claim source: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, nil // 已在处理中，不重复入队
	}

	job := &models.ProcessingJob{
		ID: uuid.NewString(), UserID: userID,
		SourceType: sourceType, SourceID: sourceID, JobType: jobType,
		Status: models.StatusQueued,
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO processing_jobs (id, user_id, source_type, source_id, job_type, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.UserID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status))
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
func (s *Store) EnqueueAnalyze(ctx context.Context, userID string, sourceType models.SourceType, sourceID string) (*models.ProcessingJob, error) {
	// 检查是否已有 queued/running 的 analyze job，避免重复
	var existing string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs WHERE user_id = ? AND source_type = ? AND source_id = ? AND job_type = 'analyze' AND status IN ('queued','running') LIMIT 1`,
		userID, string(sourceType), sourceID).Scan(&existing)
	if err == nil {
		return nil, nil // 已存在进行中的分析任务
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	job := &models.ProcessingJob{
		ID: uuid.NewString(), UserID: userID,
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobAnalyze,
		Status: models.StatusQueued,
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO processing_jobs (id, user_id, source_type, source_id, job_type, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.UserID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status))
	if err != nil {
		return nil, fmt.Errorf("插入 analyze job: %w", err)
	}
	return job, nil
}

// IndexSearch 写入/更新全文搜索索引。
func (s *Store) IndexSearch(ctx context.Context, userID string, sourceType models.SourceType, sourceID, title, body string) error {
	// FTS5 无 ON CONFLICT，先删后插
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM search_index WHERE user_id = ? AND source_type = ? AND source_id = ?`,
		userID, string(sourceType), sourceID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO search_index (user_id, source_type, source_id, title, body) VALUES (?, ?, ?, ?, ?)`,
		userID, string(sourceType), sourceID, title, body)
	return err
}

// SearchSource 全文搜索，返回匹配的 (sourceType, sourceID, title, snippet)。
func (s *Store) SearchSource(ctx context.Context, userID, query string) ([]SearchHit, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT source_type, source_id, title, snippet(search_index, 4, '<mark>', '</mark>', '...', 12)
		 FROM search_index WHERE user_id = ? AND search_index MATCH ? ORDER BY rank LIMIT 50`,
		userID, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SourceType, &h.SourceID, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SearchHit 搜索结果条目。
type SearchHit struct {
	SourceType models.SourceType
	SourceID   string
	Title      string
	Snippet    string
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*models.ProcessingJob, error) {
	j := &models.ProcessingJob{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, source_type, source_id, job_type, status, attempt_count, last_error, created_at, updated_at
		 FROM processing_jobs WHERE id = ?`, jobID).
		Scan(&j.ID, &j.UserID, &j.SourceType, &j.SourceID, &j.JobType, &j.Status, &j.AttemptCount, &j.LastError, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

// RecordUsage 记录一次 AI 调用的用量与成本。
func (s *Store) RecordUsage(ctx context.Context, userID, operation, provider, model string, inputUnits, outputUnits int, cost float64) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO usage_records (id, user_id, operation, provider, model, input_units, output_units, estimated_cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), userID, operation, provider, model, inputUnits, outputUnits, cost)
	return err
}
