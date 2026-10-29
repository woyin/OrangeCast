package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

type sourceProgressResponse struct {
	SourceType string `json:"source_type"`
	Status     string `json:"status"`
	Stages     []struct {
		Stage       string `json:"stage"`
		Label       string `json:"label"`
		Status      string `json:"status"`
		StatusLabel string `json:"status_label"`
		Detail      string `json:"detail"`
	} `json:"stages"`
	Job *struct {
		Type           string `json:"type"`
		Label          string `json:"label"`
		Status         string `json:"status"`
		QueuedPosition int    `json:"queued_position"`
	} `json:"job"`
	Next     string `json:"next"`
	Complete bool   `json:"complete"`
	Failed   bool   `json:"failed"`
}

func getSourceProgress(t *testing.T, srv *Server, cookie *http.Cookie, query string) (*http.Response, []byte) {
	t.Helper()
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/source-progress?"+query)
	return rec.Result(), rec.Body.Bytes()
}

// TestSourceProgress_RequiresSource 参数缺失或类型非法返回 400。
func TestSourceProgress_RequiresSource(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progress-bad@example.com", "password123")
	for _, query := range []string{"", "source_type=episode", "source_type=document&source_id=x"} {
		rec := doWithCookie(srv, cookie, http.MethodGet, "/api/source-progress?"+query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q 应 400，实际 %d", query, rec.Code)
		}
	}
}

// TestSourceProgress_UnknownSource 未知来源返回 404，而不是伪造“未处理”。
func TestSourceProgress_UnknownSource(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progress-404@example.com", "password123")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/api/source-progress?source_type=episode&source_id=missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知来源应 404，实际 %d", rec.Code)
	}
}

// TestSourceProgress_ReportsStagesAndNext 返回阶段状态、状态中文名与下一步文案。
func TestSourceProgress_ReportsStagesAndNext(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progress-ok@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "progress-a")

	resp, body := getSourceProgress(t, srv, cookie, "source_type=episode&source_id="+sourceID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d: %s", resp.StatusCode, body)
	}
	var data sourceProgressResponse
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if data.SourceType != "episode" || data.Status == "" {
		t.Fatalf("来源身份缺失: %+v", data)
	}
	if len(data.Stages) == 0 {
		t.Fatal("应返回阶段列表")
	}
	for _, stage := range data.Stages {
		if stage.StatusLabel == "" {
			t.Errorf("阶段 %s 缺少状态中文名", stage.Stage)
		}
	}
	if data.Next == "" {
		t.Error("应给出下一步文案")
	}
}

// TestSourceProgress_QueuedPosition 排队任务返回位次，前台可显示“第 N 位”。
func TestSourceProgress_QueuedPosition(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progress-queue@example.com", "password123")
	ctx := context.Background()
	p, err := srv.store.CreatePodcast(ctx, "https://progress-queue.xml", "P", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "progress-queue", Title: "排队单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	if _, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe); err != nil {
		t.Fatal(err)
	}

	resp, body := getSourceProgress(t, srv, cookie, "source_type=episode&source_id="+sourceID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	var data sourceProgressResponse
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if data.Job == nil {
		t.Fatal("应有排队任务")
	}
	if data.Job.Status != "queued" && data.Job.Status != "running" {
		t.Errorf("任务状态异常: %+v", data.Job)
	}
	if data.Job.QueuedPosition < 1 {
		t.Errorf("排队任务应带位次: %+v", data.Job)
	}
	if !strings.Contains(data.Next, "排队中") {
		t.Errorf("下一步文案应说明排队: %q", data.Next)
	}
}

// TestSourceDetail_RendersProgressPanel 单集页包含轮询所需的属性与实时状态行。
func TestSourceDetail_RendersProgressPanel(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "progress-page@example.com", "password123")
	sourceID := seedPracticeSource(t, srv, "progress-b")
	rec := doWithCookie(srv, cookie, http.MethodGet, "/sources/episode/"+sourceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"data-source-progress", "id=\"source-live-status\"", "data-stage=\"knowledge\"", "stage-badge"} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺少 %q", want)
		}
	}
	if !strings.Contains(body, "/static/source-progress.js") {
		t.Error("页面应加载进度轮询脚本")
	}
}
