package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func seedEpisodeForJob(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, fmt.Sprintf("https://f-%d.xml", time.Now().UnixNano()), "P", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: fmt.Sprintf("g-%d", time.Now().UnixNano()), Title: "e", AudioURL: "https://a.mp3"}})
	ep, _ := s.ListEpisodes(ctx, p.ID)
	return ep[0].ID
}

func TestEnqueueJob_OptimisticClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)

	// 第一次入队应成功
	job1, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil || job1 == nil {
		t.Fatalf("首次入队应成功: %v %v", job1, err)
	}
	// 第二次入队应返回 nil（已在 queued 状态，不可重复 claim）
	job2, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
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
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

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
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
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
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)

	j1, err := s.EnqueueAnalyze(ctx, models.SourceEpisode, sourceID)
	if err != nil || j1 == nil {
		t.Fatalf("首次 analyze 入队应成功: %v %v", j1, err)
	}
	// 第二次（前一个仍 queued）应返回 nil，不重复创建
	j2, _ := s.EnqueueAnalyze(ctx, models.SourceEpisode, sourceID)
	if j2 != nil {
		t.Error("进行中的 analyze 任务不应重复创建")
	}
}

func TestEnqueueJob_AfterFailed_CanReenqueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)
	// 任务失败 → source 进 failed
	s.MarkJobFailed(ctx, job.ID, "429")
	s.UpdateEpisodeStatus(ctx, sourceID, models.StatusFailedEp)

	// failed 状态的 source 应可重新入队（乐观锁允许 failed）
	job2, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	if job2 == nil {
		t.Error("failed 状态的 source 应可重新入队")
	}
}

func TestSearchSource_ReturnsSegmentHits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)

	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 4, Text: "主权财富基金改变全球投资格局"},
		{ID: "seg-0002", Start: 4, End: 9, Text: "新加坡政府投资公司是长期投资者"},
	}
	if err := s.IndexSearch(ctx, models.SourceEpisode, sourceID, "主权财富基金", "摘要", segs); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchSource(ctx, "主权财富基金")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("应命中分段")
	}
	// 至少一个命中带 SegmentID 与时间范围
	segHit := false
	for _, h := range hits {
		if h.SegmentID == "seg-0001" && h.Start == 0 && h.End == 4 {
			segHit = true
		}
	}
	if !segHit {
		t.Errorf("搜索结果应返回实际 Segment 与时间范围，实际 %+v", hits)
	}
}

func TestSearchSource_ExcludesCandidates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	seedEpisodeForJob(t, s)
	// 未处理的 episode（Candidate）不应被索引
	hits, _ := s.SearchSource(ctx, "任何内容")
	if len(hits) != 0 {
		t.Errorf("未索引的 Candidate 不应出现在搜索结果中，实际 %+v", hits)
	}
}

// TestGetProcessingProgress 验证 running/queued 任务被正确归类到 Active/Queued。
func TestGetProcessingProgress(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	s1 := seedEpisodeForJob(t, s)
	s2 := seedEpisodeForJob(t, s) // 使用不同 feed URL

	j1, _ := s.EnqueueJob(ctx, models.SourceEpisode, s1, models.JobTranscribe)
	j2, _ := s.EnqueueJob(ctx, models.SourceEpisode, s2, models.JobTranscribe)
	s.MarkJobRunning(ctx, j1.ID) // j1 running，j2 queued

	p, err := s.GetProcessingProgress(ctx)
	if err != nil {
		t.Fatalf("GetProcessingProgress: %v", err)
	}
	if p.Active == nil || p.Active.ID != j1.ID {
		t.Errorf("Active 应为 j1，实际 %+v", p.Active)
	}
	if len(p.Queued) != 1 || p.Queued[0].ID != j2.ID {
		t.Errorf("Queued 应为 [j2]，实际 %+v", p.Queued)
	}
}

// TestSourceTitleAndStatus 验证 SourceTitle/SourceStatus 对 episode 与未知 source 的行为。
func TestSourceTitleAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)

	if got := s.SourceTitle(ctx, models.SourceEpisode, sourceID); got != "e" {
		t.Errorf("SourceTitle 应返回 episode 标题，实际 %q", got)
	}
	if got := s.SourceStatus(ctx, models.SourceEpisode, sourceID); got != models.StatusUnprocessed {
		t.Errorf("未处理 episode 应 StatusUnprocessed，实际 %q", got)
	}
	// 未知 source → 标题截断 + StatusUnprocessed
	if got := s.SourceTitle(ctx, models.SourceEpisode, "nonexistent-id"); got != "nonexist…" {
		t.Errorf("未知 source 标题应截断为 8 字符，实际 %q", got)
	}
	if got := s.SourceStatus(ctx, models.SourceEpisode, "nonexistent-id"); got != models.StatusUnprocessed {
		t.Errorf("未知 source 应 StatusUnprocessed，实际 %q", got)
	}
	// upload source → 返回文件名 + 状态
	up, _ := s.CreateUpload(ctx, "音轨.wav", "audio/wav", 10)
	if got := s.SourceTitle(ctx, models.SourceUpload, up.ID); got != "音轨.wav" {
		t.Errorf("SourceTitle 应返回 upload 文件名，实际 %q", got)
	}
	if got := s.SourceStatus(ctx, models.SourceUpload, up.ID); got != models.StatusUnprocessed {
		t.Errorf("upload 应 StatusUnprocessed，实际 %q", got)
	}
}

