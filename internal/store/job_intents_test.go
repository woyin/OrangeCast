package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// seedIntentEpisode 建一个 unprocessed 单集供幂等入队测试使用。
func seedIntentEpisode(t *testing.T, s *Store, guid string) string {
	t.Helper()
	podcast, err := s.CreatePodcast(t.Context(), "https://feed.example.com/"+guid+".xml", "意图播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: guid, Title: "意图单集", AudioURL: "https://cdn.example.com/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	return eps[0].ID
}

// TestEnqueueJobIdempotent_ConcurrentCAS 并发同意图入队只有一个生效（数据库唯一性 CAS）。
func TestEnqueueJobIdempotent_ConcurrentCAS(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-1")
	spec := JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest,
		IntentID: "digest:episode:x", InputSnapshotJSON: `{"transcript_version":1}`, ConfigVersion: "v1",
	}

	const n = 16
	var mu sync.Mutex
	var createdIDs []string
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, created, err := s.EnqueueJobIdempotent(t.Context(), spec)
			if err != nil {
				t.Errorf("并发入队报错: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if created {
				createdIDs = append(createdIDs, job.ID)
			}
		}()
	}
	wg.Wait()

	if len(createdIDs) != 1 {
		t.Fatalf("并发入队应只创建 1 个任务，实际 %d: %v", len(createdIDs), createdIDs)
	}
	if _, err := s.GetActiveJobByIntent(t.Context(), models.SourceEpisode, epID, models.JobDigest, "digest:episode:x"); err != nil {
		t.Fatalf("活跃意图应可读取: %v", err)
	}
}

