// practice_review.go 练习问题生成任务（K02 学习实践）。
// 输入是入队时冻结的 Transcript 版本；问题必须引用真实 Segment（程序解析时间范围），
// 提示是 AI 生成、非原文（Reference 身份）。全部引用无效时不写库，显式失败可见。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// practiceReviewCheckpoint 断点：generated 阶段保存候选问题，重试免再调用模型。
type practiceReviewCheckpoint struct {
	Stage     string                `json:"stage"` // material | generated
	Generated *provider.PracticeSet `json:"generated,omitempty"`
}

// doPracticeReviewJob 从冻结转录版本生成可自我检验的问题。
func (w *Worker) doPracticeReviewJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.PracticeReview == nil {
		return fmt.Errorf("练习问题 Provider 不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot struct {
		TranscriptVersion int `json:"transcript_version"`
		Limit             int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.TranscriptVersion == 0 {
		return fmt.Errorf("任务缺少冻结的转录版本快照（应经 EnqueuePracticeReviewJob 入队）")
	}
	av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, snapshot.TranscriptVersion)
	if err != nil {
		return fmt.Errorf("读取冻结转录版本 %d: %w", snapshot.TranscriptVersion, err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return fmt.Errorf("解析转录载荷: %w", err)
	}

	cp := practiceReviewCheckpoint{}
	if exec.CheckpointJSON != "" {
		_ = json.Unmarshal([]byte(exec.CheckpointJSON), &cp)
	}
	set := cp.Generated
	model := ""
	if set == nil {
		if err := w.store.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
			return err
		}
		generated, err := bundle.PracticeReview.ReviewQuestions(payload.Segments, snapshot.Limit)
		if err != nil {
			return fmt.Errorf("生成练习问题: %w", err)
		}
		set = generated
		model = set.Model
		if err := w.savePracticeCheckpoint(ctx, job.ID, practiceReviewCheckpoint{Stage: "generated", Generated: set}); err != nil {
			return err
		}
	}

	title, err := w.sourceTitle(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return err
	}
	rows, err := w.store.ReplaceReviewQuestions(ctx, job.SourceType, job.SourceID, title,
		snapshot.TranscriptVersion, set.Items, payload.Segments, bundle.PracticeReview.Name(), model, job.ID)
	if err != nil {
		return fmt.Errorf("保存练习问题: %w", err)
	}
	w.recordCallUsage(ctx, job, "practice_review", bundle.PracticeReview.Name(), model, set.Usage)
	result, _ := json.Marshal(map[string]any{
		"questions": len(rows), "transcript_version": snapshot.TranscriptVersion,
	})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}

func (w *Worker) savePracticeCheckpoint(ctx context.Context, jobID string, cp practiceReviewCheckpoint) error {
	data, _ := json.Marshal(cp)
	return w.store.SaveJobCheckpoint(ctx, jobID, string(data))
}

// sourceTitle 返回来源的展示标题（问题列表按来源分组显示）。
func (w *Worker) sourceTitle(ctx context.Context, sourceType models.SourceType, sourceID string) (string, error) {
	switch sourceType {
	case models.SourceUpload:
		up, err := w.store.GetUploadByID(ctx, sourceID)
		if err != nil {
			return "", err
		}
		return up.OriginalFilename, nil
	default:
		ep, err := w.store.GetEpisodeByID(ctx, sourceID)
		if err != nil {
			return "", err
		}
		return ep.Title, nil
	}
}
