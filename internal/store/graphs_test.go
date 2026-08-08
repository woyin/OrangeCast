package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// seedKpEpisode 建一个含单个 KeyPoint 的 episode，返回 sourceID。
func seedKpEpisode(t *testing.T, s *Store, feedURL, guid, title, kpContent, kpDesc string) string {
	t.Helper()
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, feedURL, title, "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: guid, Title: title, AudioURL: "https://a.mp3"}})
	ep, _ := s.ListEpisodes(ctx, p.ID)
	card := &provider.KnowledgeCard{
		Title:     title,
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: kpContent, Description: kpDesc, Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, ep[0].ID, title, 1, card, segs); err != nil {
		t.Fatalf("IndexKeyPoints: %v", err)
	}
	return ep[0].ID
}

// TestGetKpGraph_CollectionEdges 验证同 Collection 的 KeyPoint 两两产生实线边。
func TestGetKpGraph_CollectionEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)

	s1 := seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "要点甲", "甲描述")
	s2 := seedKpEpisode(t, s, "https://f2.xml", "g2", "ep2", "要点乙", "乙描述")

	col, err := s.CreateCollection(ctx, "专题", "desc")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	// 两个 KeyPoint 的 citations_json 都是 ["seg-0001"],分别加入 collection
	if err := s.AddToCollection(ctx, col.ID, models.SourceEpisode, s1, `["seg-0001"]`, 0, 5, "ep1", ""); err != nil {
		t.Fatalf("AddToCollection s1: %v", err)
	}
	if err := s.AddToCollection(ctx, col.ID, models.SourceEpisode, s2, `["seg-0001"]`, 0, 5, "ep2", ""); err != nil {
		t.Fatalf("AddToCollection s2: %v", err)
	}

	gd, err := s.GetKpGraph(ctx)
	if err != nil {
		t.Fatalf("GetKpGraph: %v", err)
	}
	if len(gd.Nodes) != 2 {
		t.Fatalf("应有 2 个节点，实际 %d", len(gd.Nodes))
	}
	if len(gd.Collections) != 1 {
		t.Fatalf("应有 1 个 Collection，实际 %d", len(gd.Collections))
	}
	hasCollection := false
	for _, l := range gd.Links {
		if l.Type == "collection" {
			hasCollection = true
		}
	}
	if !hasCollection {
		t.Error("同 Collection 的 KeyPoint 应产生 collection 实线边")
	}
}

// TestGetKpGraph_SimilarEdges 验证跨 Episode、内容相似（词重叠≥0.3）的 KeyPoint 产生虚线建议边。
func TestGetKpGraph_SimilarEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)

	// 两个内容高度相似（共享"主权财富基金"+"长期投资者"）的 KeyPoint，跨 Episode
	seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "主权财富基金改变全球投资", "长期投资者")
	seedKpEpisode(t, s, "https://f2.xml", "g2", "ep2", "主权财富基金是长期投资者", "全球投资")

	gd, err := s.GetKpGraph(ctx)
	if err != nil {
		t.Fatalf("GetKpGraph: %v", err)
	}
	hasSimilar := false
	for _, l := range gd.Links {
		if l.Type == "similar" {
			hasSimilar = true
		}
	}
	if !hasSimilar {
		t.Error("内容相似的跨 Episode KeyPoint 应产生 similar 虚线边")
	}
}

// mustSeedUser 确保数据库有 Owner（部分表外键依赖 users）。
func mustSeedUser(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.ClaimOwner(context.Background(), "g@example.com", "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("认领 Owner: %v", err)
	}
}
