package store

import (
	"context"
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

// TestHoldBudget_Exhausted 本次预估计入限额：恰好等于上限可执行，超过则拒绝；释放后可再次预占。
func TestHoldBudget_Exhausted(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	// 5 分预算拒绝 1000 分预估：本次预占未计入时旧实现会放行。
	budget := int64(5)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-big", "analyze", false, "groq", "m", 1_000_000_000, 0); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("5 分预算应拒绝 1000 分预估: %v", err)
	}
	n, err := s.countHeldReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("被拒绝的预占不应落库: held=%d", n)
	}
	// 恰好等于上限：5 分预算允许 5 分首次预占。
	res, err := s.HoldBudget(ctx, "job-1", "analyze", false, "groq", "m", 5_000_000, 0)
	if err != nil || res == nil || res.EstimatedCostCents != 5 {
		t.Fatalf("恰好等于上限应可预占: %+v %v", res, err)
	}
	// 已计 5 分 + 新预占 1 分超过 5 分预算：拒绝。
	if _, err := s.HoldBudget(ctx, "job-2", "analyze", false, "groq", "m", 1_000_000, 0); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("超出预算应拒绝: %v", err)
	}
	// 释放 job-1 后可再次预占。
	if err := s.ReleaseBudget(ctx, "job-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-2", "analyze", false, "groq", "m", 5_000_000, 0); err != nil {
		t.Fatalf("释放后应可预占: %v", err)
	}
}

// TestHoldBudget_NegativeUnits 预估单位为负是无效输入，不能借负费用扩大预算空间。
func TestHoldBudget_NegativeUnits(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1000, OutputCentsPerMillion: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-neg", "analyze", false, "groq", "m", -1_000_000, 0); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("负预估单位应拒绝: %v", err)
	}
	if _, err := s.HoldBudget(ctx, "job-neg", "analyze", false, "groq", "m", 0, -1); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("负输出预估单位应拒绝: %v", err)
	}
	if err := s.SettleBudget(ctx, "no-such-job", -5); err == nil {
		t.Fatal("负实际费用应拒绝结算")
	}
}

