package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func translationSegments() []provider.Segment {
	return []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "a"}, {ID: "seg-0002", Start: 5, End: 10, Text: "b"}}
}

// TestReplaceSegmentTranslations_OnlyValidSegments 只写入真实片段，并返回写入条数。
func TestReplaceSegmentTranslations_OnlyValidSegments(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	written, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 2, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}, {ID: "ghost", Text: "编造"}, {ID: "seg-0002", Text: " "}},
		translationSegments(), "groq", "m", "job")
	if err != nil {
		t.Fatalf("ReplaceSegmentTranslations: %v", err)
	}
	if written != 1 {
		t.Fatalf("应只写 1 条，实际 %d", written)
	}
	rows, err := s.ListSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 2, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows["seg-0001"].Text != "甲" {
		t.Fatalf("译文内容错误: %+v", rows)
	}
}

// TestReplaceSegmentTranslations_ReplacesSameLanguage 同版本同语言重译替换旧译文。
func TestReplaceSegmentTranslations_ReplacesSameLanguage(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	if _, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "旧"}}, translationSegments(), "groq", "m", "j1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "新"}}, translationSegments(), "groq", "m", "j2"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "zh")
	if len(rows) != 1 || rows["seg-0001"].Text != "新" {
		t.Fatalf("应替换而不是累积: %+v", rows)
	}
	// 不同版本与不同语言各自保留。
	if _, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 2, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "v2"}}, translationSegments(), "groq", "m", "j3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "en",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "english"}}, translationSegments(), "groq", "m", "j4"); err != nil {
		t.Fatal(err)
	}
	langs, err := s.ListTranslationLanguages(ctx, models.SourceEpisode, "ep-1")
	if err != nil || len(langs) != 2 {
		t.Fatalf("应有中英两种译文: %v %v", langs, err)
	}
}

// TestReplaceSegmentTranslations_RejectsEmpty 无有效译文时显式报错且不写库。
func TestReplaceSegmentTranslations_RejectsEmpty(t *testing.T) {
	s := practiceTestStore(t)
	_, err := s.ReplaceSegmentTranslations(context.Background(), models.SourceEpisode, "ep-1", 1, "zh",
		[]provider.TranslatedSegment{{ID: "ghost", Text: "x"}}, translationSegments(), "groq", "m", "j")
	if err == nil {
		t.Fatal("无有效译文应报错")
	}
	if _, err := s.ReplaceSegmentTranslations(context.Background(), models.SourceEpisode, "ep-1", 1, " ",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "x"}}, translationSegments(), "groq", "m", "j"); err == nil {
		t.Fatal("缺目标语言应报错")
	}
}

// TestDeleteSegmentTranslationsForSource Purge 清理该来源译文。
func TestDeleteSegmentTranslationsForSource(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	if _, err := s.ReplaceSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}, translationSegments(), "groq", "m", "j"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSegmentTranslationsForSource(ctx, models.SourceEpisode, "ep-1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListSegmentTranslations(ctx, models.SourceEpisode, "ep-1", 1, "zh")
	if len(rows) != 0 {
		t.Errorf("应全部删除: %+v", rows)
	}
}
