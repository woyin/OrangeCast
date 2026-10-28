package queue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestPurge_DerivativeCleanupFailures 派生数据清理失败必须显式报错，
// 不得当作已删除继续标记完成（否则会留下孤儿问题/译文/片段）。
func TestPurge_DerivativeCleanupFailures(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, w *Worker, sourceID string)
		want    string
	}{
		{
			name: "clip table unreadable",
			prepare: func(t *testing.T, w *Worker, sourceID string) {
				if _, err := w.store.DB.Exec("DROP TABLE highlight_clips"); err != nil {
					t.Fatal(err)
				}
			},
			want: "读取片段缓存",
		},
		{
			name: "clip path escapes directory",
			prepare: func(t *testing.T, w *Worker, sourceID string) {
				if _, err := w.store.CreateHighlightClip(context.Background(), "sha-pd", 0, 5, "mp3", "../escape.mp3", 1); err != nil {
					t.Fatal(err)
				}
			},
			want: "片段路径不在片段目录内",
		},
		{
			name: "clip cache row without file is tolerated",
			prepare: func(t *testing.T, w *Worker, sourceID string) {
				if _, err := w.store.CreateHighlightClip(context.Background(), "sha-pd", 0, 5, "mp3", "missing.mp3", 1); err != nil {
					t.Fatal(err)
				}
			},
			want: "",
		},
		{
			name: "review question cleanup unreadable",
			prepare: func(t *testing.T, w *Worker, sourceID string) {
				if _, err := w.store.DB.Exec("DROP TABLE review_questions"); err != nil {
					t.Fatal(err)
				}
			},
			want: "删除练习问题",
		},
		{
			name: "translation cleanup unreadable",
			prepare: func(t *testing.T, w *Worker, sourceID string) {
				if _, err := w.store.DB.Exec("DROP TABLE segment_translations"); err != nil {
					t.Fatal(err)
				}
			},
			want: "删除对齐译文",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			sourceID := seedEpisode(t, s)
			if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 10, "sha-pd"); err != nil {
				t.Fatal(err)
			}
			tc.prepare(t, w, sourceID)
			if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
				t.Fatal(err)
			}
			err := w.ResumePurges(ctx)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("缺失片段文件应视为已删除，实际 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应报错 %q，实际 %v", tc.want, err)
			}
		})
	}
}

// TestPurge_ClipRemovalFailure 片段文件无法删除时显式报错，不跳过并标记完成。
func TestPurge_ClipRemovalFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 10, "sha-pd2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHighlightClip(ctx, "sha-pd2", 0, 5, "mp3", "blocked.mp3", 4); err != nil {
		t.Fatal(err)
	}
	// 用目录占位，使 os.Remove 以“目录非空”失败。
	if err := os.MkdirAll(filepath.Join(w.clipDir, "blocked.mp3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.clipDir, "blocked.mp3", "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	err := w.ResumePurges(ctx)
	if err == nil || !strings.Contains(err.Error(), "删除片段") {
		t.Fatalf("应报告片段删除失败，实际 %v", err)
	}
}

// TestPurge_LateStageFailures Purge 后段失败必须显式返回，让恢复流程可以重试：
// DB 行删除失败与标记完成失败都不能静默跳过。
func TestPurge_LateStageFailures(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		want string
	}{
		{
			name: "source row delete fails",
			ddl:  "CREATE TRIGGER block_source_delete BEFORE DELETE ON episodes BEGIN SELECT RAISE(ABORT,'no'); END",
			want: "删除 DB 行",
		},
		{
			name: "mark purge done fails",
			ddl:  "CREATE TRIGGER block_purge_done BEFORE UPDATE ON purges BEGIN SELECT RAISE(ABORT,'no'); END",
			want: "mark purge done",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			sourceID := seedEpisode(t, s)
			if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 10, "sha-pd3"); err != nil {
				t.Fatal(err)
			}
			if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(tc.ddl); err != nil {
				t.Fatal(err)
			}
			err := w.ResumePurges(ctx)
			if err == nil {
				t.Fatalf("%s: 期望错误，实际 nil", tc.name)
			}
			if tc.want == "mark purge done" {
				return // 该分支返回原始 SQLite 错误文本，只断言失败可见
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应报错 %q，实际 %v", tc.want, err)
			}
		})
	}
}

// TestPurge_RejectsNonLocalEvidencePath 记录的证据路径越出证据目录时拒绝删除，
// 避免按被篡改的路径删除目录外文件。
func TestPurge_RejectsNonLocalEvidencePath(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "../escape.mp3", "mp3", 10, "sha-pd4"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	err := w.ResumePurges(ctx)
	if err == nil || !strings.Contains(err.Error(), "原音路径不在证据目录内") {
		t.Fatalf("应拒绝越界路径，实际 %v", err)
	}
}