// TestHoldBudget_Concurrent 并发预占不能忽略在途预估与本次预估：总预估不得超过预算。
func TestHoldBudget_Concurrent(t *testing.T) {
	s := newTestStore(t)
	budget := int64(10)
	if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
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
	if held == 0 {
		t.Fatal("额度内的预占不应被拒绝")
	}
	total, err := s.OwnerMonthlyUsageCents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if total > 10 {
		t.Fatalf("账面总额超出预算: %d", total)
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
	// 在途 hold 2000 分（预算须覆盖本次预估才会获准）
	budget := int64(5000)
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

// TestKeypointQualityResult_Persistence 质量结果幂等持久化与读取（K02）。
func TestKeypointQualityResult_Persistence(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	r := &models.KeypointQualityResult{
		KeyPointID: "kp-1", SourceType: models.SourceEpisode, SourceID: "ep-1",
		CardVersion: 1, ContentFingerprint: "fp-1",
		Decision: models.KPQualityNeedsReview, Reasons: []string{"证据不足"},
		InputSnapshotJSON: `{"cited_segments":["[seg-0001] 原文"]}`, Provider: "groq", JobID: "job-1",
	}
	if err := s.SaveKeypointQualityResult(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveKeypointQualityResult(ctx, r); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_quality_results WHERE keypoint_id='kp-1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("同指纹应幂等一行: %d", n)
	}
	got, err := s.GetKeypointQualityResult(ctx, "kp-1", "fp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != models.KPQualityNeedsReview || len(got.Reasons) != 1 || got.Reasons[0] != "证据不足" {
		t.Fatalf("读取结果不符: %+v", got)
	}
	// 非法结论拒绝
	bad := &models.KeypointQualityResult{KeyPointID: "kp-2", CardVersion: 1, ContentFingerprint: "fp-2", Decision: "great"}
	if err := s.SaveKeypointQualityResult(ctx, bad); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("非法结论应拒绝: %v", err)
	}
}

// TestOwnerNote_EditConcurrency_K04 乐观并发：正常编辑版本+1；过期编辑冲突；重复保存不制造冲突。
func TestOwnerNote_EditConcurrency_K04(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	podcast, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g", Title: "e", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	seedSnapshotTranscript(t, s, models.SourceEpisode, eps[0].ID, "内容")

	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: eps[0].ID, Kind: "source_note",
		Content: "原始记录", CitationsJSON: `["seg-0001"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if note.Revision != 1 {
		t.Fatalf("新笔记 revision=1: %+v", note)
	}
	// 正常编辑：v1 → v2
	updated, err := s.UpdateOwnerNote(ctx, note.ID, "修订后的记录", `["seg-0001"]`, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Content != "修订后的记录" {
		t.Fatalf("编辑应递增版本: %+v", updated)
	}
	// 过期编辑（基于 v1）→ 冲突
	if _, err := s.UpdateOwnerNote(ctx, note.ID, "过期编辑", `["seg-0001"]`, "", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期编辑应冲突: %v", err)
	}
	// 重复保存同一最新版本的内容仍需版本匹配（幂等语义下不产生新行）
	if _, err := s.UpdateOwnerNote(ctx, note.ID, "修订后的记录", `["seg-0001"]`, "", 2); err != nil {
		t.Fatalf("基于最新版本的重复保存不应冲突: %v", err)
	}
}

// TestOwnerNote_ReflectionCitationDiscipline_K04 个人理解不得挂 Citation。
func TestOwnerNote_ReflectionCitationDiscipline_K04(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	podcast, _ := s.CreatePodcast(ctx, "https://f2.xml", "P2", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g2", Title: "e2", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	seedSnapshotTranscript(t, s, models.SourceEpisode, eps[0].ID, "内容")

	// OwnerReflection 带 Citation → 拒绝
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: eps[0].ID, Kind: "owner_reflection",
		Content: "我觉得...", CitationsJSON: `["seg-0001"]`,
	}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("个人理解挂 Citation 应拒绝: %v", err)
	}
	// OwnerReflection 带 Reference（可解析）→ 允许
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: eps[0].ID, Kind: "owner_reflection",
		Content: "这让我想起自己的经历", ReferencesJSON: `["seg-0001"]`,
	})
	if err != nil {
		t.Fatalf("带可解析 Reference 应允许: %v", err)
	}
	// 编辑时试图挂 Citation → 拒绝
	if _, err := s.UpdateOwnerNote(ctx, note.ID, "改成来源主张？", `["seg-0001"]`, "", note.Revision); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("编辑为 Citation 身份应拒绝: %v", err)
	}
}

// TestListeningProgress_PurgeCascade D07：来源删除时听播进度一并删除。
func TestListeningProgress_PurgeCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	epID := seedIntentEpisode(t, s, "listen-purge")
	if err := s.SaveListeningProgress(ctx, &models.ListeningProgress{
		SourceType: models.SourceEpisode, SourceID: epID,
		PlanID: "highlights:v1", PlanVersion: 1, ItemPosition: 1, ItemOffsetSeconds: 9, Speed: 1, Seq: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceRows(ctx, models.SourceEpisode, epID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetListeningProgress(ctx, models.SourceEpisode, epID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("来源删除后进度应删除: %v", err)
	}
}

func (s *Store) countHeldReservations(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM budget_reservations WHERE status = 'held'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// TestHoldBudget_ReusesHeldReservationOnRecovery R02：恢复领取同一 job 时按持久
// 状态复用已 held 的预占——不重复 INSERT、不重复计入占用、无唯一约束错误。
func TestHoldBudget_ReusesHeldReservationOnRecovery(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	first, err := s.HoldBudget(ctx, "job-r", "analyze", false, "groq", "m", 1_000_000, 0)
	if err != nil || first == nil {
		t.Fatalf("首次预占失败: %v", err)
	}
	second, err := s.HoldBudget(ctx, "job-r", "analyze", false, "groq", "m", 1_000_000, 0)
	if err != nil {
		t.Fatalf("恢复领取应复用预占，不应报唯一约束错误: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("应复用同一预占行: %s vs %s", first.ID, second.ID)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total != 1 {
		t.Fatalf("复用不应重复计入占用: total=%d err=%v", total, err)
	}
}

// TestHoldBudget_RejectsReservationAfterTerminal 已终结（settled）预占的同 job
// 重新预占是非法转移：明确拒绝，不覆盖已结算金额。
func TestHoldBudget_RejectsReservationAfterTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-t", "analyze", false, "groq", "m", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleBudget(ctx, "job-t", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-t", "analyze", false, "groq", "m", 1_000_000, 0); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("已结算预占的同 job 重复预占应被明确拒绝: %v", err)
	}
	res, err := s.getReservationByJob(ctx, "job-t")
	if err != nil || res.Status != models.BudgetSettled || res.ActualCostCents == nil || *res.ActualCostCents != 3 {
		t.Fatalf("已结算金额不应被改写: %+v %v", res, err)
	}
	// released 后同样拒绝重新预占。
	if _, err := s.HoldBudget(ctx, "job-r2", "analyze", false, "groq", "m", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseBudget(ctx, "job-r2", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-r2", "analyze", false, "groq", "m", 1_000_000, 0); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("已释放预占的同 job 重复预占应被明确拒绝: %v", err)
	}
}

// TestHoldBudget_ConcurrentSameJob 同 job 首次并发预占：查询与插入在同一事务，
// 并发命中 UNIQUE(job_id) 时复用已插入行——恰好一行，无错误、无重复占用。
func TestHoldBudget_ConcurrentSameJob(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.HoldBudget(ctx, "job-c", "analyze", false, "groq", "m", 1_000_000, 0)
			if err != nil {
				errs <- err
				return
			}
			ids <- res.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("并发同 job 预占不应失败: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("并发预占应全部复用同一行: %v", seen)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total != 1 {
		t.Fatalf("并发预占不应重复计入占用: total=%d err=%v", total, err)
	}
}

// TestBudgetPendingRemoteTransitions pending_remote：远端结果未知时预估继续占用；
// 可复用（不授权重调）；事后可按实际费用结算；重复转移幂等。
func TestBudgetPendingRemoteTransitions(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(10)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, "job-p", "analyze", false, "groq", "m", 5_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkBudgetPendingRemote(ctx, "job-p"); err != nil {
		t.Fatal(err)
	}
	// pending_remote 继续计入占用：5 分占用下 6 分新预占被拒。
	if _, err := s.HoldBudget(ctx, "job-other", "analyze", false, "groq", "m", 6_000_000, 0); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("pending_remote 应继续占用预算: %v", err)
	}
	// 复用 pending_remote 行（返回持久状态，不新建、不改状态）。
	res, err := s.HoldBudget(ctx, "job-p", "analyze", false, "groq", "m", 5_000_000, 0)
	if err != nil || res.Status != models.BudgetPendingRemote {
		t.Fatalf("pending_remote 应按持久状态复用: %+v %v", res, err)
	}
	// 重复转移幂等；已终结后转移为 no-op。
	if err := s.MarkBudgetPendingRemote(ctx, "job-p"); err != nil {
		t.Fatalf("重复 pending 转移应幂等: %v", err)
	}
	if err := s.SettleBudget(ctx, "job-p", 7); err != nil {
		t.Fatalf("pending_remote 应可按实际费用结算: %v", err)
	}
	if err := s.MarkBudgetPendingRemote(ctx, "job-p"); err != nil {
		t.Fatalf("已结算后的 pending 转移应 no-op: %v", err)
	}
	total, err := s.OwnerMonthlyUsageCents(ctx)
	if err != nil || total != 0 {
		t.Fatalf("结算后 pending 预估应移出占用: total=%d err=%v", total, err)
	}
}

// TestHoldBudget_ConcurrentSameJob_TightBudget 同 job 并发且预算恰好只容一份预占：
// 第二方必须在事务内先读到第一方的预占行并复用，而不是把对方占用计入限额而错误拒绝。
func TestHoldBudget_ConcurrentSameJob_TightBudget(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	budget := int64(1)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "m", InputCentsPerMillion: 1, OutputCentsPerMillion: 0}); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.HoldBudget(ctx, "job-tight", "analyze", false, "groq", "m", 1_000_000, 0)
			if err != nil {
				errs <- err
				return
			}
			ids <- res.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("预算仅容一份时同 job 并发预占不应被拒: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("应复用同一预占行: %v", seen)
	}
}
