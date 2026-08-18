package store

import (
	"context"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestEvaluateAutomaticDiscoveryEnforcesWindowDebounceBackpressureAndDailyLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "discovery@example.com")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "自动发现画像"})
	if err != nil {
		t.Fatal(err)
	}
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/discovery.xml", "学习播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "one", Title: "第一集", AudioURL: "https://example.com/one.mp3"}, {GUID: "two", Title: "第二集", AudioURL: "https://example.com/two.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, episode := range episodes {
		card := &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{
			{Content: episode.Title + "观点一", Citations: []string{"seg-1"}},
			{Content: episode.Title + "观点二", Citations: []string{"seg-2"}},
			{Content: episode.Title + "观点三", Citations: []string{"seg-3"}},
		}}
		segments := []provider.Segment{{ID: "seg-1", End: 1}, {ID: "seg-2", Start: 1, End: 2}, {ID: "seg-3", Start: 2, End: 3}}
		if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episode.ID, episode.Title, 1, card, segments); err != nil {
			t.Fatal(err)
		}
	}
	keyPoints, _, err := s.ListKeyPoints(ctx, 1, 20)
	if err != nil || len(keyPoints) != 6 {
		t.Fatalf("seed discovery KeyPoints: count=%d err=%v", len(keyPoints), err)
	}
	for _, keyPoint := range keyPoints {
		if err := s.SetKeyPointQualityStatus(ctx, keyPoint.ID, models.KeyPointReady); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE material_changes SET created_at=datetime('now','-31 minutes')`); err != nil {
		t.Fatal(err)
	}
	settings := models.DiscoverySettings{EditorialProfileID: profile.ID, Enabled: true, Provider: "fake", Model: "fake-scout", DailyLimit: 1, DebounceMinutes: 30}
	if err := s.SetDiscoverySettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	decision, err := s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || !decision.Ready || decision.MaterialChangeCount != 6 || decision.SourceCount != 2 {
		t.Fatalf("six reviewed changes across two episodes should become ready: decision=%+v err=%v", decision, err)
	}
	changes, err := s.ListDiscoveryWindowChanges(ctx, profile.ID, decision.WindowStartAt)
	if err != nil || len(changes) != 6 {
		t.Fatalf("window snapshot must contain exactly eligible seed changes: count=%d err=%v", len(changes), err)
	}

	batch, err := s.CreateProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "window-one", MaterialSnapshotJSON: `["snapshot"]`})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || decision.Ready || decision.Reason != "unprocessed proposal batch exists" {
		t.Fatalf("open batch must apply attention backpressure: decision=%+v err=%v", decision, err)
	}
	if err := s.SetProposalBatchStatus(ctx, batch.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	// Keep the prior batch in today's quota while making the original changes
	// visible to the post-batch window; this isolates the daily guardrail.
	if _, err := s.DB.ExecContext(ctx, `UPDATE proposal_batches SET created_at=datetime('now','-2 hours') WHERE id=?`, batch.ID); err != nil {
		t.Fatal(err)
	}
	decision, err = s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || decision.Ready || decision.Reason != "daily automatic batch limit reached" {
		t.Fatalf("daily quota must stop a second automatic batch: decision=%+v err=%v", decision, err)
	}
}

func TestEvaluateAutomaticDiscoveryRespectsDebounceAndProfileExclusion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "discovery-exclusion@example.com")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "相关性画像"})
	if err != nil {
		t.Fatal(err)
	}
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/exclusion.xml", "学习播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "one", Title: "第一集", AudioURL: "https://example.com/one.mp3"}, {GUID: "two", Title: "第二集", AudioURL: "https://example.com/two.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	for _, episode := range episodes {
		if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episode.ID, episode.Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: episode.Title + "观点", Citations: []string{"seg"}}}}, []provider.Segment{{ID: "seg", End: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	keyPoints, _, _ := s.ListKeyPoints(ctx, 1, 10)
	for _, keyPoint := range keyPoints {
		if err := s.SetKeyPointQualityStatus(ctx, keyPoint.ID, models.KeyPointReady); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDiscoverySettings(ctx, models.DiscoverySettings{EditorialProfileID: profile.ID, Enabled: true, Provider: "fake", Model: "fake", DailyLimit: 1, DebounceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	decision, err := s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || decision.Ready || decision.Reason != "fewer than six new reviewed material changes" {
		t.Fatalf("two changes should not meet the six-change floor: decision=%+v err=%v", decision, err)
	}
	for i := 0; i < 4; i++ {
		keyPoint := keyPoints[i%len(keyPoints)]
		if _, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: keyPoint.ID, SourceType: string(keyPoint.SourceType), SourceID: keyPoint.SourceID, ChangeKind: "additional", SnapshotHash: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	decision, err = s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || decision.Ready || decision.Reason != "discovery debounce active" {
		t.Fatalf("recent learning should defer discovery for the debounce window: decision=%+v err=%v", decision, err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE material_changes SET created_at=datetime('now','-31 minutes')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEditorialRelevance(ctx, models.EditorialRelevance{EditorialProfileID: profile.ID, KeyPointID: keyPoints[0].ID, Assessment: "irrelevant", Rationale: "not for this profile"}); err != nil {
		t.Fatal(err)
	}
	decision, err = s.EvaluateAutomaticDiscovery(ctx, profile.ID, time.Now().UTC())
	if err != nil || decision.Ready || decision.MaterialChangeCount >= 6 || decision.Reason != "fewer than six new reviewed material changes" {
		t.Fatalf("excluded material must not count toward a profile discovery window: decision=%+v err=%v", decision, err)
	}
}
