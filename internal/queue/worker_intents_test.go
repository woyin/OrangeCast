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

// TestWorker_ReusesCompleteResultOnRecovery 结果写入后进程中断：
// 重启回收（running→queued）再次领取时直接复用结果——不调用 Provider、不新增产物版本。
func TestWorker_ReusesCompleteResultOnRecovery(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	providerCalled := false
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		providerCalled = true
		return nil, nil
	}

	job, created, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobDigest,
		IntentID: "digest:recovery", InputSnapshotJSON: `{"transcript_version":1}`,
	})
	if err != nil || !created {
		t.Fatalf("入队失败: %v %v", created, err)
	}
	// 模拟"远端已完成且结果已落库、但终态写入前进程中断"
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(ctx, job.ID, `{"digest_id":"d-1"}`, models.JobResultComplete); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}

	// 恢复执行：应跳过 doDigest，直接终态
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
	// 无 digest 产物产生（doDigest 未执行；不存在伪造版本）
	if _, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("恢复复用不应生成新修订: %v", err)
	}
}

// TestWorker_UnknownResultBlocksReexecution unknown（结果未知）的任务恢复时阻断
// 自动重执行并可见（B02/R02-b）：不把未知结果冒充完成，也不盲目重调模型；
// 预占保留 pending_remote，不假定未知调用免费。
func TestWorker_UnknownResultBlocksReexecution(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: provider.EffectiveModel("groq", "", "digest"), InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		calls++
		return &provider.ProviderBundle{
			DigestWriter:   &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "不应生成", Blocks: validDraftBlocks()}},
			DigestRewriter: &fakeDigestRewriter{text: "渠道"},
		}, nil
	}

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobDigest,
		IntentID: "digest:unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldBudget(ctx, job.ID, "episode_digest", false, "groq", provider.EffectiveModel("groq", "", "digest"), 120_000, 30_000); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobResult(ctx, job.ID, ``, models.JobResultUnknown); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}

	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("阻断处置不应报周期错误: %v", err)
	}
	if calls != 0 {
		t.Fatal("未知结果不得自动重调模型")
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil || got.Status != models.StatusFailed {
		t.Fatalf("未知结果应以可见失败阻断: %v %+v", err, got)
	}
	if got.LastError == nil || !strings.Contains(*got.LastError, "结果未知") {
		t.Fatalf("阻断原因应可见: %+v", got.LastError)
	}
	if d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("未知结果不得冒充完成产物: %v %+v", err, d)
	}
	res, err := s.ListBudgetReservations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var pending bool
	for _, r := range res {
		if r.JobID == job.ID && r.Status == models.BudgetPendingRemote {
			pending = true
		}
	}
	if !pending {
		t.Fatalf("未知结果的预占应保留 pending_remote: %+v", res)
	}
}

// TestWorker_RecordsActualModelAndUsage B03：产物血缘记录实际模型；用量以 receipt 记账；
// 重复记账（重放）不产生第二行。
func TestWorker_RecordsActualModelAndUsage(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Transcription: &fakeTranscriber{},
			Analysis:      &fakeAnalyzer{},
			QA:            &fakeQA{},
			Highlight:     &fakeHighlight{},
		}, nil
	}
	if _, err := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe); err != nil {
		t.Fatal(err)
	}
	// 转录完成后自动衔接分析；两步各消费一个任务。
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// 产物模型 = 响应报告的实际模型，而非固定字符串。
	var cardModel string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT model FROM artifact_versions WHERE source_id=? AND kind='knowledge_card'`, up.ID).Scan(&cardModel); err != nil {
		t.Fatal(err)
	}
	if cardModel != "fake-analysis-model" {
		t.Fatalf("卡片版本应记录实际模型，实际 %q", cardModel)
	}

	// 用量记账：模型/单位来自 fake 报告值。
	var opModel string
	var inUnits, outUnits int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT model, input_units, output_units FROM usage_records WHERE operation='analysis'`).
		Scan(&opModel, &inUnits, &outUnits); err != nil {
		t.Fatalf("analysis 用量行缺失: %v", err)
	}
	if opModel != "fake-analysis-model" || inUnits != 10 || outUnits != 5 {
		t.Fatalf("用量记录不符: %s %d/%d", opModel, inUnits, outUnits)
	}

	// 同 receipt 重放不重复累计。
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='analysis'`).Scan(&n)
	jobID := ""
	if err := s.DB.QueryRowContext(ctx,
		`SELECT substr(receipt_id,1,instr(receipt_id,':analysis')-1) FROM usage_records WHERE operation='analysis'`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	w.recordCallUsage(ctx, &models.ProcessingJob{ID: jobID, AttemptCount: 0}, "analysis", "fake", "fake-analysis-model", provider.TaskUsage{InputUnits: 10, OutputUnits: 5})
	var n2 int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='analysis'`).Scan(&n2)
	if n != n2 {
		t.Fatalf("重放记账不得新增行: %d → %d", n, n2)
	}
}

