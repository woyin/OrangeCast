package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func jsonMarshalPayload(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func seedHighlightVersion(t *testing.T, s *store.Store, sourceID string, hs provider.HighlightSet) int {
	t.Helper()
	job, _, err := s.EnqueueJobIdempotent(t.Context(), store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "djseed",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := jsonMarshalPayload(hs)
	version, err := s.CreateArtifactVersion(t.Context(), models.SourceEpisode, sourceID, store.KindHighlight, "fake", "m", "1", job.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(t.Context(), models.SourceEpisode, sourceID, store.KindHighlight, version); err != nil {
		t.Fatal(err)
	}
	return version
}

// TestBuildDJPlan_DeterministicAndBounded D02：相同输入排序稳定、目标时长约束、
// 保序不重排、无重叠。
func TestBuildDJPlan_DeterministicAndBounded(t *testing.T) {
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 300},
		{ID: "seg-0002", Start: 300, End: 600},
		{ID: "seg-0003", Start: 600, End: 900},
	}
	hs := provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "h2", Gist: "第二", Citations: []string{"seg-0002"}},
		{ID: "h1", Gist: "第一", Citations: []string{"seg-0001"}},
		{ID: "h3", Gist: "第三", Citations: []string{"seg-0003"}},
	}}
	p1 := BuildDJPlan("测试节目", hs, segs, nil, 1000)
	p2 := BuildDJPlan("测试节目", hs, segs, nil, 1000)
	if len(p1.Items) != 10 || len(p2.Items) != 10 {
		t.Fatalf("开场+3×(解说+原音)+2 过渡+收尾=10 项：%d/%d", len(p1.Items), len(p2.Items))
	}
	for i := range p1.Items {
		a, b2 := p1.Items[i], p2.Items[i]
		if a.Position != b2.Position || a.Kind != b2.Kind || a.HighlightID != b2.HighlightID ||
			a.Start != b2.Start || a.End != b2.End || a.EstSeconds != b2.EstSeconds {
			t.Fatalf("相同输入应产出相同清单：位置 %d 不一致", i)
		}
	}
	// 结构：intro → 解说/原音(h1) → 过渡 → 解说/原音(h2) → 过渡 → 解说/原音(h3) → outro
	if p1.Items[0].ScriptKind != "intro" {
		t.Fatalf("首项应为开场解说: %+v", p1.Items[0])
	}
	if p1.Items[2].HighlightID != "h1" || p1.Items[5].HighlightID != "h2" || p1.Items[8].HighlightID != "h3" {
		t.Fatalf("原音应保持节目原顺序: %+v", p1.Items)
	}
	if p1.Items[9].ScriptKind != "outro" {
		t.Fatalf("末项应为收尾解说: %+v", p1.Items[9])
	}
	// 过渡脚本指向下一高光且基于其 Gist（不重复朗读原音）。
	if p1.Items[3].ScriptKind != "transition" || !strings.Contains(p1.Items[3].ScriptText, "第二") {
		t.Fatalf("过渡应预告下一段主题: %+v", p1.Items[3])
	}
	if p1.TotalSeconds > 1000 {
		t.Fatalf("总时长不得超过目标: %f", p1.TotalSeconds)
	}
	p3 := BuildDJPlan("测试节目", hs, segs, nil, 400)
	if p3.TotalSeconds > 400 {
		t.Fatalf("目标约束应生效（内容不足允许短于目标）: %f", p3.TotalSeconds)
	}
}

// TestDJPlanJob_PersistAndPreserveOld 清单持久化：新高光版本生成新清单，旧清单不变。
func TestDJPlanJob_PersistAndPreserveOld(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	hsV1 := provider.HighlightSet{Highlights: []provider.Highlight{{ID: "h1", Gist: "g", Citations: []string{"seg-0001"}}}}
	hsV2 := provider.HighlightSet{Highlights: []provider.Highlight{{ID: "h1v2", Gist: "g2", Citations: []string{"seg-0002"}}}}

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{}, nil
	}
	v1 := seedHighlightVersion(t, s, sourceID, hsV1)
	completeSeedJobs(t, s) // 终结全部种子任务，确保 ProcessOne 领取的是 DJ 清单任务
	if _, err := w.EnqueueDJPlanJob(ctx, models.SourceEpisode, sourceID, v1, 600); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	planV1, err := s.GetCurrentDJPlan(ctx, models.SourceEpisode, sourceID, v1)
	if err != nil || len(planV1.Items) == 0 {
		t.Fatalf("清单应持久化: %v", err)
	}

	v2 := seedHighlightVersion(t, s, sourceID, hsV2)
	completeSeedJobs(t, s) // 第二次种子同样会留下排队任务
	if _, err := w.EnqueueDJPlanJob(ctx, models.SourceEpisode, sourceID, v2, 600); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	planV2, err := s.GetCurrentDJPlan(ctx, models.SourceEpisode, sourceID, v2)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.GetDJPlan(ctx, planV1.ID)
	if err != nil || len(old.Items) != len(planV1.Items) {
		t.Fatalf("旧清单应保持原内容: %v %+v", err, old)
	}
	// 旧清单项内容不变（含脚本文本）。
	for i := range planV1.Items {
		if old.Items[i].ScriptText != planV1.Items[i].ScriptText || old.Items[i].HighlightID != planV1.Items[i].HighlightID {
			t.Fatalf("旧清单第 %d 项被改动: %+v", i, old.Items[i])
		}
	}
	// 新清单使用新高光。
	found := false
	for _, it := range planV2.Items {
		if it.HighlightID == "h1v2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新清单应使用新高光: %+v", planV2.Items)
	}
}

func TestDJFrozenTranscriptNeverFallsBackToCurrent(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := t.Context()
	id := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, id)
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: id}
	if segs := w.frozenSegments(ctx, job, &models.ProcessingJobExecution{InputSnapshotJSON: `{"transcript_version":999}`}); segs != nil {
		t.Fatal("missing frozen version mapped to current", segs)
	}
	hv := seedHighlightVersion(t, s, id, provider.HighlightSet{Highlights: []provider.Highlight{{ID: "h1", Gist: "来源", Citations: []string{"seg-0001"}}}})
	completeSeedJobs(t, s)
	queued, err := w.EnqueueDJPlanJob(ctx, models.SourceEpisode, id, hv, 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=json_set(input_snapshot_json,'$.transcript_version',999) WHERE id=?`, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.doDJPlanJob(ctx, queued, &provider.ProviderBundle{}); err == nil {
		t.Fatal("missing frozen source generated new positions")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE artifact_versions SET payload='malformed' WHERE source_id=? AND kind='transcript'`, id); err != nil {
		t.Fatal(err)
	}
	if segs := w.frozenSegments(ctx, job, nil); segs != nil {
		t.Fatal("corrupt current transcript accepted")
	}
	if segs := w.frozenSegments(ctx, job, &models.ProcessingJobExecution{InputSnapshotJSON: `{"transcript_version":1}`}); segs != nil {
		t.Fatal("corrupt frozen transcript accepted")
	}
}
