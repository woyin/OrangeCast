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

// qualityAnalyzer 可脚本化的判定 Provider。
type qualityAnalyzer struct {
	verdict provider.KeypointQualityVerdict
	err     error
	calls   int
	diag    *provider.IdeationDiagnosis
	diagErr error
}

func (f *qualityAnalyzer) Analyze(transcript string, segments []provider.Segment) (*provider.AnalyzeResult, error) {
	return &provider.AnalyzeResult{Card: &provider.KnowledgeCard{Title: "T"}}, nil
}
func (f *qualityAnalyzer) Name() string { return "fake" }

func (f *qualityAnalyzer) AssessKeypointQuality(ctx context.Context, input provider.KeypointQualityInput) (*provider.KeypointQualityVerdict, provider.TaskUsage, error) {
	f.calls++
	if f.err != nil {
		return nil, provider.TaskUsage{}, f.err
	}
	v := f.verdict
	return &v, provider.TaskUsage{InputUnits: 30, OutputUnits: 5}, nil
}

func seedKeypointsForQuality(t *testing.T, s *store.Store, sourceID string) {
	t.Helper()
	ctx := context.Background()
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "kpq-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	cards := `{"keypoints":[{"id":"kp-1","content":"主讲人认为低剂量咖啡与更低风险相关","description":"限定：相关性非因果","citations":["seg-0001"]}]}`
	payload := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[{"content":"主讲人认为低剂量咖啡与更低风险相关","description":"限定：相关性非因果","citations":["seg-0001"]}],"chapters":[],"quotes":[],"tags":[]}`
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "fake", "m", "1", job.ID, payload); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, 1); err != nil {
		t.Fatal(err)
	}
	_ = cards
	// 重点索引（K02 任务读取的来源）
	if _, err := s.IndexKeyPoints(ctx, models.SourceEpisode, sourceID, "标题", 1, mustCard(t, payload), mustSegments(t)); err != nil {
		t.Fatal(err)
	}
}

func mustCard(t *testing.T, payload string) *provider.KnowledgeCard {
	t.Helper()
	var c provider.KnowledgeCard
	if err := jsonUnmarshal(payload, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func mustSegments(t *testing.T) []provider.Segment {
	t.Helper()
	return []provider.Segment{{ID: "seg-0001", Start: 0, End: 30, Text: "本期讨论主权基金的配置变化"}}
}

func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// TestKeypointQualityJob_EndToEnd K02：判定任务产出可读结果（通过与拦截并存）。
func TestKeypointQualityJob_EndToEnd(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedKeypointsForQuality(t, s, sourceID)
	completeSeedJobs(t, s)

	qa := &qualityAnalyzer{verdict: provider.KeypointQualityVerdict{Decision: models.KPQualityReady, Reasons: []string{"原文支持该观点"}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	job, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("判定任务失败: %v", err)
	}
	results, err := s.ListKeypointQualityResults(ctx, models.SourceEpisode, sourceID, 1)
	if err != nil || len(results) != 1 {
		t.Fatalf("应有一条质量结果: %v %+v", err, results)
	}
	if results[0].Decision != models.KPQualityReady || len(results[0].Reasons) == 0 {
		t.Fatalf("结论应带原因: %+v", results[0])
	}
	if !strings.Contains(results[0].InputSnapshotJSON, "seg-0001") {
		t.Fatalf("输入快照应含引用原文: %s", results[0].InputSnapshotJSON)
	}
	if qa.calls != 1 {
		t.Fatalf("判定应恰好调用一次模型: %d", qa.calls)
	}
}

// TestKeypointQualityJob_ModelUnavailableFailsVisibly 判定模型不可用 → 任务显式失败，不伪装通过。
func TestKeypointQualityJob_ModelUnavailableFailsVisibly(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedKeypointsForQuality(t, s, sourceID)
	completeSeedJobs(t, s)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: &qualityAnalyzer{err: context.DeadlineExceeded}}, nil
	}
	job, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "判定失败") {
		t.Fatalf("模型不可用应显式失败: %+v", got)
	}
	results, _ := s.ListKeypointQualityResults(ctx, models.SourceEpisode, sourceID, 1)
	if len(results) != 0 {
		t.Fatalf("判定失败不得落任何结果: %+v", results)
	}
}

// TestKeypointQualityJob_ProgramInvalidSkipsModel 程序检查终审（空引用等）不调用模型。
func TestKeypointQualityJob_ProgramInvalidSkipsModel(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedKeypointsForQuality(t, s, sourceID)
	completeSeedJobs(t, s)
	// 直插一条引用不存在的重点行（模拟索引过滤外的历史脏数据，同一卡片版本）。
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, evidence_status, quality_status, created_at)
		 VALUES ('kp-bad', 'episode', ?, '标题', '空引用观点', '', '["seg-9999"]', 'citation', 0, 0, 1, 'automatic', 'inbox', 'ok', 'needs_review', datetime('now'))`, sourceID); err != nil {
		t.Fatal(err)
	}
	qa := &qualityAnalyzer{verdict: provider.KeypointQualityVerdict{Decision: models.KPQualityReady}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	if _, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	results, _ := s.ListKeypointQualityResults(ctx, models.SourceEpisode, sourceID, 1)
	var invalid *models.KeypointQualityResult
	for _, r := range results {
		if r.Decision == models.KPQualityInvalid {
			invalid = r
		}
	}
	if invalid == nil || len(invalid.Reasons) == 0 {
		t.Fatalf("空引用重点应被程序拦截: %+v", results)
	}
}
