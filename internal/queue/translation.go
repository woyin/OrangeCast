// translation.go 对齐译文任务（Translation，CitedDerivative）。
// 输入是入队时冻结的 Transcript 版本 + 目标语言；译文不改写原语言，也不改时间范围。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// translationSnapshot 翻译任务的冻结输入。
type translationSnapshot struct {
	TranscriptVersion int    `json:"transcript_version"`
	TargetLanguage    string `json:"target_language"`
}

// translationCheckpoint 断点：generated 阶段保存候选译文集。
type translationCheckpoint struct {
	Stage     string                   `json:"stage"`
	Generated *provider.TranslationSet `json:"generated,omitempty"`
}

// doTranslateJob 从冻结转录版本生成对齐译文。
func (w *Worker) doTranslateJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.Translation == nil {
		return fmt.Errorf("翻译 Provider 不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot translationSnapshot
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.TranscriptVersion == 0 || snapshot.TargetLanguage == "" {
		return fmt.Errorf("任务缺少冻结的转录版本或目标语言（应经 EnqueueTranslateJob 入队）")
	}
	av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, snapshot.TranscriptVersion)
	if err != nil {
		return fmt.Errorf("读取冻结转录版本 %d: %w", snapshot.TranscriptVersion, err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return fmt.Errorf("解析转录载荷: %w", err)
	}

	cp := translationCheckpoint{}
	if exec.CheckpointJSON != "" {
		_ = json.Unmarshal([]byte(exec.CheckpointJSON), &cp)
	}
	set := cp.Generated
	model := ""
	if set == nil {
		if err := w.store.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
			return err
		}
		generated, err := bundle.Translation.Translate(payload.Segments, snapshot.TargetLanguage)
		if err != nil {
			return fmt.Errorf("生成译文: %w", err)
		}
		set = generated
		model = set.Model
		data, _ := json.Marshal(translationCheckpoint{Stage: "generated", Generated: set})
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(data)); err != nil {
			return err
		}
	}

	written, err := w.store.ReplaceSegmentTranslations(ctx, job.SourceType, job.SourceID, snapshot.TranscriptVersion,
		snapshot.TargetLanguage, provider.ValidateTranslationSet(set, payload.Segments), payload.Segments,
		bundle.Translation.Name(), model, job.ID)
	if err != nil {
		return fmt.Errorf("保存译文: %w", err)
	}
	w.recordCallUsage(ctx, job, "translate", bundle.Translation.Name(), model, set.Usage)
	result, _ := json.Marshal(map[string]any{
		"segments": written, "transcript_version": snapshot.TranscriptVersion, "target_language": snapshot.TargetLanguage,
	})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}
