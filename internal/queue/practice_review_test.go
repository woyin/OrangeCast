package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// countingPractice 统计远端生成次数（验证断点复用与幂等入队）。
type countingPractice struct {
	set   provider.PracticeSet
	calls int
}

func (f *countingPractice) ReviewQuestions(segments []provider.Segment, limit int) (*provider.PracticeSet, error) {
	f.calls++
	out := f.set
	return &out, nil
}

func (f *countingPractice) Name() string { return "fake" }

// TestPracticeReviewJob_EndToEnd 冻结版本 → 生成 → 过滤无效引用 → 落库 → 结果 complete。
func TestPracticeReviewJob_EndToEnd(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{
		{Question: "可信问题", Citations: []string{"seg-0001"}, AnswerHint: "提示"},
		{Question: "无依据问题", Citations: []string{"ghost"}},
	}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: fake}, nil
	}
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.InputSnapshotJSON == "" {
		t.Fatal("入队应冻结转录版本快照")
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("练习问题任务失败: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("远端生成应恰好一次，实际 %d", fake.calls)
	}
	rows, err := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Prompt != "可信问题" {
		t.Fatalf("只应保存可核验问题: %+v", rows)
	}
	exec, _ = s.GetJobExecution(ctx, job.ID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("结果应 complete: %+v", exec)
	}
}

// TestEnqueuePracticeReviewJob_Idempotent 同一转录版本重复入队命中同一活跃任务。
func TestEnqueuePracticeReviewJob_Idempotent(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job1, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job1 == nil {
		t.Fatalf("首次入队失败: %v", err)
	}
	job2, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil {
		t.Fatal(err)
	}
	if job2 == nil || job2.ID != job1.ID {
		t.Fatalf("同意图应复用活跃任务: %v vs %v", job2, job1)
	}
	// 显式重新生成使用新意图，可再次入队。
	job3, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "regenerate")
	if err != nil || job3 == nil || job3.ID == job1.ID {
		t.Fatalf("显式重新生成应入队新任务: %v %v", job3, err)
	}
}

// TestEnqueuePracticeReviewJob_RequiresTranscript 未转录显式拒绝。
func TestEnqueuePracticeReviewJob_RequiresTranscript(t *testing.T) {
	s, _ := newTestWorker(t)
	sourceID := seedEpisode(t, s)
	if _, err := s.EnqueuePracticeReviewJob(context.Background(), models.SourceEpisode, sourceID, 6, ""); err == nil {
		t.Fatal("未转录应拒绝入队")
	}
}

// TestPracticeReviewJob_CheckpointReusesGeneration 生成已落断点时重试不再调用模型。
func TestPracticeReviewJob_CheckpointReusesGeneration(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{
		{Question: "断点问题", Citations: []string{"seg-0001"}},
	}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: fake}, nil
	}
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	// 注入“已生成”断点，模拟生成后、落库前中断。
	checkpoint, _ := json.Marshal(practiceReviewCheckpoint{Stage: "generated", Generated: &fake.set})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("任务失败: %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("断点应复用已生成问题，实际调用 %d 次", fake.calls)
	}
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if len(rows) != 1 {
		t.Fatalf("断点内容应落库: %+v", rows)
	}
}

// TestPracticeReviewJob_NoProvider 未配置 Provider 时显式失败。
func TestPracticeReviewJob_NoProvider(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{}, nil
	}
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx) // 失败任务由 worker 记为 failed，不以返回错误表达
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed {
		t.Fatalf("缺少 Provider 应记为失败，实际 %s", stored.Status)
	}
}

// TestPracticeReviewJob_UploadSourceTitle upload 来源使用原始文件名作为来源标题。
func TestPracticeReviewJob_UploadSourceTitle(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "访谈录音.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedDigestTranscript(t, s, models.SourceUpload, up.ID)
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{
		{Question: "录音讲了什么？", Citations: []string{"seg-0001"}},
	}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: fake}, nil
	}
	w.WithClipDir(t.TempDir())
	if _, err := s.EnqueuePracticeReviewJob(ctx, models.SourceUpload, up.ID, 6, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("任务失败: %v", err)
	}
	rows, err := s.ListReviewQuestions(ctx, models.SourceUpload, up.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("应保存一条问题: %+v %v", rows, err)
	}
	if rows[0].SourceTitle != "访谈录音.wav" {
		t.Errorf("upload 来源标题应为原始文件名，实际 %q", rows[0].SourceTitle)
	}
}

// failingPractice 让远端生成失败，验证失败原因可见且不写库。
type failingPractice struct{}

func (f *failingPractice) ReviewQuestions([]provider.Segment, int) (*provider.PracticeSet, error) {
	return nil, errors.New("provider down")
}
func (f *failingPractice) Name() string { return "fake" }

// TestPracticeReviewJob_GenerationFailure 生成失败留下显式失败原因，不静默写入空问题。
func TestPracticeReviewJob_GenerationFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: &failingPractice{}}, nil
	}
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "生成练习问题") {
		t.Fatalf("应记录生成失败原因: %+v", stored)
	}
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if len(rows) != 0 {
		t.Errorf("失败时不应写入问题，实际 %d", len(rows))
	}
}

