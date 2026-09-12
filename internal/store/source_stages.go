// source_stages.go 单集分阶段状态（B09 / ADR-0024 §5）。
// 一个 processed 状态不再代表所有增强完成：知识、高光、解说各自给出阶段状态、
// 可见原因与可采取动作（重试带回失败任务的冻结输入）。
// 状态推导是纯读取：GET 与轮询不入队、不调用模型。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// SourceStage 单集的一个处理阶段状态。
type SourceStage struct {
	Stage      string `json:"stage"` // knowledge | highlight | narration
	StageLabel string `json:"stage_label"`
	Status     string `json:"status"` // ready | pending | running | failed | partial | waiting
	Detail     string `json:"detail"` // 人读原因与可采取动作
	LastJobID  string `json:"last_job_id,omitempty"`
}

// SourceStageStatuses 推导单集三阶段状态（纯只读，无副作用）。
func (s *Store) SourceStageStatuses(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*SourceStage, error) {
	stages := make([]*SourceStage, 0, 3)

	// 知识阶段：当前卡片版本存在即就绪。
	card, cardErr := s.GetCurrentVersion(ctx, sourceType, sourceID, KindKnowledgeCard)
	switch {
	case cardErr == nil:
		stages = append(stages, &SourceStage{Stage: "knowledge", StageLabel: "知识", Status: "ready", Detail: fmt.Sprintf("知识就绪（卡片 v%d）", card.Version)})
	case errors.Is(cardErr, ErrNotFound):
		stages = append(stages, &SourceStage{Stage: "knowledge", StageLabel: "知识", Status: "pending", Detail: "尚未生成知识卡片"})
	default:
		return nil, cardErr
	}
	knowledgeReady := cardErr == nil

	// 高光阶段。
	highlightStage, err := s.highlightStage(ctx, sourceType, sourceID, knowledgeReady)
	if err != nil {
		return nil, err
	}
	stages = append(stages, highlightStage)

	// 解说阶段：以当前高光版本的每段 Gist 是否都有当前解说判定。
	narrationStage, err := s.narrationStage(ctx, sourceType, sourceID, knowledgeReady)
	if err != nil {
		return nil, err
	}
	stages = append(stages, narrationStage)
	return stages, nil
}

func (s *Store) highlightStage(ctx context.Context, sourceType models.SourceType, sourceID string, knowledgeReady bool) (*SourceStage, error) {
	stage := &SourceStage{Stage: "highlight", StageLabel: "DJ 高光"}
	if _, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindHighlight); err == nil {
		stage.Status, stage.Detail = "ready", "高光可用，可进入 DJ 精听"
		return stage, nil
	}
	job, status, err := s.lastStageJob(ctx, sourceType, sourceID, models.JobHighlight)
	if err != nil {
		return nil, err
	}
	stage.LastJobID = job
	switch {
	case status == "queued" || status == "running":
		stage.Status, stage.Detail = "running", "高光生成中"
	case status == "failed":
		stage.Status, stage.Detail = "failed", "高光生成失败："+stageReason(jobStageError(ctx, s, job))+"，可从失败阶段重试"
	case !knowledgeReady:
		stage.Status, stage.Detail = "waiting", "等待知识阶段完成"
	default:
		stage.Status, stage.Detail = "pending", "尚未生成高光，可发起生成"
	}
	return stage, nil
}

func (s *Store) narrationStage(ctx context.Context, sourceType models.SourceType, sourceID string, knowledgeReady bool) (*SourceStage, error) {
	stage := &SourceStage{Stage: "narration", StageLabel: "AI 解说"}
	hv, hvErr := s.GetCurrentVersion(ctx, sourceType, sourceID, KindHighlight)
	switch {
	case hvErr == nil:
		var hs provider.HighlightSet
		if err := json.Unmarshal([]byte(hv.Payload), &hs); err != nil {
			return nil, err
		}
		existing, err := s.ListCurrentNarrationsForSource(ctx, sourceType, sourceID)
		if err != nil {
			return nil, err
		}
		need, have := 0, 0
		for _, h := range hs.Highlights {
			if h.ID == "" || h.Gist == "" {
				continue
			}
			need++
			if _, ok := existing[h.ID]; ok {
				have++
			}
		}
		if need > 0 && have == need {
			stage.Status, stage.Detail = "ready", "解说可用"
			return stage, nil
		}
		if have > 0 {
			stage.Status = "partial"
		}
	case !errors.Is(hvErr, ErrNotFound):
		return nil, hvErr
	}
	job, status, err := s.lastStageJob(ctx, sourceType, sourceID, models.JobNarration)
	if err != nil {
		return nil, err
	}
	stage.LastJobID = job
	switch {
	case status == "queued" || status == "running":
		stage.Status, stage.Detail = "running", "解说合成中"
	case status == "failed":
		stage.Status, stage.Detail = "failed", "解说合成失败："+stageReason(jobStageError(ctx, s, job))+"，可只重试解说"
	case stage.Status == "partial":
		stage.Detail = fmt.Sprintf("解说部分缺失，可重试补齐")
	case stage.Status == "":
		hvExists := hvErr == nil
		if !knowledgeReady {
			stage.Status, stage.Detail = "waiting", "等待知识阶段完成"
		} else if !hvExists {
			stage.Status, stage.Detail = "waiting", "等待高光阶段完成"
		} else {
			stage.Status, stage.Detail = "pending", "尚未合成解说"
		}
	}
	return stage, nil
}

