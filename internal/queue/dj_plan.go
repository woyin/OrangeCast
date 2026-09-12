// dj_plan.go DJ 编排 Module（D02 / ADR-0024 §3）。
// 小 Interface：来源快照 + 可用高光（已过 D01 校验）+ 目标时长 → 有序持久化清单。
// 首版策略（确定性）：保持节目原顺序逐项纳入；每项估算 = 解说估计时长（有真实
// 解说用真实时长，否则用常量估计）+ 原音区间时长；超出目标即停止（保序、
// 不重排、不截断单项）；内容不足允许短于目标。
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// djNarrationEstimateSeconds 无真实解说时的串场估计时长（D02；D04 生成后回填真实值）。
const djNarrationEstimateSeconds = 8.0

// djTargetSecondsDefault 目标时长默认值（秒）：Owner 未指定时使用。
const djTargetSecondsDefault = 1500.0

// BuildDJPlan 确定性编排：相同输入产生相同清单（测试保证排序稳定、无重叠）。
func BuildDJPlan(highlights provider.HighlightSet, segments []provider.Segment, narrations map[string]*store.NarrationRow, targetSeconds float64) *models.DJPlan {
	if targetSeconds <= 0 {
		targetSeconds = djTargetSecondsDefault
	}
	index := map[string]int{}
	for i, seg := range segments {
		index[seg.ID] = i
	}
	ordered := make([]provider.Highlight, len(highlights.Highlights))
	copy(ordered, highlights.Highlights)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := firstIndex(ordered[i].Citations, index), firstIndex(ordered[j].Citations, index)
		return oi < oj
	})

	plan := &models.DJPlan{TargetSeconds: targetSeconds}
	total := 0.0
	for _, h := range ordered {
		span := segmentSpan(h.Citations, segments)
		if span <= 0 {
			continue
		}
		var narrSeconds float64
		narrationID := ""
		if row, ok := narrations[h.ID]; ok && row.DurationSeconds > 0 {
			narrSeconds = row.DurationSeconds
			narrationID = row.ID
		} else {
			narrSeconds = djNarrationEstimateSeconds
		}
		itemTotal := span + narrSeconds
		if total+itemTotal > targetSeconds {
			break // 保序截停：不重排、不截断单项；内容不足允许短于目标
		}
		if narrationID != "" {
			plan.Items = append(plan.Items, models.DJPlanItem{
				Kind: models.DJItemNarration, HighlightID: h.ID, NarrationID: narrationID,
				EstSeconds: narrSeconds, Reason: "开场解说（真实合成时长）",
			})
			total += narrSeconds
		}
		plan.Items = append(plan.Items, models.DJPlanItem{
			Kind: models.DJItemEvidence, HighlightID: h.ID, SegmentIDs: append([]string(nil), h.Citations...),
			Start: spanStart(h.Citations, segments), End: spanEnd(h.Citations, segments),
			EstSeconds: span, Reason: "原顺序纳入；上下文完整（连续区间）",
		})
		total += span
		if narrationID == "" {
			plan.Items = append(plan.Items, models.DJPlanItem{
				Kind: models.DJItemNarration, HighlightID: h.ID,
				EstSeconds: narrSeconds, Reason: "串场解说（估计时长，缺失时页面标注）",
			})
			total += narrSeconds
		}
	}
	plan.TotalSeconds = total
	return plan
}

func firstIndex(cites []string, index map[string]int) int {
	best := 1 << 30
	for _, c := range cites {
		if i, ok := index[c]; ok && i < best {
			best = i
		}
	}
	return best
}

func segmentSpan(cites []string, segments []provider.Segment) float64 {
	return spanEnd(cites, segments) - spanStart(cites, segments)
}

func spanStart(cites []string, segments []provider.Segment) float64 {
	_, start, _ := segmentSpanBounds(cites, segments)
	return start
}

func spanEnd(cites []string, segments []provider.Segment) float64 {
	end, _, _ := segmentSpanBounds(cites, segments)
	return end
}

func segmentSpanBounds(cites []string, segments []provider.Segment) (end, start float64, ok bool) {
	index := map[string]provider.Segment{}
	for _, seg := range segments {
		index[seg.ID] = seg
	}
	first := true
	var lo, hi float64
	for _, c := range cites {
		seg, ok2 := index[c]
		if !ok2 {
			continue
		}
		if first {
			lo, hi = seg.Start, seg.End
			first = false
			continue
		}
		if seg.Start < lo {
			lo = seg.Start
		}
		if seg.End > hi {
			hi = seg.End
		}
	}
	return hi, lo, !first
}

// doDJPlanJob DJ 清单任务：冻结高光版本，编排并持久化清单。
func (w *Worker) doDJPlanJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot struct {
		HighlightVersion int     `json:"highlight_version"`
		TargetSeconds    float64 `json:"target_seconds"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.HighlightVersion == 0 {
		return fmt.Errorf("任务缺少高光版本快照（应经 EnqueueDJPlanJob 入队）")
	}
	hv, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight, snapshot.HighlightVersion)
	if err != nil {
		return fmt.Errorf("读取冻结高光版本: %w", err)
	}
	var hs provider.HighlightSet
	if err := json.Unmarshal([]byte(hv.Payload), &hs); err != nil {
		return fmt.Errorf("解析高光载荷: %w", err)
	}
	segments := w.frozenSegments(ctx, job, exec)
	narrations, _ := w.store.ListCurrentNarrationsForSource(ctx, job.SourceType, job.SourceID)
	plan := BuildDJPlan(hs, segments, narrations, snapshot.TargetSeconds)
	plan.SourceType, plan.SourceID, plan.HighlightVersion = job.SourceType, job.SourceID, snapshot.HighlightVersion
	plan.InputSnapshotJSON = exec.InputSnapshotJSON
	created, err := w.store.CreateDJPlan(ctx, plan)
	if err != nil {
		return fmt.Errorf("持久化 DJ 清单: %w", err)
	}
	result, _ := json.Marshal(map[string]any{"dj_plan_id": created.ID, "items": len(created.Items)})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}

// frozenSegments 读取任务快照冻结的转录段（无记录时回退当前版本，兼容旧任务）。
func (w *Worker) frozenSegments(ctx context.Context, job *models.ProcessingJob, exec *models.ProcessingJobExecution) []provider.Segment {
	var snap struct {
		TranscriptVersion int `json:"transcript_version"`
	}
	if exec != nil {
		_ = json.Unmarshal([]byte(exec.InputSnapshotJSON), &snap)
	}
	if snap.TranscriptVersion > 0 {
		if av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, snap.TranscriptVersion); err == nil {
			var payload provider.TranscriptPayload
			if json.Unmarshal([]byte(av.Payload), &payload) == nil {
				return payload.Segments
			}
		}
	}
	if av, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript); err == nil {
		var payload provider.TranscriptPayload
		if json.Unmarshal([]byte(av.Payload), &payload) == nil {
			return payload.Segments
		}
	}
	return nil
}

// EnqueueDJPlanJob 入队 DJ 清单编排任务（冻结高光版本与目标时长）。
func (w *Worker) EnqueueDJPlanJob(ctx context.Context, sourceType models.SourceType, sourceID string, highlightVersion int, targetSeconds float64) (*models.ProcessingJob, error) {
	snapshot, err := json.Marshal(map[string]any{
		"highlight_version": highlightVersion,
		"target_seconds":    targetSeconds,
	})
	if err != nil {
		return nil, err
	}
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
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
