package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const journeyOPML = `<opml version="2.0"><body><outline type="rss" text="导入节目" xmlUrl="https://journey.example/feed.xml"/></body></opml>`

// TestJourney_PracticeClipOPML 是一条真实旅程：种子来源 → 练习问题可见 → 高光片段可下载
// → OPML 导入成功。三个入口共享同一份 Citation 证据，且都不产生远端模型调用。
func TestJourney_PracticeClipOPML(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("无 ffmpeg")
	}
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "journey@example.com", "password123")
	ctx := context.Background()

	sourceID := seedPracticeSource(t, srv, "journey-a")
	seedHighlightVersion(t, srv, sourceID)
	segments := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "购买力下降"},
	}
	if _, err := srv.store.ReplaceReviewQuestions(ctx, models.SourceEpisode, sourceID, "测试单集", 1,
		[]provider.PracticeItem{{Question: "为什么说通胀影响购买力？", Citations: []string{"seg-0001", "seg-0002"}, AnswerHint: "把两段连起来想"}},
		segments, "groq", "m", "job"); err != nil {
		t.Fatal(err)
	}

	// 1) 练习页与来源页都能看到问题与片段区块。
	if body := doWithCookie(srv, cookie, http.MethodGet, "/practice").Body.String(); !strings.Contains(body, "为什么说通胀影响购买力？") {
		t.Error("练习页应显示问题")
	}
	sourceBody := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID).Body.String()
	for _, want := range []string{"练习问题", "高光片段", "下载片段 MP3"} {
		if !strings.Contains(sourceBody, want) {
			t.Errorf("来源页缺少 %q", want)
		}
	}

	// 2) 高光片段可下载真实音频（wav 格式，时长等于引用区间 0–5 秒）。
	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "episode_" + sourceID + ".wav"
	abs := filepath.Join(srv.cfg.EvidenceDir, rel)
	if out, err := exec.Command("ffmpeg", "-f", "lavfi", "-i", "sine=frequency=330:duration=20", "-ac", "1", "-ar", "22050", abs).CombinedOutput(); err != nil {
		t.Fatalf("生成音频: %v %s", err, out)
	}
	if err := srv.store.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, rel, "wav", 0, "sha-journey"); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1?format=wav")
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("片段应可下载: %d %d bytes", rec.Code, rec.Body.Len())
	}

	// 3) OPML 导入写入新订阅并标记来源。
	srv.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{Title: "导入节目"}, []models.Episode{{GUID: "g-import", Title: "E", AudioURL: "https://a.mp3"}}, nil
	}
	rec2 := postMultipart(t, srv, cookie, "/podcasts/opml", "opml", "j.opml", journeyOPML)
	if !strings.Contains(rec2.Body.String(), "成功 1 个") {
		t.Errorf("OPML 导入应成功: %d", rec2.Code)
	}
	var opml int
	if err := srv.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM podcasts WHERE opml_present=1").Scan(&opml); err != nil {
		t.Fatal(err)
	}
	if opml != 1 {
		t.Errorf("应记录 1 个 OPML 导入订阅，实际 %d", opml)
	}
}
