package queue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// heldReservationOf 读取 job 的预占行（无则 nil）。
func heldReservationOf(t *testing.T, s *store.Store, jobID string) *models.BudgetReservation {
	t.Helper()
	res, err := s.ListBudgetReservations(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.JobID == jobID {
			return r
		}
	}
	return nil
}

// TestWorker_RecoveryFastPathSettlesBudget R02：结果已持久化的任务恢复复用时，
// 同步结算已 held 的预占——不重调 Provider，也不把预估永久留在占用中。
func TestWorker_RecoveryFastPathSettlesBudget(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}

	providerCalled := false
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		providerCalled = true
		return nil, nil
	}

	job, created, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobDigest,
		IntentID: "digest:budget-recovery",
	})
	if err != nil || !created {
		t.Fatalf("入队失败: %v %v", created, err)
	}
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	// 模拟"结果落库后、终态写入前"持有预占的进程中断。
	if _, err := s.HoldBudget(ctx, job.ID, "episode_digest", false, "groq", "m", 120_000, 30_000); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(ctx, job.ID, `{"digest_id":"d-1"}`, models.JobResultComplete); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}

	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("恢复执行失败: %v", err)
	}
	if providerCalled {
		t.Fatal("命中 complete 结果时不得调用 Provider")
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil || got.Status != models.StatusSucceeded {
		t.Fatalf("任务应成功终结: %v %+v", err, got)
	}
	res := heldReservationOf(t, s, job.ID)
	if res == nil || res.Status != models.BudgetSettled {
		t.Fatalf("恢复复用应结算预占: %+v", res)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total != 0 {
		t.Fatalf("结算后预估应移出占用: total=%d err=%v", total, err)
	}
}

// TestWorker_FailureWithoutReceiptKeepsPendingBudget R02：失败且无已知费用 receipt
// （不能据此推断远端未调用）时，预占转入 pending_remote 保留待处理，
// 预估继续计入月度占用，不假定未知调用免费；重复收尾幂等。
func TestWorker_FailureWithoutReceiptKeepsPendingBudget(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: provider.EffectiveModel("groq", "", "analyze"), InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{err: errors.New("远端调用中断，结果未知")}, Highlight: &fakeHighlight{}}, nil
	}

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("失败任务不应报周期错误: %v", err)
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusFailed {
		t.Fatalf("任务应标记失败: %+v", job)
	}
	res := heldReservationOf(t, s, job.ID)
	if res == nil || res.Status != models.BudgetPendingRemote {
		t.Fatalf("无已知结果的失败应保留 pending_remote 预占: %+v", res)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total == 0 {
		t.Fatalf("pending_remote 预估应继续计入占用: total=%d err=%v", total, err)
	}
	// 重复收尾幂等：状态不变、占用不变。
	w.finalizeJobBudgetOnFailure(ctx, job)
	res2 := heldReservationOf(t, s, job.ID)
	if res2 == nil || res2.Status != models.BudgetPendingRemote || res2.ID != res.ID {
		t.Fatalf("重复收尾应幂等: %+v vs %+v", res2, res)
	}
}

// TestWorker_FinalizeOnFailure_SettlesKnownReceipt R02：失败但已获得远端结果
// （有已知费用 receipt）时按实际费用结算，不重复计费。
func TestWorker_FinalizeOnFailure_SettlesKnownReceipt(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-known", "analyze", false, "groq", "m", 100_000, 20_000); err != nil {
		t.Fatal(err)
	}
	// 远端调用完成并已落账（结果已知），后续校验失败导致任务失败。
	if err := s.RecordUsageReceipt(ctx, models.UsageReceipt{
		ReceiptID: "job-known:analysis", Operation: "analysis", Provider: "groq", Model: "m",
		InputUnits: 100_000, OutputUnits: 20_000, CostKnown: true, CostCents: 1,
	}); err != nil {
		t.Fatal(err)
	}
	w.finalizeJobBudgetOnFailure(ctx, &models.ProcessingJob{ID: "job-known"})
	res := heldReservationOf(t, s, "job-known")
	if res == nil || res.Status != models.BudgetSettled || res.ActualCostCents == nil || *res.ActualCostCents != 1 {
		t.Fatalf("已知结果应按实际费用结算: %+v", res)
	}
	// 重复结算不覆盖已结算金额。
	w.finalizeJobBudgetOnFailure(ctx, &models.ProcessingJob{ID: "job-known"})
	res2 := heldReservationOf(t, s, "job-known")
	if res2.Status != models.BudgetSettled || *res2.ActualCostCents != 1 {
		t.Fatalf("重复结算不应覆盖金额: %+v", res2)
	}
}

