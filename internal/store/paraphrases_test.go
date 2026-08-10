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
