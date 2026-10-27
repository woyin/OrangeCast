package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// existingJobID 复用该来源已有的处理任务作为产物来历（artifact_versions.job_id 外键）。
func existingJobID(t *testing.T, srv *Server, sourceID string) string {
	t.Helper()
	var id string
	if err := srv.store.DB.QueryRowContext(context.Background(),
		`SELECT id FROM processing_jobs WHERE source_type='episode' AND source_id=? LIMIT 1`,
		sourceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestHighlightClip_MissingSource 无高光版本时返回 404，不猜测区间。
func TestHighlightClip_MissingSource(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip1@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/missing/hl-1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("应返回 404，实际 %d", rec.Code)
	}
}

// TestHighlightClip_RejectsUnknownHighlight 高光 ID 不存在时 404。
func TestHighlightClip_RejectsUnknownHighlight(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip2@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "clip-a")
	seedHighlightVersion(t, srv, sourceID)
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-unknown")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知高光应 404，实际 %d", rec.Code)
	}
}

// TestHighlightClip_RejectsUnsupportedFormat 只接受 mp3 与 wav。
func TestHighlightClip_RejectsUnsupportedFormat(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip3@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/x/hl-1?format=flac")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法格式应 400，实际 %d", rec.Code)
	}
}

// TestHighlightClip_RequiresEvidenceAudio 没有证据音频时明确拒绝，不返回空文件。
func TestHighlightClip_RequiresEvidenceAudio(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip4@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "clip-b")
	seedHighlightVersion(t, srv, sourceID)
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无证据音频应 404，实际 %d", rec.Code)
	}
}

// TestHighlightClip_NoTranscript 有高光但无转录时无法解析区间，显式 404。
func TestHighlightClip_NoTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip5@example.com", "password123")
	ctx := context.Background()
	p, err := srv.store.CreatePodcast(ctx, "https://clip-no-tr.xml", "P", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "clip-no-tr", Title: "E", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	seedJob, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MarkJobRunning(ctx, seedJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkJobSucceeded(ctx, seedJob.ID); err != nil {
		t.Fatal(err)
	}
	jobID := seedJob.ID
	payload := `{"highlights":[{"id":"hl-1","gist":"g","citations":["seg-0001"]}]}`
	if _, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", jobID, payload); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无转录应 404，实际 %d", rec.Code)
	}
}

// TestHighlightClip_RejectsPost 非 GET/HEAD 方法显式拒绝。
func TestHighlightClip_RejectsPost(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip6@example.com", "password123")
	// 未带 CSRF 的写请求先被中间件拒绝（403）；带 CSRF 才到达 handler 的 405。
	rec := doWithCookie(srv, cookie, http.MethodPost, "/api/clip/episode/x/hl-1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("无 CSRF 的 POST 应 403，实际 %d", rec.Code)
	}
}

// TestHighlightClip_UnresolvableCitations 高光引用无法解析区间时 422，不返回 0 秒片段。
func TestHighlightClip_UnresolvableCitations(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip7@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "clip-unresolvable")
	ctx := context.Background()
	jobID := existingJobID(t, srv, sourceID)
	payload := `{"highlights":[{"id":"hl-1","gist":"g","citations":["ghost"]}]}`
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", jobID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, version); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("无有效区间应 422，实际 %d", rec.Code)
	}
}