// TestWorker_BudgetGateBlocksAndSettles B04：预算配置缺口显式失败（不调 Provider、
// 原因可见）；正常执行成功后预占被结算。
func TestWorker_BudgetGateBlocksAndSettles(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	providerCalled := false
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		providerCalled = true
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}, nil
	}

	// 未登记价格：付费任务被配置缺口阻塞（可见原因，不冒充免费，不调用 Provider）。
	if _, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("被拒任务不应报周期错误: %v", err)
	}
	if providerCalled {
		t.Fatal("配置缺口下不得调用 Provider")
	}
	jobs, _ := s.ListRecentCompleted(ctx, 5)
	if len(jobs) == 0 || jobs[0].LastError == nil || !strings.Contains(*jobs[0].LastError, "预算检查拒绝任务") {
		t.Fatalf("失败原因应可见: %+v", jobs)
	}

	// 登记价格后：任务可执行，成功后预占被结算。
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: provider.EffectiveModel("groq", "", "analyze"), InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("正常分析失败: %v", err)
	}
	res, err := s.ListBudgetReservations(ctx, 10)
	if err != nil || len(res) == 0 {
		t.Fatalf("应存在预算预占记录: %v", err)
	}
	var settled bool
	for _, r := range res {
		if r.Status == models.BudgetSettled {
			settled = true
		}
	}
	if !settled {
		t.Fatalf("成功任务应结算预占: %+v", res)
	}
}

// TestPipeline_KnowledgeDJDepthChainsFullDJ B08：knowledge_dj 深度的自动摄取
// 衔接 转录→分析→高光→解说，全部产物就绪。
func TestPipeline_KnowledgeDJDepthChainsFullDJ(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "dj.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedEvidence(t, s, w, models.SourceUpload, up.ID)

	hl := &countingHighlight{set: provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-1", Gist: "开场", Citations: []string{"seg-0001"}},
	}}}
	nar := &taskNarration{available: true}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Transcription: &fakeTranscriber{},
			Analysis:      &fakeAnalyzer{},
			Highlight:     hl,
			Narration:     nar,
		}, nil
	}
	if _, err := s.EnqueueIngestionJobWithSnapshot(ctx, models.SourceUpload, up.ID, models.JobTranscribe, `{"processing_depth":"knowledge_dj"}`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindKnowledgeCard); err != nil {
		t.Fatalf("卡片应就绪: %v", err)
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindHighlight); err != nil {
		t.Fatalf("高光应就绪: %v", err)
	}
	rows, err := s.ListCurrentNarrationsForSource(ctx, models.SourceUpload, up.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("解说应就绪: %v %+v", err, rows)
	}
}

// TestPipeline_KnowledgeDepthStopsAfterKeyPoints B08：knowledge 深度止于重点，
// 不排队任何 DJ 任务（按深度断言替代旧的 Automated 一刀切规则）。
func TestPipeline_KnowledgeDepthStopsAfterKeyPoints(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "k.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Transcription: &fakeTranscriber{},
			Analysis:      &fakeAnalyzer{},
			Highlight:     &countingHighlight{},
			Narration:     &taskNarration{available: true},
		}, nil
	}
	if _, err := s.EnqueueIngestionJobWithSnapshot(ctx, models.SourceUpload, up.ID, models.JobTranscribe, `{"processing_depth":"knowledge"}`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindKnowledgeCard); err != nil {
		t.Fatalf("卡片应就绪: %v", err)
	}
	pending, _ := s.ListQueuedOrRunning(ctx)
	for _, j := range pending {
		if j.JobType == models.JobHighlight || j.JobType == models.JobNarration {
			t.Fatalf("knowledge 深度不得排队 DJ 任务: %+v", pending)
		}
	}
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindHighlight); err == nil {
		t.Fatal("knowledge 深度不应生成高光")
	}
}

// TestManualAnalyzeChainsHighlightJob B08：手动分析同样走任务编排（单一编排）。
func TestManualAnalyzeChainsHighlightJob(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &countingHighlight{}}, nil
	}
	if _, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.ListQueuedOrRunning(ctx)
	chained := false
	for _, j := range pending {
		if j.JobType == models.JobHighlight {
			chained = true
			exec, _ := s.GetJobExecution(ctx, j.ID)
			if !strings.Contains(exec.InputSnapshotJSON, "chain_narration") {
				t.Fatalf("手动链应衔接解说: %s", exec.InputSnapshotJSON)
			}
		}
	}
	if !chained {
		t.Fatalf("手动分析应衔接高光任务: %+v", pending)
	}
}
