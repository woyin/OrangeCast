// job_intents.go 任务执行契约（B02 / ADR-0024 §5）：
//   - 幂等入队：同 (source, job_type, intent) 的并发入队由部分唯一索引做 CAS，
//     只有一个任务生效；显式"重新生成"使用新意图 ID 即可再次入队。
//   - 输入快照：已知输入在入队时冻结，后续阶段用衔接事务绑定新产物。
//   - 结果先于终态持久化：complete 结果在进程中断后可复用，不重放远端调用；
//     unknown 表示远端结果未知，不冒充完成也不无限自动重试。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// JobIntentSpec 幂等入队规格。
type JobIntentSpec struct {
	SourceType         models.SourceType
	SourceID           string
	JobType            models.JobType
	IntentID           string // 必填；空意图走 EnqueueJob 旧语义
	InputSnapshotJSON  string
	ConfigVersion      string
	ConfiguredProvider string
	ConfiguredModel    string
	Automated          bool
}

// EnqueueJobIdempotent 以意图为单位入队：并发/重复请求命中活跃意图时返回
// 已有任务（created=false）。依赖 0027 的部分唯一索引实现并发 CAS。
func (s *Store) EnqueueJobIdempotent(ctx context.Context, spec JobIntentSpec) (*models.ProcessingJob, bool, error) {
	if spec.IntentID == "" {
		return nil, false, fmt.Errorf("%w: 幂等入队必须提供 intent_id", ErrInvalidEditorialState)
	}
	job := &models.ProcessingJob{
		ID: uuid.NewString(), SourceType: spec.SourceType, SourceID: spec.SourceID,
		JobType: spec.JobType, Status: models.StatusQueued, Automated: spec.Automated,
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO processing_jobs
		   (id, source_type, source_id, job_type, status, is_automated,
		    intent_id, input_snapshot_json, config_version, configured_provider, configured_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, string(job.SourceType), job.SourceID, string(job.JobType), string(job.Status), boolToInt(spec.Automated),
		spec.IntentID, spec.InputSnapshotJSON, spec.ConfigVersion, spec.ConfiguredProvider, spec.ConfiguredModel)
	if err != nil {
		if isUniqueConstraintErr(err) {
			existing, gerr := s.GetActiveJobByIntent(ctx, spec.SourceType, spec.SourceID, spec.JobType, spec.IntentID)
			if gerr != nil {
				return nil, false, gerr
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("幂等入队任务: %w", err)
	}
	return job, true, nil
}

// GetActiveJobByIntent 读取某意图当前 queued/running 的任务；无则 ErrNotFound。
func (s *Store) GetActiveJobByIntent(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType, intentID string) (*models.ProcessingJob, error) {
	var id string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs
		 WHERE source_type=? AND source_id=? AND job_type=? AND intent_id=? AND status IN ('queued','running') LIMIT 1`,
		string(sourceType), sourceID, string(jobType), intentID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// GetJobExecution 读取任务的执行契约数据（旧行返回空默认值，兼容读取）。
func (s *Store) GetJobExecution(ctx context.Context, jobID string) (*models.ProcessingJobExecution, error) {
	e := &models.ProcessingJobExecution{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(intent_id,''), COALESCE(input_snapshot_json,''), COALESCE(config_version,''),
		        COALESCE(configured_provider,''), COALESCE(configured_model,''),
		        COALESCE(checkpoint_json,''), COALESCE(result_json,''), COALESCE(result_state,'')
		 FROM processing_jobs WHERE id = ?`, jobID).
		Scan(&e.IntentID, &e.InputSnapshotJSON, &e.ConfigVersion,
			&e.ConfiguredProvider, &e.ConfiguredModel,
			&e.CheckpointJSON, &e.ResultJSON, &e.ResultState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// SaveJobCheckpoint 保存步骤断点（running 任务），崩溃恢复后从断点继续。
func (s *Store) SaveJobCheckpoint(ctx context.Context, jobID, checkpointJSON string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET checkpoint_json = ?, updated_at = datetime('now') WHERE id = ? AND status IN ('queued','running')`,
		checkpointJSON, jobID)
	return err
}

// SaveJobResult 在任务终态之前持久化结果（崩溃安全）：complete 结果恢复时复用；
// unknown 表示远端可能完成但本地没有可用结果。结果不可覆盖已 complete 的结果。
func (s *Store) SaveJobResult(ctx context.Context, jobID, resultJSON, state string) error {
	if state != models.JobResultComplete && state != models.JobResultUnknown {
		return fmt.Errorf("%w: 非法结果状态 %q", ErrInvalidEditorialState, state)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET result_json = ?, result_state = ?, updated_at = datetime('now')
		 WHERE id = ? AND status IN ('queued','running') AND result_state != ?`,
		resultJSON, state, jobID, models.JobResultComplete)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: 任务 %s 不在接受结果的状态", ErrInvalidEditorialState, jobID)
	}
	return nil
}

// isUniqueConstraintErr 判定是否唯一约束冲突（modernc/sqlite 驱动错误文本）。
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT")
}

// EnqueueIngestionJobWithSnapshot 以输入快照入队订阅自动任务（B05）：
// 快照在入队时冻结处理深度与策略，已排队任务不随后续设置变化漂移。
func (s *Store) EnqueueIngestionJobWithSnapshot(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType, inputSnapshotJSON string) (*models.ProcessingJob, error) {
	return s.enqueueJobWithSnapshot(ctx, sourceType, sourceID, jobType, true, inputSnapshotJSON)
}

// enqueueJobWithSnapshot 是 enqueueJob 的快照扩展；旧 enqueueJob 走空快照保持兼容。
func (s *Store) enqueueJobWithSnapshot(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType, automated bool, inputSnapshotJSON string) (*models.ProcessingJob, error) {
	job, err := s.enqueueJob(ctx, sourceType, sourceID, jobType, automated)
	if err != nil || job == nil {
		return job, err
	}
	if inputSnapshotJSON != "" {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE processing_jobs SET input_snapshot_json = ?, intent_id = ? WHERE id = ? AND intent_id = ''`,
			inputSnapshotJSON, "ingest:"+job.ID, job.ID); err != nil {
			return job, err
		}
	}
	return job, nil
}
