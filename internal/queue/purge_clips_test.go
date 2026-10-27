package queue

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestPurge_RemovesHighlightClips 删除来源时同时清理派生片段缓存（ADR-0032）：
// 片段可按原音重建，因此既不进备份，也不在来源删除后保留孤儿文件。
func TestPurge_RemovesHighlightClips(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	if err := os.MkdirAll(w.clipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clipPath := filepath.Join(w.clipDir, "sha-purge_h0_5000.mp3")
	if err := os.WriteFile(clipPath, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHighlightClip(ctx, "sha-purge", 0, 5, "mp3", "sha-purge_h0_5000.mp3", 4); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 10, "sha-purge"); err != nil {
		t.Fatal(err)
	}

	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := w.ResumePurges(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(clipPath); !os.IsNotExist(err) {
		t.Errorf("片段文件应删除，实际 err=%v", err)
	}
	if _, err := s.GetHighlightClip(ctx, "sha-purge", 0, 5, "mp3"); err == nil {
		t.Error("片段缓存行应删除")
	}
}
