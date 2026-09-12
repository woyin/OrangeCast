package queue

import (
	"context"
	"encoding/json"
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
	p1 := BuildDJPlan(hs, segs, nil, 1000)
	p2 := BuildDJPlan(hs, segs, nil, 1000)
	if len(p1.Items) != 6 || len(p2.Items) != 6 {
		t.Fatalf("每段产生解说+原音两项（共 6）：%d/%d", len(p1.Items), len(p2.Items))
	}
	for i := range p1.Items {
		a, b2 := p1.Items[i], p2.Items[i]
		if a.Position != b2.Position || a.Kind != b2.Kind || a.HighlightID != b2.HighlightID ||
			a.Start != b2.Start || a.End != b2.End || a.EstSeconds != b2.EstSeconds {
			t.Fatalf("相同输入应产出相同清单：位置 %d 不一致", i)
		}
	}
	if p1.Items[0].HighlightID != "h1" || p1.Items[2].HighlightID != "h2" || p1.Items[4].HighlightID != "h3" {
		t.Fatalf("应保持节目原顺序: %+v", p1.Items)
	}
	if p1.TotalSeconds > 1000 {
		t.Fatalf("总时长不得超过目标: %f", p1.TotalSeconds)
	}
	p3 := BuildDJPlan(hs, segs, nil, 400)
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
	if err != nil || len(old.Items) != len(planV1.Items) || old.Items[0].HighlightID != "h1" {
		t.Fatalf("旧清单应保持原内容: %+v", old)
	}
	if planV2.Items[0].HighlightID != "h1v2" {
		t.Fatalf("新清单应使用新高光: %+v", planV2.Items)
	}
}
