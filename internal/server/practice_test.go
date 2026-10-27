package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedPracticeSource 建一条来源并写入当前转录版本（不处理登录）。
func seedPracticeSource(t *testing.T, srv *Server, guid string) string {
	t.Helper()
	ctx := context.Background()
	p, err := srv.store.CreatePodcast(ctx, "https://"+guid+".xml", "Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: guid, Title: "测试单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	seedTranscript(t, srv, eps[0].ID)
	return eps[0].ID
}

// seedHighlightVersion 写入当前高光版本（含一条可解析区间的引用）。
func seedHighlightVersion(t *testing.T, srv *Server, sourceID string) {
	t.Helper()
	ctx := context.Background()
	payload := `{"highlights":[{"id":"hl-1","gist":"值得听","citations":["seg-0001"]}]}`
	// 复用该来源已有的处理任务作为产物来历（artifact_versions.job_id 外键）。
	var jobID string
	if err := srv.store.DB.QueryRowContext(ctx,
		`SELECT id FROM processing_jobs WHERE source_type='episode' AND source_id=? LIMIT 1`,
		sourceID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, "groq", "m", "1", jobID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight, version); err != nil {
		t.Fatal(err)
	}
}

// TestPracticePage_ShowsQuestions 练习页展示问题、AI 提示与来源定位。
func TestPracticePage_ShowsQuestions(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "practice@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "practice-a")
	ctx := context.Background()
	if _, err := srv.store.ReplaceReviewQuestions(ctx, models.SourceEpisode, sourceID, "测试单集", 1,
		[]provider.PracticeItem{{Question: "这段讲了什么？", Citations: []string{"seg-0001"}, AnswerHint: "回忆结论"}},
		[]provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}}, "groq", "m", "job"); err != nil {
		t.Fatal(err)
	}

	rec := doWithCookie(srv, cookie, http.MethodGet, "/practice")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"这段讲了什么？", "AI 提示（非原文）", "测试单集", "先自己回答"} {
		if !strings.Contains(body, want) {
			t.Errorf("练习页缺少 %q", want)
		}
	}
}

// TestPracticePage_EmptyState 没有问题时不报错，给出生成入口。
func TestPracticePage_EmptyState(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "practice-empty@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/practice")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "还没有练习问题") {
		t.Error("空状态应提示生成入口")
	}
}

// TestPracticeGenerate_EnqueuesJob 显式生成为有转录的来源入队持久任务。
func TestPracticeGenerate_EnqueuesJob(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "practice-gen@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "practice-b")

	rec := postForm(t, srv, cookie, "/practice/action",
		"action=generate&source_type=episode&source_id="+sourceID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("生成应重定向，实际 %d: %s", rec.Code, rec.Body.String())
	}
	jobs, err := srv.store.ListQueuedOrRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.JobType == models.JobPracticeReview {
			found = true
		}
	}
	if !found {
		t.Fatal("应入队 practice_review 任务")
	}
}

// TestPracticeGenerate_BatchCountsSources 批量生成按来源计数并重定向到练习页。
func TestPracticeGenerate_BatchCountsSources(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "practice-batch@example.com", "password123")
	seedPracticeSource(t, srv, "practice-c")
	rec := postForm(t, srv, cookie, "/practice/action", "action=generate&all=1")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("批量生成应重定向，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "generated=1") {
		t.Errorf("应报告 1 个来源已入队，实际 %q", rec.Header().Get("Location"))
	}
}

// TestPracticeAction_RejectsUnknownAction 未知动作显式 400。
func TestPracticeAction_RejectsUnknownAction(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "practice-bad@example.com", "password123")
	rec := postForm(t, srv, cookie, "/practice/action", "action=delete")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知动作应 400，实际 %d", rec.Code)
	}
}
