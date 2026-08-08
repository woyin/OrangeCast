package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestPurgeSourceDerivedData 验证 Purge 时删除派生数据：
// KeyPoint（含 FTS）、Paraphrase、StudySession。
func TestPurgeSourceDerivedData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	// 建 episode 作为 source
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "要点"}}

	// 1) KeyPoint
	card := &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "关键要点", Description: "d", Citations: []string{"seg-0001"}}},
	}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, sourceID, "ep", 1, card, segs); err != nil {
		t.Fatalf("IndexKeyPoints: %v", err)
	}
	// 2) Paraphrase
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, sourceID, "问答", "讲解", "groq", "m", []string{"seg-0001"}, segs); err != nil {
		t.Fatalf("CreateParaphrase: %v", err)
	}
	// 3) StudySession
	if _, err := s.CreateStudySession(ctx, models.SourceEpisode, sourceID, "会话"); err != nil {
		t.Fatalf("CreateStudySession: %v", err)
	}

	// Purge-derived-data 删除
	if err := s.DeleteKeyPointsForSource(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("DeleteKeyPointsForSource: %v", err)
	}
	if err := s.DeleteParaphrasesForSource(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("DeleteParaphrasesForSource: %v", err)
	}
	if err := s.DeleteStudySessionsForSource(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("DeleteStudySessionsForSource: %v", err)
	}

	// 验证全部清空
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_index WHERE source_id=?`, sourceID).Scan(&n)
	if n != 0 {
		t.Errorf("KeyPoint 应删除，剩余 %d", n)
	}
	var kpFts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_search`).Scan(&kpFts)
	if kpFts != 0 {
		t.Errorf("KeyPoint FTS 应删除，剩余 %d", kpFts)
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paraphrases WHERE source_id=?`, sourceID).Scan(&n)
	if n != 0 {
		t.Errorf("Paraphrase 应删除，剩余 %d", n)
	}
	sessions, _ := s.ListStudySessions(ctx, models.SourceEpisode, sourceID)
	if len(sessions) != 0 {
		t.Errorf("StudySession 应删除，剩余 %d", len(sessions))
	}
}
