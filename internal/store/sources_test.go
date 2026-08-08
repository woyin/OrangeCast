package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestDeleteSourceAndDependents_CascadesAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	p, _ := s.CreatePodcast(ctx, "https://feed.xml", "Pod", "", "")
	eps := []models.Episode{{GUID: "g1", Title: "ep1", AudioURL: "https://a.mp3"}}
	s.MergeEpisodes(ctx, p.ID, eps)
	ep, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := ep[0].ID

	// 写入关联数据（现行模型：artifact_versions + evidence + search）
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "m", "1", job.ID, `{"text":"hello"}`)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindKnowledgeCard, "groq", "m", "1", job.ID, `{"title":"t"}`)
	s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_x.mp3", "mp3", 10, "abc")
	s.IndexSearch(ctx, models.SourceEpisode, sourceID, "title", "summary", []provider.Segment{{ID: "seg-0001", Start: 0, End: 1, Text: "hello"}})

	// 删除 source（两阶段 purge 的 DB 部分）
	if err := s.DeleteSourceAndDependents(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("级联删除失败: %v", err)
	}

	// episode 本体应删
	if _, err := s.GetEpisodeByID(ctx, sourceID); err != ErrNotFound {
		t.Error("episode 应被删除")
	}
	// artifact_versions 应删
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE source_id=?`, sourceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("artifact_versions 应被级联删除，剩余 %d", n)
	}
	// evidence_audio 应删
	if _, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID); err != ErrNotFound {
		t.Error("evidence_audio 应被级联删除")
	}
	// processing_jobs 应删
	jobs, _ := s.DB.Query("SELECT 1 FROM processing_jobs WHERE source_id=?", sourceID)
	if jobs.Next() {
		jobs.Close()
		t.Error("processing_jobs 应被级联删除")
	}
	// search_index 应删
	hits, _ := s.SearchSource(ctx, "hello")
	if len(hits) != 0 {
		t.Error("search_index 应被级联删除")
	}
}

func TestDeleteSource_Nonexistent_NoError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	if err := s.DeleteSourceAndDependents(ctx, models.SourceEpisode, "missing"); err != nil {
		t.Errorf("删除不存在的 source 不应报错: %v", err)
	}
}

// TestDeleteSource_UnknownType 验证未知 source_type 时报错。
func TestDeleteSource_UnknownType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.DeleteSourceRows(ctx, "bogus", "x"); err == nil {
		t.Fatal("未知 source_type 应报错")
	}
}
