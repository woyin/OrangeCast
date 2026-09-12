package queue

import (
	"context"
	"errors"
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

// TestWorker_UnknownResultReexecutes unknown（结果未知）的任务恢复时重新执行，
// 不把未知结果冒充完成。
func TestWorker_UnknownResultReexecutes(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	calls := 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		calls++
		return &provider.ProviderBundle{
			DigestWriter:   &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "恢复后生成", Blocks: validDraftBlocks()}},
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
	if err := s.SaveJobResult(ctx, job.ID, ``, models.JobResultUnknown); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("unknown 结果应重新执行: %v", err)
	}
	if calls == 0 {
		t.Fatal("unknown 结果应触发真实重执行")
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || d.Title != "恢复后生成" {
		t.Fatalf("重执行应正常落库: %v %+v", err, d)
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
