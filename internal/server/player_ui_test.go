package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestSourceDetail_RendersCustomPlayer 可播放来源渲染自定义播放器：播放/跳过按钮、
// 音量、缓冲条与无障碍标签；原生 audio 保留 controls 作为无脚本兜底。
func TestSourceDetail_RendersCustomPlayer(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "player-ui@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://player.test/feed", "播放器", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "player-1", Title: "可播放单集", AudioURL: "https://media.test/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+eps[0].ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	wants := []string{
		`class="player-bar player-card"`,
		`id="play-btn"`,
		`data-state="paused"`,
		`id="player-volume"`,
		`id="player-buffered"`,
		`id="player-state"`,
		`class="icon-play"`,
		`class="icon-pause"`,
		`aria-label="播放"`,
		`id="source-audio"`,
		`controls`,
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("播放器缺少 %q", want)
		}
	}
}
