// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：Source 详情、音频、版本、AI DJ、搜索、进度、设置、处理、Purge）。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleSourceDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	if len(parts) >= 3 && parts[2] == "download" {
		srv.handleDownloadMarkdown(w, r)
		return
	}
	if len(parts) >= 3 && parts[2] == "dj" {
		srv.handleDJ(w, r)
		return
	}
	if len(parts) >= 4 && parts[2] == "versions" && parts[3] == "revert" {
		srv.handleRevertVersion(w, r)
		return
	}
	if len(parts) >= 3 && parts[2] == "versions" {
		srv.handleVersions(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	// 取 source 处理状态与最近一次失败原因（供前端展示进度/错误/重试）
	status, lastError := srv.sourceStatusAndError(r.Context(), sourceType, sourceID)

	// 播放只依赖 EvidenceAudio（ADR-0005）：存在则用内部端点；
	// 尚未处理的 episode 用外链预览，upload 用原始落盘文件（处理后由证据替代）。
	audioURL := "/api/audio/" + string(sourceType) + "/" + sourceID
	if _, err := srv.store.GetEvidenceAudio(r.Context(), sourceType, sourceID); err != nil {
		if sourceType == models.SourceEpisode {
			if ep, err := srv.store.GetEpisodeByID(r.Context(), sourceID); err == nil {
				audioURL = ep.AudioURL
			} else {
				http.NotFound(w, r)
				return
			}
		} else if _, err := srv.store.GetUploadByID(r.Context(), sourceID); err != nil {
			http.NotFound(w, r)
			return
		}
	}

	// 当前 Transcript / KnowledgeCard 版本（ADR-0011）
	var segments []provider.Segment
	title := titleForStatus(status)
	summary := ""
	var card map[string]any
	if t, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript); err == nil {
		var tp provider.TranscriptPayload
		if json.Unmarshal([]byte(t.Payload), &tp) == nil {
			segments = tp.Segments
		}
	}
	if c, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard); err == nil {
		var cardData provider.KnowledgeCard
		if json.Unmarshal([]byte(c.Payload), &cardData) == nil {
			title = cardData.Title
			summary = cardData.Summary.Text
			card = cardView(cardData, segments)
		}
	}

	data := map[string]any{
		"Title":      title,
		"Summary":    summary,
		"AudioURL":   audioURL,
		"Segments":   segments,
		"Card":       card,
		"Status":     string(status),
		"LastError":  lastError,
		"SourceType": string(sourceType),
		"SourceID":   sourceID,
		"CSRF":       auth.CSRFValue(r),
	}
	srv.tmpl.Render(w, "source_detail.html", data)
}

// handleAudio 提供 Source 音频：优先 EvidenceAudio（ADR-0005）；
// upload 在证据生成前回退到原始落盘文件（仅该实例内可访问）。
func (srv *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	// /api/audio/{sourceType}/{sourceID}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/audio/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	// 优先证据音频
	if ev, err := srv.store.GetEvidenceAudio(r.Context(), sourceType, sourceID); err == nil {
		path := filepath.Join(srv.cfg.EvidenceDir, ev.RelPath)
		if _, serr := os.Stat(path); serr == nil {
			http.ServeFile(w, r, path)
			return
		}
	}
	// upload 回退：原始落盘文件（处理前预览）
	if sourceType == models.SourceUpload {
		if _, err := srv.store.GetUploadByID(r.Context(), sourceID); err == nil {
			path := filepath.Join(srv.cfg.TempDir, "uploads", sourceID)
			http.ServeFile(w, r, path)
			return
		}
	}
	http.NotFound(w, r)
}

