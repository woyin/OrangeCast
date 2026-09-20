package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type automaticDiscoveryScout struct {
	calls int
	err   error
}

func (f *automaticDiscoveryScout) Scout(_ context.Context, request provider.ScoutRequest) (*provider.ScoutResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	materials := request.Themes[0].Materials
	first, second := materials[0], materials[0]
	for _, material := range materials[1:] {
		if material.SourceID != first.SourceID {
			second = material
			break
		}
	}
	title := "学习成果如何改变创作判断"
	thesis := "新学习成果应先经质量闸门，再形成 Owner 可承担的创作方向。"
	if f.calls > 1 {
		title = "第二窗口的新学习对照"
		thesis = "第二窗口的新证据应更新 Owner 的创作判断。"
	}
	return &provider.ScoutResult{Proposals: []provider.ScoutProposal{{Kind: "fresh", Title: title, Thesis: thesis, Audience: "知识工作者", Rationale: "跨 Episode 的共同模式", CandidateKeyPointIDs: []string{first.KeyPointID, second.KeyPointID}}}}, nil
}

func (f *automaticDiscoveryScout) Name() string { return "fake-scout" }

func TestRunAutomaticDiscoveryCreatesOneDurableBatchAndCreationProposal(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "自动发现画像", TargetAudience: "创作者", Voice: "清晰"})
	if err != nil {
		t.Fatal(err)
	}
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/automatic.xml", "学习播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "episode-one", Title: "第一集", AudioURL: "https://example.com/one.mp3"}, {GUID: "episode-two", Title: "第二集", AudioURL: "https://example.com/two.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	for _, episode := range episodes {
		if _, err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episode.ID, episode.Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: episode.Title + "洞见一", Citations: []string{"seg-1"}}, {Content: episode.Title + "洞见二", Citations: []string{"seg-2"}}, {Content: episode.Title + "洞见三", Citations: []string{"seg-3"}}}}, []provider.Segment{{ID: "seg-1", End: 1}, {ID: "seg-2", Start: 1, End: 2}, {ID: "seg-3", Start: 2, End: 3}}); err != nil {
			t.Fatal(err)
		}
	}
	keyPoints, _, err := srv.store.ListKeyPoints(t.Context(), 1, 10)
	if err != nil || len(keyPoints) != 6 {
		t.Fatalf("seed keypoints: count=%d err=%v", len(keyPoints), err)
	}
	for _, keyPoint := range keyPoints {
		if err := srv.store.SetKeyPointQualityStatus(t.Context(), keyPoint.ID, models.KeyPointReady); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.store.DB.ExecContext(t.Context(), `UPDATE material_changes SET created_at=datetime('now','-31 minutes')`); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetDiscoverySettings(t.Context(), models.DiscoverySettings{EditorialProfileID: profile.ID, Enabled: true, Provider: "fake", Model: "fake-scout", DailyLimit: 1, DebounceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	scout := &automaticDiscoveryScout{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Scout: scout}, nil
	}

	if err := srv.RunAutomaticDiscovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	if scout.calls != 1 {
		t.Fatalf("eligible discovery window should make one provider call, got %d", scout.calls)
	}
	batches, err := srv.store.ListCreationProposals(t.Context(), profile.ID)
	if err != nil || len(batches) != 1 || batches[0].ProposalBatchID == "" || batches[0].Status != "proposed" {
		t.Fatalf("automatic result must become a batch-owned CreationProposal: proposals=%+v err=%v", batches, err)
	}
	var batchStatus string
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT status FROM proposal_batches WHERE id=?`, batches[0].ProposalBatchID).Scan(&batchStatus); err != nil || batchStatus != "ready" {
		t.Fatalf("provider result must make its durable batch visible: status=%q err=%v", batchStatus, err)
	}
	if err := srv.RunAutomaticDiscovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	if scout.calls != 1 {
		t.Fatalf("open result batch must apply durable backpressure, got %d calls", scout.calls)
	}

	// R16：真实决策（接受唯一提案）→ 同一事务内最后一条决策自动完成批次。
	if err := srv.store.DecideProposal(t.Context(), batches[0].ID, "accept", "Owner 承担该主张", "", ""); err != nil {
		t.Fatalf("accept via DecideProposal: %v", err)
	}
	var firstBatchStatus string
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT status FROM proposal_batches WHERE id=?`, batches[0].ProposalBatchID).Scan(&firstBatchStatus); err != nil {
		t.Fatal(err)
	}
	if firstBatchStatus != "completed" {
		t.Fatalf("last decision must complete the batch automatically: %q", firstBatchStatus)
	}
	if _, err := srv.store.DB.ExecContext(t.Context(), `UPDATE proposal_batches SET created_at=datetime('now','-2 hours') WHERE id=?`, batches[0].ProposalBatchID); err != nil {
		t.Fatal(err)
	}
	for i, keyPoint := range keyPoints {
		if _, err := srv.store.RecordMaterialChange(t.Context(), models.MaterialChange{KeyPointID: keyPoint.ID, SourceType: string(keyPoint.SourceType), SourceID: keyPoint.SourceID, ChangeKind: "revised", SnapshotHash: fmt.Sprintf("revised-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.store.DB.ExecContext(t.Context(), `UPDATE material_changes SET created_at=datetime('now','-31 minutes')`); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetDiscoverySettings(t.Context(), models.DiscoverySettings{EditorialProfileID: profile.ID, Enabled: true, Provider: "fake", Model: "fake-scout", DailyLimit: 2, DebounceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	// R16：新窗口经生产入口实际生成第二个成功批次与提案。
	scout.err = nil
	if err := srv.RunAutomaticDiscovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	if scout.calls != 2 {
		t.Fatalf("new window must reach the provider again, got %d calls", scout.calls)
	}
	proposals2, err := srv.store.ListCreationProposals(t.Context(), profile.ID)
	if err != nil || len(proposals2) != 2 {
		t.Fatalf("second window must create a second proposal: %d %v", len(proposals2), err)
	}
	// ListCreationProposals 是最新在前：按 batch/status 找第二批仍 proposed 的提案，
	// 不依赖排序位置（首批已 accepted）。
	var secondProposal *models.CreationProposal
	firstBatchID := batches[0].ProposalBatchID
	for _, p := range proposals2 {
		if p.Status == "proposed" && p.ProposalBatchID != firstBatchID {
			secondProposal = p
			break
		}
	}
	if secondProposal == nil {
		t.Fatalf("应找到第二批 proposed 提案: %+v", proposals2)
	}
	// 完成第二批（决策唯一提案），再写第三轮新变化并注入 provider 失败。
	if err := srv.store.DecideProposal(t.Context(), secondProposal.ID, "save", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var secondBatchStatus string
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT status FROM proposal_batches WHERE id=?`, secondProposal.ProposalBatchID).Scan(&secondBatchStatus); err != nil {
		t.Fatal(err)
	}
	if secondBatchStatus != "completed" {
		t.Fatalf("second batch must complete after its last decision: %q", secondBatchStatus)
	}
	// 让第二批成为第三轮的窗口起点（明确早于第三轮变化）。
	if _, err := srv.store.DB.ExecContext(t.Context(), `UPDATE proposal_batches SET created_at=datetime('now','-2 hours') WHERE id=?`, secondProposal.ProposalBatchID); err != nil {
		t.Fatal(err)
	}
	// 第三轮需要至少六条变化且至少两个 Source，否则调度会在 Provider 前阻断.
	if len(keyPoints) < 6 {
		t.Fatalf("第三轮测试需要六条重点: %d", len(keyPoints))
	}
	seenSources := map[string]bool{}
	for i, kp := range keyPoints {
		seenSources[kp.SourceID] = true
		if _, err := srv.store.RecordMaterialChange(t.Context(), models.MaterialChange{KeyPointID: kp.ID, SourceType: string(kp.SourceType), SourceID: kp.SourceID, ChangeKind: "revised", SnapshotHash: fmt.Sprintf("third-window-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seenSources) < 2 {
		t.Fatal("第三轮变化必须来自至少两个 Source")
	}
	if _, err := srv.store.DB.ExecContext(t.Context(), `UPDATE material_changes SET created_at=datetime('now','-31 minutes') WHERE snapshot_hash LIKE 'third-window-%'`); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetDiscoverySettings(t.Context(), models.DiscoverySettings{EditorialProfileID: profile.ID, Enabled: true, Provider: "fake", Model: "fake-scout", DailyLimit: 3, DebounceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	scout.err = errors.New("provider unavailable")
	if err := srv.RunAutomaticDiscovery(t.Context()); err == nil {
		t.Fatal("provider failure must surface from a scheduled run")
	}
	var failed int
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM proposal_batches WHERE editorial_profile_id=? AND status='failed' AND failure_reason LIKE '%provider unavailable%'`, profile.ID).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("failed automatic discovery must remain visible to the Owner: count=%d err=%v", failed, err)
	}
}

// ---- R15：历史召回与新增价值校验（自动发现）----

// seedDiscoveryEpisode 为发现集成测试写一个 Episode：转录 + 卡片（供引用校验与
// IndexKeyPoints）+ 一条重点，并置为 ready（产生发现窗口变化）。返回 epID。
func seedDiscoveryEpisode(t *testing.T, srv *Server, guid, content, segID string) string {
	t.Helper()
	ctx := context.Background()
	podcast, err := srv.store.CreatePodcast(ctx, "https://feed.example.com/"+guid+".xml", guid+" Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: guid, Title: guid + " 单集", AudioURL: "https://cdn.example.com/" + guid + ".mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := srv.store.ListEpisodes(ctx, podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("episode setup: %v", err)
	}
	epID := eps[0].ID
	job, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, epID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	payload := provider.TranscriptPayload{
		Language: "zh", Text: content,
		Segments: []provider.Segment{{ID: segID, Start: 0, End: 10, Text: content}},
	}
	payloadBytes, _ := json.Marshal(payload)
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, "transcript", "test", "test", "1", job.ID, string(payloadBytes))
	if err != nil {
		t.Fatal(err)
	}
	srv.store.MarkJobRunning(ctx, job.ID)
	srv.store.MarkJobSucceeded(ctx, job.ID)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, "transcript", version)

	card := provider.KnowledgeCard{
		Title:     guid + " 卡片",
		Summary:   provider.CitedText{Text: content, Citations: []string{segID}},
		KeyPoints: []provider.KeyPoint{{Content: content, Citations: []string{segID}}},
		Chapters:  []provider.Chapter{{Title: "C", Citations: []string{segID}}},
	}
	cardBytes, _ := json.Marshal(card)
	cardVersion, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, "knowledge_card", "test", "test", "1", job.ID, string(cardBytes))
	if err != nil {
		t.Fatal(err)
	}
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, "knowledge_card", cardVersion)
	if _, err := srv.store.IndexKeyPoints(ctx, models.SourceEpisode, epID, guid+" 单集", 1, &card, payload.Segments); err != nil {
		t.Fatal(err)
	}
	return epID
}

