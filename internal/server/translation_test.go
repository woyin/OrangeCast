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

// seedTranscriptSegments 覆盖测试播种的转录片段（与 seedTranscript 保持一致）。
var seedTranscriptSegments = []provider.Segment{
	{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"},
	{ID: "seg-0002", Start: 5, End: 10, Text: "购买力下降"},
}

// TestTranslationPage_ShowsAlignedRows 译文页并列显示原文与译文，未翻译段标为缺失。
func TestTranslationPage_ShowsAlignedRows(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-page@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-a")
	ctx := context.Background()
	if _, err := srv.store.ReplaceSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0001", Text: "通胀就是物价上涨"}}, seedTranscriptSegments, "groq", "m", "job"); err != nil {
		t.Fatal(err)
	}

	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/translation?lang=zh")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"通胀是物价上升", "通胀就是物价上涨", "尚未翻译", "带证衍生"} {
		if !strings.Contains(body, want) {
			t.Errorf("译文页缺少 %q", want)
		}
	}
}

// TestTranslationPage_RequiresTranscript 无转录时明确 404，不展示空表。
func TestTranslationPage_RequiresTranscript(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-none@example.com", "password123")
	ctx := context.Background()
	p, err := srv.store.CreatePodcast(ctx, "https://tr-none.xml", "P", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "tr-none", Title: "E", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+eps[0].ID+"/translation?lang=zh")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无转录应 404，实际 %d", rec.Code)
	}
}

// TestTranslationAction_EnqueuesJob 显式提交翻译后入队持久任务。
func TestTranslationAction_EnqueuesJob(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-post@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-b")
	rec := postForm(t, srv, cookie, "/sources/episode/"+sourceID+"/translation", "target_language=zh")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("应重定向，实际 %d: %s", rec.Code, rec.Body.String())
	}
	jobs, err := srv.store.ListQueuedOrRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.JobType == models.JobTranslate {
			found = true
		}
	}
	if !found {
		t.Fatal("应入队 translate 任务")
	}
}

// TestTranslationAction_RejectsMissingLanguage 缺目标语言显式 400。
func TestTranslationAction_RejectsMissingLanguage(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-bad@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-c")
	rec := postForm(t, srv, cookie, "/sources/episode/"+sourceID+"/translation", "target_language=")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺语言应 400，实际 %d", rec.Code)
	}
}

// TestTranslationAPI_ReturnsAlignedSegments API 返回逐段对齐与缺失标记。
func TestTranslationAPI_ReturnsAlignedSegments(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "tr-api@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "tr-d")
	ctx := context.Background()
	if _, err := srv.store.ReplaceSegmentTranslations(ctx, models.SourceEpisode, sourceID, 1, "zh",
		[]provider.TranslatedSegment{{ID: "seg-0002", Text: "购买力降低"}}, seedTranscriptSegments, "groq", "m", "job"); err != nil {
		t.Fatal(err)
	}
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/translation/episode/"+sourceID+"?lang=zh")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	var body struct {
		Segments []struct {
			SegmentID   string  `json:"segment_id"`
			Original    string  `json:"original"`
			Translation *string `json:"translation"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Segments) != 2 {
		t.Fatalf("应返回两段，实际 %d", len(body.Segments))
	}
	if body.Segments[0].Translation != nil {
		t.Error("未翻译段应为 null")
	}
	if body.Segments[1].Translation == nil || *body.Segments[1].Translation != "购买力降低" {
		t.Errorf("第二段应有译文: %+v", body.Segments[1])
	}
}

// TestMindMapAPI_BuildsChapterTree 导图以章节为骨架、要点挂到覆盖其引用的章节。
func TestMindMapAPI_BuildsChapterTree(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-a")
	ctx := context.Background()
	card := `{"title":"通胀入门","summary":{"text":"概览","citations":["seg-0001"]},` +
		`"keyPoints":[{"content":"物价上升","description":"d","citations":["seg-0001"]}],` +
		`"chapters":[{"title":"第一章","gist":"g","citations":["seg-0001","seg-0002"]}],` +
		`"quotes":[],"tags":[],"suggestedQuestions":[]}`
	var jobID string
	if err := srv.store.DB.QueryRowContext(ctx, "SELECT id FROM processing_jobs WHERE source_id=? LIMIT 1", sourceID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", jobID, card)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, version); err != nil {
		t.Fatal(err)
	}

	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/mindmap/episode/"+sourceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Root struct {
			Label    string `json:"label"`
			Children []struct {
				Label    string `json:"label"`
				Kind     string `json:"kind"`
				Children []struct {
					Label string `json:"label"`
				} `json:"children"`
			} `json:"children"`
		} `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Root.Label != "通胀入门" {
		t.Errorf("根节点应为卡片标题: %q", body.Root.Label)
	}
	if len(body.Root.Children) == 0 || body.Root.Children[0].Kind != "chapter" {
		t.Fatalf("应有章节节点: %+v", body.Root.Children)
	}
	if len(body.Root.Children[0].Children) != 1 || body.Root.Children[0].Children[0].Label != "物价上升" {
		t.Errorf("要点应挂到覆盖其引用的章节: %+v", body.Root.Children[0].Children)
	}
}

// TestMindMapAPI_RequiresCard 无卡片时 404，不编造结构。
func TestMindMapAPI_RequiresCard(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm-none@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-b")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/mindmap/episode/"+sourceID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无卡片应 404，实际 %d", rec.Code)
	}
}

// TestMindMapPage_RendersContainer 页面渲染容器与只读数据地址。
func TestMindMapPage_RendersContainer(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "mm-page@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "mm-c")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID+"/mindmap")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/api/mindmap/episode/"+sourceID) {
		t.Error("页面应引用只读数据地址")
	}
}
