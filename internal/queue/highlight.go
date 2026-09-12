// highlight.go 高光独立任务（B06 / ADR-0024 §5）。
// 高光作为独立可恢复任务运行：输入是入队时冻结的 Transcript 版本（不随 current
// 指针漂移）；生成 → 校验 → 持久化三段以 checkpoint 衔接——已生成未持久化的
// 集合在重试时从断点复用，不重复调用远端模型；失败不回抹已完成的卡片。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// highlightCheckpoint 高光任务断点：generated 阶段保存完整候选集，重试免再生成。
type highlightCheckpoint struct {
	Stage        string                 `json:"stage"` // material | generated | validated
	GeneratedSet *provider.HighlightSet `json:"generated_set,omitempty"`
}

// doHighlightJob 执行高光独立任务：冻结版本素材 → 生成（可断点复用）→ 校验 →
// 不可变版本持久化 → 结果落库（供恢复复用）。
func (w *Worker) doHighlightJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.Highlight == nil {
		return fmt.Errorf("Highlight Provider 不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot struct {
		TranscriptVersion   int    `json:"transcript_version"`
		TranscriptVersionID string `json:"transcript_version_id"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.TranscriptVersion == 0 {
		return fmt.Errorf("任务缺少冻结的转录版本快照（应经 EnqueueHighlightJob 入队）")
	}
	av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, snapshot.TranscriptVersion)
	if err != nil {
		return fmt.Errorf("读取冻结转录版本 %d: %w", snapshot.TranscriptVersion, err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return fmt.Errorf("解析转录载荷: %w", err)
	}

	cp := highlightCheckpoint{Stage: "material"}
	if exec.CheckpointJSON != "" {
		_ = json.Unmarshal([]byte(exec.CheckpointJSON), &cp)
	}

	// 生成阶段：断点已有候选集则复用，不重复调用远端模型。
	raw := cp.GeneratedSet
	if raw == nil {
		generated, err := bundle.Highlight.GenerateHighlights(payload.Segments)
		if err != nil {
			return fmt.Errorf("生成高光: %w", err)
		}
		raw = generated
		if err := w.saveHighlightCheckpoint(ctx, job.ID, highlightCheckpoint{Stage: "generated", GeneratedSet: raw}); err != nil {
			return err
		}
	}

	validated, err := provider.ValidateHighlightSet(raw, payload.Segments)
	if err != nil {
		return fmt.Errorf("高光校验: %w", err)
	}

	// 持久化：不可变版本 + current 指针 + 用量记账 + 结果，全部可从断点恢复重试。
	highlightModel := provider.EffectiveModel(bundle.Highlight.Name(), "", "analyze")
	if tc, err := w.taskConfigFor(job); err == nil {
		highlightModel = provider.EffectiveModel(bundle.Highlight.Name(), tc.Model, "analyze")
	}
	contentJSON, _ := json.Marshal(validated)
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindHighlight, bundle.Highlight.Name(), highlightModel, "1", job.ID, string(contentJSON))
	if err != nil {
		return fmt.Errorf("创建高光版本: %w", err)
	}
	if err := w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight, version); err != nil {
		return fmt.Errorf("设置当前高光版本: %w", err)
	}
	w.recordCallUsage(ctx, job, "highlight", bundle.Highlight.Name(), highlightModel, raw.Usage)
	if err := w.saveHighlightCheckpoint(ctx, job.ID, highlightCheckpoint{Stage: "validated"}); err != nil {
		return err
	}
	result, _ := json.Marshal(map[string]any{"highlight_version": version, "transcript_version": snapshot.TranscriptVersion})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}

func (w *Worker) saveHighlightCheckpoint(ctx context.Context, jobID string, cp highlightCheckpoint) error {
	data, _ := json.Marshal(cp)
	return w.store.SaveJobCheckpoint(ctx, jobID, string(data))
}