// TestAutomaticDiscoveryRequest_HistoricalRecall R15 集成：只把当前窗口两集的
// MaterialChange 传给请求构造；历史 Theme 真实召回相关素材；LocalOnly 与画像
// 不相关历史材料被逐条排除；HistoricalWorks 有界。
func TestAutomaticDiscoveryRequest_HistoricalRecall(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 当前窗口：两个 Episode 各一条重点。
	ep1 := seedDiscoveryEpisode(t, srv, "disc-ep1", "主讲人认为主权基金配置决定收益", "seg-d1")
	ep2 := seedDiscoveryEpisode(t, srv, "disc-ep2", "主权基金配置的利率环境影响估值", "seg-d2")

	// 历史：相关（可召回）、LocalOnly（排除）、画像不相关（排除）。
	seedDiscoveryEpisode(t, srv, "disc-h1", "主权基金配置的历史案例回顾", "seg-h1")
	seedDiscoveryEpisode(t, srv, "disc-h2", "完全无关的烹饪技巧合集分享", "seg-h2")
	epLO := seedDiscoveryEpisode(t, srv, "disc-lo", "主权基金配置的地方观察记录", "seg-lo")
	if err := srv.store.SetSourceProductionPolicy(ctx, models.SourceEpisode, epLO, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}

	kps, _, err := srv.store.ListKeyPointsFiltered(ctx, store.KeyPointFilter{}, 1, 50)
	if err != nil || len(kps) < 5 {
		t.Fatalf("应有足够重点: %d %v", len(kps), err)
	}
	for _, kp := range kps {
		if err := srv.store.SetKeyPointQualityStatus(ctx, kp.ID, models.KeyPointReady); err != nil {
			t.Fatal(err)
		}
	}
	// 画像不相关：disc-h2 的重点显式标记 irrelevant（历史召回排除）。
	for _, kp := range kps {
		if strings.Contains(kp.SourceTitle, "disc-h2") {
			if err := srv.store.SetEditorialRelevance(ctx, models.EditorialRelevance{
				EditorialProfileID: profile.ID, KeyPointID: kp.ID, Assessment: "irrelevant", Rationale: "不相关",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 只取当前窗口两集的 MaterialChange（历史/LocalOnly/不相关重点仅存在于索引，
	// 供历史召回，不进入当前窗口）。
	allChanges, err := srv.store.ListDiscoveryWindowChanges(ctx, profile.ID, "2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	windowSources := map[string]bool{ep1: true, ep2: true}
	var changes []*models.MaterialChange
	for _, ch := range allChanges {
		if windowSources[ch.SourceID] {
			changes = append(changes, ch)
		}
	}
	if len(changes) < 2 {
		t.Fatalf("当前窗口应有两集变化: %d", len(changes))
	}

	req, err := srv.automaticDiscoveryRequest(ctx, profile, "test", changes)
	if err != nil {
		t.Fatalf("请求构造失败: %v", err)
	}
	if len(req.Themes) == 0 || req.Themes[0].ID != "automatic-discovery" || len(req.Themes[0].Materials) != 2 {
		t.Fatalf("当前窗口主题应恰好含两集材料: %+v", req.Themes)
	}
	var histTheme *provider.ScoutTheme
	for i := range req.Themes {
		if req.Themes[i].ID == "historical-context" {
			histTheme = &req.Themes[i]
		}
	}
	if histTheme == nil {
		t.Fatal("应召回历史主题")
	}
	if len(histTheme.Materials) == 0 || len(histTheme.Materials) > 6 {
		t.Fatalf("历史召回应有界（≤6）且非空: %d", len(histTheme.Materials))
	}
	for _, m := range histTheme.Materials {
		if m.SourceID == epLO {
			t.Fatal("LocalOnly 历史材料不得进入发送快照")
		}
		if strings.Contains(m.Content, "烹饪") {
			t.Fatal("画像不相关历史材料不得进入发送快照")
		}
	}
	// HistoricalWorks 有界与只取最新：创建 12 条（work-00..work-11），
	// created_at 写固定递增时间；请求应恰好 10 条，含最新 work-11、不含最旧 work-00。
	for i := 0; i < 12; i++ {
		w, err := srv.store.CreateCreationHistory(ctx, models.CreationHistory{
			EditorialProfileID: profile.ID, Status: "published", CreationForm: "article",
			Title:     fmt.Sprintf("work-%02d", i),
			CoreClaim: fmt.Sprintf("历史主张 %02d", i),
			Content:   "内容",
		})
		if err != nil {
			t.Fatal(err)
		}
		// 固定递增时间：work-00 最旧 … work-11 最新。
		if _, err := srv.store.DB.ExecContext(ctx,
			`UPDATE creation_history SET created_at = ? WHERE id = ?`,
			fmt.Sprintf("2026-01-01 00:00:%02d", i), w.ID); err != nil {
			t.Fatal(err)
		}
	}
	req2, err := srv.automaticDiscoveryRequest(ctx, profile, "test", changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(req2.HistoricalWorks) != 10 {
		t.Fatalf("作品历史应恰好有界为 10: %d", len(req2.HistoricalWorks))
	}
	titles := map[string]bool{}
	for _, w := range req2.HistoricalWorks {
		titles[w.Title] = true
	}
	if !titles["work-11"] {
		t.Fatalf("应包含最新的 work-11: %+v", titles)
	}
	if titles["work-00"] {
		t.Fatalf("不应包含最旧的 work-00: %+v", titles)
	}
}
