// keypoint_quality.go 重点质量判定任务（K02 / ADR-0024 §5）。
// 输入冻结卡片版本与转录版本；程序检查先行，独立判定只参考被引用 Segment 原文；
// 结果逐条幂等持久化（store），K03 再消费结果更新正式质量状态。
// 判定模型不可用时任务显式失败——不把"没有判定"伪装成"通过"。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// EnqueueKeypointQualityJob 入队重点质量判定（K02）：冻结卡片版本与转录版本。
func (w *Worker) EnqueueKeypointQualityJob(ctx context.Context, sourceType models.SourceType, sourceID string, cardVersion int) (*models.ProcessingJob, error) {
	av, err := w.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript)
	if err != nil {
		return nil, fmt.Errorf("尚未完成转录，无法判定重点质量: %w", err)
	}
	snapshot, err := json.Marshal(map[string]any{"card_version": cardVersion, "transcript_version": av.Version})
	if err != nil {
		return nil, err
	}
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobKeypointQuality,
		IntentID:          fmt.Sprintf("kpq:%s:%s:cv%d", sourceType, sourceID, cardVersion),
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

// keypointQualityChecker 独立判定能力（Groq/OpenAI 均实现）。
type keypointQualityChecker interface {
	AssessKeypointQuality(ctx context.Context, input provider.KeypointQualityInput) (*provider.KeypointQualityVerdict, provider.TaskUsage, error)
	Name() string
}

// doKeypointQualityJob 执行判定：程序预检 → 独立判定 → 幂等落库 + 记账。
func (w *Worker) doKeypointQualityJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	checker, ok := bundle.Analysis.(keypointQualityChecker)
	if !ok {
		return fmt.Errorf("Analysis Provider 不支持重点质量判定")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot struct {
		CardVersion       int `json:"card_version"`
		TranscriptVersion int `json:"transcript_version"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.CardVersion == 0 {
		return fmt.Errorf("任务缺少冻结的卡片版本快照（应经 EnqueueKeypointQualityJob 入队）")
	}
	kps, err := w.store.ListKeyPointRowsByCardVersion(ctx, job.SourceType, job.SourceID, snapshot.CardVersion)
	if err != nil {
		return fmt.Errorf("读取重点: %w", err)
	}
	av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, snapshot.TranscriptVersion)
	if err != nil {
		return fmt.Errorf("读取冻结转录版本 %d: %w", snapshot.TranscriptVersion, err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return fmt.Errorf("解析转录载荷: %w", err)
	}

	var allTexts []string
	for _, kp := range kps {
		allTexts = append(allTexts, kp.Content)
	}
	assessed, failed := 0, 0
	for _, kp := range kps {
		input := provider.KeypointQualityInput{
			KeyPointID:      kp.ID,
			Content:         kp.Content,
			Description:     kp.Description,
			Citations:       parseCitations(kp.CitationsJSON),
			Segments:        payload.Segments,
			SameSourceTexts: withoutText(allTexts, kp.Content),
		}
		// 第一层：程序检查（不依赖模型，终审直接落库）。
		usage := provider.TaskUsage{}
		var verdict *provider.KeypointQualityVerdict
		if pre, final := provider.PrecheckKeypointQuality(input); final {
			verdict = &pre
		} else {
			// 第二层：独立判定（只参考被引用原文；模型不可用则显式失败，不伪装通过）。
			v, u, err := checker.AssessKeypointQuality(ctx, input)
			if err != nil {
				return fmt.Errorf("重点 %s 判定失败: %w", kp.ID, err)
			}
			verdict, usage = v, u
		}
		snap, _ := json.Marshal(map[string]any{
			"cited_segments":    citedSegmentTexts(input.Segments, input.Citations),
			"same_source_count": len(input.SameSourceTexts),
		})
		fingerprint := provider.FingerprintKeypoint(kp.Content, kp.Description, input.Citations)
		if err := w.store.SaveKeypointQualityResult(ctx, &models.KeypointQualityResult{
			KeyPointID: kp.ID, SourceType: job.SourceType, SourceID: job.SourceID,
			CardVersion: snapshot.CardVersion, ContentFingerprint: fingerprint,
			Decision: verdict.Decision, Reasons: verdict.Reasons,
			InputSnapshotJSON: string(snap), Provider: checker.Name(), Model: "", JobID: job.ID,
		}); err != nil {
			return fmt.Errorf("保存质量结果: %w", err)
		}
		if usage.InputUnits > 0 || usage.OutputUnits > 0 {
			w.recordCallUsage(ctx, job, "keypoint_quality", checker.Name(), "", usage)
		}
		assessed++
		_ = failed
	}
	result, _ := json.Marshal(map[string]any{"card_version": snapshot.CardVersion, "assessed": assessed})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}

func parseCitations(citationsJSON string) []string {
	var out []string
	_ = json.Unmarshal([]byte(citationsJSON), &out)
	return out
}

func withoutText(texts []string, exclude string) []string {
	var out []string
	for _, t := range texts {
		if t != exclude {
			out = append(out, t)
		}
	}
	return out
}

// citedSegmentTexts 提取被引用 Segment 的原文（判定依据快照）。
func citedSegmentTexts(segments []provider.Segment, citations []string) []string {
	set := map[string]bool{}
	for _, c := range citations {
		set[c] = true
	}
	var out []string
	for _, seg := range segments {
		if set[seg.ID] {
			out = append(out, "["+seg.ID+"] "+seg.Text)
		}
	}
	return out
}
