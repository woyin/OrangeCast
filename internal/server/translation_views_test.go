package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedCard 写入一份当前知识卡片版本（复用来源已有任务作为产物来历）。
func seedCard(t *testing.T, srv *Server, sourceID, payload string) {
	t.Helper()
	ctx := context.Background()
	var jobID string
	if err := srv.store.DB.QueryRowContext(ctx, "SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1", sourceID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", jobID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, version); err != nil {
		t.Fatal(err)
	}
}

// TestTranslationPage_ShowsLanguages 已有译文时页面列出语言入口。
func TestTranslationPage_ShowsLanguages(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-lang@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-e")
	if _, err := srv.store.ReplaceSegmentTranslations(context.Background(), models.SourceEpisode, sourceID, 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "甲"}}, seedTranscriptSegments, "groq", "m", "j"); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/translation")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已有译文") {
		t.Error("应列出已有译文语言")
	}
}

// TestReadonlyAPIs_RejectWrites 译文与导图 API 都是只读：写请求被拒绝。
func TestReadonlyAPIs_RejectWrites(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ro@example.com", "password123")
	for _, path := range []string{"/api/translation/episode/x?lang=zh", "/api/mindmap/episode/x"} {
		rec := doWithCookie(srv, cookie, http.MethodPost, path)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s 写请求应被拒绝，实际 %d", path, rec.Code)
		}
	}
}

// TestTranslationAPI_MissingTranscript 无转录时 404。
func TestTranslationAPI_MissingTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-none-api@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/translation/episode/missing?lang=zh")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无转录应 404，实际 %d", rec.Code)
	}
}

// TestMindMapAPI_UnresolvableChapterSkipped 无法解析区间的章节不进入导图。
func TestMindMapAPI_UnresolvableChapterSkipped(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm-skip@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-d")
	seedCard(t, srv, sourceID, `{"title":"卡","summary":{"text":"概览","citations":["ghost"]},"keyPoints":[],"chapters":[{"title":"幽灵章","gist":"g","citations":["ghost"]}],"quotes":[],"tags":[],"suggestedQuestions":[]}`)

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
	for _, child := range body.Root.Children {
		if child["label"] == "幽灵章" {
			t.Error("无法解析区间的章节不应进入导图")
		}
	}
}

// TestMindMapAPI_CorruptCard 卡片载荷损坏时 500，不静默返回空导图。
func TestMindMapAPI_CorruptCard(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm-corrupt@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-e")
	seedCard(t, srv, sourceID, "not-json")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/mindmap/episode/"+sourceID)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("损坏卡片应 500，实际 %d", rec.Code)
	}
}
