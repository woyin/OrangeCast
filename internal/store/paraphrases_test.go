package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestParaphrase_RecentNRetention (ADR-0018 R2)
// 每个锚点保留最近 3 次；同锚点第 4 次淘汰最旧的；不同锚点独立保留。
func TestParaphrase_RecentNRetention(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	srcType, srcID := models.SourceEpisode, eps[0].ID
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "第一段"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "第二段"},
		{ID: "seg-0003", Start: 10, End: 15, Text: "第三段"},
	}

	refsA := []string{"seg-0001", "seg-0002"} // anchor A
	// 同锚点插入 4 次
	for i := 0; i < 4; i++ {
		if _, err := s.CreateParaphrase(ctx, srcType, srcID, "q"+itoa(i), "讲解"+itoa(i), "groq", "m", refsA, segs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListParaphrasesForAnchor(ctx, srcType, srcID, ParaphraseAnchor(refsA))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("同锚点应保留最近 3 条，实际 %d", len(got))
	}
	// 最近在前：讲解3,2,1；讲解0 应被淘汰
	if got[0].Body != "讲解3" || got[2].Body != "讲解1" {
		t.Errorf("淘汰最旧失败，顺序=%v", []string{got[0].Body, got[1].Body, got[2].Body})
	}

	// 不同锚点独立保留
	refsB := []string{"seg-0003"}
	if _, err := s.CreateParaphrase(ctx, srcType, srcID, "q", "讲解B", "groq", "m", refsB, segs); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListParaphrasesForSource(ctx, srcType, srcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("跨锚点总数应为 4（anchor A 的 3 + anchor B 的 1），实际 %d", len(all))
	}

	// relation_kind 必须为 reference
	for _, r := range all {
		if r.RelationKind != models.RelationReference {
			t.Errorf("paraphrase.relation_kind 应为 reference，实际 %s", r.RelationKind)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
