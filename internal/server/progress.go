// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：处理进度页面与 JSON API）。
package server

import (
	"fmt"
	"net/http"

	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	progress, _ := srv.store.GetProcessingProgress(r.Context())
	// 给每个 job 补上 Source 标题
	type jobView struct {
		Job   *models.ProcessingJob
		Title string
		Stage string // 友好阶段名
	}
	var activeView *jobView
	if progress.Active != nil {
		activeView = &jobView{
			Job:   progress.Active,
			Title: srv.store.SourceTitle(r.Context(), progress.Active.SourceType, progress.Active.SourceID),
			Stage: stageLabel(string(srv.store.SourceStatus(r.Context(), progress.Active.SourceType, progress.Active.SourceID))),
		}
	}
	queuedViews := make([]jobView, 0, len(progress.Queued))
	for i, j := range progress.Queued {
		queuedViews = append(queuedViews, jobView{
			Job:   j,
			Title: srv.store.SourceTitle(r.Context(), j.SourceType, j.SourceID),
			Stage: fmt.Sprintf("排队第 %d 位", i+1),
		})
	}
	// 最近完成的 5 个任务
	recentJobs, _ := srv.store.ListRecentCompleted(r.Context(), 5)
	type recentView struct {
		Job   *models.ProcessingJob
		Title string
	}
	recentViews := make([]recentView, 0, len(recentJobs))
	for _, j := range recentJobs {
		recentViews = append(recentViews, recentView{
			Job:   j,
			Title: srv.store.SourceTitle(r.Context(), j.SourceType, j.SourceID),
		})
	}
	srv.tmpl.Render(w, "progress.html", map[string]any{
		"Active": activeView,
		"Queued": queuedViews,
		"Recent": recentViews,
	})
}

// handleProgressAPI 返回 JSON（供前端 5s 轮询）。
func (srv *Server) handleProgressAPI(w http.ResponseWriter, r *http.Request) {
	progress, err := srv.store.GetProcessingProgress(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type jobJSON struct {
		JobID      string `json:"job_id"`
		SourceType string `json:"source_type"`
		SourceID   string `json:"source_id"`
		Title      string `json:"title"`
		JobType    string `json:"job_type"`
		Stage      string `json:"stage"`
	}
	var active *jobJSON
	if progress.Active != nil {
		active = &jobJSON{
			JobID:      progress.Active.ID,
			SourceType: string(progress.Active.SourceType),
			SourceID:   progress.Active.SourceID,
			Title:      srv.store.SourceTitle(r.Context(), progress.Active.SourceType, progress.Active.SourceID),
			JobType:    string(progress.Active.JobType),
			Stage:      stageLabel(string(srv.store.SourceStatus(r.Context(), progress.Active.SourceType, progress.Active.SourceID))),
		}
	}
	queued := make([]jobJSON, 0, len(progress.Queued))
	for i, j := range progress.Queued {
		queued = append(queued, jobJSON{
			JobID:      j.ID,
			SourceType: string(j.SourceType),
			SourceID:   j.SourceID,
			Title:      srv.store.SourceTitle(r.Context(), j.SourceType, j.SourceID),
			JobType:    string(j.JobType),
			Stage:      fmt.Sprintf("排队第 %d 位", i+1),
		})
	}
	// 最近完成的任务
	recentJobs, _ := srv.store.ListRecentCompleted(r.Context(), 5)
	type recentJSON struct {
		JobID      string `json:"job_id"`
		SourceType string `json:"source_type"`
		SourceID   string `json:"source_id"`
		Title      string `json:"title"`
		JobType    string `json:"job_type"`
		Status     string `json:"status"`
		UpdatedAt  string `json:"updated_at"`
	}
	recent := make([]recentJSON, 0, len(recentJobs))
	for _, j := range recentJobs {
		recent = append(recent, recentJSON{
			JobID:      j.ID,
			SourceType: string(j.SourceType),
			SourceID:   j.SourceID,
			Title:      srv.store.SourceTitle(r.Context(), j.SourceType, j.SourceID),
			JobType:    string(j.JobType),
			Status:     string(j.Status),
			UpdatedAt:  j.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active": active,
		"queued": queued,
		"recent": recent,
	})
}

// stageLabel 把 Source 处理状态转成友好文字。
func stageLabel(status string) string {
	switch status {
	case "transcribing":
		return "正在转录音频"
	case "analyzing":
		return "正在生成知识卡片"
	case "queued":
		return "等待处理"
	case "processed":
		return "处理完成"
	case "failed":
		return "处理失败"
	default:
		return "等待处理"
	}
}
