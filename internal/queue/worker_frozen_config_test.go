package queue

import (
	"context"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestWorker_ExecutesFrozenConfig R04：入队时冻结 Provider/生效模型到任务行；
// 排队后修改设置不改变该任务输入；worker 使用持久化快照而非当前 settings。
func TestWorker_ExecutesFrozenConfig(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	// 入队时的配置 A（settings 默认：groq + 生效模型）。
	if err := s.UpdateSettings(ctx, &models.Settings{
		AnalysisProvider: ptr("openai"), AnalysisModel: ptr("gpt-test-a"),
	}); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-frozen",
	})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.ConfiguredProvider != "openai" || exec.ConfiguredModel != "gpt-test-a" {
		t.Fatalf("入队应冻结 Provider/生效模型: %+v", exec)
	}
	if tc, err := w.taskConfigFor(job); err != nil || tc.Provider != "openai" || tc.Model != "gpt-test-a" {
		t.Fatalf("worker 应读取冻结配置: %+v %v", tc, err)
	}

	// 排队后修改设置：任务输入不漂移。
	if err := s.UpdateSettings(ctx, &models.Settings{
		AnalysisProvider: ptr("groq"), AnalysisModel: ptr("gpt-test-b"),
	}); err != nil {
		t.Fatal(err)
	}
	if tc, err := w.taskConfigFor(job); err != nil || tc.Provider != "openai" || tc.Model != "gpt-test-a" {
		t.Fatalf("排队后改设置不得改变任务输入: %+v %v", tc, err)
	}

	// 显式重分析（新任务）冻结新配置 B；旧任务仍为 A。
	job2, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-frozen-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tc, err := w.taskConfigFor(job2); err != nil || tc.Provider != "groq" || tc.Model != "gpt-test-b" {
		t.Fatalf("新任务应冻结新配置: %+v %v", tc, err)
	}
}

// TestWorker_LegacyJobFallsBackToSettings R04：没有冻结配置的旧任务走当前设置
// 兼容路径；不伪造它从未保存的历史配置。
func TestWorker_LegacyJobFallsBackToSettings(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟旧任务：配置列从未保存（清空）。
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET configured_provider='', configured_model='', config_version='' WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings(ctx, &models.Settings{
		AnalysisProvider: ptr("groq"), AnalysisModel: ptr("legacy-fallback"),
	}); err != nil {
		t.Fatal(err)
	}
	tc, err := w.taskConfigFor(job)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Provider != "groq" || tc.Model != "legacy-fallback" {
		t.Fatalf("旧任务应回退到当前设置且不漂移为杜撰值: %+v", tc)
	}
}

// TestWorker_PolicyRevocationStillBlocks R04：冻结配置不绕过来源访问策略——
// 入队后 Owner 将来源设为 LocalOnly，执行时动态检查阻止外发，Provider 不被调用。
func TestWorker_PolicyRevocationStillBlocks(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobAnalyze, IntentID: "a-policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 入队后 Owner 撤销外发授权。
	if err := s.SetSourceProductionPolicy(ctx, models.SourceEpisode, sourceID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	providerCalled := false
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		providerCalled = true
		return &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("策略拒绝不应报周期错误: %v", err)
	}
	if providerCalled {
		t.Fatal("策略撤销后不得调用 Provider")
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil || got.Status != models.StatusFailed {
		t.Fatalf("策略阻止应以可见失败终结: %v %+v", err, got)
	}
	if got.LastError == nil || !strings.Contains(*got.LastError, "访问策略") {
		t.Fatalf("阻止原因应可见: %+v", got.LastError)
	}
}

func ptr[T any](v T) *T { return &v }
