package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestEvidenceAudio_Lifecycle 验证 EvidenceAudio 写入/读取/幂等覆盖/标记缺失。
func TestEvidenceAudio_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sourceID := "ep-1"

	// 不存在 → ErrNotFound
	if _, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID); err != ErrNotFound {
		t.Fatalf("未写入时应 ErrNotFound，实际 %v", err)
	}

	// 写入
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "ep-1.mp3", "mp3", 100, "sha1"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	ev, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if ev.RelPath != "ep-1.mp3" || ev.SHA256 != "sha1" || ev.Status != "ready" {
		t.Errorf("写入内容不符: %+v", ev)
	}

	// 幂等覆盖（不产生重复记录）
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "ep-1-v2.mp3", "mp3", 200, "sha2"); err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	ev, _ = s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID)
	if ev.SHA256 != "sha2" || ev.SizeBytes != 200 {
		t.Errorf("应被覆盖为新值: %+v", ev)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM evidence_audio WHERE source_id=?`, sourceID).Scan(&n)
	if n != 1 {
		t.Errorf("幂等覆盖不应产生重复记录，实际 %d", n)
	}

	// 标记缺失
	if err := s.MarkEvidenceMissing(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("标记缺失失败: %v", err)
	}
	ev, _ = s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID)
	if ev.Status != "missing" {
		t.Errorf("标记缺失后 status 应为 missing，实际 %q", ev.Status)
	}
}

// TestPurgeIntent_Lifecycle 验证 Purge 意图：创建幂等、列出 pending、标记完成。
func TestPurgeIntent_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sourceID := "ep-1"

	// 无 pending → 空
	purges, err := s.ListPendingPurges(ctx)
	if err != nil {
		t.Fatalf("ListPendingPurges: %v", err)
	}
	if len(purges) != 0 {
		t.Fatalf("初始应为空，实际 %d", len(purges))
	}

	// 创建意图
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("CreatePurgeIntent: %v", err)
	}
	// 重复创建 → 幂等，不产生重复
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("重复 CreatePurgeIntent: %v", err)
	}
	purges, _ = s.ListPendingPurges(ctx)
	if len(purges) != 1 {
		t.Fatalf("应只有 1 条 pending，实际 %d", len(purges))
	}
	if purges[0].SourceID != sourceID || purges[0].Status != "pending" {
		t.Errorf("purge 内容不符: %+v", purges[0])
	}

	// 标记完成 → 不再出现在 pending
	if err := s.MarkPurgeDone(ctx, purges[0].ID); err != nil {
		t.Fatalf("MarkPurgeDone: %v", err)
	}
	purges, _ = s.ListPendingPurges(ctx)
	if len(purges) != 0 {
		t.Errorf("完成后 pending 应为空，实际 %d", len(purges))
	}
}
