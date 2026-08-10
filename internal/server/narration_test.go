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

	// 知识卡片（DJ 页结尾 Take Aways = KeyPoints）
	cv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID,
		`{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[{"content":"要点","citations":["seg-0001"]}],"chapters":[],"quotes":[],"tags":[]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, cv)

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

// TestNarrationServe_MissingFile_404 验证 Narration 记录存在但 wav 文件缺失时 404。
func TestNarrationServe_MissingFile_404(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "nsm@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 写入 Narration 记录，但 wav 文件不存在
	srv.store.CreateNarration(ctx, models.SourceEpisode, sourceID, "hl-a", "af_heart", "kokoro-82m", "missing.wav", 1.5, 10, "kokoro")

	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/narration/episode/"+sourceID+"/hl-a")
	if rec.Code != http.StatusNotFound {
		t.Errorf("wav 文件缺失应 404，实际 %d", rec.Code)
	}
}

// TestDJ_NoHighlight_404 验证无高光版本时 DJ 页返回 404。
func TestDJ_NoHighlight_404(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "dj404@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	// 不写 Highlight 版本 → DJ 页 404
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusNotFound {
		t.Errorf("无高光应 404，实际 %d", rec.Code)
	}
}

// TestDJ_TranscriptMissing_404 验证有高光但无转录稿时 DJ 页 404。
func TestDJ_TranscriptMissing_404(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "djtr@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 只写高光版本，不写转录稿
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	hv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", job.ID,
		`{"highlights":[{"id":"hl-a","gist":"g","citations":["seg-0001"]}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, hv)

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusNotFound {
		t.Errorf("无转录稿应 404，实际 %d", rec.Code)
	}
}

// TestDJ_BadPath_404 验证非 dj 后缀路径返回 404。
func TestDJ_BadPath_404(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "djbad@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/x/other")
	if rec.Code != http.StatusNotFound {
		t.Errorf("非法路径应 404，实际 %d", rec.Code)
	}
}

// TestDJ_CorruptHighlight_500 验证高光载荷损坏时 DJ 页返回 500。
func TestDJ_CorruptHighlight_500(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "djch@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	hv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", job.ID, `{bad json`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, hv)

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("高光载荷损坏应 500，实际 %d", rec.Code)
	}
}

// TestDJ_CorruptTranscript_500 验证转录载荷损坏时 DJ 页返回 500。
func TestDJ_CorruptTranscript_500(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "djct@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	hv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", job.ID,
		`{"highlights":[{"id":"hl-a","gist":"g","citations":["seg-0001"]}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, hv)
	tv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, `{bad json`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv)

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("转录载荷损坏应 500，实际 %d", rec.Code)
	}
}

// TestDJ_InvalidCitationSkipped 验证高光引用不存在的 Segment 时被跳过。
// 覆盖 handleDJ 中 ResolveCitationSpan !ok → continue 分支。
func TestDJ_InvalidCitationSkipped(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "djskip@example.com", "password123")
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID

	// 高光引用不存在的 segment（seg-9999）→ ResolveCitationSpan !ok → 跳过
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	hs := &provider.HighlightSet{Highlights: []provider.Highlight{
		{ID: "h1", Gist: "gist", Citations: []string{"seg-9999"}},
	}}
	hsJSON, _ := json.Marshal(hs)
	hv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", job.ID, string(hsJSON))
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, hv)
	// 转录版本存在（含有效 segment）
	tp := &provider.TranscriptPayload{Language: "en", Text: "x", Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}}
	tpJSON, _ := json.Marshal(tp)
	tv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, string(tpJSON))
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv)

	// 页面应正常渲染，但高光因引用无效被跳过（不 panic）
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
}