// handleNarration serve 一个 Highlight 的当前 Narration wav（ADR-0019）。
// 路径：/api/narration/{sourceType}/{sourceID}/{highlightID}
// Narration 存于 NarrationDir（独立于 evidence），wav 格式。
func (srv *Server) handleNarration(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/narration/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]
	highlightID := parts[2]
	nar, err := srv.store.GetCurrentNarration(r.Context(), sourceType, sourceID, highlightID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(srv.cfg.NarrationDir, nar.RelPath)
	if _, serr := os.Stat(path); serr != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

// handleVersions 查看一个 Source 的不可变版本历史（ADR-0011）。
func (srv *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	title := sourceID
	if sourceType == models.SourceEpisode {
		if ep, err := srv.store.GetEpisodeByID(r.Context(), sourceID); err == nil {
			title = ep.Title
		} else {
			http.NotFound(w, r)
			return
		}
	} else {
		if up, err := srv.store.GetUploadByID(r.Context(), sourceID); err == nil {
			title = up.OriginalFilename
		} else {
			http.NotFound(w, r)
			return
		}
	}

	transcripts, _ := srv.store.ListArtifactVersions(r.Context(), sourceType, sourceID, store.KindTranscript)
	cards, _ := srv.store.ListArtifactVersions(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)
	currentT, _ := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	currentC, _ := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)

	curT, curC := 0, 0
	if currentT != nil {
		curT = currentT.Version
	}
	if currentC != nil {
		curC = currentC.Version
	}
	srv.tmpl.Render(w, "versions.html", map[string]any{
		"SourceType": string(sourceType), "SourceID": sourceID, "Title": title,
		"Transcripts": transcripts, "Cards": cards,
		"CurrentTranscript": curT, "CurrentCard": curC,
		"CSRF": auth.CSRFValue(r),
	})
}

// handleRevertVersion 把 Source 当前版本指针回退到指定版本（ADR-0011 "可恢复"）。
func (srv *Server) handleRevertVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]
	kindStr := r.FormValue("kind")
	version, err := strconv.Atoi(r.FormValue("version"))
	if err != nil {
		http.Error(w, "版本号无效", http.StatusBadRequest)
		return
	}
	kind := store.ArtifactKind(kindStr)
	if kind != store.KindTranscript && kind != store.KindKnowledgeCard {
		http.Error(w, "kind 无效", http.StatusBadRequest)
		return
	}
	if err := srv.store.SetCurrentVersion(r.Context(), sourceType, sourceID, kind, version); err != nil {
		http.Error(w, "回退失败：版本不存在", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID+"/versions", http.StatusSeeOther)
}

