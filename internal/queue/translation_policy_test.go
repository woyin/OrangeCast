package queue

import (
	"context"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestTranslateJob_BlockedBySourcePolicy 来源被限制为 LocalOnly 时，翻译任务
// 不得把内容发给远端；任务失败且不写译文。
func TestTranslateJob_BlockedBySourcePolicy(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	if err := s.UpdateSourcePolicy(ctx, models.SourceEpisode, sourceID, models.SourcePolicy{
		ProductionUse: "internal", ModelDataPolicy: models.ModelDataLocalOnly,
	}); err != nil {
		t.Fatal(err)
	}
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: fake}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	if fake.calls != 0 {
		t.Errorf("策略禁止时不得调用远端，实际 %d 次", fake.calls)
	}
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "禁止发送至") {
		t.Fatalf("应因策略失败: %+v", stored)
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh")
	if len(rows) != 0 {
		t.Errorf("失败时不应写译文: %+v", rows)
	}
}

// TestPracticeReviewJob_BlockedBySourcePolicy 练习问题同样受来源策略约束。
func TestPracticeReviewJob_BlockedBySourcePolicy(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	if err := s.UpdateSourcePolicy(ctx, models.SourceEpisode, sourceID, models.SourcePolicy{
		ProductionUse: "internal", ModelDataPolicy: models.ModelDataLocalOnly,
	}); err != nil {
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
	if fake.calls != 0 {
		t.Errorf("策略禁止时不得调用远端，实际 %d 次", fake.calls)
	}
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, sourceID)
	if len(rows) != 0 {
		t.Errorf("失败时不应写问题: %+v", rows)
	}
}

// TestTranslateJob_RequiresAudioPrice 配置月预算后，未登记文本价格时翻译任务
// 必须在调用远端前被预算检查拒绝，不能先花钱再报错。
func TestTranslateJob_RequiresAudioPrice(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	budget := int64(1000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Translation: fake}, nil
	}
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(ctx)
	if fake.calls != 0 {
		t.Errorf("预算未定价时不得调用远端，实际 %d 次", fake.calls)
	}
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "价格") {
		t.Fatalf("应因价格未登记失败: %+v", stored)
	}
	_ = store.ErrBudgetUnpriced
}

// TestPracticeReviewJob_RequiresAudioPrice 练习问题与翻译一样受预算检查约束，
// 未登记文本价格时必须在调用远端前拒绝。
func TestPracticeReviewJob_RequiresAudioPrice(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	budget := int64(5000)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
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
	if fake.calls != 0 {
		t.Errorf("预算未定价时不得调用远端，实际 %d 次", fake.calls)
	}
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed || stored.LastError == nil || !strings.Contains(*stored.LastError, "价格") {
		t.Fatalf("应因价格未登记失败: %+v", stored)
	}
}
