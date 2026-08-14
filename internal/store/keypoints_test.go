package store

import (
	"context"
	"encoding/json"
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
	hybrid, err := s.SearchKeyPointsHybrid(ctx, "说明一", 10)
	if err != nil || len(hybrid) == 0 || hybrid[0].Content != "要点一" {
		t.Fatalf("hybrid retrieval should preserve the cited KeyPoint: rows=%+v err=%v", hybrid, err)
	}
	var providerName, model string
	if err := s.DB.QueryRowContext(ctx, `SELECT provider,model FROM keypoint_embeddings WHERE keypoint_id=?`, hybrid[0].ID).Scan(&providerName, &model); err != nil || providerName != "local" || model != "char-ngram-v1" {
		t.Fatalf("embedding provenance must be versioned: provider=%q model=%q err=%v", providerName, model, err)
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

func TestIndexKeyPoints_ReindexPreservesManualAndMatchingAutomaticIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	podcast, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	seedCurrentTranscript(t, s, models.SourceEpisode, episodes[0].ID, segs)
	card := &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "自动要点", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episodes[0].ID, "ep", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	automatic, _, err := s.ListKeyPoints(ctx, 1, 10)
	if err != nil || len(automatic) != 1 {
		t.Fatalf("first automatic keypoint: rows=%+v err=%v", automatic, err)
	}
	if err := s.SetKeyPointProductionStatus(ctx, automatic[0].ID, models.KeyPointShortlisted); err != nil {
		t.Fatal(err)
	}
	manual, err := s.CreateManualKeyPoint(ctx, KeyPointRow{
		SourceType: models.SourceEpisode, SourceID: episodes[0].ID, SourceTitle: "ep", Content: "人工补充", Description: "d",
		CitationsJSON: `["seg-0001"]`, TimeStart: 0, TimeEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episodes[0].ID, "ep", 2, card, segs); err != nil {
		t.Fatal(err)
	}
	rows, total, err := s.ListKeyPoints(ctx, 1, 10)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("manual and automatic keypoints should survive reindex: rows=%+v total=%d err=%v", rows, total, err)
	}
	gotAutomatic, err := s.GetKeyPoint(ctx, automatic[0].ID)
	if err != nil || gotAutomatic.ProductionStatus != models.KeyPointInbox || gotAutomatic.Origin != models.KeyPointAutomatic {
		t.Fatalf("matching automatic keypoint should keep ID but refresh status: kp=%+v err=%v", gotAutomatic, err)
	}
	gotManual, err := s.GetKeyPoint(ctx, manual.ID)
	if err != nil || gotManual.Origin != models.KeyPointManual || gotManual.ProductionStatus != models.KeyPointInbox {
		t.Fatalf("manual keypoint should remain independent: kp=%+v err=%v", gotManual, err)
	}
}

func seedCurrentTranscript(t *testing.T, s *Store, sourceType models.SourceType, sourceID string, segments []provider.Segment) {
	t.Helper()
	payload, err := json.Marshal(provider.TranscriptPayload{Text: "test", Segments: segments})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueJob(context.Background(), sourceType, sourceID, models.JobTranscribe)
	if err != nil || job == nil {
		t.Fatalf("enqueue transcript fixture: job=%+v err=%v", job, err)
	}
	version, err := s.CreateArtifactVersion(context.Background(), sourceType, sourceID, KindTranscript, "test", "test", "test", job.ID, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(context.Background(), sourceType, sourceID, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
}

func TestManualKeyPointValidationAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	podcast, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	seedCurrentTranscript(t, s, models.SourceEpisode, episodes[0].ID, []provider.Segment{{ID: "seg", Start: 0, End: 1, Text: "x"}})
	if _, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodes[0].ID, Content: "x", CitationsJSON: `[]`, TimeEnd: 1}); err == nil {
		t.Fatal("manual keypoint without citation should fail")
	}
	if _, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: "missing", Content: "x", CitationsJSON: `["seg"]`, TimeEnd: 1}); err != ErrNotFound {
		t.Fatalf("missing source should return ErrNotFound: %v", err)
	}
	manual, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodes[0].ID, SourceTitle: "ep", Content: "x", CitationsJSON: `["seg"]`, TimeEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyPointProductionStatus(ctx, manual.ID, models.KeyPointUsed); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetKeyPoint(ctx, manual.ID)
	if err != nil || got.ProductionStatus != models.KeyPointUsed {
		t.Fatalf("status should persist: kp=%+v err=%v", got, err)
	}
	if err := s.SetKeyPointProductionStatus(ctx, manual.ID, models.KeyPointProductionStatus("wrong")); err == nil {
		t.Fatal("invalid status should fail")
	}
	if err := s.SetKeyPointProductionStatus(ctx, "missing", models.KeyPointInbox); err != ErrNotFound {
		t.Fatalf("missing keypoint should return ErrNotFound: %v", err)
	}
	if _, err := s.GetKeyPoint(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing keypoint should be ErrNotFound: %v", err)
	}
}

