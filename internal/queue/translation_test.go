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

// countingTranslation 统计远端翻译调用次数。
type countingTranslation struct {
	set   provider.TranslationSet
	calls int
}

func (f *countingTranslation) Translate([]provider.Segment, string) (*provider.TranslationSet, error) {
	f.calls++
	out := f.set
	return &out, nil
}
func (f *countingTranslation) Name() string { return "fake" }

// TestTranslateJob_EndToEnd 冻结版本 → 翻译 → 落库 → 结果 complete。
func TestTranslateJob_EndToEnd(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{
		{ID: "seg-0001", Text: "译文一"}, {ID: "ghost", Text: "编造"},
	}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: fake}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("翻译任务失败: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("应调用一次，实际 %d", fake.calls)
	}
	rows, err := s.ListSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh")
	if err != nil || len(rows) != 1 {
		t.Fatalf("应写入 1 条可核验译文: %+v %v", rows, err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("结果应 complete: %+v", exec)
	}
}

// TestTranslateJob_Idempotent 同版本同语言重复入队命中同一活跃任务。
func TestTranslateJob_Idempotent(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	first, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || second == nil || second.ID != first.ID {
		t.Fatalf("应复用活跃任务: %v %v", second, err)
	}
	// 不同语言是新意图。
	english, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "en", "")
	if err != nil || english == nil || english.ID == first.ID {
		t.Fatalf("不同语言应入队新任务: %v %v", english, err)
	}
}

// TestTranslateJob_RequiresInput 无转录或缺语言时显式拒绝。
func TestTranslateJob_RequiresInput(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	if _, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", ""); err == nil {
		t.Fatal("无转录应拒绝")
	}
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	if _, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "  ", ""); err == nil {
		t.Fatal("缺目标语言应拒绝")
	}
}

// TestTranslateJob_CheckpointReuses 断点已生成时不再调用模型。
func TestTranslateJob_CheckpointReuses(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{{ID: "seg-0001", Text: "断点译文"}}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: fake}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	cp, _ := json.Marshal(translationCheckpoint{Stage: "generated", Generated: &fake.set})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cp)); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("断点应复用译文，实际调用 %d 次", fake.calls)
	}
}

// TestTranslateJob_NoProvider 缺 Provider 时记为失败。
func TestTranslateJob_NoProvider(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed {
		t.Fatalf("缺 Provider 应失败: %+v", stored)
	}
}

// failingTranslation 让翻译失败，验证原因可见且不写库。
type failingTranslation struct{}

func (f *failingTranslation) Translate([]provider.Segment, string) (*provider.TranslationSet, error) {
	return nil, errors.New("translate down")
}
func (f *failingTranslation) Name() string { return "fake" }

// TestTranslateJob_GenerationFailure 翻译失败记录显式原因，不写空译文。
func TestTranslateJob_GenerationFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: &failingTranslation{}}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "生成译文") {
		t.Fatalf("应记录生成失败原因: %+v", stored)
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh")
	if len(rows) != 0 {
		t.Errorf("失败时不应写入译文: %+v", rows)
	}
}

// TestTranslateJob_MissingSnapshot 缺冻结快照时显式失败。
func TestTranslateJob_MissingSnapshot(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobTranslate,
		IntentID: "translate:no-snapshot",
	})
	if err != nil || job == nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: &countingTranslation{}}, nil
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "冻结的转录版本或目标语言") {
		t.Fatalf("应报告缺快照: %+v", stored)
	}
}

// TestTranslateJob_CorruptPayload 转录载荷损坏时显式失败。
func TestTranslateJob_CorruptPayload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE artifact_versions SET payload='not-json' WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: &countingTranslation{}}, nil
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

// TestTranslateJob_MissingFrozenVersion 冻结版本被删除时显式失败。
func TestTranslateJob_MissingFrozenVersion(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM artifact_versions WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: &countingTranslation{}}, nil
	}
	_ = w.ProcessOne(ctx)
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "读取冻结转录版本") {
		t.Fatalf("应报告版本缺失: %+v", stored)
	}
}

// TestTranslateJob_RemoteBoundaryFailure 远端边界不可持久化时不发起调用。
func TestTranslateJob_RemoteBoundaryFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_translate_boundary BEFORE UPDATE OF remote_call_started ON processing_jobs BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
		t.Fatal(err)
	}
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}}}
	if err := w.doTranslateJob(ctx, job, &provider.ProviderBundle{Translation: fake}); err == nil {
		t.Fatal("边界写库失败应报错")
	}
	if fake.calls != 0 {
		t.Errorf("边界失败不得调用远端，实际 %d", fake.calls)
	}
}

// TestTranslateJob_ContractReadFailure 任务契约不可读时显式失败。
func TestTranslateJob_ContractReadFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	ghost := &models.ProcessingJob{ID: "missing", SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobTranslate}
	if err := w.doTranslateJob(ctx, ghost, &provider.ProviderBundle{Translation: &countingTranslation{}}); err == nil {
		t.Fatal("契约读取失败应报错")
	}
}
