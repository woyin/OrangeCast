package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestIndexKeyPoints_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)

	card := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "要点一", Description: "说明一", Citations: []string{"seg-0001", "seg-0002"}},
			{Content: "要点二", Description: "说明二", Citations: []string{"seg-0003"}},
		},
		Chapters: []provider.Chapter{{Title: "CH", Gist: "G", Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "第一段"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "第二段"},
		{ID: "seg-0003", Start: 10, End: 15, Text: "第三段"},
	}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	kps, total, err := s.ListKeyPoints(ctx, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("应索引 2 个 KeyPoint，实际 %d", total)
	}
	if len(kps) != 2 {
		t.Fatalf("应返回 2 行，实际 %d", len(kps))
	}
	// 第一个要点的时间范围应是 seg-0001+seg-0002 的 min-max = 0-10
	if kps[0].TimeStart != 0 || kps[0].TimeEnd != 10 {
		t.Errorf("第一个要点时间范围应为 0-10，实际 %.1f-%.1f", kps[0].TimeStart, kps[0].TimeEnd)
	}
}

func TestIndexKeyPoints_ReindexReplaces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}

	card1 := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "旧要点", Citations: []string{"seg-0001"}},
			{Content: "旧要点二", Citations: []string{"seg-0001"}},
		},
		Chapters: []provider.Chapter{{Title: "C", Gist: "G", Citations: []string{"seg-0001"}}},
	}
	s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card1, segs)

	card2 := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "新要点", Citations: []string{"seg-0001"}},
		},
		Chapters: []provider.Chapter{{Title: "C", Gist: "G", Citations: []string{"seg-0001"}}},
	}
	s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 2, card2, segs)

	_, total, _ := s.ListKeyPoints(ctx, 1, 10)
	if total != 1 {
		t.Errorf("重新索引后应只有 1 个 KeyPoint（先删后插），实际 %d", total)
	}
}

func TestSearchKeyPoints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "沟通"}}
	card := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "communication skills", Citations: []string{"seg-0001"}},
		},
		Chapters: []provider.Chapter{{Title: "C", Gist: "G", Citations: []string{"seg-0001"}}},
	}
	s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card, segs)

	results, total, err := s.SearchKeyPoints(ctx, "communication", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("搜索应命中 1 个，实际 %d", total)
	}
	if len(results) != 1 || results[0].Content != "communication skills" {
		t.Errorf("搜索结果不符: %+v", results)
	}
}

// TestSearchKeyPoints_PageNormalization 验证 SearchKeyPoints 的 page/perPage 边界归一化。
func TestSearchKeyPoints_PageNormalization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "沟通"}}
	card := &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "communication skills", Citations: []string{"seg-0001"}}},
	}
	s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card, segs)

	// 非法 page/perPage（0/负数）应归一化不报错
	if _, total, err := s.SearchKeyPoints(ctx, "communication", 0, 0); err != nil {
		t.Fatalf("page=0/perPage=0 应归一化不报错: %v", err)
	} else if total != 1 {
		t.Errorf("命中数应为 1，实际 %d", total)
	}
	if _, _, err := s.SearchKeyPoints(ctx, "communication", -1, -5); err != nil {
		t.Fatalf("负数 page/perPage 应归一化不报错: %v", err)
	}
}

func TestTokenizeKp(t *testing.T) {
	tokens := tokenizeKp("沟通技巧很重要")
	if len(tokens) == 0 {
		t.Error("应产出 token")
	}
	// bigram 应包含 "沟通"
	if _, ok := tokens["沟通"]; !ok {
		t.Errorf("bigram 应包含'沟通'，实际 %v", tokens)
	}
}

func TestJaccard(t *testing.T) {
	a := map[string]int{"apple": 1, "banana": 1}
	b := map[string]int{"apple": 1, "cherry": 1}
	sim := jaccard(a, b)
	// 交集=1 (apple), 并集=3 (apple, banana, cherry), 1/3 ≈ 0.333
	if sim < 0.3 || sim > 0.4 {
		t.Errorf("Jaccard 应约 0.33，实际 %.3f", sim)
	}
	// 完全相同
	if jaccard(a, a) != 1.0 {
		t.Error("完全相同的集合 Jaccard 应为 1.0")
	}
	// 完全不同
	if jaccard(a, map[string]int{"zzz": 1}) != 0 {
		t.Error("无交集 Jaccard 应为 0")
	}
}

