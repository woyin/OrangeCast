package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestParaphraseAnchor 验证锚点序列化：去重、保序排序、空白清理。
func TestParaphraseAnchor(t *testing.T) {
	// 去重 + 排序
	got := ParaphraseAnchor([]string{"seg-0002", "seg-0001", "seg-0002"})
	want := `["seg-0001","seg-0002"]`
	if got != want {
		t.Errorf("ParaphraseAnchor = %q, want %q", got, want)
	}
	// 空白清理 + 空 id 忽略
	got = ParaphraseAnchor([]string{"  seg-0002  ", "", "seg-0001"})
	want = `["seg-0001","seg-0002"]`
	if got != want {
		t.Errorf("空白清理不符: %q", got)
	}
	// 空输入 → nil 切片序列化为 null（调用方以 nil 判断无锚点）
	if got := ParaphraseAnchor(nil); got != `null` {
		t.Errorf("空输入应返回 null，实际 %q", got)
	}
}

// TestGetParaphrase_NotFound 验证不存在的复述讲解返回 ErrNotFound。
func TestGetParaphrase_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.GetParaphrase(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("不存在的复述讲解应返回 ErrNotFound，实际 %v", err)
	}
}

// TestParaphrase_RecentNRetention (ADR-0018 R2)
// 每个锚点保留最近 3 次；同锚点第 4 次淘汰最旧的；不同锚点独立保留。
func TestParaphrase_RecentNRetention(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	srcType, srcID := models.SourceEpisode, eps[0].ID
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "第一段"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "第二段"},
		{ID: "seg-0003", Start: 10, End: 15, Text: "第三段"},
	}

	refsA := []string{"seg-0001", "seg-0002"} // anchor A
	// 同锚点插入 4 次
	for i := 0; i < 4; i++ {
		if _, err := s.CreateParaphrase(ctx, srcType, srcID, "q"+itoa(i), "讲解"+itoa(i), "groq", "m", refsA, segs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListParaphrasesForAnchor(ctx, srcType, srcID, ParaphraseAnchor(refsA))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("同锚点应保留最近 3 条，实际 %d", len(got))
	}
	// 最近在前：讲解3,2,1；讲解0 应被淘汰
	if got[0].Body != "讲解3" || got[2].Body != "讲解1" {
		t.Errorf("淘汰最旧失败，顺序=%v", []string{got[0].Body, got[1].Body, got[2].Body})
	}

	// 不同锚点独立保留
	refsB := []string{"seg-0003"}
	if _, err := s.CreateParaphrase(ctx, srcType, srcID, "q", "讲解B", "groq", "m", refsB, segs); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListParaphrasesForSource(ctx, srcType, srcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("跨锚点总数应为 4（anchor A 的 3 + anchor B 的 1），实际 %d", len(all))
	}

	// relation_kind 必须为 reference
	for _, r := range all {
		if r.RelationKind != models.RelationReference {
			t.Errorf("paraphrase.relation_kind 应为 reference，实际 %s", r.RelationKind)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestCreateParaphrase_NoRefs 验证无参考片段时报错。
func TestCreateParaphrase_NoRefs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, "ep-1", "q", "讲解", "groq", "m", nil, nil); err == nil {
		t.Fatal("无参考片段应报错")
	}
}

// TestCreateParaphrase_InvalidTime 验证参考片段无法解析出有效时间范围时报错。
func TestCreateParaphrase_InvalidTime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 参考片段 ID 在 segments 中不存在 → 无法解析时间范围
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, "ep-1", "q", "讲解", "groq", "m",
		[]string{"seg-9999"}, []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}); err == nil {
		t.Fatal("参考片段无法解析时间范围应报错")
	}
}

// TestParaphrases_DBErrors 验证 paraphrases 系列操作在表缺失时返回错误。
// 覆盖 CreateParaphrase 写入失败与 listParaphrases 查询失败分支。
func TestParaphrases_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE paraphrases`); err != nil {
		t.Fatal(err)
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, "ep1", "q", "b", "p", "m", []string{"seg-0001"}, segs); err == nil {
		t.Error("paraphrases 表缺失时 CreateParaphrase 应报错")
	}
	if _, err := s.ListParaphrasesForSource(ctx, models.SourceEpisode, "ep1"); err == nil {
		t.Error("paraphrases 表缺失时 ListParaphrasesForSource 应报错")
	}
}

// TestListParaphrases_ScanError 验证 paraphrases 行数据异常时 Scan 失败。
// 覆盖 listParaphrases 中 rows.Scan 失败分支（time_start 非数字）。
func TestListParaphrases_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 插入 time_start 非数字的记录 → Scan 到 float 失败
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO paraphrases (id, source_type, source_id, anchor, segment_ids, relation_kind, time_start, time_end, question, body, provider, model)
		 VALUES ('p1', 'episode', 'ep1', '["s"]', '["s"]', 'reference', 'bad', 0, 'q', 'b', 'p', 'm')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListParaphrasesForSource(ctx, models.SourceEpisode, "ep1"); err == nil {
		t.Fatal("time_start 非数字应导致 Scan 失败")
	}
}

// TestCreateParaphrase_PruneError 验证同锚点淘汰旧记录失败时报错。
// 覆盖 CreateParaphrase 中 "淘汰旧 Paraphrase" 错误分支（DELETE 被触发器中止）。
func TestCreateParaphrase_PruneError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	// 先插入 4 条使下一次写入触发淘汰（DELETE 实际删除行）
	for i := 0; i < 4; i++ {
		if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, "ep1", "q", "b", "p", "m", []string{"seg-0001"}, segs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER abort_prune BEFORE DELETE ON paraphrases BEGIN SELECT RAISE(ABORT,'no delete'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateParaphrase(ctx, models.SourceEpisode, "ep1", "q", "b", "p", "m", []string{"seg-0001"}, segs); err == nil {
		t.Fatal("淘汰 DELETE 被中止时 CreateParaphrase 应报错")
	}
}