// TestEnqueueJobIdempotent_IntentLifecycle 活跃意图去重；失败后同意图可重试；不同意图并行。
func TestEnqueueJobIdempotent_IntentLifecycle(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-2")
	spec := JobIntentSpec{SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest, IntentID: "intent-a"}

	job, created, err := s.EnqueueJobIdempotent(t.Context(), spec)
	if err != nil || !created {
		t.Fatalf("首次入队应创建: %v %v", created, err)
	}
	// 活跃期间重复入队 → 命中已有任务
	if _, created, err := s.EnqueueJobIdempotent(t.Context(), spec); err != nil || created {
		t.Fatalf("活跃意图不应重复创建: %v %v", created, err)
	}
	// 不同意图（显式重新生成）允许并行/后续入队
	spec2 := spec
	spec2.IntentID = "intent-b"
	if _, created, err := s.EnqueueJobIdempotent(t.Context(), spec2); err != nil || !created {
		t.Fatalf("新意图应允许入队: %v %v", created, err)
	}
	// 失败后意图退出活跃集 → 同意图可再次入队
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobFailed(t.Context(), job.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.EnqueueJobIdempotent(t.Context(), spec); err != nil || !created {
		t.Fatalf("失败后同意图应可重试: %v %v", created, err)
	}
}

// TestJobExecution_CompatAndRoundTrip 旧行兼容读取；快照/断点/结果读写闭环。
func TestJobExecution_CompatAndRoundTrip(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-3")

	// 旧语义入队（无意图、无契约列）→ 兼容读取为空值
	legacy, err := s.EnqueueJob(t.Context(), models.SourceEpisode, epID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(t.Context(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.IntentID != "" || exec.ResultState != "" || exec.CheckpointJSON != "" {
		t.Fatalf("旧任务契约数据应为空默认值: %+v", exec)
	}

	// 新语义：入队冻结输入 → 断点 → 结果（先于终态）
	job, created, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest,
		IntentID: "intent-rt", InputSnapshotJSON: `{"transcript_version":2}`, ConfigVersion: "cfg-7",
		ConfiguredProvider: "groq", ConfiguredModel: "llama-3",
	})
	if err != nil || !created {
		t.Fatalf("入队失败: %v %v", created, err)
	}
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobCheckpoint(t.Context(), job.ID, `{"stage":"compose"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(t.Context(), job.ID, `{"digest":"d1"}`, models.JobResultComplete); err != nil {
		t.Fatal(err)
	}
	// complete 结果不可被覆盖（防重放）
	if err := s.SaveJobResult(t.Context(), job.ID, `{"digest":"d2"}`, models.JobResultComplete); err == nil {
		t.Fatal("complete 结果不应被覆盖")
	}
	exec, err = s.GetJobExecution(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.IntentID != "intent-rt" || exec.InputSnapshotJSON != `{"transcript_version":2}` ||
		exec.ConfigVersion != "cfg-7" || exec.ConfiguredModel != "llama-3" ||
		exec.CheckpointJSON != `{"stage":"compose"}` || exec.ResultJSON != `{"digest":"d1"}` ||
		exec.ResultState != models.JobResultComplete {
		t.Fatalf("契约数据读写不符: %+v", exec)
	}
}

// TestJobResult_UnknownState unknown（远端结果未知）可保存，且可被后续 complete 替代。
func TestJobResult_UnknownState(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-4")
	job, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest, IntentID: "intent-u",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(t.Context(), job.ID, ``, "bogus"); err == nil {
		t.Fatal("非法结果状态应被拒绝")
	}
	if err := s.SaveJobResult(t.Context(), job.ID, ``, models.JobResultUnknown); err != nil {
		t.Fatalf("unknown 应可保存: %v", err)
	}
	if err := s.SaveJobResult(t.Context(), job.ID, `{"ok":true}`, models.JobResultComplete); err != nil {
		t.Fatalf("unknown 应可被 complete 替代: %v", err)
	}
	exec, _ := s.GetJobExecution(t.Context(), job.ID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("最终应为 complete: %+v", exec)
	}
}

// TestJobResult_SurvivesRestartSimulated 结果写入后进程中断：重启回收后结果仍在、不可重放。
func TestJobResult_SurvivesRestartSimulated(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-5")
	job, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest, IntentID: "intent-r",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(t.Context(), job.ID, `{"digest":"keep"}`, models.JobResultComplete); err != nil {
		t.Fatal(err)
	}
	// 模拟崩溃：running → queued，结果列不受影响
	if err := s.ResetRunningOnStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(t.Context(), job.ID)
	if err != nil || got.Status != models.StatusQueued {
		t.Fatalf("重启后应回到 queued: %v %+v", err, got)
	}
	exec, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || exec.ResultState != models.JobResultComplete || exec.ResultJSON != `{"digest":"keep"}` {
		t.Fatalf("恢复后结果应保留: %v %+v", err, exec)
	}
}

// TestEnqueueJobIdempotent_RejectsEmptyIntent 空意图显式拒绝（旧语义走 EnqueueJob）。
func TestEnqueueJobIdempotent_RejectsEmptyIntent(t *testing.T) {
	s := newTestStore(t)
	epID := seedIntentEpisode(t, s, "intent-6")
	if _, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobDigest,
	}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("空意图应显式拒绝: %v", err)
	}
}

// TestUsageReceipt_DedupAndUnknownPrice 同一 receipt 重放不重复累计；未知价格记 NULL 不冒充免费。
func TestUsageReceipt_DedupAndUnknownPrice(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	r := models.UsageReceipt{
		ReceiptID: "job-1:analysis", AttemptID: "job-1:0", Operation: "analysis",
		Provider: "groq", Model: "llama-test", InputUnits: 100, OutputUnits: 40,
		CostKnown: true, CostCents: 7,
	}
	if err := s.RecordUsageReceipt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageReceipt(ctx, r); err != nil {
		t.Fatalf("重放不应报错: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, r.ReceiptID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("同一 receipt 只应有一行，实际 %d", n)
	}
	var cost *float64
	if err := s.DB.QueryRowContext(ctx, `SELECT estimated_cost FROM usage_records WHERE receipt_id=?`, r.ReceiptID).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost == nil || *cost != 7 {
		t.Fatalf("已知费用应为 7 分，实际 %v", cost)
	}

	// 未知价格：estimated_cost NULL
	unknown := r
	unknown.ReceiptID = "job-2:analysis"
	unknown.CostKnown = false
	if err := s.RecordUsageReceipt(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT estimated_cost FROM usage_records WHERE receipt_id=?`, unknown.ReceiptID).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != nil {
		t.Fatalf("未知价格不得记为数值（不冒充免费）: %v", *cost)
	}
}

// TestResolveUsageCost 双零=确定零成本；有价格按计算；无价格 unknown。
func TestResolveUsageCost(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	cents, known, err := s.ResolveUsageCost(ctx, "groq", "m0", 0, 0)
	if err != nil || !known || cents != 0 {
		t.Fatalf("零单位应为确定零成本: %d %v %v", cents, known, err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "priced", InputCentsPerMillion: 50, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	cents, known, err = s.ResolveUsageCost(ctx, "groq", "priced", 2_000_000, 0)
	if err != nil || !known || cents != 100 {
		t.Fatalf("已知价格应计算费用（向上取整）: %d %v %v", cents, known, err)
	}
	_, known, err = s.ResolveUsageCost(ctx, "groq", "unpriced", 10, 10)
	if err != nil || known {
		t.Fatalf("未配置价格应 unknown: %v %v", known, err)
	}
}
