package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestHoldBudget_NoBudgetConfigured 未配置全局预算：不预占、不阻塞（画像/单篇限制仍生效）。
func TestHoldBudget_NoBudgetConfigured(t *testing.T) {
	s := newTestStore(t)
	res, err := s.HoldBudget(t.Context(), "job-1", "analyze", false, "groq", "m", 100, 10)
	if err != nil || res != nil {
		t.Fatalf("未配置预算不应预占或阻塞: %+v %v", res, err)
	}
}

// TestHoldBudget_UnpricedBlocksAutomated 已配置预算但未登记价格：付费自动任务显示配置缺口。
func TestHoldBudget_UnpricedBlocksAutomated(t *testing.T) {
	s := newTestStore(t)
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(t.Context(), "job-1", "analyze", false, "groq", "no-price", 100, 10); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatalf("未登记价格应返回配置缺口: %v", err)
	}
	// 登记价格后可预占
	if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1000, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	res, err := s.HoldBudget(t.Context(), "job-1", "analyze", false, "groq", "m", 1_000_000, 0)
	if err != nil || res == nil {
		t.Fatalf("登记价格后应可预占: %+v %v", res, err)
	}
	if res.EstimatedCostCents != 1000 {
		t.Fatalf("预估费用应按价格计算: %d", res.EstimatedCostCents)
	}
}

// TestHoldBudget_Exhausted 预算不足（含在途预估）拒绝新任务；释放后可再次预占。
func TestHoldBudget_Exhausted(t *testing.T) {
	s := newTestStore(t)
	budget := int64(5)
	if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1000, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(t.Context(), "job-1", "analyze", false, "groq", "m", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	// 5 分预算已被 1 分 hold + 剩余不足 → 第二个 5 分预估被拒
	if _, err := s.HoldBudget(t.Context(), "job-2", "analyze", false, "groq", "m", 5_000_000, 0); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("预算不足应拒绝: %v", err)
	}
	// 释放 job-1 后可预占
	if err := s.ReleaseBudget(t.Context(), "job-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(t.Context(), "job-2", "analyze", false, "groq", "m", 5_000_000, 0); err != nil {
		t.Fatalf("释放后应可预占: %v", err)
	}
}

// TestHoldBudget_Concurrent 并发预占不能忽略在途预估：总预估不得超过预算。
func TestHoldBudget_Concurrent(t *testing.T) {
	s := newTestStore(t)
	budget := int64(10)
	if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1000, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	held := 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.HoldBudget(t.Context(), "job-"+string(rune('a'+i)), "analyze", false, "groq", "m", 1_000_000, 0)
			if err == nil {
				mu.Lock()
				held++
				mu.Unlock()
			} else if !errors.Is(err, ErrBudgetExhausted) {
				t.Errorf("并发预占出现意外错误: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if held > 10 {
		t.Fatalf("并发预占突破预算上限: held=%d budget=10", held)
	}
}

// TestOwnerMonthlyUsageCents_Aggregates 学习、digest（无画像）与在途预估都进入月度汇总。
func TestOwnerMonthlyUsageCents_Aggregates(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	// 学习侧：receipt 已知费用 3 分
	if err := s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: "job-a:analysis", Operation: "analysis", Provider: "groq", Model: "m", CostKnown: true, CostCents: 3}); err != nil {
		t.Fatal(err)
	}
	// digest（无画像）:editorial 记录 4 分
	if _, err := s.RecordEditorialUsage(ctx, models.EditorialUsageRecord{TaskKind: "digest_compose", EntityID: "d1", Provider: "groq", Model: "m", PromptVersion: "v", CostCents: 4}); err != nil {
		t.Fatal(err)
	}
	// 在途 hold 2 分
	budget := int64(100)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 2000, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-b", "analyze", false, "groq", "m", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	usage, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage != 3+4+2000 {
		t.Fatalf("月度汇总应含学习+digest+在途预估: %d", usage)
	}
	// 结算：hold 移除。实际费用由 usage_records 的 receipt 行承载（真实结算路径
	// 的 actual 来自 receipt 合计），不与 held 重复计数。
	if err := s.SettleBudget(ctx, "job-b", 1); err != nil {
		t.Fatal(err)
	}
	usage, _ = s.OwnerMonthlyUsageCents(ctx)
	if usage != 3+4 {
		t.Fatalf("结算后 hold 移除，已落账 receipt 仍计入: %d", usage)
	}
	// 释放（结果未知）后 receipt 仍计入
	if err := s.ReleaseBudget(ctx, "job-b", true); err != nil {
		t.Fatal(err)
	}
	usage, _ = s.OwnerMonthlyUsageCents(ctx)
	if usage != 3+4 {
		t.Fatalf("释放未知后已落账费用仍应计入: %d", usage)
	}
}

