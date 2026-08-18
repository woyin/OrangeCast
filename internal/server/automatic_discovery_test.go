package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
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
	return &provider.ScoutResult{Proposals: []provider.ScoutProposal{{Kind: "fresh", Title: "学习成果如何改变创作判断", Thesis: "新学习成果应先经质量闸门，再形成 Owner 可承担的创作方向。", Audience: "知识工作者", Rationale: "跨 Episode 的共同模式", CandidateKeyPointIDs: []string{first.KeyPointID, second.KeyPointID}}}}, nil
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
		if err := srv.store.IndexKeyPoints(t.Context(), models.SourceEpisode, episode.ID, episode.Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: episode.Title + "洞见一", Citations: []string{"seg-1"}}, {Content: episode.Title + "洞见二", Citations: []string{"seg-2"}}, {Content: episode.Title + "洞见三", Citations: []string{"seg-3"}}}}, []provider.Segment{{ID: "seg-1", End: 1}, {ID: "seg-2", Start: 1, End: 2}, {ID: "seg-3", Start: 2, End: 3}}); err != nil {
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
	if err := srv.store.SetProposalBatchStatus(t.Context(), batches[0].ProposalBatchID, "completed", "reviewed"); err != nil {
		t.Fatal(err)
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
	scout.err = errors.New("provider unavailable")
	if err := srv.RunAutomaticDiscovery(t.Context()); err == nil {
		t.Fatal("provider failure must surface from a scheduled run")
	}
	var failed int
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM proposal_batches WHERE editorial_profile_id=? AND status='failed' AND failure_reason LIKE '%provider unavailable%'`, profile.ID).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("failed automatic discovery must remain visible to the Owner: count=%d err=%v", failed, err)
	}
}