// TestListRecentCompleted 验证按 updated_at 倒序返回最近完成的 N 个任务。
func TestListRecentCompleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	s1 := seedEpisodeForJob(t, s)
	s2 := seedEpisodeForJob(t, s)

	j1, _ := s.EnqueueJob(ctx, models.SourceEpisode, s1, models.JobTranscribe)
	j2, _ := s.EnqueueJob(ctx, models.SourceEpisode, s2, models.JobTranscribe)
	s.MarkJobRunning(ctx, j1.ID)
	s.MarkJobRunning(ctx, j2.ID)
	if err := s.MarkJobSucceeded(ctx, j1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, j2.ID); err != nil {
		t.Fatal(err)
	}

	recent, err := s.ListRecentCompleted(ctx, 5)
	if err != nil {
		t.Fatalf("ListRecentCompleted: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("应有 2 个已完成任务，实际 %d", len(recent))
	}
	// 两个都在最近列表（顺序按 updated_at，不严格断言先后）
	ids := map[string]bool{}
	for _, j := range recent {
		ids[j.ID] = true
	}
	if !ids[j1.ID] || !ids[j2.ID] {
		t.Errorf("最近完成应包含两个任务，实际 %+v", recent)
	}
}

// TestRecordUsage 验证 AI 用量记录写入。
func TestRecordUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RecordUsage(ctx, "transcribe", "groq", "whisper", 100, 200, 0.5); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("应有 1 条用量记录，实际 %d", n)
	}
}

// TestClaimHeartbeatReset 验证 worker 领取/心跳/启动恢复的完整租约生命周期。
func TestClaimHeartbeatReset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

	// 领取 queued 任务
	claimed, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("应领取到 job，实际 %+v", claimed)
	}
	if claimed.Status != models.StatusRunning {
		t.Errorf("领取后应 running，实际 %q", claimed.Status)
	}

	// 无更多任务 → nil
	again, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil || again != nil {
		t.Errorf("无任务应返回 nil，实际 %+v err=%v", again, err)
	}

	// ListQueuedOrRunning 应含该 running 任务
	pending, _ := s.ListQueuedOrRunning(ctx)
	if len(pending) != 1 || pending[0].ID != job.ID {
		t.Errorf("ListQueuedOrRunning 应含 running 任务，实际 %+v", pending)
	}

	// 心跳续约
	if err := s.HeartbeatJob(ctx, job.ID, "60 seconds"); err != nil {
		t.Fatalf("HeartbeatJob: %v", err)
	}

	// 启动恢复：running → queued
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatalf("ResetRunningOnStartup: %v", err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusQueued {
		t.Errorf("启动恢复后应 queued，实际 %q", got.Status)
	}
}

// TestIndexSearch_ClosedDBNerror 验证数据库关闭后 IndexSearch 报错（覆盖错误分支）。
func TestIndexSearch_ClosedDBNerror(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	s.Close()
	if err := s.IndexSearch(ctx, models.SourceEpisode, "ep-1", "T", "S", []provider.Segment{{ID: "s1", Start: 0, End: 1, Text: "x"}}); err == nil {
		t.Fatal("数据库关闭后 IndexSearch 应报错")
	}
}

// TestGetJob_NotFound 验证查询不存在的 job 返回 ErrNotFound。
func TestGetJob_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	if _, err := s.GetJob(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("不存在的 job 应 ErrNotFound，实际 %v", err)
	}
}

// TestClaimNextJob_ReclaimsExpiredLease 验证租约过期的 running 任务可被重新领取。
func TestClaimNextJob_ReclaimsExpiredLease(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForJob(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)

	// 把 lease_until 改为过去 → 视为 stale，可被重新领取
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET lease_until = datetime('now', '-1 hour') WHERE id = ?`, job.ID); err != nil {
		t.Fatalf("设置过期租约: %v", err)
	}
	claimed, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("应重新领取过期租约的 job，实际 %+v", claimed)
	}
}
