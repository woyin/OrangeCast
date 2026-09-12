package store

import (
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// 本文件为 G01/G02/G03/G05/G07/G08 新增 store API 的包内覆盖测试：
// 跨包（queue/server）测试不计入 store 自身覆盖率，故在此补最小读写闭环。

func seedDigestFlowSource(t *testing.T, s *Store, guid, title string) string {
	t.Helper()
	podcast, err := s.CreatePodcast(t.Context(), "https://feed.example.com/"+guid+".xml", title, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: guid, Title: title, AudioURL: "https://cdn.example.com/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	return eps[0].ID
}

func seedDigestFlowCard(t *testing.T, s *Store, epID string) {
	t.Helper()
	job, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobAnalyze, IntentID: "flow-an-" + epID,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`
	if _, err := s.CreateArtifactVersion(t.Context(), models.SourceEpisode, epID, KindKnowledgeCard, "fake", "m", "1", job.ID, payload); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(t.Context(), models.SourceEpisode, epID, KindKnowledgeCard, 1); err != nil {
		t.Fatal(err)
	}
}

// TestDigestFlow_FreezeEnqueueList G01/G07：冻结入队 + 按源列出修订。
func TestDigestFlow_FreezeEnqueueList(t *testing.T) {
	s := newTestStore(t)
	epID := seedDigestFlowSource(t, s, "flow-1", "流程播客")
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	seedDigestFlowCard(t, s, epID)

	job, err := s.EnqueueDigestJob(t.Context(), models.SourceEpisode, epID)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	exec, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || !strings.Contains(exec.InputSnapshotJSON, "snapshot_id") {
		t.Fatalf("入队应冻结快照: %+v", exec)
	}
	if _, err := s.ListEpisodeDigestsForSource(t.Context(), models.SourceEpisode, epID); err != nil {
		t.Fatalf("按源列出修订: %v", err)
	}
}

// TestDigestFlow_RevisionAndReadiness G03/G05：派生修订 + 就绪评估闭环。
func TestDigestFlow_RevisionAndReadiness(t *testing.T) {
	s := newTestStore(t)
	epID := seedDigestFlowSource(t, s, "flow-2", "流程播客二")
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	seedDigestFlowCard(t, s, epID)
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.PublishEpisodeDigest(t.Context(), &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epID, Title: "初版",
		Provider: "p", Model: "m", PromptVersion: "v", SourceSnapshotID: snap.ID,
	}, []models.DigestBlock{{Type: models.DigestBlockParaphrase, Text: "正文", Citations: []string{"seg-0001"}}},
		[]models.DigestSearchSource{{Query: "q", URL: "https://ex.com/a", Title: "A", DocumentID: "doc-a"}},
		[]models.DigestFactGap{{Text: "缺口"}})
	if err != nil {
		t.Fatal(err)
	}
	// pending 落源 → 不可交付，问题可 JSON 化。
	readiness, err := s.EvaluateDigestReadiness(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Deliverable || len(readiness.Issues) == 0 {
		t.Fatalf("pending 落源应不可交付: %+v", readiness)
	}
	if readiness.MarshalIssuesJSON() == "[]" {
		t.Fatal("问题清单不应为空")
	}
	if !strings.Contains(readiness.Summary(), "草稿") || strings.Contains(readiness.Summary(), "可交付：") {
		t.Fatalf("摘要应为草稿语义: %s", readiness.Summary())
	}
	// 确认落源 → 可交付。
	rows, _ := s.ListDigestSearchSources(t.Context(), base.ID)
	if err := s.SetDigestSearchSourceStatus(t.Context(), rows[0].ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	readiness, _ = s.EvaluateDigestReadiness(t.Context(), base.ID)
	if !readiness.Deliverable {
		t.Fatalf("确认后应可交付: %+v", readiness)
	}
	// 派生修订：过期基准冲突；正确基准成功且父链/原因记录。
	blockPtrs, _ := s.ListDigestBlocks(t.Context(), base.ID)
	blocks := make([]models.DigestBlock, 0, len(blockPtrs))
	for _, b := range blockPtrs {
		blocks = append(blocks, *b)
	}
	if _, err := s.CreateDigestRevision(t.Context(), DigestRevisionInput{
		Base: base, BaseVersionClaimed: 99, Reason: "x", Blocks: blocks,
	}); err != ErrDigestRevisionConflict {
		t.Fatalf("过期基准应冲突: %v", err)
	}
	rev, err := s.CreateDigestRevision(t.Context(), DigestRevisionInput{
		Base: base, BaseVersionClaimed: 1, Reason: "调整", Blocks: blocks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Version != 2 || rev.ParentDigestID != base.ID || rev.Reason != "调整" {
		t.Fatalf("修订血缘不符: %+v", rev)
	}
	// 重试入口（G09 语义在 G03）：改写任务入队 + 指纹读取。
	if _, err := s.EnqueueDigestRewriteJob(t.Context(), rev.ID, "xiaohongshu"); err != nil {
		t.Fatalf("改写入队: %v", err)
	}
	if h, err := s.GetDigestRewriteInputHash(t.Context(), rev.ID, "xiaohongshu"); err != nil || h != "" {
		t.Fatalf("未改写时指纹应为空: %q %v", h, err)
	}
}

// TestDigestFlow_DJPlanPersist D02：清单写入与按序读取。
func TestDigestFlow_DJPlanPersist(t *testing.T) {
	s := newTestStore(t)
	epID := seedDigestFlowSource(t, s, "flow-3", "DJ 播客")
	plan, err := s.CreateDJPlan(t.Context(), &models.DJPlan{
		SourceType: models.SourceEpisode, SourceID: epID, HighlightVersion: 1,
		TargetSeconds: 600, TotalSeconds: 42, InputSnapshotJSON: `{"highlight_version":1}`,
		Items: []models.DJPlanItem{
			{Kind: models.DJItemNarration, ScriptKind: "intro", ScriptText: "开场词", EstSeconds: 12, Reason: "开场"},
			{Kind: models.DJItemEvidence, HighlightID: "h1", SegmentIDs: []string{"seg-0001"}, Start: 0, End: 30, EstSeconds: 30, Reason: "原音"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDJPlan(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].ScriptKind != "intro" || got.Items[1].SegmentIDs[0] != "seg-0001" {
		t.Fatalf("清单读写不符: %+v", got)
	}
	cur, err := s.GetCurrentDJPlan(t.Context(), models.SourceEpisode, epID, 1)
	if err != nil || cur.ID != plan.ID {
		t.Fatalf("当前清单读取: %v", err)
	}
	// 非法清单拒绝
	if _, err := s.CreateDJPlan(t.Context(), &models.DJPlan{SourceType: models.SourceEpisode, SourceID: epID, HighlightVersion: 0}); err == nil {
		t.Fatal("缺少高光版本应拒绝")
	}
}

// TestDigestFlow_QualityAndHistoryReads K02/G08 存储读取面。
func TestDigestFlow_QualityAndHistoryReads(t *testing.T) {
	s := newTestStore(t)
	epID := seedDigestFlowSource(t, s, "flow-4", "读取播客")
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	if _, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobKeypointQuality,
		IntentID: "kpq-read", InputSnapshotJSON: `{"card_version":1,"transcript_version":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListKeyPointRowsByCardVersion(t.Context(), models.SourceEpisode, epID, 1); err != nil {
		t.Fatalf("按版本读取重点: %v", err)
	}
	if err := s.SaveKeypointQualityResult(t.Context(), &models.KeypointQualityResult{
		KeyPointID: "kp-x", SourceType: models.SourceEpisode, SourceID: epID,
		CardVersion: 1, ContentFingerprint: "fp", Decision: models.KPQualityReady, Reasons: []string{"ok"},
	}); err != nil {
		t.Fatal(err)
	}
	results, err := s.ListKeypointQualityResults(t.Context(), models.SourceEpisode, epID, 1)
	if err != nil || len(results) != 1 {
		t.Fatalf("质量结果列表: %v %+v", err, results)
	}
	// 日限额与预算预留列表（预算面）。
	limit := int64(3)
	if err := s.SetAutoDailyJobLimit(t.Context(), &limit); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAutoDailyJobLimit(t.Context())
	if err != nil || got == nil || *got != 3 {
		t.Fatalf("日限额读写: %v %+v", err, got)
	}
	if _, err := s.ListBudgetReservations(t.Context(), 10); err != nil {
		t.Fatalf("预留列表: %v", err)
	}
}
