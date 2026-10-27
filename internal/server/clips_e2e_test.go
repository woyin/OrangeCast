package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestHighlightClip_RealCutAndCache 用真实音频验证：首次请求切出片段并落缓存，
// 第二次请求命中缓存（不重复转码），片段时长与引用区间一致。
func TestHighlightClip_RealCutAndCache(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("无 ffmpeg，跳过真实切片段验证")
	}
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip-e2e@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "clip-e2e")
	seedHighlightVersion(t, srv, sourceID)

	ctx := context.Background()
	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "episode_" + sourceID + ".wav"
	abs := filepath.Join(srv.cfg.EvidenceDir, rel)
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "sine=frequency=440:duration=20", "-ac", "1", "-ar", "22050", abs)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成证据音频失败: %v %s", err, out)
	}
	if err := srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, rel, "wav", 0, "sha-e2e"); err != nil {
		t.Fatal(err)
	}

	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("首次请求应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type 应为 audio/mpeg，实际 %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("片段不应为空")
	}
	clip, err := srv.store.GetHighlightClip(ctx, "sha-e2e", 0, 5, "mp3")
	if err != nil {
		t.Fatalf("应写入片段缓存: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.clipDir(), clip.RelPath)); err != nil {
		t.Fatalf("缓存文件应存在: %v", err)
	}

	// 片段时长必须等于引用区间（程序解析，不是 AI 估算）。
	if out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", filepath.Join(srv.clipDir(), clip.RelPath)).Output(); err != nil {
		t.Fatalf("ffprobe: %v", err)
	} else if d, parseErr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); parseErr != nil || d < 4.5 || d > 5.6 {
		t.Errorf("片段时长应接近 5 秒，实际 %q", strings.TrimSpace(string(out)))
	}

	// 第二次请求命中缓存：先删除证据音频，仍能返回片段即证明没有重新转码。
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	rec2 := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec2.Code != http.StatusOK {
		t.Fatalf("缓存命中应仍返回 200，实际 %d", rec2.Code)
	}
	if rec2.Body.Len() != rec.Body.Len() {
		t.Errorf("两次输出应一致: %d vs %d", rec.Body.Len(), rec2.Body.Len())
	}
}
