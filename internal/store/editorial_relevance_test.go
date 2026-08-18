package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestDefaultProfileAndOwnerRelevanceOverride(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "relevance@example.com")
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil || profile.Name != defaultEditorialProfileName {
		t.Fatalf("default profile should be created: profile=%+v err=%v", profile, err)
	}
	again, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil || again.ID != profile.ID {
		t.Fatalf("default profile must be idempotent: first=%+v again=%+v err=%v", profile, again, err)
	}
	podcast, _ := s.CreatePodcast(ctx, "https://feed.example.com/relevance.xml", "相关性播客", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "rel", Title: "相关性", AudioURL: "https://cdn.example.com/rel.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	s.IndexKeyPoints(ctx, models.SourceEpisode, episodes[0].ID, episodes[0].Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}})
	points, _, _ := s.ListKeyPoints(ctx, 1, 10)
	if err := s.SetEditorialRelevance(ctx, models.EditorialRelevance{EditorialProfileID: profile.ID, KeyPointID: points[0].ID, Assessment: "relevant", OwnerOverride: "excluded", Rationale: "不适合当前表达"}); err != nil {
		t.Fatal(err)
	}
	if eligible, err := s.IsKeyPointEligibleForProfile(ctx, profile.ID, points[0].ID); err != nil || eligible {
		t.Fatalf("owner exclusion must block discovery: eligible=%v err=%v", eligible, err)
	}
	if err := s.SetEditorialRelevance(ctx, models.EditorialRelevance{EditorialProfileID: profile.ID, KeyPointID: points[0].ID, Assessment: "relevant", Rationale: "模型重评"}); err != nil {
		t.Fatal(err)
	}
	relevance, err := s.GetEditorialRelevance(ctx, profile.ID, points[0].ID)
	if err != nil || relevance.OwnerOverride != "excluded" {
		t.Fatalf("assessment must not overwrite owner decision: relevance=%+v err=%v", relevance, err)
	}
}