// TestPracticeReviewJob_MissingSnapshot 缺少冻结快照时显式失败（兼容旧任务的契约校验）。
func TestPracticeReviewJob_MissingSnapshot(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: &countingPractice{}}, nil
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobPracticeReview,
		IntentID: "practice_review:no-snapshot",
	})
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "冻结的转录版本快照") {
		t.Fatalf("应报告缺少快照: %+v", stored)
	}
}

// TestPracticeReviewJob_CorruptTranscriptPayload 转录载荷损坏时显式失败，不生成空问题。
func TestPracticeReviewJob_CorruptTranscriptPayload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE artifact_versions SET payload='not-json' WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: &countingPractice{}}, nil
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "解析转录载荷") {
		t.Fatalf("应报告载荷损坏: %+v", stored)
	}
}

// TestPracticeReviewJob_MissingFrozenVersion 冻结版本被删除后显式失败（不静默用新版本）。
func TestPracticeReviewJob_MissingFrozenVersion(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM artifact_versions WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: &countingPractice{}}, nil
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "读取冻结转录版本") {
		t.Fatalf("应报告冻结版本缺失: %+v", stored)
	}
}

// TestPracticeReviewJob_CheckpointWriteFailure 断点写库失败显式失败，不继续落库。
func TestPracticeReviewJob_CheckpointWriteFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_practice_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
		t.Fatal(err)
	}
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{{Question: "q", Citations: []string{"seg-0001"}}}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{PracticeReview: fake}, nil
	}
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed {
		t.Fatalf("断点失败应记为失败: %+v", stored)
	}
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if len(rows) != 0 {
		t.Errorf("断点失败不应落库问题，实际 %d", len(rows))
	}
}

// TestPracticeReviewJob_GetExecutionFailure 任务行不可读时执行体显式失败。
func TestPracticeReviewJob_GetExecutionFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	// 用一个不存在的任务身份调用执行体：读取任务契约即失败。
	ghost := &models.ProcessingJob{ID: "missing-job", SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobPracticeReview}
	if err := w.doPracticeReviewJob(ctx, ghost, &provider.ProviderBundle{PracticeReview: &countingPractice{}}); err == nil {
		t.Fatal("读取任务契约失败应报错")
	}
}

// TestPracticeReviewJob_SaveFailure 落库失败显式返回错误，不谎报成功。
func TestPracticeReviewJob_SaveFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{{Question: "q", Citations: []string{"seg-0001"}}}}}
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_review_insert BEFORE INSERT ON review_questions BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
		t.Fatal(err)
	}
	_ = w.doPracticeReviewJob(ctx, job, &provider.ProviderBundle{PracticeReview: fake})
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if len(rows) != 0 {
		t.Errorf("落库失败不应留下问题，实际 %d", len(rows))
	}
}

// TestPracticeReviewJob_SourceTitleFailure 来源行被删除时显式失败，不用空标题冒充。
func TestPracticeReviewJob_SourceTitleFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM episodes WHERE id=?", sourceID); err != nil {
		t.Fatal(err)
	}
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{{Question: "q", Citations: []string{"seg-0001"}}}}}
	if err := w.doPracticeReviewJob(ctx, job, &provider.ProviderBundle{PracticeReview: fake}); err == nil {
		t.Fatal("来源缺失应报错")
	}
}

// TestPracticeReviewJob_UploadTitleFailure upload 来源被删除时显式失败。
func TestPracticeReviewJob_UploadTitleFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "录音.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedDigestTranscript(t, s, models.SourceUpload, up.ID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceUpload, up.ID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM uploads WHERE id=?", up.ID); err != nil {
		t.Fatal(err)
	}
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{{Question: "q", Citations: []string{"seg-0001"}}}}}
	if err := w.doPracticeReviewJob(ctx, job, &provider.ProviderBundle{PracticeReview: fake}); err == nil {
		t.Fatal("upload 来源缺失应报错")
	}
}

// TestPracticeReviewJob_RemoteBoundaryFailure 远端调用边界无法持久化时不发起调用。
func TestPracticeReviewJob_RemoteBoundaryFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueuePracticeReviewJob(ctx, models.SourceEpisode, sourceID, 6, "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_remote_boundary BEFORE UPDATE OF remote_call_started ON processing_jobs BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
		t.Fatal(err)
	}
	fake := &countingPractice{set: provider.PracticeSet{Items: []provider.PracticeItem{{Question: "q", Citations: []string{"seg-0001"}}}}}
	if err := w.doPracticeReviewJob(ctx, job, &provider.ProviderBundle{PracticeReview: fake}); err == nil {
		t.Fatal("远端边界写库失败应报错")
	}
	if fake.calls != 0 {
		t.Errorf("边界无法持久化时不得发起远端调用，实际 %d 次", fake.calls)
	}
}
