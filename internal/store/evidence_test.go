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

// TestGetEvidenceAudio_NotFound 验证不存在的证据返回 ErrNotFound。
// 覆盖 GetEvidenceAudio 中 sql.ErrNoRows 分支。
func TestGetEvidenceAudio_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, "ep-none"); err != ErrNotFound {
		t.Errorf("不存在的证据应返回 ErrNotFound，实际 %v", err)
	}
}

// TestEvidence_DBErrors 验证 evidence 系列查询在表缺失时返回错误。
// 覆盖 GetEvidenceAudio/ListPendingPurges 查询错误分支。
func TestEvidence_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, tbl := range []string{"evidence_audio", "purges"} {
		if _, err := s.DB.ExecContext(ctx, `DROP TABLE `+tbl); err != nil {
			t.Fatalf("DROP TABLE %s: %v", tbl, err)
		}
	}
	if _, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, "ep1"); err == nil {
		t.Error("evidence_audio 表缺失时 GetEvidenceAudio 应报错")
	}
	if _, err := s.ListPendingPurges(ctx); err == nil {
		t.Error("purges 表缺失时 ListPendingPurges 应报错")
	}
}

// TestDeleteSourceRows_CascadeFails 验证级联删除失败时返回错误。
// 覆盖 DeleteSourceRows 中某条 DELETE 失败分支（删除 evidence_audio 表）。
func TestDeleteSourceRows_CascadeFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 删除 evidence_audio 表 → 循环中的 DELETE 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE evidence_audio`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceRows(ctx, models.SourceEpisode, "ep1"); err == nil {
		t.Fatal("evidence_audio 表缺失时 DeleteSourceRows 应报错")
	}
}

// TestDeleteSourceRows_Upload 验证 upload 源的 DeleteSourceRows 删除 uploads 行。
// 覆盖 DeleteSourceRows 中 sourceType == SourceUpload 分支。
func TestDeleteSourceRows_Upload(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceRows(ctx, models.SourceUpload, up.ID); err != nil {
		t.Fatalf("DeleteSourceRows(upload): %v", err)
	}
	if _, err := s.GetUploadByID(ctx, up.ID); err != ErrNotFound {
		t.Errorf("upload 应被删除，实际 err=%v", err)
	}
}

// TestDeleteSourceRows_UploadDeleteFails 验证 upload 源删除失败时报错。
// 覆盖 DeleteSourceRows 中 source 本体删除失败分支（删除 uploads 表）。
func TestDeleteSourceRows_UploadDeleteFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE uploads`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceRows(ctx, models.SourceUpload, "up-1"); err == nil {
		t.Fatal("uploads 表缺失时 DeleteSourceRows 应报错")
	}
}
