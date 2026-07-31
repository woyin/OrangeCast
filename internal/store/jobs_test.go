package store

import (
	"context"
	"testing"

	"github.com/breestealth/wisepod/internal/models"
)

func seedEpisodeForJob(t *testing.T, s *Store, userID string) string {
	t.Helper()
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, userID, "https://f.xml", "P", "", "")
	s.MergeEpisodes(ctx, p.ID, userID, []models.Episode{{GUID: "g", Title: "e", AudioURL: "https://a.mp3"}})
	ep, _ := s.ListEpisodes(ctx, p.ID)
	return ep[0].ID
}

func TestEnqueueJob_OptimisticClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s, u.ID)

	// 第一次入队应成功
	job1, err := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil || job1 == nil {
		t.Fatalf("首次入队应成功: %v %v", job1, err)
	}
	// 第二次入队应返回 nil（已在 queued 状态，不可重复 claim）
	job2, err := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	if job2 != nil {
		t.Error("已 queued 的 source 不应重复入队")
	}
}

func TestMarkJobRunning_PreventsDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s, u.ID)
	job, _ := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)

	// 第一次 queued→running 应成功
	ok, err := s.MarkJobRunning(ctx, job.ID)
	if err != nil || !ok {
		t.Fatalf("首次 MarkJobRunning 应成功: %v %v", ok, err)
	}
	// 第二次应失败（已是 running，防重复处理）
	ok2, _ := s.MarkJobRunning(ctx, job.ID)
	if ok2 {
		t.Error("running 状态不应再次 claim")
	}
}

func TestMarkJobSucceeded_IdempotentGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s, u.ID)
	job, _ := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)

	// running→succeeded 成功
	if err := s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	// 已 succeeded 再调应无效果（幂等，不报错但也不改状态）
	s.MarkJobSucceeded(ctx, job.ID)
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusSucceeded {
		t.Errorf("应保持 succeeded，实际 %s", got.Status)
	}
}

func TestEnqueueAnalyze_Dedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s, u.ID)

	j1, err := s.EnqueueAnalyze(ctx, u.ID, models.SourceEpisode, sourceID)
	if err != nil || j1 == nil {
		t.Fatalf("首次 analyze 入队应成功: %v %v", j1, err)
	}
	// 第二次（前一个仍 queued）应返回 nil，不重复创建
	j2, _ := s.EnqueueAnalyze(ctx, u.ID, models.SourceEpisode, sourceID)
	if j2 != nil {
		t.Error("进行中的 analyze 任务不应重复创建")
	}
}

func TestEnqueueJob_AfterFailed_CanReenqueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s, u.ID)
	job, _ := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)
	// 任务失败 → source 进 failed
	s.MarkJobFailed(ctx, job.ID, "429")
	s.UpdateEpisodeStatus(ctx, sourceID, models.StatusFailedEp)

	// failed 状态的 source 应可重新入队（乐观锁允许 failed）
	job2, err := s.EnqueueJob(ctx, u.ID, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	if job2 == nil {
		t.Error("failed 状态的 source 应可重新入队")
	}
}
