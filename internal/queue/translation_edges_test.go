package queue

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestTranslateJob_CheckpointWriteFailure 断点写库失败显式失败，不继续落库译文。
func TestTranslateJob_CheckpointWriteFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_translate_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
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
	stored, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.StatusFailed {
		t.Fatalf("断点失败应记为失败: %+v", stored)
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh")
	if len(rows) != 0 {
		t.Errorf("断点失败不应落库译文: %+v", rows)
	}
}

// TestTranslateJob_SaveFailure 译文落库失败显式返回错误，不谎报完成。
func TestTranslateJob_SaveFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueTranslateJob(ctx, models.SourceEpisode, sourceID, "zh", "")
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER no_translation_insert BEFORE INSERT ON segment_translations BEGIN SELECT RAISE(ABORT,'no'); END"); err != nil {
		t.Fatal(err)
	}
	fake := &countingTranslation{set: provider.TranslationSet{Segments: []provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}}}
	if err := w.doTranslateJob(ctx, job, &provider.ProviderBundle{Translation: fake}); err == nil {
		t.Fatal("落库失败应报错")
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh")
	if len(rows) != 0 {
		t.Errorf("落库失败不应留下译文: %+v", rows)
	}
}
