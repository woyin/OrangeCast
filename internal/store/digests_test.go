package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// newDigest 创建一份用于测试的精读文（含两个块）。
func newDigest(t *testing.T, s *Store, st models.SourceType, sid, title string, degraded bool) *models.EpisodeDigest {
	t.Helper()
	d, err := s.CreateEpisodeDigest(context.Background(), &models.EpisodeDigest{
		SourceType: st, SourceID: sid, Title: title, Degraded: degraded,
		Provider: "groq", Model: "llama-3.3-70b-versatile", PromptVersion: "digest-v1",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "转述：本期讨论了 A。", Citations: []string{"seg-1"}},
		{Type: models.DigestBlockAIExpansion, Text: "AI 展开：打个比方……"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestEpisodeDigest_PerSourceVersioning 版本随创建递增；current 取 MAX(version)。
func TestEpisodeDigest_PerSourceVersioning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, sid := models.SourceEpisode, "ep-1"

	d1 := newDigest(t, s, st, sid, "第一版", false)
	if d1.Version != 1 {
		t.Fatalf("首版 Version = %d, want 1", d1.Version)
	}
	d2 := newDigest(t, s, st, sid, "第二版", true)
	if d2.Version != 2 {
		t.Fatalf("次版 Version = %d, want 2", d2.Version)
	}
	// 另一 Source 独立版本空间
	other := newDigest(t, s, st, "ep-2", "别集", false)
	if other.Version != 1 {
		t.Fatalf("别集首版 Version = %d, want 1", other.Version)
	}

	cur, err := s.GetCurrentEpisodeDigest(ctx, st, sid)
	if err != nil || cur.ID != d2.ID || !cur.Degraded {
		t.Fatalf("current 应为第二版且 degraded：cur=%+v err=%v", cur, err)
	}
	byID, err := s.GetEpisodeDigest(ctx, d1.ID)
	if err != nil || byID.Title != "第一版" || byID.Degraded {
		t.Fatalf("按 ID 读取第一版失败：%v %+v", err, byID)
	}

	list, err := s.ListEpisodeDigests(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("全局列表应含两个 Source 各一条：%v %+v", err, list)
	}
}

// TestEpisodeDigest_BlocksPersisted 块按 position 升序读回，citations 反序列化。
func TestEpisodeDigest_BlocksPersisted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := newDigest(t, s, models.SourceDocument, "doc-1", "文档精读", false)
	blocks, err := s.ListDigestBlocks(ctx, d.ID)
	if err != nil || len(blocks) != 2 {
		t.Fatalf("块数量：%v %+v", err, blocks)
	}
	if blocks[0].Type != models.DigestBlockParaphrase || len(blocks[0].Citations) != 1 || blocks[0].Citations[0] != "seg-1" {
		t.Fatalf("转述块损坏：%+v", blocks[0])
	}
	if blocks[0].Position >= blocks[1].Position {
		t.Fatalf("块应按 position 升序：%+v %+v", blocks[0], blocks[1])
	}
}

// TestEpisodeDigest_InvalidInput 空 Source/标题/空文本块应被拒绝。
func TestEpisodeDigest_InvalidInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{SourceType: models.SourceEpisode}, nil); err == nil {
		t.Fatal("缺 SourceID/标题应报错")
	}
	if _, err := s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{SourceType: models.SourceEpisode, SourceID: "ep", Title: "t"},
		[]models.DigestBlock{{Type: models.DigestBlockNote, Text: ""}}); err == nil {
		t.Fatal("空文本块应报错")
	}
}

