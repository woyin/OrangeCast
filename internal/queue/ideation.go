// ideation.go 构思诊断持久任务（C03 / ADR-0024 §1）。
// 输入冻结轮次：材料只读轮次快照（不回查当前素材）；诊断经 Validate 校验后
// 作为 MaterialDiagnosis 落库并回写轮次状态；记账走 receipt（B03）。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// ideationQualityChecker 复用分析角色 Provider 的诊断能力。
type ideationDiagnosisProvider interface {
	DiagnoseIdeation(ctx context.Context, req provider.IdeationDiagnosisRequest) (*provider.IdeationDiagnosis, provider.TaskUsage, error)
	Name() string
}

// EnqueueIdeationDiagnosisJob 入队一轮的诊断任务。
func (w *Worker) EnqueueIdeationDiagnosisJob(ctx context.Context, sessionID, roundID string) (*models.ProcessingJob, error) {
	round, err := w.store.GetIdeationRoundByID(ctx, roundID)
	if err != nil {
		return nil, err
	}
	snapshot, _ := json.Marshal(map[string]string{
		"session_id": sessionID, "round_id": round.ID,
	})
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sessionID, JobType: models.JobIdeationDiagnosis,
		IntentID:          fmt.Sprintf("ideation_diag:%s:%s", sessionID, round.ID),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil // 同轮诊断任务进行中
	}
	return job, nil
}

// doIdeationDiagnosisJob 执行一轮诊断：读冻结快照 → 诊断 → 校验 → 落库 + 回写轮次。
func (w *Worker) doIdeationDiagnosisJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	prov, ok := bundle.Analysis.(ideationDiagnosisProvider)
	if !ok {
		return fmt.Errorf("Analysis Provider 不支持构思诊断")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var snapshot struct {
		SessionID string `json:"session_id"`
		RoundID   string `json:"round_id"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.RoundID == "" {
		return fmt.Errorf("任务缺少轮次快照")
	}
	round, err := w.store.GetIdeationRoundByID(ctx, snapshot.RoundID)
	if err != nil {
		return fmt.Errorf("读取轮次: %w", err)
	}
	var materialSnap []struct {
		ID        string `json:"id"`
		Content   string `json:"content"`
		Citations string `json:"citations"`
		Error     string `json:"error,omitempty"`
	}
	_ = json.Unmarshal([]byte(round.MaterialSnapshotJSON), &materialSnap)
	var materials []provider.IdeationMaterial
	var prior []string
	prior = append(prior, round.UserInput)
	for _, m := range materialSnap {
		if m.Error != "" || m.ID == "" {
			continue
		}
		materials = append(materials, provider.IdeationMaterial{ID: m.ID, Content: m.Content})
	}
	req := provider.IdeationDiagnosisRequest{
		Question:    round.UserInput,
		Constraints: round.ConstraintsJSON,
		PriorRounds: prior,
		Materials:   materials,
	}
	w.markRemoteCallStarted(ctx, job) // R02-b：到达远端调用边界
	diag, usage, err := prov.DiagnoseIdeation(ctx, req)
	if err != nil {
		_ = w.store.MarkIdeationRoundDiagnosed(ctx, round.ID, "", true)
		return fmt.Errorf("构思诊断: %w", err)
	}
	if errs := provider.ValidateIdeationDiagnosis(diag, req); len(errs) > 0 {
		_ = w.store.MarkIdeationRoundDiagnosed(ctx, round.ID, "", true)
		return fmt.Errorf("诊断校验失败: %v", errs)
	}
	diagJSON, err := diag.ToJSON()
	if err != nil {
		return err
	}
	md, err := w.store.CreateMaterialDiagnosisForRound(ctx, snapshot.SessionID, round.ID, diagJSON, round.MaterialSnapshotJSON)
	if err != nil {
		return err
	}
	if usage.InputUnits > 0 || usage.OutputUnits > 0 {
		w.recordCallUsage(ctx, job, "ideation_diagnosis", prov.Name(), "", usage)
	}
	return w.store.MarkIdeationRoundDiagnosed(ctx, round.ID, md.ID, false)
}
