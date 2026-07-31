package store

import (
	"context"
	"testing"

	"github.com/breestealth/wisepod/internal/models"
)

func TestDeleteSourceAndDependents_CascadesAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	p, _ := s.CreatePodcast(ctx, u.ID, "https://feed.xml", "Pod", "", "")
	eps := []models.Episode{{GUID: "g1", Title: "ep1", AudioURL: "https://a.mp3"}}
	s.MergeEpisodes(ctx, p.ID, u.ID, eps)
	ep, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := ep[0].ID

	// 写入关联数据
	s.UpsertTranscript(ctx, u.ID, models.SourceEpisode, sourceID, "en", "hello", "[{\"start\":0}]")
	s.UpsertAnalysis(ctx, u.ID, models.SourceEpisode, sourceID, "title", "summary", "{}")
	s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.IndexSearch(ctx, u.ID, models.SourceEpisode, sourceID, "title", "hello")

	// 删除 source
	if err := s.DeleteSourceAndDependents(ctx, u.ID, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("级联删除失败: %v", err)
	}

	// episode 本体应删
	if _, err := s.GetEpisodeByID(ctx, sourceID); err != ErrNotFound {
		t.Error("episode 应被删除")
	}
	// transcript 应删（孤儿数据清理，第1题）
	if _, err := s.GetTranscript(ctx, u.ID, models.SourceEpisode, sourceID); err != ErrNotFound {
		t.Error("transcript 应被级联删除")
	}
	// analysis 应删
	if _, err := s.GetAnalysis(ctx, u.ID, models.SourceEpisode, sourceID); err != ErrNotFound {
		t.Error("analysis 应被级联删除")
	}
	// processing_jobs 应删
	jobs, _ := s.DB.Query("SELECT 1 FROM processing_jobs WHERE source_id=?", sourceID)
	if jobs.Next() {
		jobs.Close()
		t.Error("processing_jobs 应被级联删除")
	}
}

func TestDeleteSource_WrongUser_NoEffect(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u1 := seedUser(t, s, "a@b.com")
	u2 := seedUser(t, s, "c@d.com")

	p, _ := s.CreatePodcast(ctx, u1.ID, "https://f.xml", "P", "", "")
	s.MergeEpisodes(ctx, p.ID, u1.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}})
	ep, _ := s.ListEpisodes(ctx, p.ID)

	// u2 试图删 u1 的 source（带 user_id 校验，应无效）
	s.DeleteSourceAndDependents(ctx, u2.ID, models.SourceEpisode, ep[0].ID)
	if _, err := s.GetEpisodeByID(ctx, ep[0].ID); err != nil {
		t.Error("其他用户不应能删除，episode 应仍存在")
	}
}
