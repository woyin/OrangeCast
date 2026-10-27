package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// practiceTestStore 打开测试库并认领 Owner（练习问题仓储只依赖来源身份）。
func practiceTestStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	seedUser(t, s, "practice-"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.com")
	return s
}

func practiceSegments() []provider.Segment {
	return []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 10, Text: "第一段"},
		{ID: "seg-0002", Start: 10, End: 20, Text: "第二段"},
	}
}

// TestReplaceReviewQuestions_KeepsOnlyVerifiableCitations 只保留真实存在的片段，
// 时间范围由程序解析（min start–max end）。
func TestReplaceReviewQuestions_KeepsOnlyVerifiableCitations(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	rows, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, "ep-1", "本期标题", 3,
		[]provider.PracticeItem{
			{Question: "可信问题", Citations: []string{"seg-0001", "ghost"}, AnswerHint: "提示"},
			{Question: "全部无效", Citations: []string{"ghost"}},
		}, practiceSegments(), "groq", "m", "job-1")
	if err != nil {
		t.Fatalf("ReplaceReviewQuestions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("只应保留可核验的问题，实际 %d", len(rows))
	}
	if len(rows[0].Citations) != 1 || rows[0].Citations[0] != "seg-0001" {
		t.Errorf("无效引用应被丢弃: %+v", rows[0].Citations)
	}
	if rows[0].TimeStart != 0 || rows[0].TimeEnd != 10 {
		t.Errorf("时间应由片段解析: %v-%v", rows[0].TimeStart, rows[0].TimeEnd)
	}
	if rows[0].SourceTitle != "本期标题" || rows[0].TranscriptVersion != 3 {
		t.Errorf("来源身份错误: %+v", rows[0])
	}
}

// TestReplaceReviewQuestions_RejectsUnverifiableSet 全部引用无效时不写库并显式报错。
func TestReplaceReviewQuestions_RejectsUnverifiableSet(t *testing.T) {
	s := practiceTestStore(t)
	_, err := s.ReplaceReviewQuestions(context.Background(), models.SourceEpisode, "ep-1", "t", 1,
		[]provider.PracticeItem{{Question: "无依据", Citations: []string{"ghost"}}}, practiceSegments(), "groq", "m", "job")
	if !errors.Is(err, ErrNoReviewQuestions) {
		t.Fatalf("应返回 ErrNoReviewQuestions，实际 %v", err)
	}
	rows, _ := s.ListReviewQuestions(context.Background(), models.SourceEpisode, "ep-1")
	if len(rows) != 0 {
		t.Errorf("失败时不得写库，实际 %d 行", len(rows))
	}
}

// TestReplaceReviewQuestions_ReplacesSameVersion 同一转录版本重新生成时替换旧问题。
func TestReplaceReviewQuestions_ReplacesSameVersion(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	if _, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, "ep-1", "t", 1,
		[]provider.PracticeItem{{Question: "旧问题", Citations: []string{"seg-0001"}}}, practiceSegments(), "groq", "m", "j1"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, "ep-1", "t", 1,
		[]provider.PracticeItem{{Question: "新问题", Citations: []string{"seg-0002"}}}, practiceSegments(), "groq", "m", "j2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Prompt != "新问题" {
		t.Fatalf("同版本应替换而不是累积: %+v", rows)
	}
	// 新转录版本的问题与旧版本并存，历史不被覆盖。
	if _, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, "ep-1", "t", 2,
		[]provider.PracticeItem{{Question: "新版本问题", Citations: []string{"seg-0001"}}}, practiceSegments(), "groq", "m", "j3"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, "ep-1")
	if len(all) != 2 {
		t.Errorf("不同转录版本应各自保留，实际 %d", len(all))
	}
}

// TestGetReviewQuestion_NotFound 未知 ID 返回 ErrNotFound。
func TestGetReviewQuestion_NotFound(t *testing.T) {
	s := practiceTestStore(t)
	if _, err := s.GetReviewQuestion(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
}

// TestListRecentReviewQuestions_Limit 跨来源按时间倒序并遵守上限。
func TestListRecentReviewQuestions_Limit(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"ep-1", "ep-2"} {
		if _, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, id, id, 1,
			[]provider.PracticeItem{{Question: "问题 " + id, Citations: []string{"seg-0001"}}}, practiceSegments(), "groq", "m", "j"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListRecentReviewQuestions(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("应只返回 1 条，实际 %d", len(rows))
	}
}

// TestDeleteReviewQuestionsForSource Purge 清理该来源的问题。
func TestDeleteReviewQuestionsForSource(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	if _, err := s.ReplaceReviewQuestions(ctx, models.SourceEpisode, "ep-1", "t", 1,
		[]provider.PracticeItem{{Question: "问题", Citations: []string{"seg-0001"}}}, practiceSegments(), "groq", "m", "j"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteReviewQuestionsForSource(ctx, models.SourceEpisode, "ep-1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListReviewQuestions(ctx, models.SourceEpisode, "ep-1")
	if len(rows) != 0 {
		t.Errorf("应全部删除，剩余 %d", len(rows))
	}
}

// TestHighlightClipCache 片段按音频身份与区间唯一，重复写入返回同一行。
func TestHighlightClipCache(t *testing.T) {
	s := practiceTestStore(t)
	ctx := context.Background()
	first, err := s.CreateHighlightClip(ctx, "sha-a", 1.5, 9.5, "mp3", "a.mp3", 100)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateHighlightClip(ctx, "sha-a", 1.5, 9.5, "mp3", "a.mp3", 100)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID {
		t.Errorf("同键应命中同一行: %s vs %s", first.ID, again.ID)
	}
	if _, err := s.GetHighlightClip(ctx, "sha-a", 1.5, 9.5, "wav"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不同格式应是不同缓存键，实际 %v", err)
	}
	paths, err := s.DeleteHighlightClipsForSource(ctx, "sha-a")
	if err != nil || len(paths) != 1 || paths[0] != "a.mp3" {
		t.Fatalf("清理应返回文件路径: %v %v", paths, err)
	}
}