// TestDigestSearchSources_UpsertAndStatus ⑥b：落源去重写入 + 状态流转 + 剔除联动删块。
func TestDigestSearchSources_UpsertAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := newDigest(t, s, models.SourceEpisode, "ep-9", "t", false)

	// 直接写一个 cited_fact 块以便验证剔除联动
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO digest_blocks (id,digest_id,position,block_type,text,citations_json,target_source_id) VALUES ('blk-x',?,3,'cited_fact','引用事实','["doc-seg-1"]','doc-77')`,
		d.ID); err != nil {
		t.Fatal(err)
	}

	err := s.AddDigestSearchSources(ctx, d.ID, []models.DigestSearchSource{
		{Query: "行业增长率", URL: "https://example.com/a", Title: "报道A", DocumentID: "doc-77"},
		{Query: "行业增长率", URL: "https://example.com/a", Title: "重复", DocumentID: "doc-77"}, // 同 URL 幂等
	})
	if err != nil {
		t.Fatal(err)
	}
	srcs, _ := s.ListDigestSearchSources(ctx, d.ID)
	if len(srcs) != 1 || srcs[0].Status != "pending" || srcs[0].DocumentID != "doc-77" {
		t.Fatalf("落源写入/去重失败：%+v", srcs)
	}
	if err := s.SetDigestSearchSourceStatus(ctx, srcs[0].ID, "bogus"); err == nil {
		t.Fatal("非法状态应报错")
	}
	if err := s.SetDigestSearchSourceStatus(ctx, srcs[0].ID, "rejected"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDigestSearchSourceStatus(ctx, "missing", "confirmed"); err != ErrNotFound {
		t.Fatalf("未知落源应 ErrNotFound：%v", err)
	}
	// 剔除联动：删除引用 doc-77 的块
	if err := s.DeleteDigestBlocksForTarget(ctx, d.ID, "doc-77"); err != nil {
		t.Fatal(err)
	}
	blocks, _ := s.ListDigestBlocks(ctx, d.ID)
	if len(blocks) != 2 {
		t.Fatalf("剔除后应只剩 2 个原块：%+v", blocks)
	}
}

// TestDigestFactGaps_Persist 缺口写入（含重复幂等）与读取。
func TestDigestFactGaps_Persist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := newDigest(t, s, models.SourceEpisode, "ep-10", "t", true)
	if err := s.AddDigestFactGaps(ctx, d.ID, []models.DigestFactGap{
		{Text: "2024 年行业规模"},
		{Text: "2024 年行业规模"}, // 幂等
		{Text: "公司估值", DocumentID: "doc-5"},
	}); err != nil {
		t.Fatal(err)
	}
	gaps, _ := s.ListDigestFactGaps(ctx, d.ID)
	if len(gaps) != 2 {
		t.Fatalf("缺口应去重后为 2：%+v", gaps)
	}
}

// TestDigestRewrite_Upsert 渠道版本按 (digest, channel) 覆盖更新。
func TestDigestRewrite_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := newDigest(t, s, models.SourceEpisode, "ep-11", "t", false)
	if _, err := s.UpsertDigestRewrite(ctx, &models.DigestRewrite{DigestID: d.ID, Channel: models.DigestChannelXiaohongshu, Text: "第一版", Provider: "groq", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertDigestRewrite(ctx, &models.DigestRewrite{DigestID: d.ID, Channel: models.DigestChannelXiaohongshu, Text: "重写后", Provider: "groq", Model: "m2"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetDigestRewrite(ctx, d.ID, models.DigestChannelXiaohongshu)
	if err != nil || r.Text != "重写后" || r.Model != "m2" {
		t.Fatalf("渠道版本应覆盖更新：%v %+v", err, r)
	}
	if _, err := s.GetDigestRewrite(ctx, d.ID, "missing-channel"); err != ErrNotFound {
		t.Fatalf("未知渠道应 ErrNotFound：%v", err)
	}
}

// TestDeleteEpisodeDigestsForSource Purge 清理级联到子表。
func TestDeleteEpisodeDigestsForSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := newDigest(t, s, models.SourceEpisode, "ep-12", "t", false)
	_ = s.AddDigestSearchSources(ctx, d.ID, []models.DigestSearchSource{{Query: "q", URL: "https://e.com/x", Title: "T", DocumentID: "doc-1"}})
	_ = s.AddDigestFactGaps(ctx, d.ID, []models.DigestFactGap{{Text: "g"}})
	if _, err := s.UpsertDigestRewrite(ctx, &models.DigestRewrite{DigestID: d.ID, Channel: models.DigestChannelXiaohongshu, Text: "x", Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEpisodeDigestsForSource(ctx, models.SourceEpisode, "ep-12"); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, table := range []string{"episode_digests", "digest_blocks", "digest_search_sources", "digest_fact_gaps", "digest_rewrites"} {
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s 应被清空：n=%d err=%v", table, n, err)
		}
	}
}

// TestEpisodeDigest_DBErrors 删表法覆盖查询失败分支。
func TestEpisodeDigest_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episode_digests`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, "ep"); err == nil {
		t.Fatal("表缺失应报错")
	}
	if _, err := s.ListEpisodeDigests(ctx); err == nil {
		t.Fatal("表缺失应报错")
	}
	if _, err := s.CreateEpisodeDigest(ctx, &models.EpisodeDigest{SourceType: models.SourceEpisode, SourceID: "ep", Title: "t"}, nil); err == nil {
		t.Fatal("建版事务应失败")
	}
}
