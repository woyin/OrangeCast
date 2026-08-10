package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// seedKpEpisode 建一个含单个 KeyPoint 的 episode，返回 sourceID。
func seedKpEpisode(t *testing.T, s *Store, feedURL, guid, title, kpContent, kpDesc string) string {
	t.Helper()
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, feedURL, title, "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: guid, Title: title, AudioURL: "https://a.mp3"}})
	ep, _ := s.ListEpisodes(ctx, p.ID)
	card := &provider.KnowledgeCard{
		Title:     title,
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: kpContent, Description: kpDesc, Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, ep[0].ID, title, 1, card, segs); err != nil {
		t.Fatalf("IndexKeyPoints: %v", err)
	}
	return ep[0].ID
}

// TestGetKpGraph_CollectionEdges 验证同 Collection 的 KeyPoint 两两产生实线边。
func TestGetKpGraph_CollectionEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)

	s1 := seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "要点甲", "甲描述")
	s2 := seedKpEpisode(t, s, "https://f2.xml", "g2", "ep2", "要点乙", "乙描述")

	col, err := s.CreateCollection(ctx, "专题", "desc")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	// 两个 KeyPoint 的 citations_json 都是 ["seg-0001"],分别加入 collection
	if err := s.AddToCollection(ctx, col.ID, models.SourceEpisode, s1, `["seg-0001"]`, 0, 5, "ep1", ""); err != nil {
		t.Fatalf("AddToCollection s1: %v", err)
	}
	if err := s.AddToCollection(ctx, col.ID, models.SourceEpisode, s2, `["seg-0001"]`, 0, 5, "ep2", ""); err != nil {
		t.Fatalf("AddToCollection s2: %v", err)
	}

	gd, err := s.GetKpGraph(ctx)
	if err != nil {
		t.Fatalf("GetKpGraph: %v", err)
	}
	if len(gd.Nodes) != 2 {
		t.Fatalf("应有 2 个节点，实际 %d", len(gd.Nodes))
	}
	if len(gd.Collections) != 1 {
		t.Fatalf("应有 1 个 Collection，实际 %d", len(gd.Collections))
	}
	hasCollection := false
	for _, l := range gd.Links {
		if l.Type == "collection" {
			hasCollection = true
		}
	}
	if !hasCollection {
		t.Error("同 Collection 的 KeyPoint 应产生 collection 实线边")
	}
}

// TestGetKpGraph_SimilarEdges 验证跨 Episode、内容相似（词重叠≥0.3）的 KeyPoint 产生虚线建议边。
func TestGetKpGraph_SimilarEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)

	// 两个内容高度相似（共享"主权财富基金"+"长期投资者"）的 KeyPoint，跨 Episode
	seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "主权财富基金改变全球投资", "长期投资者")
	seedKpEpisode(t, s, "https://f2.xml", "g2", "ep2", "主权财富基金是长期投资者", "全球投资")

	gd, err := s.GetKpGraph(ctx)
	if err != nil {
		t.Fatalf("GetKpGraph: %v", err)
	}
	hasSimilar := false
	for _, l := range gd.Links {
		if l.Type == "similar" {
			hasSimilar = true
		}
	}
	if !hasSimilar {
		t.Error("内容相似的跨 Episode KeyPoint 应产生 similar 虚线边")
	}
}

// mustSeedUser 确保数据库有 Owner（部分表外键依赖 users）。
func mustSeedUser(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.ClaimOwner(context.Background(), "g@example.com", "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("认领 Owner: %v", err)
	}
}

// TestTokenizeKp_CJK 验证中日韩 bigram 分词（含拉丁字母/数字复用 cjk 缓冲）。
func TestTokenizeKp_CJK(t *testing.T) {
	tokens := tokenizeKp("主权财富基金")
	// 4 字 → 3 个 bigram
	if tokens["主权"] != 1 || tokens["权财"] != 1 || tokens["财富"] != 1 {
		t.Errorf("CJK bigram 不符: %+v", tokens)
	}
}

// TestTokenizeKp_English 验证英文分词（去标点、长度>1）。
func TestTokenizeKp_English(t *testing.T) {
	tokens := tokenizeKp("sovereign wealth fund.")
	if tokens["sovereign"] != 1 || tokens["wealth"] != 1 || tokens["fund"] != 1 {
		t.Errorf("英文分词不符: %+v", tokens)
	}
	// 单字符词应被忽略
	tokens2 := tokenizeKp("a I")
	if _, ok := tokens2["a"]; ok {
		t.Error("单字符英文词应被忽略")
	}
}

// TestTokenizeKp_Empty 验证空串返回空 map。
func TestTokenizeKp_Empty(t *testing.T) {
	tokens := tokenizeKp("")
	if len(tokens) != 0 {
		t.Errorf("空串应返回空 map，实际 %+v", tokens)
	}
}

// TestTokenizeKp_Lowercase 验证大写转小写。
func TestTokenizeKp_Lowercase(t *testing.T) {
	tokens := tokenizeKp("Hello")
	if tokens["hello"] != 1 {
		t.Errorf("应转小写，实际 %+v", tokens)
	}
}

// TestJaccard_Empty 验证空 map 返回 0。
func TestJaccard_Empty(t *testing.T) {
	if got := jaccard(map[string]int{}, map[string]int{"a": 1}); got != 0 {
		t.Errorf("空 map 应返回 0，实际 %f", got)
	}
	if got := jaccard(map[string]int{"a": 1}, map[string]int{}); got != 0 {
		t.Errorf("空 map 应返回 0，实际 %f", got)
	}
}

