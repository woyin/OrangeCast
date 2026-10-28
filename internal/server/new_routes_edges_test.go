package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestNewRoutes_PathAndMethodEdges 新增端点对残缺路径与方法必须给出明确状态码。
func TestNewRoutes_PathAndMethodEdges(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "edges@example.com", "password123")
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/translation/episode", http.StatusNotFound},
		{http.MethodGet, "/api/mindmap/episode", http.StatusNotFound},
		{http.MethodGet, "/api/clip/episode/only-two", http.StatusNotFound},
		{http.MethodGet, "/sources/episode/abc/translation", http.StatusNotFound},
		{http.MethodGet, "/sources/episode/abc/mindmap", http.StatusNotFound},
		{http.MethodGet, "/practice/action", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		rec := doWithCookie(srv, cookie, tc.method, tc.path)
		if rec.Code != tc.want {
			t.Errorf("%s %s -> %d，期望 %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

// TestMindMapAPI_CorruptTranscript 转录损坏时导图仍可用（区间解析失败只丢节点）。
func TestMindMapAPI_CorruptTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm-tr@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-tr")
	ctx := context.Background()
	if _, err := srv.store.DB.ExecContext(ctx,
		"UPDATE artifact_versions SET payload='not-json' WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	if err := seedCardVersion(t, srv, sourceID, `{"title":"卡","summary":{"text":"s","citations":["seg-0001"]},"keyPoints":[],"chapters":[{"title":"章","gist":"g","citations":["seg-0001"]}],"quotes":[],"tags":[],"suggestedQuestions":[]}`); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/mindmap/episode/"+sourceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	var body struct {
		Root struct {
			Children []map[string]any `json:"children"`
		} `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Root.Children) != 0 {
		t.Errorf("转录损坏时不应挂出无法定位的节点: %+v", body.Root.Children)
	}
}

// TestTranslationPage_CorruptTranscript 转录损坏时译文页 500，不展示空表。
func TestTranslationPage_CorruptTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-corrupt@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-corrupt")
	if _, err := srv.store.DB.ExecContext(context.Background(),
		"UPDATE artifact_versions SET payload='not-json' WHERE source_type='episode' AND source_id=? AND kind='transcript'", sourceID); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/translation?lang=zh")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("损坏转录应 500，实际 %d", rec.Code)
	}
	recAPI := doWithCookie(srv, cookie, http.MethodGet, "/api/translation/episode/"+sourceID+"?lang=zh")
	if recAPI.Code != http.StatusInternalServerError {
		t.Fatalf("API 遇损坏转录应 500，实际 %d", recAPI.Code)
	}
}

// TestClip_CorruptHighlightSet 高光载荷损坏时 500，不返回空文件。
func TestClip_CorruptHighlightSet(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "clip-corrupt@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "clip-corrupt")
	ctx := context.Background()
	var jobID string
	if err := srv.store.DB.QueryRowContext(ctx, "SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1", sourceID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", jobID, "not-json")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, version); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/clip/episode/"+sourceID+"/hl-1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("损坏高光应 500，实际 %d", rec.Code)
	}
}

// seedCardVersion 写入一份当前知识卡片版本。
func seedCardVersion(t *testing.T, srv *Server, sourceID, payload string) error {
	t.Helper()
	ctx := context.Background()
	var jobID string
	if err := srv.store.DB.QueryRowContext(ctx, "SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1", sourceID).Scan(&jobID); err != nil {
		return err
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", jobID, payload)
	if err != nil {
		return err
	}
	return srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, version)
}

// 引用 provider 包，保持与卡片文本字段一致的类型使用。
var _ = provider.KnowledgeCard{}
