package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/notesync"
	"github.com/woyin/orangecast/internal/store"
)

func seedExportableEpisode(t *testing.T, srv *Server) string {
	t.Helper()
	ctx := context.Background()
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	id := eps[0].ID
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, id, models.JobTranscribe)
	vt, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, id, store.KindTranscript, "groq", "m", "1", job.ID,
		`{"segments":[{"id":"seg-0001","start":7,"end":12,"text":"通胀是物价上升"}]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, id, store.KindTranscript, vt)
	card := `{"title":"通胀解析","summary":{"text":"概览","citations":["seg-0001"]},"keyPoints":[{"content":"要点","description":"d","citations":["seg-0001"]}],"chapters":[],"quotes":[{"text":"通胀是物价上升","citations":["seg-0001"]}],"tags":[]}`
	vc, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, id, store.KindKnowledgeCard, "groq", "m", "1", job.ID, card)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, id, store.KindKnowledgeCard, vc)
	srv.store.DB.ExecContext(ctx, `UPDATE episodes SET processing_status='processed' WHERE id=?`, id)
	return id
}

func TestSourceExport(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "export@example.com", "password123")
	id := seedExportableEpisode(t, srv)

	var got struct{ Highlights []notesync.Highlight }
	var auth string
	rw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer rw.Close()
	vault := filepath.Join(t.TempDir(), "vault")
	srv.cfg.ObsidianExportDir = vault

	csrf := ""
	for _, c := range doWithCookie(srv, cookie, http.MethodGet, "/dashboard").Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{"_csrf": {csrf}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	msgOf := func(rec *httptest.ResponseRecorder) (string, string) {
		u, _ := url.Parse(rec.Header().Get("Location"))
		return u.Query().Get("export_msg"), u.Query().Get("export_ok")
	}
	base := "/sources/episode/" + id + "/export/"

	// 未配置 token
	if msg, ok := msgOf(post(base + "readwise")); ok == "1" || !strings.Contains(msg, "READWISE_TOKEN") {
		t.Errorf("未配置应失败：%q %q", msg, ok)
	}
	// 配置后推送：金句与要点各一条
	srv.readwise = &notesync.Readwise{Token: "tok", Endpoint: rw.URL}
	rec := post(base + "readwise")
	if msg, ok := msgOf(rec); ok != "1" || !strings.Contains(msg, "2 条") {
		t.Errorf("推送结果：%q %q", msg, ok)
	}
	if auth != "Token tok" || len(got.Highlights) != 2 || got.Highlights[0].Text != "通胀是物价上升" || got.Highlights[0].Location != 7 {
		t.Errorf("推送内容：%q %+v", auth, got.Highlights)
	}
	// Obsidian
	rec = post(base + "obsidian")
	if msg, ok := msgOf(rec); ok != "1" || !strings.Contains(msg, "通胀解析") {
		t.Errorf("obsidian 结果：%q %q", msg, ok)
	}
	if b, err := os.ReadFile(filepath.Join(vault, "通胀解析.md")); err != nil || !strings.Contains(string(b), "通胀解析") {
		t.Errorf("vault 文件：%v", err)
	}
	// 详情页：已配置时出现入口，且回显结果
	page := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+id+"?export_ok=1&export_msg="+url.QueryEscape("已推送 2 条高光到 Readwise")).Body.String()
	for _, want := range []string{"推送高光到 Readwise", "写入 Obsidian vault", "已推送 2 条高光到 Readwise"} {
		if !strings.Contains(page, want) {
			t.Errorf("详情页应含 %q", want)
		}
	}
	// 未知目标、GET、无卡片、未配置目录
	if rec := post(base + "nope"); rec.Code != http.StatusNotFound {
		t.Errorf("未知目标应 404，实际 %d", rec.Code)
	}
	if rec := doWithCookie(srv, cookie, http.MethodGet, base+"readwise"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 应 405，实际 %d", rec.Code)
	}
	srv.cfg.ObsidianExportDir = ""
	if msg, ok := msgOf(post(base + "obsidian")); ok == "1" || !strings.Contains(msg, "OBSIDIAN_EXPORT_DIR") {
		t.Errorf("未配置目录：%q %q", msg, ok)
	}
	if msg, ok := msgOf(post("/sources/episode/no-such/export/readwise")); ok == "1" || msg == "" {
		t.Errorf("无卡片应失败：%q %q", msg, ok)
	}
	// 配置了 token 但目标不可达 → 回显错误
	srv.readwise = &notesync.Readwise{Token: "tok", Endpoint: "http://127.0.0.1:1"}
	if msg, ok := msgOf(post(base + "readwise")); ok == "1" || !strings.Contains(msg, "Readwise") {
		t.Errorf("推送失败应回显：%q %q", msg, ok)
	}
	// 没有高光可推
	srv.readwise = &notesync.Readwise{Token: "tok", Endpoint: rw.URL}
	srv.store.DB.ExecContext(context.Background(), `UPDATE artifact_versions SET payload=json_set(payload,'$.quotes',json('[]'),'$.keyPoints',json('[]')) WHERE kind=?`, store.KindKnowledgeCard)
	if msg, _ := msgOf(post(base + "readwise")); !strings.Contains(msg, "没有可推送") {
		t.Errorf("空卡片：%q", msg)
	}
}

// 转录里带说话人时，转录稿只在说话人变化处显示一次标签。
func TestSourceDetailShowsSpeakerLabels(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "speaker@example.com", "password123")
	id := seedExportableEpisode(t, srv)
	ctx := context.Background()
	var jobID string // 种子数据已入队过转录任务，重复入队会返回 nil，直接取已有的
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	v2, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, id, store.KindTranscript, "groq", "m", "1", jobID,
		`{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"第一句","speaker":"主持人"},{"id":"seg-0002","start":5,"end":9,"text":"第二句","speaker":"主持人"},{"id":"seg-0003","start":9,"end":12,"text":"第三句","speaker":"嘉宾"},{"id":"seg-0004","start":12,"end":15,"text":"无标签句"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, id, store.KindTranscript, v2)
	body := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+id).Body.String()
	if n := strings.Count(body, `<strong class="speaker">主持人</strong>`); n != 1 {
		t.Errorf("连续同一说话人只标一次，实际 %d 次", n)
	}
	if !strings.Contains(body, `<strong class="speaker">嘉宾</strong>`) {
		t.Error("换人时应出现新标签")
	}
	if strings.Count(body, `class="speaker"`) != 2 {
		t.Error("没有 speaker 的句子不应产生标签")
	}
}