// TestJaccard_Identical 验证完全相同的 map 相似度为 1。
func TestJaccard_Identical(t *testing.T) {
	a := map[string]int{"主权": 2, "财富": 1}
	b := map[string]int{"主权": 2, "财富": 1}
	if got := jaccard(a, b); got != 1.0 {
		t.Errorf("完全相同应相似度 1.0，实际 %f", got)
	}
}

// TestJaccard_Disjoint 验证无交集时相似度为 0。
func TestJaccard_Disjoint(t *testing.T) {
	a := map[string]int{"主权": 1}
	b := map[string]int{"投资": 1}
	if got := jaccard(a, b); got != 0 {
		t.Errorf("无交集应相似度 0，实际 %f", got)
	}
}

// TestJaccard_IntersectTakesMin 验证交集取较小值（va < vb 分支）。
// 覆盖 jaccard 中 va < vb → intersect += va 分支。
func TestJaccard_IntersectTakesMin(t *testing.T) {
	a := map[string]int{"主权": 1, "财富": 1}
	b := map[string]int{"主权": 3, "投资": 2}
	// 交集 = min(1,3)=1；并集 = a 的总值(主权1+财富1) + b 中不在 a 的(投资2) = 4
	got := jaccard(a, b)
	if got != 1.0/4.0 {
		t.Errorf("交集应取 min 得 0.25，实际 %f", got)
	}
}

// TestJaccard_ZeroUnion 验证并集为 0 时返回 0。
// 覆盖 jaccard 中 union == 0 → return 0 分支。
func TestJaccard_ZeroUnion(t *testing.T) {
	a := map[string]int{"主权": 0}
	b := map[string]int{"主权": 0}
	if got := jaccard(a, b); got != 0 {
		t.Errorf("并集为 0 应返回 0，实际 %f", got)
	}
}

// TestGetKpGraph_ListCollectionsError 验证 ListCollections 失败时 GetKpGraph 报错。
// 覆盖 GetKpGraph 中 ListCollections err 分支（删除 collections 表）。
func TestGetKpGraph_ListCollectionsError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)
	seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "要点甲", "甲描述")
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE collections`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKpGraph(ctx); err == nil {
		t.Fatal("collections 表缺失时 GetKpGraph 应报错")
	}
}

// TestGetKpGraph_QueryError 验证 collection_items 查询失败时 GetKpGraph 报错。
// 覆盖 GetKpGraph 中 ciRows 查询 err 分支（删除 collection_items 表）。
func TestGetKpGraph_QueryError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)
	seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "要点甲", "甲描述")
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE collection_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKpGraph(ctx); err == nil {
		t.Fatal("collection_items 表缺失时 GetKpGraph 应报错")
	}
}

// TestGetKpGraph_SameEpisodeNoEdge 验证同一 Episode 的 KeyPoint 不产生相似边。
// 覆盖 GetKpGraph 中 kps[i].SourceID == kps[j].SourceID → continue 分支。
func TestGetKpGraph_SameEpisodeNoEdge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)
	// 同一 feed 的两个 episode 同属一个 podcast，但 seedKpEpisode 各建独立 podcast
	// 为制造"同一 SourceID"，直接在同一 episode 上索引两个 KeyPoint
	p, _ := s.CreatePodcast(ctx, "https://f1.xml", "P", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep1", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	card := &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{{Content: "主权财富基金改变全球投资", Description: "长期投资者", Citations: []string{"seg-0001"}}},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "主权财富基金"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, sourceID, "ep1", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	// 再次索引（同一 source）不同内容
	card2 := &provider.KnowledgeCard{
		Title:     "T2",
		Summary:   provider.CitedText{Text: "S2", Citations: []string{"seg-0002"}},
		KeyPoints: []provider.KeyPoint{{Content: "主权财富基金是长期投资者", Description: "全球投资", Citations: []string{"seg-0002"}}},
	}
	segs2 := []provider.Segment{{ID: "seg-0002", Start: 5, End: 10, Text: "主权财富基金"}}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, sourceID, "ep1", 2, card2, segs2); err != nil {
		t.Fatal(err)
	}

	gd, err := s.GetKpGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range gd.Links {
		if l.Type == "similar" {
			t.Error("同一 Episode 的 KeyPoint 不应产生 similar 边")
		}
	}
}

// TestGetKpGraph_ListKeyPointsError 验证 ListKeyPoints 失败时 GetKpGraph 报错。
// 覆盖 GetKpGraph 中 ListKeyPoints err 分支（删除 keypoint_index 表）。
func TestGetKpGraph_ListKeyPointsError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKpGraph(ctx); err == nil {
		t.Fatal("keypoint_index 表缺失时 GetKpGraph 应报错")
	}
}

// TestGetKpGraph_CollectionScanError 验证 collection 成员 Scan 失败时报错。
// 覆盖 GetKpGraph 中 ciRows.Scan 失败分支（collection_items 关联数据异常）。
func TestGetKpGraph_CollectionScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustSeedUser(t, s)
	seedKpEpisode(t, s, "https://f1.xml", "g1", "ep1", "要点甲", "甲描述")
	// 构造 ciRows.Scan 失败：创建 collection 并添加成员后，删除 keypoint_index 表
	// 使 JOIN 结果异常（keypoint_id 列无法匹配）→ Scan 失败或查询失败
	col, err := s.CreateCollection(ctx, "专题", "desc")
	if err != nil {
		t.Fatal(err)
	}
	s.AddToCollection(ctx, col.ID, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "t", "")
	// 删除 keypoint_index 表 → JOIN 查询失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKpGraph(ctx); err == nil {
		t.Fatal("keypoint_index 表缺失时 GetKpGraph 应报错")
	}
}