// lastStageJob 返回某阶段最近一次任务（任意终态/进行态）的 ID 与状态。
func (s *Store) lastStageJob(ctx context.Context, sourceType models.SourceType, sourceID string, jobType models.JobType) (string, string, error) {
	var id, status string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, status FROM processing_jobs WHERE source_type=? AND source_id=? AND job_type=? ORDER BY created_at DESC, updated_at DESC LIMIT 1`,
		string(sourceType), sourceID, string(jobType)).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return id, status, nil
}

func jobStageError(ctx context.Context, s *Store, jobID string) string {
	if jobID == "" {
		return ""
	}
	exec, err := s.GetJobExecution(ctx, jobID)
	if err != nil {
		return ""
	}
	var lastErr string
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(last_error,'') FROM processing_jobs WHERE id=?`, jobID).Scan(&lastErr)
	if exec.ResultState == models.JobResultUnknown {
		return "结果未知（远端可能已执行）"
	}
	return lastErr
}

// stageReason 把任务错误归类为人读原因（预算 / 引擎不可用 / 其他）。
func stageReason(lastErr string) string {
	switch {
	case strings.Contains(lastErr, "预算"):
		return "预算不足或缺少价格配置"
	case strings.Contains(lastErr, "解说引擎不可用"):
		return "解说引擎不可用"
	default:
		return lastErr
	}
}

// stageJobTypes 阶段名 → 任务类型。
var stageJobTypes = map[string]models.JobType{
	"knowledge": models.JobAnalyze,
	"highlight": models.JobHighlight,
	"narration": models.JobNarration,
}

// RetryStageJob 从失败阶段重试并带回冻结输入（B09）：
// 复用最近一次失败任务的 input_snapshot_json 与 intent——双击重试命中同一活跃
// 意图时幂等返回既有任务（created=false）；无失败历史时按各阶段默认入队。
func (s *Store) RetryStageJob(ctx context.Context, sourceType models.SourceType, sourceID, stage string) (*models.ProcessingJob, bool, error) {
	jobType, ok := stageJobTypes[stage]
	if !ok {
		return nil, false, fmt.Errorf("%w: 未知阶段 %q", ErrInvalidEditorialState, stage)
	}
	var id, snapshot, intent string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, COALESCE(input_snapshot_json,''), COALESCE(intent_id,'') FROM processing_jobs
		 WHERE source_type=? AND source_id=? AND job_type=? AND status='failed'
		 ORDER BY updated_at DESC LIMIT 1`,
		string(sourceType), sourceID, string(jobType)).Scan(&id, &snapshot, &intent)
	if err == nil && snapshot != "" {
		if intent == "" {
			intent = fmt.Sprintf("retry:%s:%s:%s", stage, sourceID, id)
		}
		return s.EnqueueJobIdempotent(ctx, JobIntentSpec{
			SourceType: sourceType, SourceID: sourceID, JobType: jobType,
			IntentID: intent, InputSnapshotJSON: snapshot,
		})
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	// 无失败历史：按阶段默认入队。
	switch stage {
	case "highlight":
		job, err := s.EnqueueHighlightJob(ctx, sourceType, sourceID)
		return job, job != nil, err
	case "narration":
		hv, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindHighlight)
		if err != nil {
			return nil, false, fmt.Errorf("%w: 尚未生成高光", ErrInvalidEditorialState)
		}
		snap, err := json.Marshal(map[string]any{"highlight_version": hv.Version, "highlight_version_id": hv.ID})
		if err != nil {
			return nil, false, err
		}
		return s.EnqueueJobIdempotent(ctx, JobIntentSpec{
			SourceType: sourceType, SourceID: sourceID, JobType: models.JobNarration,
			IntentID:          fmt.Sprintf("narration:%s:%s:hv%d", sourceType, sourceID, hv.Version),
			InputSnapshotJSON: string(snap),
		})
	case "knowledge":
		job, err := s.EnqueueAnalyze(ctx, sourceType, sourceID)
		if err != nil {
			return nil, false, err
		}
		return job, job != nil, nil
	}
	return nil, false, fmt.Errorf("%w: 未知阶段 %q", ErrInvalidEditorialState, stage)
}
