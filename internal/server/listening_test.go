package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestListeningProgress_RoundTrip D07：保存→读取闭环；旧 seq 不覆盖新状态。
func TestListeningProgress_RoundTrip(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "listen@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/listen.xml", "收听播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "l1", Title: "收听单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)

	// 获取 CSRF cookie（POST 受 CSRF 保护）。
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/listening-progress", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	rec := post(`{"source_type":"episode","source_id":"` + eps[0].ID + `","plan_id":"highlights:v1","plan_version":1,"item_position":2,"highlight_id":"h1","item_offset_seconds":12.5,"speed":1.5,"seq":100}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存应 200: %d %s", rec.Code, rec.Body.String())
	}
	p, err := srv.store.GetListeningProgress(t.Context(), models.SourceEpisode, eps[0].ID)
	if err != nil || p.ItemPosition != 2 || p.ItemOffsetSeconds != 12.5 || p.Seq != 100 {
		t.Fatalf("进度应保存: %+v %v", p, err)
	}
	// 倒序请求（更小 seq）：不覆盖新状态
	rec = post(`{"source_type":"episode","source_id":"` + eps[0].ID + `","item_position":1,"item_offset_seconds":1,"seq":50}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("旧 seq 请求应 200 且忽略: %d", rec.Code)
	}
	p2, _ := srv.store.GetListeningProgress(t.Context(), models.SourceEpisode, eps[0].ID)
	if p2.ItemPosition != 2 {
		t.Fatalf("旧 seq 不得覆盖新状态: %+v", p2)
	}
}

// TestListeningProgress_PageWiring D07：DJ 页包含进度保存/恢复与 Media Session 接线。
func TestListeningProgress_PageWiring(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "djlisten@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/djl.xml", "DJ收听", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "djl-1", Title: "收听DJ单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	seedHighlightAndNarration(t, srv, eps[0].ID)
	rec := doWithCookie(srv, session, http.MethodGet, "/sources/episode/"+eps[0].ID+"/dj")
	if rec.Code != http.StatusOK {
		t.Fatalf("DJ 页应 200: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"/api/listening-progress", // 保存与恢复端点
		"继续听",                     // 恢复提示
		"mediaSession",            // 系统媒体控件接入
		"visibilitychange",        // 离开页面补存
		"highlights:v",            // 清单身份
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("DJ 页缺少 %q", want)
		}
	}
}
