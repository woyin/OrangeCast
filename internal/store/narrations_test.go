package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestNarration_PerHighlightVersioning (ADR-0019 R4)
// 每个 highlight_id 各有独立递增的 version；current 取 MAX(version)。
func TestNarration_PerHighlightVersioning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, sid := models.SourceEpisode, "ep-1"

	// highlight A 的 v1
	v, err := s.CreateNarration(ctx, st, sid, "hl-A", "af_heart", "kokoro-82m", "ep-1_hl-A_1.wav", 3.2, 50, "kokoro")
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Errorf("首版应为 v1，实际 %d", v)
	}
	// highlight A 的 v2（重新生成换音色）
	v2, err := s.CreateNarration(ctx, st, sid, "hl-A", "bm_sky", "kokoro-82m", "ep-1_hl-A_2.wav", 3.1, 51, "kokoro")
	if err != nil {
		t.Fatal(err)
	}
	if v2 != 2 {
		t.Errorf("第二版应为 v2，实际 %d", v2)
	}
	// highlight B 的 v1（独立版本序列）
	vb, err := s.CreateNarration(ctx, st, sid, "hl-B", "af_heart", "kokoro-82m", "ep-1_hl-B_1.wav", 2.5, 40, "kokoro")
	if err != nil {
		t.Fatal(err)
	}
	if vb != 1 {
		t.Errorf("hl-B 首版应为 v1（独立序列），实际 %d", vb)
	}

	// current of hl-A 应为 v2（MAX）
	cur, err := s.GetCurrentNarration(ctx, st, sid, "hl-A")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != 2 || cur.Voice != "bm_sky" {
		t.Errorf("hl-A current 应为 v2/bm_sky，实际 v%d/%s", cur.Version, cur.Voice)
	}
	// current of hl-B 应为 v1
	curB, err := s.GetCurrentNarration(ctx, st, sid, "hl-B")
	if err != nil {
		t.Fatal(err)
	}
	if curB.Version != 1 {
		t.Errorf("hl-B current 应为 v1，实际 v%d", curB.Version)
	}

	// ListCurrentNarrationsForSource 应返回 2 条（hl-A, hl-B）
	all, err := s.ListCurrentNarrationsForSource(ctx, st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("应返回 2 条 current，实际 %d", len(all))
	}
	if all["hl-A"].Version != 2 || all["hl-B"].Version != 1 {
		t.Errorf("映射内容错误: %+v", all)
	}
}

// TestNarration_DeleteForSource (Purge 清理)
func TestNarration_DeleteForSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, sid := models.SourceEpisode, "ep-1"
	s.CreateNarration(ctx, st, sid, "hl-A", "v", "m", "p", 1, 1, "kokoro")
	s.CreateNarration(ctx, st, sid, "hl-B", "v", "m", "p", 1, 1, "kokoro")
	if err := s.DeleteNarrationsForSource(ctx, st, sid); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListCurrentNarrationsForSource(ctx, st, sid)
	if len(all) != 0 {
		t.Errorf("删除后应无 narration，实际 %d", len(all))
	}
}
