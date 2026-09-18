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
	card    *provider.KnowledgeCard // 非空时 Analyze 返回该卡片（链路测试用）
}

func (f *qualityAnalyzer) Analyze(transcript string, segments []provider.Segment) (*provider.AnalyzeResult, error) {
	card := f.card
	if card == nil {
		card = &provider.KnowledgeCard{Title: "T"}
	}
	return &provider.AnalyzeResult{Card: card}, nil
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

// TestAnalysisChainsQualityToReady R05：从正常分析入口一路执行到 Ready 并可参与发现；
// 重跑不增加变化。分析（索引后幂等入队质量）→ 质量判定 → 正式状态 ready +
// 一条 material_change；重复判定不新增变化。
func TestAnalysisChainsQualityToReady(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	qa := &qualityAnalyzer{
		card: &provider.KnowledgeCard{
			Title:   "主权基金",
			Summary: provider.CitedText{Text: "配置变化", Citations: []string{"seg-0001"}},
			KeyPoints: []provider.KeyPoint{{
				Content: "主讲人认为低剂量咖啡与更低风险相关", Description: "限定：相关性非因果",
				Citations: []string{"seg-0001"},
			}},
			Chapters: []provider.Chapter{{Title: "开篇", Citations: []string{"seg-0001"}}},
		},
		verdict: provider.KeypointQualityVerdict{Decision: models.KPQualityReady, Reasons: []string{"原文支持"}},
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: qa}, nil
	}
	if _, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-chain",
	}); err != nil {
		t.Fatal(err)
	}
	// 第一步：分析（含索引 + 幂等入队质量判定）。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("分析失败: %v", err)
	}
	// 第二步：质量判定（自动衔接的任务）。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("质量判定失败: %v", err)
	}
	kps, _, err := s.ListKeyPointsFiltered(ctx, store.KeyPointFilter{SourceType: models.SourceEpisode, SourceID: sourceID}, 1, 10)
	if err != nil || len(kps) != 1 {
		t.Fatalf("应有一条重点: %v %+v", err, kps)
	}
	if kps[0].QualityStatus != models.KeyPointReady {
		t.Fatalf("判定通过后应为 ready: %+v", kps[0])
	}
	var changes int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM material_changes WHERE keypoint_id = ? AND change_kind='quality_approved'`, kps[0].ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("应恰好一条进入发现的变化: %d", changes)
	}
	// 重跑质量判定：幂等，不新增变化。
	if _, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, kps[0].CardVersion); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("重跑判定失败: %v", err)
	}
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM material_changes WHERE keypoint_id = ? AND change_kind='quality_approved'`, kps[0].ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("重跑不得新增变化: %d", changes)
	}
}

// TestApplyKeypointQuality_StaleAndOwnerGuard R05：旧结果/过期判定不批准新内容；
// Owner 决策（owner_confirmed、排除、人工修改）不被自动判定覆盖。
func TestApplyKeypointQuality_StaleAndOwnerGuard(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedKeypointsForQuality(t, s, sourceID)
	completeSeedJobs(t, s)

	// 三条重点：正常自动、owner_confirmed、Owner 排除（dismissed）。
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, evidence_status, quality_status, created_at)
		 VALUES ('kp-owner', 'episode', ?, '标题', 'Owner 已确认的观点', '', '["seg-0001"]', 'citation', 0, 0, 1, 'automatic', 'inbox', 'ok', 'owner_confirmed', datetime('now')),
		        ('kp-excluded', 'episode', ?, '标题', 'Owner 排除的观点', '', '["seg-0001"]', 'citation', 0, 0, 1, 'automatic', 'dismissed', 'ok', 'needs_review', datetime('now')),
		        ('kp-edited', 'episode', ?, '标题', 'Owner 修改过的观点', '', '["seg-0001"]', 'citation', 0, 0, 1, 'edited', 'inbox', 'ok', 'needs_review', datetime('now'))`,
		sourceID, sourceID, sourceID); err != nil {
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
		t.Fatalf("判定失败: %v", err)
	}
	getQ := func(id string) string {
		var q string
		if err := s.DB.QueryRowContext(ctx, `SELECT quality_status FROM keypoint_index WHERE id=?`, id).Scan(&q); err != nil {
			t.Fatal(err)
		}
		return q
	}
	if q := getQ("kp-owner"); q != "owner_confirmed" {
		t.Fatalf("owner_confirmed 不得被自动判定覆盖: %s", q)
	}
	if q := getQ("kp-excluded"); q != "needs_review" {
		t.Fatalf("Owner 排除的重点不应进入发现资格: %s", q)
	}
	if q := getQ("kp-edited"); q != "needs_review" {
		t.Fatalf("人工修改的重点不得被覆盖: %s", q)
	}
	// 自动重点（seed 的 kp-1）通过并 ready。
	var autoReady int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM keypoint_index WHERE source_id=? AND card_version=1
		   AND id NOT IN ('kp-owner','kp-excluded','kp-edited') AND quality_status='ready'`,
		sourceID).Scan(&autoReady); err != nil {
		t.Fatal(err)
	}
	if autoReady != 1 {
		t.Fatalf("自动重点应恰好一条 ready: %d", autoReady)
	}

	// 过期判定：自动重点内容变化后指纹不匹配 → 不批准新内容。
	var kpID string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM keypoint_index WHERE content LIKE '主讲人认为%'`).Scan(&kpID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE keypoint_index SET content='内容已变化的新表述', quality_status='needs_review' WHERE id=?`, kpID); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ApplyKeypointQualityResults(ctx, models.SourceEpisode, sourceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SkippedNoMatch == 0 {
		t.Fatalf("指纹不匹配的旧结果应被跳过: %+v", stats)
	}
	if q := getQ(kpID); q != "needs_review" {
		t.Fatalf("过期判定不得批准新内容: %s", q)
	}
}
