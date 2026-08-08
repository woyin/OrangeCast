package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestClosedDB_CoversErrorBranches 通过关闭数据库触发各 store 方法的首批错误分支，
// 覆盖一般无法通过正常路径触达的 DB I/O 错误（closed-DB 法）。
func TestClosedDB_CoversErrorBranches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "closed@example.com")
	sourceID := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "m", "1", job.ID, `{"text":"x"}`)

	// 预置并行数据（供 GetParaphrase/GetCurrentVersion 等在关闭前查询）
	pp := &ParaphraseRow{ID: "pp1", SourceType: models.SourceEpisode, SourceID: sourceID}
	_ = pp

	s.Close()

	// artifacts
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "g", "m", "1", job.ID, `{}`); err == nil {
		t.Error("关闭后 CreateArtifactVersion 应报错")
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript); err == nil {
		t.Error("关闭后 GetCurrentVersion 应报错")
	}
	// evidence
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "p", "mp3", 1, "a"); err == nil {
		t.Error("关闭后 UpsertEvidenceAudio 应报错")
	}
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err == nil {
		t.Error("关闭后 CreatePurgeIntent 应报错")
	}
	if err := s.DeleteSourceRows(ctx, models.SourceEpisode, sourceID); err == nil {
		t.Error("关闭后 DeleteSourceRows 应报错")
	}
	// jobs
	if _, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe); err == nil {
		t.Error("关闭后 EnqueueJob 应报错")
	}
	if err := s.IndexSearch(ctx, models.SourceEpisode, sourceID, "t", "s", []provider.Segment{{ID: "s1", Start: 0, End: 1, Text: "x"}}); err == nil {
		t.Error("关闭后 IndexSearch 应报错")
	}
	if _, err := s.ClaimNextJob(ctx, "30 seconds"); err == nil {
		t.Error("关闭后 ClaimNextJob 应报错")
	}
	if _, err := s.ListRecentCompleted(ctx, 5); err == nil {
		t.Error("关闭后 ListRecentCompleted 应报错")
	}
	// narrations
	if _, err := s.CreateNarration(ctx, models.SourceEpisode, sourceID, "hl", "v", "m", "p", 1, 10, "k"); err == nil {
		t.Error("关闭后 CreateNarration 应报错")
	}
	// podcasts
	if _, err := s.CreatePodcast(ctx, "https://x.xml", "t", "d", ""); err == nil {
		t.Error("关闭后 CreatePodcast 应报错")
	}
	if _, err := s.MergeEpisodes(ctx, "pod", []models.Episode{{GUID: "g", Title: "e", AudioURL: "https://a.mp3"}}); err == nil {
		t.Error("关闭后 MergeEpisodes 应报错")
	}
	// paraphrases
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, sourceID, "q", "b", "p", "m", []string{"s1"}, []provider.Segment{{ID: "s1", Start: 0, End: 1, Text: "x"}}); err == nil {
		t.Error("关闭后 CreateParaphrase 应报错")
	}
	if _, err := s.GetParaphrase(ctx, "pp1"); err == nil {
		t.Error("关闭后 GetParaphrase 应报错")
	}
}
