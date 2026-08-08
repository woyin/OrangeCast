package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// postFormRaw 直接 POST（不先 GET CSRF）——用于测试无 CSRF 被拒。
func doRawPost(srv *Server, cookie *http.Cookie, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

// TestPurgeRoute_RegisteredAndRemoves (ADR-0012)
// /api/purge 路由可达（修复前 handlePurge 未注册，Purge 功能不可达）。
// POST 触发后删除 Source 及其证据。
func TestPurgeRoute_RegisteredAndRemoves(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "purge@example.com", "password123")
	ctx := context.Background()

	// 造数据：episode + transcript 版本 + 证据文件
	p, _ := srv.store.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "https://a.mp3"}})
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	job, _ := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, `{"text":"x"}`)
	os.MkdirAll(srv.cfg.EvidenceDir, 0o755)
	evPath := filepath.Join(srv.cfg.EvidenceDir, "episode_"+sourceID+".mp3")
	os.WriteFile(evPath, []byte("audio"), 0o644)

	// 前置：episode 存在
	if _, err := srv.store.GetEpisodeByID(ctx, sourceID); err != nil {
		t.Fatalf("前置：episode 应存在: %v", err)
	}

	// 带 CSRF 的 POST 触发 Purge
	rec := postForm(t, srv, cookie, "/api/purge",
		"source_type=episode&source_id="+sourceID)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
		t.Fatalf("Purge 应 303 或 200，实际 %d: %s", rec.Code, rec.Body.String())
	}

	// episode 已删除
	if _, err := srv.store.GetEpisodeByID(ctx, sourceID); err != store.ErrNotFound {
		t.Errorf("episode 应被删除，实际 err=%v", err)
	}
	// 证据文件已删除
	if _, err := os.Stat(evPath); !os.IsNotExist(err) {
		t.Error("证据文件应被删除")
	}
	// artifact_versions 已删除
	var n int
	srv.store.DB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE source_id=?`, sourceID).Scan(&n)
	if n != 0 {
		t.Errorf("artifact_versions 应为 0，实际 %d", n)
	}
}

// TestPurge_NotFoundSource (ADR-0012)
// Purge 不存在的 source 不应 panic 或 500。
func TestPurge_NotFoundSource(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "purge2@example.com", "password123")
	rec := postForm(t, srv, cookie, "/api/purge", "source_type=episode&source_id=nonexistent")
	if rec.Code >= http.StatusInternalServerError {
		t.Fatalf("Purge 不存在 source 不应 5xx，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestPurge_RequiresCSRF
// 无 CSRF 的 Purge POST 应被拒绝（安全门槛）。
func TestPurge_RequiresCSRF(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "purge3@example.com", "password123")
	rec := doRawPost(srv, cookie, "/api/purge", "source_type=episode&source_id=x")
	if rec.Code == http.StatusOK || rec.Code == http.StatusSeeOther {
		t.Error("无 CSRF 的 Purge 应被拒绝")
	}
}
