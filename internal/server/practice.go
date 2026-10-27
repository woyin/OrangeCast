// practice.go 练习问题（K02 学习实践）：把来源变成可自我检验的问题。
// 问题挂 Citation（真实 Segment，可核验）；提示挂 Reference（AI 生成、非原文），
// 二者在页面上必须视觉区分。生成是付费任务，只能由显式 POST 触发并进入持久队列。
package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// practiceQuestionView 页面展示一条问题及其来源定位。
func (srv *Server) practiceQuestionView(r *http.Request, q models.ReviewQuestion) map[string]any {
	return map[string]any{
		"ID": q.ID, "Prompt": q.Prompt, "AnswerHint": q.AnswerHint,
		"SourceTitle": q.SourceTitle, "SourceType": q.SourceType, "SourceID": q.SourceID,
		"TimeStart": q.TimeStart, "TimeEnd": q.TimeEnd, "CreatedAt": q.CreatedAt,
		"Model": q.Model, "Citations": q.Citations,
	}
}

// handlePracticeQuestions 展示跨来源的练习问题，可按来源过滤。
func (srv *Server) handlePracticeQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	filterType := models.SourceType(strings.TrimSpace(r.URL.Query().Get("source_type")))
	filterID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	var questions []models.ReviewQuestion
	var err error
	switch {
	case filterID != "" && filterType != "":
		questions, err = srv.store.ListReviewQuestions(r.Context(), filterType, filterID)
	default:
		questions, err = srv.store.ListRecentReviewQuestions(r.Context(), 40)
	}
	if err != nil {
		http.Error(w, "读取练习问题失败", http.StatusInternalServerError)
		return
	}
	views := make([]map[string]any, 0, len(questions))
	for _, q := range questions {
		views = append(views, srv.practiceQuestionView(r, q))
	}

	// 可生成问题的来源（有当前转录版本），用于单集/批量生成入口。
	var sources []map[string]any
	for _, sourceType := range []models.SourceType{models.SourceEpisode, models.SourceUpload} {
		list, listErr := srv.store.ListSourcesWithTranscripts(r.Context(), sourceType)
		if listErr != nil {
			http.Error(w, "读取来源失败", http.StatusInternalServerError)
			return
		}
		for _, item := range list {
			sources = append(sources, map[string]any{"Type": string(sourceType), "ID": item.ID, "Title": item.Title})
		}
	}

	srv.tmpl.Render(w, "practice.html", map[string]any{
		"Questions": views, "Sources": sources, "FilterID": filterID, "FilterType": string(filterType),
		"Generated": r.URL.Query().Get("generated"), "Skipped": r.URL.Query().Get("skipped"),
		"CSRF": auth.CSRFValue(r),
	})
}

// handlePracticeAction 处理显式生成动作。
// generate：为单一来源或用全部有转录的来源入队问题生成任务（幂等，每源每转录版本一次）。
// 自测本身是只读的（先自己回答，再展开提示与来源定位）；个人掌握度仍记录在既有
// 回顾流程（/review 与 /review/daily）中，不在此另立一套状态。
func (srv *Server) handlePracticeAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	if strings.TrimSpace(r.FormValue("action")) != "generate" {
		http.Error(w, "未知动作", http.StatusBadRequest)
		return
	}
	srv.practiceGenerate(w, r)
}

func (srv *Server) practiceGenerate(w http.ResponseWriter, r *http.Request) {
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	targets := []store.SourceTitle{{ID: sourceID}}
	if sourceID == "" || r.FormValue("all") == "1" {
		sourceType = models.SourceEpisode
		targets = nil
		for _, st := range []models.SourceType{models.SourceEpisode, models.SourceUpload} {
			list, err := srv.store.ListSourcesWithTranscripts(r.Context(), st)
			if err != nil {
				http.Error(w, "读取来源失败", http.StatusInternalServerError)
				return
			}
			for _, item := range list {
				targets = append(targets, store.SourceTitle{ID: string(st) + ":" + item.ID, Title: item.Title})
			}
		}
	}
	generated, skipped := 0, 0
	for _, target := range targets {
		st, id := sourceType, target.ID
		if strings.Contains(id, ":") {
			parts := strings.SplitN(id, ":", 2)
			st, id = models.SourceType(parts[0]), parts[1]
		}
		job, err := srv.store.EnqueuePracticeReviewJob(r.Context(), st, id, 6, r.FormValue("refresh"))
		if err != nil || job == nil {
			skipped++
			continue
		}
		generated++
	}
	redirect := "/practice?generated=" + strconv.Itoa(generated) + "&skipped=" + strconv.Itoa(skipped)
	if sourceID != "" && r.FormValue("all") != "1" {
		redirect = "/sources/" + string(sourceType) + "/" + sourceID
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