func TestSpanFromSegments(t *testing.T) {
	segs := map[string]provider.Segment{
		"s1": {ID: "s1", Start: 5, End: 10},
		"s2": {ID: "s2", Start: 0, End: 3},
		"s3": {ID: "s3", Start: 20, End: 25},
	}
	start, end := spanFromSegments([]string{"s1", "s2", "s3"}, segs)
	if start != 0 || end != 25 {
		t.Errorf("span 应为 0-25，实际 %.1f-%.1f", start, end)
	}
	// 全部引用无效 → 0,0（覆盖 len(starts)==0 分支）
	start, end = spanFromSegments([]string{"missing1", "missing2"}, segs)
	if start != 0 || end != 0 {
		t.Errorf("全部无效引用应 0,0，实际 %.1f-%.1f", start, end)
	}
	// 空引用 → 0,0（覆盖 len(citations)==0 分支）
	start, end = spanFromSegments(nil, segs)
	if start != 0 || end != 0 {
		t.Errorf("空引用应 0,0，实际 %.1f-%.1f", start, end)
	}
}

func TestValidCitations(t *testing.T) {
	segs := map[string]provider.Segment{
		"s1": {ID: "s1"},
		"s2": {ID: "s2"},
	}
	out := validCitations([]string{"s1", "missing", "s2", "s1"}, segs)
	if len(out) != 2 || out[0] != "s1" || out[1] != "s2" {
		t.Errorf("应去重+过滤后返回 [s1 s2]，实际 %v", out)
	}
}

// TestListKeyPoints_PageNormalization 验证 page/perPage 边界归一化（负数/0 回退到默认）。
func TestListKeyPoints_PageNormalization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	// 用非法 page/perPage（0/负数）调用，不应报错且应为默认分页。
	if _, total, err := s.ListKeyPoints(ctx, 0, 0); err != nil {
		t.Fatalf("page=0/perPage=0 应归一化不报错: %v", err)
	} else if total != 0 {
		t.Errorf("空库 total 应为 0，实际 %d", total)
	}
	if _, _, err := s.ListKeyPoints(ctx, -1, -5); err != nil {
		t.Fatalf("page=-1/perPage=-5 应归一化不报错: %v", err)
	}
}

// TestIndexKeyPoints_SkipsInvalid 验证无效 KeyPoint 被跳过：
// 无有效 Citation 的删除、时间范围非法（end<=start）的跳过，仅索引有效项。
func TestIndexKeyPoints_SkipsInvalid(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)

	card := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "有效要点", Citations: []string{"seg-0001"}},   // 有效
			{Content: "无效引用要点", Citations: []string{"seg-9999"}}, // 无有效引用 → 跳过
			{Content: "非法时间要点", Citations: []string{"seg-0002"}}, // seg-0002 end<=start → 跳过
		},
		Chapters: []provider.Chapter{{Title: "C", Gist: "G", Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "a"},
		{ID: "seg-0002", Start: 10, End: 5, Text: "b"}, // end < start，非法时间范围
	}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	kps, total, _ := s.ListKeyPoints(ctx, 1, 10)
	if total != 1 || len(kps) != 1 {
		t.Fatalf("应只索引 1 个有效 KeyPoint，实际 total=%d len=%d", total, len(kps))
	}
	if kps[0].Content != "有效要点" {
		t.Errorf("应索引有效要点，实际 %q", kps[0].Content)
	}
}

// TestListKeyPoints_CountError 验证 COUNT 查询失败时报错。
// 覆盖 ListKeyPoints 中 COUNT 查询失败分支（删除 keypoint_index 表）。
func TestListKeyPoints_CountError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ListKeyPoints(ctx, 1, 10); err == nil {
		t.Fatal("keypoint_index 表缺失时 ListKeyPoints 应报错")
	}
}

// TestListKeyPoints_QueryError 验证分页查询失败时报错。
// 覆盖 ListKeyPoints 中 SELECT 查询失败分支（重建缺列的表使 COUNT 成功但 SELECT 失败）。
func TestListKeyPoints_QueryError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	// 重建只有 id 列的表 → COUNT 成功，但 SELECT 缺列失败
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE keypoint_index (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ListKeyPoints(ctx, 1, 10); err == nil {
		t.Fatal("SELECT 缺列应报错")
	}
}
