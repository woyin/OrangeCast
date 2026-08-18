// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：KeyPoint 全局视图、知识图谱、Annotation/Pin/Collection）。
package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleKeyPoints(w http.ResponseWriter, r *http.Request) {
	page := pageParam(r)
	const perPage = 20
	filter := store.KeyPointFilter{SourceType: models.SourceType(strings.TrimSpace(r.URL.Query().Get("source_type"))), SourceID: strings.TrimSpace(r.URL.Query().Get("source_id")), PodcastID: strings.TrimSpace(r.URL.Query().Get("podcast_id")), ThemeID: strings.TrimSpace(r.URL.Query().Get("theme_id")), Status: models.KeyPointProductionStatus(strings.TrimSpace(r.URL.Query().Get("status"))), QualityStatus: models.KeyPointQualityStatus(strings.TrimSpace(r.URL.Query().Get("quality_status"))), From: strings.TrimSpace(r.URL.Query().Get("from")), To: strings.TrimSpace(r.URL.Query().Get("to"))}
	kps, total, err := srv.store.ListKeyPointsFiltered(r.Context(), filter, page, perPage)
	if err != nil {
		http.Error(w, "加载失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "keypoints.html", map[string]any{
		"KeyPoints":  kps,
		"Page":       page,
		"TotalPages": totalPages(total, perPage),
		"Total":      total,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
		"CSRF":       auth.CSRFValue(r),
		"Filter":     filter,
	})
}

func (srv *Server) handleKeyPointBatchStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "请求格式无效", http.StatusBadRequest)
		return
	}
	ids := r.Form["keypoint_ids"]
	if len(ids) == 0 {
		ids = strings.Split(r.FormValue("keypoint_ids_csv"), ",")
	}
	status := models.KeyPointProductionStatus(strings.TrimSpace(r.FormValue("status")))
	if err := srv.store.SetKeyPointProductionStatuses(r.Context(), ids, status); err != nil {
		http.Error(w, "批量更新失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/keypoints", http.StatusSeeOther)
}
func (srv *Server) handleKeyPointsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	kps, err := srv.store.SearchKeyPointsHybrid(r.Context(), q, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	total := len(kps)
	type result struct {
		ID               string  `json:"id"`
		SourceType       string  `json:"source_type"`
		SourceID         string  `json:"source_id"`
		SourceTitle      string  `json:"source_title"`
		Content          string  `json:"content"`
		Description      string  `json:"description"`
		ProductionStatus string  `json:"production_status"`
		QualityStatus    string  `json:"quality_status"`
		StaleAt          string  `json:"stale_at"`
		Origin           string  `json:"origin"`
		TimeStart        float64 `json:"time_start"`
		TimeEnd          float64 `json:"time_end"`
	}
	results := make([]result, 0, len(kps))
	for _, kp := range kps {
		results = append(results, result{
			ID: kp.ID, SourceType: string(kp.SourceType), SourceID: kp.SourceID,
			SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description,
			ProductionStatus: string(kp.ProductionStatus), QualityStatus: string(kp.QualityStatus), StaleAt: kp.StaleAt, Origin: string(kp.Origin),
			TimeStart: kp.TimeStart, TimeEnd: kp.TimeEnd,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "total": total})
}

// handleKeyPointStatus moves one KeyPoint through the production Inbox.
func (srv *Server) handleKeyPointStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.FormValue("keypoint_id"))
	status := models.KeyPointProductionStatus(strings.TrimSpace(r.FormValue("status")))
	if err := srv.store.SetKeyPointProductionStatus(r.Context(), id, status); err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "关键要点不存在"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}
func (srv *Server) handleKeyPointQuality(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.FormValue("keypoint_id"))
	status := models.KeyPointQualityStatus(strings.TrimSpace(r.FormValue("quality_status")))
	if err := srv.store.SetKeyPointQualityStatus(r.Context(), id, status); err != nil {
		if err == store.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "关键要点不存在"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quality_status": status})
}

func (srv *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	srv.tmpl.Render(w, "graph.html", map[string]any{
		"CSRF": auth.CSRFValue(r),
	})
}

// handleGraphAPI 返回 KeyPoint 粒度图谱 JSON。
func (srv *Server) handleGraphAPI(w http.ResponseWriter, r *http.Request) {
	gd, err := srv.store.GetKpGraph(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, gd)
}
func (srv *Server) handleAnnotation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	segmentIDs := r.FormValue("segment_ids")
	body := r.FormValue("body")
	var timeStart, timeEnd float64
	fmt.Sscanf(r.FormValue("time_start"), "%f", &timeStart)
	fmt.Sscanf(r.FormValue("time_end"), "%f", &timeEnd)

	if body == "" {
		// 空标注 = 删除
		srv.store.DeleteAnnotation(r.Context(), sourceType, sourceID, segmentIDs)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "deleted"})
		return
	}
	a, err := srv.store.UpsertAnnotation(r.Context(), sourceType, sourceID, segmentIDs, timeStart, timeEnd, body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "saved", "annotation": a})
}
func (srv *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	segmentIDs := r.FormValue("segment_ids")
	sourceTitle := r.FormValue("source_title")
	var timeStart, timeEnd float64
	fmt.Sscanf(r.FormValue("time_start"), "%f", &timeStart)
	fmt.Sscanf(r.FormValue("time_end"), "%f", &timeEnd)

	pinned, err := srv.store.TogglePin(r.Context(), sourceType, sourceID, segmentIDs, timeStart, timeEnd, sourceTitle)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pinned": pinned})
}
func (srv *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cols, err := srv.store.ListCollections(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"collections": cols})
		return
	}
	if r.Method == http.MethodPost {
		title := strings.TrimSpace(r.FormValue("title"))
		desc := strings.TrimSpace(r.FormValue("description"))
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title 必填"})
			return
		}
		c, err := srv.store.CreateCollection(r.Context(), title, desc)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collection": c})
		return
	}
}
func (srv *Server) handleCollectionItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	collectionID := r.FormValue("collection_id")
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	segmentIDs := r.FormValue("segment_ids")
	sourceTitle := r.FormValue("source_title")
	note := r.FormValue("note")
	var timeStart, timeEnd float64
	fmt.Sscanf(r.FormValue("time_start"), "%f", &timeStart)
	fmt.Sscanf(r.FormValue("time_end"), "%f", &timeEnd)

	if r.FormValue("action") == "remove" {
		err := srv.store.RemoveFromCollection(r.Context(), collectionID, sourceType, sourceID, segmentIDs)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "removed"})
		return
	}

	err := srv.store.AddToCollection(r.Context(), collectionID, sourceType, sourceID, segmentIDs, timeStart, timeEnd, sourceTitle, note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "added"})
}