// handleDJ 渲染 DJ 播放清单页面。
func (srv *Server) handleDJ(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 3 || parts[2] != "dj" {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	// 读取当前 Highlight 版本
	hv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindHighlight)
	if err != nil {
		http.Error(w, "尚无高光片段，请先完成处理", http.StatusNotFound)
		return
	}
	var hs provider.HighlightSet
	if err := json.Unmarshal([]byte(hv.Payload), &hs); err != nil {
		http.Error(w, "高光数据损坏", http.StatusInternalServerError)
		return
	}

	// 读取当前 Transcript（用于解析 Citation 时间范围）
	tv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "尚无转录稿", http.StatusNotFound)
		return
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tv.Payload), &tp); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}

	// 读取 KnowledgeCard（结尾 Take Aways = KeyPoints）
	var card provider.KnowledgeCard
	if cv, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard); err == nil {
		json.Unmarshal([]byte(cv.Payload), &card)
	}

	// 构造播放清单：每个 Highlight 解析时间范围
	type highlightView struct {
		HighlightID  string
		Gist         string
		Start        float64
		End          float64
		Citations    []string
		NarrationURL string // 当前 Narration wav 的 URL；空=未生成（前端跳过+显示标记）
	}
	narrations, _ := srv.store.ListCurrentNarrationsForSource(r.Context(), sourceType, sourceID)
	var highlights []highlightView
	for _, h := range hs.Highlights {
		start, end, ok := provider.ResolveCitationSpan(h.Citations, tp.Segments)
		if !ok {
			continue
		}
		hv := highlightView{
			HighlightID: h.ID, Gist: h.Gist, Start: start, End: end, Citations: h.Citations,
		}
		if nar, ok := narrations[h.ID]; ok {
			hv.NarrationURL = "/api/narration/" + string(sourceType) + "/" + sourceID + "/" + h.ID
			_ = nar
		}
		highlights = append(highlights, hv)
	}

	audioURL := "/api/audio/" + string(sourceType) + "/" + sourceID
	srv.tmpl.Render(w, "dj.html", map[string]any{
		"SourceType": string(sourceType),
		"SourceID":   sourceID,
		"Title":      card.Title,
		"Highlights": highlights,
		"KeyPoints":  card.KeyPoints,
		"AudioURL":   audioURL,
		"CSRF":       auth.CSRFValue(r),
	})
}
func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []store.SearchHit
	if q != "" {
		hits, _ = srv.store.SearchSource(r.Context(), q)
	}
	srv.tmpl.Render(w, "search.html", map[string]any{"Query": q, "Hits": hits})
}
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
func (srv *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// 每个任务独立配置 Provider + Model（ADR-0009 扩展）
		st := &models.Settings{}
		strPtr := func(v string) *string {
			if v != "" {
				return &v
			}
			return nil
		}
		st.TranscriptionModel = strPtr(r.FormValue("transcription_model"))
		st.AnalysisModel = strPtr(r.FormValue("analysis_model"))
		st.HighlightModel = strPtr(r.FormValue("highlight_model"))
		st.QAModel = strPtr(r.FormValue("qa_model"))
		st.TranscriptionProvider = strPtr(r.FormValue("transcription_provider"))
		st.AnalysisProvider = strPtr(r.FormValue("analysis_provider"))
		st.HighlightProvider = strPtr(r.FormValue("highlight_provider"))
		st.QAProvider = strPtr(r.FormValue("qa_provider"))
		st.GroqAPIKey = strPtr(r.FormValue("groq_api_key"))
		st.GroqBaseURL = strPtr(r.FormValue("groq_base_url"))
		st.OpenAIAPIKey = strPtr(r.FormValue("openai_api_key"))
		st.OpenAIBaseURL = strPtr(r.FormValue("openai_base_url"))
		_ = srv.store.UpdateSettings(r.Context(), st)
		// 立即刷新 Selector 的 key/URL
		gKey, gURL, oKey, oURL := "", "", "", ""
		if st.GroqAPIKey != nil {
			gKey = *st.GroqAPIKey
		}
		if st.GroqBaseURL != nil {
			gURL = *st.GroqBaseURL
		}
		if st.OpenAIAPIKey != nil {
			oKey = *st.OpenAIAPIKey
		}
		if st.OpenAIBaseURL != nil {
			oURL = *st.OpenAIBaseURL
		}
		srv.selector.ApplySettings(gKey, gURL, oKey, oURL)
		http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
		return
	}
	st, _ := srv.store.GetSettings(r.Context())
	deref := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	srv.tmpl.Render(w, "settings.html", map[string]any{
		"TranscriptionModel":    deref(st.TranscriptionModel),
		"AnalysisModel":         deref(st.AnalysisModel),
		"HighlightModel":        deref(st.HighlightModel),
		"QAModel":               deref(st.QAModel),
		"TranscriptionProvider": deref(st.TranscriptionProvider),
		"AnalysisProvider":      deref(st.AnalysisProvider),
		"HighlightProvider":     deref(st.HighlightProvider),
		"QAProvider":            deref(st.QAProvider),
		"HasOpenAI":             srv.selector.HasOpenAI(),
		"Saved":                 r.URL.Query().Get("saved") == "1",
		"CSRF":                  auth.CSRFValue(r),
	})
}

// handlePurge 显式 Purge（ADR-0012）：完整、不可逆地删除 Source 及其全部证据与处理历史。
func (srv *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	if err := srv.worker.PurgeSource(r.Context(), sourceType, sourceID); err != nil {
		http.Error(w, "Purge 失败", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/uploads", http.StatusSeeOther)
}
func (srv *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	job, err := srv.store.EnqueueJob(r.Context(), sourceType, sourceID, models.JobTranscribe)
	if err != nil {
		http.Error(w, "入队失败", http.StatusInternalServerError)
		return
	}
	_ = job // SQLite 驱动 worker 循环会领取并处理（ADR-0006）
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleProcessBatch 批量入队（便利入口，不持久化批次）。
// 部分成功：能入的入，状态不对的跳过；返回 enqueued/skipped 计数。
func (srv *Server) handleProcessBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceIDs := r.Form["source_id"]
	podcastID := r.FormValue("podcast_id")

	enqueued := 0
	skipped := 0
	for _, sid := range sourceIDs {
		job, err := srv.store.EnqueueJob(r.Context(), sourceType, sid, models.JobTranscribe)
		if err != nil {
			skipped++
			continue
		}
		if job != nil {
			enqueued++
		} else {
			skipped++
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/podcasts/%s?enqueued=%d&skipped=%d", podcastID, enqueued, skipped), http.StatusSeeOther)
}