func TestManualKeyPointFTSFailureRollsBackAndIndexFailureReturns(t *testing.T) {
	ctx := context.Background()

	t.Run("manual FTS write rolls back index row", func(t *testing.T) {
		s := newTestStore(t)
		seedUser(t, s, "a@b.com")
		podcast, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
		s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
		episodes, _ := s.ListEpisodes(ctx, podcast.ID)
		if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodes[0].ID, Content: "x", CitationsJSON: `["seg"]`, TimeEnd: 1}); err == nil {
			t.Fatal("missing FTS table should fail manual creation")
		}
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_index`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed manual creation must not leave index row: count=%d err=%v", count, err)
		}
	})

	t.Run("automatic reindex surfaces missing index", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
			t.Fatal(err)
		}
		card := &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "x", Citations: []string{"seg"}}}}
		if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "source", "title", 1, card, []provider.Segment{{ID: "seg", End: 1}}); err == nil {
			t.Fatal("missing index table should fail automatic reindex")
		}
	})
}

func TestKeyPointMutationQueryErrorsSurface(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKeyPoint(ctx, "keypoint"); err == nil {
		t.Fatal("missing index table should surface GetKeyPoint query error")
	}
	if err := s.SetKeyPointProductionStatus(ctx, "keypoint", models.KeyPointInbox); err == nil {
		t.Fatal("missing index table should surface status update error")
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

// TestIndexKeyPoints_DBErrors 验证 IndexKeyPoints 在表缺失时返回错误。
// 覆盖 IndexKeyPoints 中 DELETE keypoint_search/index 失败分支。
func TestIndexKeyPoints_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, segs); err == nil {
		t.Fatal("keypoint_search 表缺失时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_InsertError 验证写入 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 "写入 keypoint_index" 分支（删除 keypoint_index 表）。
func TestIndexKeyPoints_InsertError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, segs); err == nil {
		t.Fatal("keypoint_index 表缺失时 IndexKeyPoints 应报错")
	}
}

// TestSearchKeyPoints_DBErrors 验证 SearchKeyPoints 在表缺失时返回错误。
// 覆盖 SearchKeyPoints 中 COUNT/分页查询失败分支。
func TestSearchKeyPoints_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SearchKeyPoints(ctx, "wealth", 1, 10); err == nil {
		t.Fatal("keypoint_search 表缺失时 SearchKeyPoints 应报错")
	}
}

// TestDeleteKeyPointsForSource_Error 验证删除失败时报错。
// 覆盖 DeleteKeyPointsForSource 中 DELETE 失败分支（删除 keypoint_search 表）。
func TestDeleteKeyPointsForSource_Error(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKeyPointsForSource(ctx, models.SourceEpisode, "ep1"); err == nil {
		t.Fatal("keypoint_search 表缺失时 DeleteKeyPointsForSource 应报错")
	}
}

// TestIndexKeyPoints_DeleteIndexError 验证删除 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 DELETE keypoint_index 失败分支。
func TestIndexKeyPoints_DeleteIndexError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, segs); err == nil {
		t.Fatal("keypoint_index 表缺失时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_BeginTxError 验证事务开启失败时报错。
// 覆盖 IndexKeyPoints 中 db.BeginTx 错误分支（关闭 DB）。
func TestIndexKeyPoints_BeginTxError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Close()
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("关闭 DB 时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_DeleteIndexOnlyError 验证删除 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 DELETE keypoint_index 错误分支（仅删 index 表，search 保留）。
func TestIndexKeyPoints_DeleteIndexOnlyError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("keypoint_index 表缺失时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_SearchInsertError 验证写入 FTS 索引失败时报错。
// 覆盖 IndexKeyPoints 中 keypoint_search INSERT 错误分支（search 表缺失）。
func TestIndexKeyPoints_SearchInsertError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("keypoint_search 表缺失时 IndexKeyPoints 应报错")
	}
}

// TestSearchKeyPoints_QueryErrorAfterCount 验证分页查询失败时报错。
// 覆盖 SearchKeyPoints 中 SELECT 查询错误分支（重建缺列的表使 COUNT 成功但 SELECT 失败）。
func TestSearchKeyPoints_QueryErrorAfterCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE keypoint_search (keypoint_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SearchKeyPoints(ctx, "wealth", 1, 10); err == nil {
		t.Fatal("SELECT 缺列应报错")
	}
}

// TestListKeyPoints_ScanError 验证 KeyPoint 行数据异常时 Scan 失败。
// 覆盖 scanKeyPointRows 中 rows.Scan 失败分支（card_version 非整数）。
func TestListKeyPoints_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, eps[0].ID, "ep", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE keypoint_index SET card_version='bad' WHERE source_id=?`, eps[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ListKeyPoints(ctx, 1, 10); err == nil {
		t.Fatal("card_version 非整数应导致 Scan 失败")
	}
}

// TestIndexKeyPoints_DeleteIndexViewError 验证删除 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 DELETE keypoint_index 错误分支（重建为视图使 DELETE 失败）。
func TestIndexKeyPoints_DeleteIndexViewError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE VIEW keypoint_index AS SELECT 1 AS id`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("keypoint_index 为视图时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_DeleteIndexTriggerError 验证删除 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 DELETE keypoint_index 错误分支（触发器中止，需有行可删）。
func TestIndexKeyPoints_DeleteIndexTriggerError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, created_at)
		 VALUES ('kp1','episode','ep1','t','c','d','["s"]','citation',0,1,1,datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER abort_kpdel BEFORE DELETE ON keypoint_index BEGIN SELECT RAISE(ABORT,'no'); END`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("keypoint_index DELETE 被中止时 IndexKeyPoints 应报错")
	}
}