// TestAutoDailyJobLimit 自动任务达到当日上限被拒；手动任务不受限。
func TestAutoDailyJobLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	limit := int64(1)
	if err := s.SetAutoDailyJobLimit(ctx, &limit); err != nil {
		t.Fatal(err)
	}
	podcast, err := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	// 创建两个当日 automated 任务行（日限额按任务行计数）。
	for _, jt := range []models.JobType{models.JobTranscribe, models.JobAnalyze} {
		if _, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
			SourceType: models.SourceEpisode, SourceID: eps[0].ID, JobType: jt,
			IntentID: "intent-" + string(jt), Automated: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.HoldBudget(ctx, "auto-1", "transcribe", true, "groq", "m", 0, 0); !errors.Is(err, ErrAutoDailyLimitReached) {
		t.Fatalf("达到日上限后自动任务应被拒: %v", err)
	}
	if _, err := s.HoldBudget(ctx, "manual-1", "transcribe", false, "groq", "m", 0, 0); err != nil {
		t.Fatalf("手动任务不受日限额约束: %v", err)
	}
}

// TestPodcastProcessingDepth_B05 深度读写与合法组合检查；旧订阅回填 knowledge。
func TestPodcastProcessingDepth_B05(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, err := s.CreatePodcast(ctx, "https://feed.example.com/depth-store.xml", "深度播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 旧订阅（未显式设置深度）回填为 knowledge，行为不变。
	pod, err := s.GetPodcastByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pod.ProcessingDepth != string(models.DepthKnowledge) {
		t.Fatalf("旧订阅应回填 knowledge，实际 %q", pod.ProcessingDepth)
	}
	// 合法值写入与读取。
	if err := s.SetPodcastProcessingDepth(ctx, p.ID, models.DepthKnowledgeDJ); err != nil {
		t.Fatal(err)
	}
	pod, _ = s.GetPodcastByID(ctx, p.ID)
	if pod.ProcessingDepth != string(models.DepthKnowledgeDJ) {
		t.Fatalf("深度应更新为 knowledge_dj: %+v", pod)
	}
	// 非法值显式拒绝（深度不是布尔开关，不塞任意字符串）。
	if err := s.SetPodcastProcessingDepth(ctx, p.ID, models.ProcessingDepth("everything")); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("非法深度应拒绝: %v", err)
	}
	// 深度更新只影响新意图：已入队任务使用入队时的快照。
	if _, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: "ep-x", JobType: models.JobAnalyze,
		IntentID: "snap-depth", InputSnapshotJSON: `{"processing_depth":"knowledge_dj"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPodcastProcessingDepth(ctx, p.ID, models.DepthKnowledge); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetActiveJobByIntent(ctx, models.SourceEpisode, "ep-x", models.JobAnalyze, "snap-depth")
	if err != nil {
		t.Fatal(err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.InputSnapshotJSON != `{"processing_depth":"knowledge_dj"}` {
		t.Fatalf("排队任务快照不应漂移: %s", exec.InputSnapshotJSON)
	}
}
