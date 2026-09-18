// dj_plans.go DJ 播放清单持久化（D02）。
// 清单按 (source, highlight_version) 版本化：新高光版本生成新清单；
// 旧清单行永不修改（正在收听的旧清单不被替换）。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// CreateDJPlan 写入一份新清单（版本 = 该 (source, highlight_version) 下 MAX+1）。
func (s *Store) CreateDJPlan(ctx context.Context, plan *models.DJPlan) (*models.DJPlan, error) {
	if plan == nil || plan.SourceID == "" || plan.HighlightVersion <= 0 || len(plan.Items) == 0 {
		return nil, fmt.Errorf("%w: DJ 清单缺少来源/高光版本/条目", ErrInvalidEditorialState)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var maxVer int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM dj_plans WHERE source_type=? AND source_id=? AND highlight_version=?`,
		string(plan.SourceType), plan.SourceID, plan.HighlightVersion).Scan(&maxVer); err != nil {
		return nil, err
	}
	plan.ID = uuid.NewString()
	plan.Version = maxVer + 1
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO dj_plans (id, source_type, source_id, version, target_seconds, total_seconds, highlight_version, input_snapshot_json)
		 VALUES (?,?,?,?,?,?,?,?)`,
		plan.ID, string(plan.SourceType), plan.SourceID, plan.Version,
		plan.TargetSeconds, plan.TotalSeconds, plan.HighlightVersion, plan.InputSnapshotJSON); err != nil {
		return nil, fmt.Errorf("写入 DJ 清单: %w", err)
	}
	for i := range plan.Items {
		it := &plan.Items[i]
		it.Position = i + 1
		segIDs, _ := json.Marshal(it.SegmentIDs)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dj_plan_items (id, plan_id, position, kind, highlight_id, narration_id, segment_ids_json, start_seconds, end_seconds, est_seconds, reason, script_kind, script_text)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), plan.ID, it.Position, it.Kind, it.HighlightID, it.NarrationID,
			string(segIDs), it.Start, it.End, it.EstSeconds, it.Reason, it.ScriptKind, it.ScriptText); err != nil {
			return nil, fmt.Errorf("写入 DJ 清单项: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDJPlan(ctx, plan.ID)
}

// GetCurrentDJPlan 读取某 (source, highlight_version) 的当前清单；无则 ErrNotFound。
func (s *Store) GetCurrentDJPlan(ctx context.Context, sourceType models.SourceType, sourceID string, highlightVersion int) (*models.DJPlan, error) {
	var id string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM dj_plans WHERE source_type=? AND source_id=? AND highlight_version=? ORDER BY version DESC LIMIT 1`,
		string(sourceType), sourceID, highlightVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetDJPlan(ctx, id)
}

// GetLatestDJPlanForSource 读取该来源最新一份清单（任意高光版本）；无则 ErrNotFound。
func (s *Store) GetLatestDJPlanForSource(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.DJPlan, error) {
	var id string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM dj_plans WHERE source_type=? AND source_id=? ORDER BY created_at DESC, version DESC LIMIT 1`,
		string(sourceType), sourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetDJPlan(ctx, id)
}

// EnqueueDJPlanJob 入队 DJ 清单编排任务（冻结高光版本与目标时长）；
// worker 与页面生成动作共用同一幂等意图，重复请求不重复入队。
func (s *Store) EnqueueDJPlanJob(ctx context.Context, sourceType models.SourceType, sourceID string, highlightVersion int, targetSeconds float64) (*models.ProcessingJob, error) {
	snapshot, err := json.Marshal(map[string]any{
		"highlight_version": highlightVersion,
		"target_seconds":    targetSeconds,
	})
	if err != nil {
		return nil, err
	}
	job, created, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobDJPlan,
		IntentID:          fmt.Sprintf("dj_plan:%s:%s:hv%d:%v", sourceType, sourceID, highlightVersion, targetSeconds),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil
	}
	return job, nil
}

// GetDJPlan 读取清单及全部条目（按 position 排序）。
func (s *Store) GetDJPlan(ctx context.Context, id string) (*models.DJPlan, error) {
	plan := &models.DJPlan{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, version, target_seconds, total_seconds, highlight_version, input_snapshot_json, created_at
		 FROM dj_plans WHERE id=?`, id).
		Scan(&plan.ID, &plan.SourceType, &plan.SourceID, &plan.Version, &plan.TargetSeconds,
			&plan.TotalSeconds, &plan.HighlightVersion, &plan.InputSnapshotJSON, &plan.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT position, kind, highlight_id, narration_id, segment_ids_json, start_seconds, end_seconds, est_seconds, reason, COALESCE(script_kind,''), COALESCE(script_text,'')
		 FROM dj_plan_items WHERE plan_id=? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it := models.DJPlanItem{}
		var segIDs string
		if err := rows.Scan(&it.Position, &it.Kind, &it.HighlightID, &it.NarrationID, &segIDs,
			&it.Start, &it.End, &it.EstSeconds, &it.Reason, &it.ScriptKind, &it.ScriptText); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(segIDs), &it.SegmentIDs)
		plan.Items = append(plan.Items, it)
	}
	return plan, rows.Err()
}
