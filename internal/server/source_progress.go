// source_progress.go 单集处理进度（只读轮询接口）。
// 只读取既有状态：来源状态、三阶段状态、当前任务与排队位次；不触发任何处理或模型调用。
package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// sourceProgressStage 阶段进度（前端按 stage 更新对应行）。
type sourceProgressStage struct {
	Stage       string `json:"stage"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
	Detail      string `json:"detail,omitempty"`
}

// sourceProgressJob 该来源当前排队或运行中的任务。
type sourceProgressJob struct {
	Type           string `json:"type"`
	Label          string `json:"label"`
	Status         string `json:"status"`
	QueuedPosition int    `json:"queued_position"`
}

// handleSourceProgressAPI 返回某来源的处理进度。
// 路径：/api/source-progress?source_type=episode&source_id=…
func (srv *Server) handleSourceProgressAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.URL.Query().Get("source_type")))
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	if sourceID == "" || (sourceType != models.SourceEpisode && sourceType != models.SourceUpload) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要有效的 source_type 与 source_id"})
		return
	}
	if !srv.sourceExists(r.Context(), sourceType, sourceID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "来源不存在"})
		return
	}
	status, lastError := srv.sourceStatusAndError(r.Context(), sourceType, sourceID)

	stages := make([]sourceProgressStage, 0, 3)
	if list, err := srv.store.SourceStageStatuses(r.Context(), sourceType, sourceID); err == nil {
		for _, stage := range list {
			stages = append(stages, sourceProgressStage{Stage: stage.Stage, Label: stage.StageLabel, Status: stage.Status, StatusLabel: stageStatusLabel(stage.Status), Detail: stage.Detail})
		}
	}

	job := currentSourceJob(r, srv, sourceType, sourceID)
	next := nextStepHint(status, job)
	writeJSON(w, http.StatusOK, map[string]any{
		"source_type": string(sourceType), "source_id": sourceID,
		"status": string(status), "status_label": stageLabel(string(status)),
		"error": lastError, "stages": stages, "job": job, "next": next,
		"complete": status == models.StatusProcessed, "failed": status == models.StatusFailedEp,
	})
}

// sourceExists 判断 episode/upload 是否真实存在（避免把未知 ID 当成“尚未处理”）。
func (srv *Server) sourceExists(ctx context.Context, sourceType models.SourceType, sourceID string) bool {
	switch sourceType {
	case models.SourceEpisode:
		_, err := srv.store.GetEpisodeByID(ctx, sourceID)
		return err == nil
	case models.SourceUpload:
		_, err := srv.store.GetUploadByID(ctx, sourceID)
		return err == nil
	}
	return false
}

// currentSourceJob 找出该来源的运行中任务；没有则返回排队位次。
func currentSourceJob(r *http.Request, srv *Server, sourceType models.SourceType, sourceID string) *sourceProgressJob {
	progress, err := srv.store.GetProcessingProgress(r.Context())
	if err != nil {
		return nil
	}
	if progress.Active != nil && progress.Active.SourceType == sourceType && progress.Active.SourceID == sourceID {
		return &sourceProgressJob{
			Type: string(progress.Active.JobType), Label: jobTypeLabel(progress.Active.JobType), Status: "running",
		}
	}
	position := 0
	for _, queued := range progress.Queued {
		if queued.SourceType != sourceType {
			continue
		}
		position++
		if queued.SourceID == sourceID {
			return &sourceProgressJob{
				Type: string(queued.JobType), Label: jobTypeLabel(queued.JobType), Status: "queued",
				QueuedPosition: position,
			}
		}
	}
	return nil
}

// nextStepHint 把状态翻成一句“接下来会发生什么”，避免页面停在旧进度上。
func nextStepHint(status models.EpisodeProcessingStatus, job *sourceProgressJob) string {
	if job != nil {
		if job.QueuedPosition > 0 {
			return "排队中：第 " + strconv.Itoa(job.QueuedPosition) + " 位，轮到后自动开始"
		}
		return "正在执行：" + job.Label
	}
	switch status {
	case models.StatusProcessed:
		return "处理完成"
	case models.StatusFailedEp:
		return "处理失败，可在下方按阶段重试"
	case models.StatusQueuedEp:
		return "已入队，等待 worker 领取"
	case models.StatusUnprocessed:
		return "尚未开始处理"
	}
	return "等待下一步"
}

// stageStatusLabel 阶段状态的中文名（页面徽标与轮询结果共用）。
func stageStatusLabel(status string) string {
	switch status {
	case "ready":
		return "就绪"
	case "pending":
		return "等待"
	case "running":
		return "进行中"
	case "failed":
		return "失败"
	case "partial":
		return "部分完成"
	case "waiting":
		return "排队中"
	}
	return status
}

// jobTypeLabel 任务类型的中文名（用于“正在执行：…”）。
func jobTypeLabel(jobType models.JobType) string {
	labels := map[models.JobType]string{
		models.JobTranscribe: "转录音频", models.JobAnalyze: "生成知识卡片",
		models.JobHighlight: "挑选高光片段", models.JobNarration: "合成 AI 解说",
		models.JobDigest: "生成精读文", models.JobKeypointQuality: "检查重点质量",
		models.JobPracticeReview: "生成练习问题", models.JobTranslate: "生成对齐译文",
		models.JobDJPlan: "编排 DJ 清单", models.JobKnowledgeArticle: "生成知识文章",
		models.JobWeeklyReview: "生成每周回顾", models.JobQuestionStudy: "问题学习",
	}
	if label, ok := labels[jobType]; ok {
		return label
	}
	return string(jobType)
}