// TestWorker_PreCallFailureReleasesBudget R02-b：未到达远端调用边界的失败
// （如 Provider 路由失败）明确释放 released_no_call，不永久占用预算。
func TestWorker_PreCallFailureReleasesBudget(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	budget := int64(10)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: provider.EffectiveModel("groq", "", "analyze"), InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return nil, errors.New("Provider 路由不可用（调用前失败）")
	}

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-precall",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("调用前失败不应报周期错误: %v", err)
	}
	res := heldReservationOf(t, s, job.ID)
	if res == nil || res.Status != models.BudgetReleasedNoCall {
		t.Fatalf("调用前失败应明确释放: %+v", res)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total != 0 {
		t.Fatalf("释放后不应占用预算: total=%d err=%v", total, err)
	}
}

// TestWorker_ResumeAfterHoldReusesReservation R02：预占后模拟重启（重新领取）：
// 复用已 held 预占，一个逻辑调用只预占一次、只计费一次、结算一次。
func TestWorker_ResumeAfterHoldReusesReservation(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: provider.EffectiveModel("groq", "", "analyze"), InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		calls++
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}, nil
	}

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	// 预占后进程中断；重启恢复重新领取。
	if _, err := s.HoldBudget(ctx, job.ID, "analyze", false, "groq", provider.EffectiveModel("groq", "", "analyze"), 100_000, 20_000); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("恢复执行失败: %v", err)
	}
	if calls != 1 {
		t.Fatalf("一个逻辑调用只执行一次: calls=%d", calls)
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil || got.Status != models.StatusSucceeded {
		t.Fatalf("任务应成功: %v %+v", err, got)
	}
	resList, err := s.ListBudgetReservations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range resList {
		if r.JobID == job.ID {
			n++
			if r.Status != models.BudgetSettled {
				t.Fatalf("恢复完成后应结算一次: %+v", r)
			}
		}
	}
	if n != 1 {
		t.Fatalf("预占后重启不得重复预占: %d 行", n)
	}
	var receipts int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM usage_records WHERE receipt_id LIKE ?`, job.ID+":%").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("一个逻辑调用只计费一次: receipts=%d", receipts)
	}
}

// TestWorker_DailyLimitDefersInsteadOfFailing R03：额度不足的自动任务保持可重试
// （running + 未来租约 + 可见原因），不标记失败、不标记来源失败；手动任务与后续
// 任务不被阻塞；跨日后按现有调度规则继续完成。
func TestWorker_DailyLimitDefersInsteadOfFailing(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	podcast, err := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{
		{GUID: "g1", Title: "e1", AudioURL: "https://a1.mp3"},
		{GUID: "g2", Title: "e2", AudioURL: "https://a2.mp3"},
	}); err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	for _, ep := range eps {
		seedDigestTranscript(t, s, models.SourceEpisode, ep.ID)
	}
	limit := int64(1)
	if err := s.SetAutoDailyJobLimit(ctx, &limit); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}, nil
	}

	// 第一集：额度内获准并完成（占今日名额）。
	j1, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: eps[0].ID, JobType: models.JobAnalyze, IntentID: "a-d1", Automated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("第一集分析失败: %v", err)
	}
	got, _ := s.GetJob(ctx, j1.ID)
	if got.Status != models.StatusSucceeded {
		t.Fatalf("第一集应成功: %+v", got)
	}

	// 第二集：额度不足 → 延迟而非失败，原因可见。
	j2, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: eps[1].ID, JobType: models.JobAnalyze, IntentID: "a-d2", Automated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// R05 衔接：先排空第一集的质量判定任务，直到 j2 被领取并延迟。
	for i := 0; i < 10; i++ {
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatalf("额度拒绝不应报周期错误: %v", err)
		}
		got, _ := s.GetJob(ctx, j2.ID)
		if got.Status != models.StatusQueued {
			break
		}
	}
	got, _ = s.GetJob(ctx, j2.ID)
	if got.Status != models.StatusRunning {
		t.Fatalf("额度不足的任务应保持可重试（running+租约），不得标记失败: %+v", got)
	}
	if got.LeaseUntil == nil || !strings.Contains(*got.LeaseUntil, "20") {
		t.Fatalf("应设置未来租约避免阻塞队列头: %+v", got)
	}
	if got.LastError == nil || !strings.Contains(*got.LastError, "额度已满") {
		t.Fatalf("延迟原因应可见: %+v", got.LastError)
	}

	// 现有调度规则继续：租约未到期的延迟任务被跳过（不阻塞其他任务）。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetJob(ctx, j2.ID)
	if got2.Status != models.StatusRunning {
		t.Fatalf("租约未到期前不应被重复领取: %+v", got2)
	}
	// 重置准入时间并等租约到期（模拟延迟后跨日继续）。
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET intent_admitted_at = datetime('now','-1 day'), lease_until = datetime('now','-1 second')`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("跨日后继续执行失败: %v", err)
	}
	got, _ = s.GetJob(ctx, j2.ID)
	if got.Status != models.StatusSucceeded {
		t.Fatalf("跨日后第二集应完成: %+v", got)
	}
}
