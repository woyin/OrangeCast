package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedHighlightAndNarration 为 episode 写入 highlight 版本 + 一条 Narration。
func seedHighlightAndNarration(t *testing.T, srv *Server, sourceID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	hs := &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "hl-a", Gist: "第一个高光解说", Citations: []string{"seg-0001", "seg-0002"}},
		{ID: "hl-b", Gist: "第二个高光解说", Citations: []string{"seg-0003"}},
	}}
	payload, _ := json.Marshal(hs)
	v, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", job.ID, string(payload))
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, v)

	// 转录（DJ 页解析 citation 时间范围需要）
	tv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"a"},{"id":"seg-0002","start":5,"end":10,"text":"b"},{"id":"seg-0003","start":10,"end":15,"text":"c"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv)

	// 一条 Narration（hl-a）
	os.MkdirAll(srv.cfg.NarrationDir, 0o755)
	rel := sourceID + "_hl-a_1.wav"
	wavPath := filepath.Join(srv.cfg.NarrationDir, rel)
	os.WriteFile(wavPath, []byte("fake-wav-content"), 0o644)
	srv.store.CreateNarration(ctx, models.SourceEpisode, sourceID, "hl-a", "af_heart", "kokoro-82m", rel, 1.5, 10, "kokoro")
	return rel, wavPath
}

// TestDJRenders_WithNarrationURLs (ADR-0019 R3)
// DJ 页应渲染：自动播放全集按钮、有 Narration 的高光带试听按钮、无 Narration 的显示"未生成"标记。
func TestDJRenders_WithNarrationURLs(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "dj@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedHighlightAndNarration(t, srv, sourceID)

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "自动播放全集") {
		t.Error("DJ 页应含自动播放全集按钮")
	}
	if !strings.Contains(body, "dj-autoplay") {
		t.Error("应含 dj-autoplay 元素")
	}
	// hl-a 有 Narration → 带试听按钮，无"未生成"标记
	if !strings.Contains(body, `data-narration="/api/narration/episode/`+sourceID+`/hl-a"`) {
		t.Error("hl-a 应带 Narration URL")
	}
	// hl-b 无 Narration → 显示"AI 解说未生成"
	if !strings.Contains(body, "AI 解说未生成") {
		t.Error("无 Narration 的高光应显示未生成标记")
	}
	// 原音手动播放按钮仍在
	if !strings.Contains(body, "播放这段原音") {
		t.Error("DJ 页应保留原音手动播放按钮")
	}
}

// TestNarrationServe_ReturnsWav (ADR-0019)
// /api/narration/... serve 当前 wav；不存在时 404。
func TestNarrationServe_ReturnsWav(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ns@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	_, wavPath := seedHighlightAndNarration(t, srv, sourceID)

	// 有 Narration → 200 + wav 内容
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/narration/episode/"+sourceID+"/hl-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("serve Narration 应 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "fake-wav-content" {
		t.Errorf("应返回 wav 内容，实际 %q", rec.Body.String())
	}
	_ = wavPath

	// 无 Narration → 404
	rec404 := doWithCookie(srv, cookie, http.MethodGet, "/api/narration/episode/"+sourceID+"/hl-nope")
	if rec404.Code != http.StatusNotFound {
		t.Errorf("不存在的 Narration 应 404，实际 %d", rec404.Code)
	}
}