// TestIndexKeyPoints_InsertIndexError 验证写入 keypoint_index 失败时报错。
// 覆盖 IndexKeyPoints 中 "写入 keypoint_index" 错误分支（CHECK 约束拒绝负时间）。
func TestIndexKeyPoints_InsertIndexError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE keypoint_index (id TEXT, source_type TEXT, source_id TEXT, source_title TEXT, content TEXT, description TEXT, citations_json TEXT, relation_kind TEXT, time_start REAL CHECK(time_start >= 0), time_end REAL, card_version INTEGER, created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: -1, End: 0, Text: "x"}}); err == nil {
		t.Fatal("负 time_start 违反 CHECK 约束应报错")
	}
}

// TestIndexKeyPoints_InsertSearchError2 验证写入 FTS 索引失败时报错。
// 覆盖 IndexKeyPoints 中 keypoint_search INSERT 错误分支（重建缺列的表）。
func TestIndexKeyPoints_InsertSearchError2(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_search`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE keypoint_search (keypoint_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	card := &provider.KnowledgeCard{Title: "T", KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, "ep1", "t", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("keypoint_search 缺列时 IndexKeyPoints 应报错")
	}
}

// TestSearchKeyPoints_JoinError 验证全文搜索 JOIN 查询失败时报错。
// 覆盖 SearchKeyPoints 中 SELECT 查询错误分支（keypoint_index 缺失使 JOIN 失败）。
func TestSearchKeyPoints_JoinError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SearchKeyPoints(ctx, "wealth", 1, 10); err == nil {
		t.Fatal("keypoint_index 缺失时 SearchKeyPoints 应报错")
	}
}
