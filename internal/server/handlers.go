package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/markdown"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/store"
)

// ---- 认证与 Owner 认领 ----

// handleRegister 首次认领唯一 Owner（ADR-0003）。
// GET：实例未认领时显示认领表单；已认领时重定向到 /login。
// POST：仅当 users 为空时创建 Owner，其余情况拒绝。
func (srv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		claimed, err := srv.isClaimed(r.Context())
		if err != nil {
			http.Error(w, "内部错误", http.StatusInternalServerError)
			return
		}
		if claimed {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		srv.tmpl.Render(w, "register.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	// POST：认领
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	if err := auth.ValidateEmail(email); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "邮箱格式无效", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.ValidatePassword(pw); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "密码至少 8 位", "CSRF": auth.CSRFValue(r)})
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	u, err := srv.store.ClaimOwner(r.Context(), email, hash)
	if err != nil {
		if err == store.ErrOwnerExists {
			srv.tmpl.Render(w, "register.html", map[string]any{"Error": "实例已被认领", "CSRF": auth.CSRFValue(r)})
			return
		}
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "认领失败", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID, srv.cfg.PublicSchemeIsHTTPS()); err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// isClaimed 判断实例是否已被认领（users 表非空）。
func (srv *Server) isClaimed(ctx context.Context) (bool, error) {
	n, err := store.CountUsers(ctx, srv.store.DB)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (srv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "login.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	// 登录限流（ADR-0013）：按客户端 IP 固定窗口
	if !srv.loginLimiter.Allow(auth.ClientIP(r, srv.cfg.TrustedProxies)) {
		http.Error(w, "尝试过于频繁，请稍后再试", http.StatusTooManyRequests)
		return
	}
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	u, err := srv.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误", "CSRF": auth.CSRFValue(r)})
		return
	}
	ok, err := auth.VerifyPassword(pw, u.PasswordHash)
	if err != nil || !ok {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误", "CSRF": auth.CSRFValue(r)})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID, srv.cfg.PublicSchemeIsHTTPS()); err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (srv *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w, r, srv.store)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- Dashboard ----

func (srv *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	u, _ := srv.store.GetUserByID(r.Context(), userID)
	srv.tmpl.Render(w, "dashboard.html", map[string]any{"Email": u.Email})
}

// ---- Podcasts ----

func (srv *Server) handlePodcasts(w http.ResponseWriter, r *http.Request) {
	ps, err := srv.store.ListPodcasts(r.Context())
	if err != nil {
		http.Error(w, "加载失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "podcasts.html", map[string]any{"Podcasts": ps})
}

func (srv *Server) handlePodcastNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	podcast, eps, err := rss.FetchFeed(feedURL)
	if err != nil {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{"Error": "抓取/解析 feed 失败: " + err.Error(), "CSRF": auth.CSRFValue(r)})
		return
	}
	p, err := srv.store.CreatePodcast(r.Context(), feedURL, podcast.Title, podcast.Description, podcast.ImageURL)
	if err != nil {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{"Error": "订阅失败（可能已订阅）", "CSRF": auth.CSRFValue(r)})
		return
	}
	if _, err := srv.store.MergeEpisodes(r.Context(), p.ID, eps); err != nil {
		http.Error(w, "保存单集失败", http.StatusInternalServerError)
		return
	}
	_ = srv.store.UpdatePodcastFetched(r.Context(), p.ID)
	http.Redirect(w, r, "/podcasts/"+p.ID, http.StatusSeeOther)
}

func (srv *Server) handlePodcastDetail(w http.ResponseWriter, r *http.Request) {
	// /podcasts/{id} 或 /podcasts/new
	path := strings.TrimPrefix(r.URL.Path, "/podcasts/")
	if path == "new" {
		srv.handlePodcastNew(w, r)
		return
	}
	p, err := srv.store.GetPodcastByID(r.Context(), path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	const perPage = 10
	eps, total, _ := srv.store.ListEpisodesPaginated(r.Context(), path, page, perPage)
	totalPages := (total + perPage - 1) / perPage
	batchEnqueued := r.URL.Query().Get("enqueued")
	batchSkipped := r.URL.Query().Get("skipped")
	batchDone := ""
	if batchEnqueued != "" || batchSkipped != "" {
		batchDone = "1"
	}
	srv.tmpl.Render(w, "podcast_detail.html", map[string]any{
		"Podcast": p, "Episodes": eps, "CSRF": auth.CSRFValue(r),
		"BatchDone": batchDone, "BatchEnqueued": batchEnqueued, "BatchSkipped": batchSkipped,
		"Page": page, "TotalPages": totalPages, "Total": total,
		"PrevPage": page - 1, "NextPage": page + 1,
	})
}

// ---- Uploads ----

func (srv *Server) handleUploads(w http.ResponseWriter, r *http.Request) {
	us, _ := srv.store.ListUploads(r.Context())
	srv.tmpl.Render(w, "uploads.html", map[string]any{"Uploads": us})
}

func (srv *Server) handleUploadNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	// 限制大小 200MB
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "上传失败: " + err.Error(), "CSRF": auth.CSRFValue(r)})
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "请选择音频文件", "CSRF": auth.CSRFValue(r)})
		return
	}
	defer file.Close()
	if !isAllowedAudio(header.Filename, header.Header.Get("Content-Type")) {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "仅支持 mp3/m4a/wav", "CSRF": auth.CSRFValue(r)})
		return
	}
	up, err := srv.store.CreateUpload(r.Context(), header.Filename, header.Header.Get("Content-Type"), header.Size)
	if err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "保存失败", "CSRF": auth.CSRFValue(r)})
		return
	}
	// 音频持久化到临时目录（worker 从这里读，处理后保留以便重试）
	if err := saveUploadFile(srv.cfg.TempDir, up.ID, file); err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "存储音频失败", "CSRF": auth.CSRFValue(r)})
		return
	}
	http.Redirect(w, r, "/sources/upload/"+up.ID, http.StatusSeeOther)
}

func isAllowedAudio(name, contentType string) bool {
	for _, ext := range []string{".mp3", ".m4a", ".wav"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return strings.HasPrefix(contentType, "audio/")
}

// ---- Source 详情（播放器联动核心页）----

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

// ---- 进度（ADR-0015）----

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

// ---- AI DJ 模式（ADR-0016）----

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
		Gist      string
		Start     float64
		End       float64
		Citations []string
	}
	var highlights []highlightView
	for _, h := range hs.Highlights {
		start, end, ok := citationSpan(h.Citations, tp.Segments)
		if !ok {
			continue
		}
		highlights = append(highlights, highlightView{
			Gist: h.Gist, Start: start, End: end, Citations: h.Citations,
		})
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

// ---- 搜索 ----

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []store.SearchHit
	if q != "" {
		hits, _ = srv.store.SearchSource(r.Context(), q)
	}
	srv.tmpl.Render(w, "search.html", map[string]any{"Query": q, "Hits": hits})
}

// ---- 设置 ----

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
		_ = srv.store.UpdateSettings(r.Context(), st)
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

// ---- API ----

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

func (srv *Server) handleQA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	question := r.FormValue("question")

	av, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "无转录稿"})
		return
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &tp); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "转录载荷损坏"})
		return
	}
	// 读 settings 选 Q&A Provider + Model
	st, _ := srv.store.GetSettings(r.Context())
	qaProvider := "groq"
	if st.QAProvider != nil && *st.QAProvider != "" {
		qaProvider = *st.QAProvider
	}
	qaModel := ""
	if st.QAModel != nil {
		qaModel = *st.QAModel
	}
	bundle, err := srv.selector.BundleForTask(provider.TaskConfig{Provider: qaProvider, Model: qaModel})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	res, err := bundle.QA.Answer(question, tp.Segments)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "回答失败"})
		return
	}
	// 证据契约（ADR-0008 / Phase 7）：只有模型实际引用的 Segment 才能成为 Citation。
	// 无可靠引用时明确拒答，绝不附加"被检索到"的片段伪装成依据。
	if len(res.Sources) == 0 || strings.TrimSpace(res.Answer) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "证据不足，无法可靠回答：模型没有引用任何可核验片段。请换一种问法或查看转录稿。",
			"answer":  "",
			"sources": []provider.Source{},
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
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

// handleDownloadMarkdown 下载单 Source 的 KnowledgeNote Markdown（Roadmap Phase 5）。
// 确定性渲染：frontmatter + 摘要/要点/章节/金句（全部带 Citation 链接）+ 标签。
func (srv *Server) handleDownloadMarkdown(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sources/"), "/")
	if len(parts) < 3 || parts[2] != "download" {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]

	card, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)
	if err != nil {
		http.Error(w, "尚无知识卡片", http.StatusNotFound)
		return
	}
	tr, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "尚无转录稿", http.StatusNotFound)
		return
	}
	var cardData provider.KnowledgeCard
	if err := json.Unmarshal([]byte(card.Payload), &cardData); err != nil {
		http.Error(w, "卡片数据损坏", http.StatusInternalServerError)
		return
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tr.Payload), &tp); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}
	title := cardData.Title
	md, err := markdown.Render(markdown.Input{
		Card: &cardData, Segments: tp.Segments,
		SourceType: string(sourceType), SourceID: sourceID,
		Title: title, BaseURL: srv.cfg.PublicURL,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		http.Error(w, "渲染失败", http.StatusInternalServerError)
		return
	}
	filename := sanitizeFilename(title) + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = w.Write([]byte(md))
}

// sanitizeFilename 清理文件名中的不安全字符。
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ' ', r >= 0x4E00 && r <= 0x9FFF:
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "cloudwisepod-note"
	}
	return out
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

// sourceStatusAndError 返回 source 的处理状态与最近一次失败原因。
func (srv *Server) sourceStatusAndError(ctx context.Context, sourceType models.SourceType, sourceID string) (models.EpisodeProcessingStatus, string) {
	var status models.EpisodeProcessingStatus
	if sourceType == models.SourceEpisode {
		ep, err := srv.store.GetEpisodeByID(ctx, sourceID)
		if err != nil {
			return models.StatusUnprocessed, ""
		}
		status = ep.ProcessingStatus
	} else {
		up, err := srv.store.GetUploadByID(ctx, sourceID)
		if err != nil {
			return models.StatusUnprocessed, ""
		}
		status = up.ProcessingStatus
	}
	// 失败时取最近一次失败 job 的错误
	var lastError string
	if status == models.StatusFailedEp {
		row := srv.store.DB.QueryRowContext(ctx,
			`SELECT COALESCE(last_error,'') FROM processing_jobs
			 WHERE source_type=? AND source_id=? AND status='failed'
			 ORDER BY updated_at DESC LIMIT 1`,
			string(sourceType), sourceID)
		_ = row.Scan(&lastError)
	}
	return status, lastError
}

// titleForStatus 处理中/失败时的占位标题。
func titleForStatus(status models.EpisodeProcessingStatus) string {
	switch status {
	case models.StatusFailedEp:
		return "处理失败"
	case models.StatusProcessed:
		return ""
	default:
		if status == models.StatusUnprocessed {
			return "尚未处理"
		}
		return "处理中…"
	}
}

// qaResultToResponse 依据证据契约决定 Q&A 响应（Phase 7）。
// 无可靠引用或空答案 → 422 明确拒答；否则 200 返回答案 + 引用。
func qaResultToResponse(res *provider.QAResult) (int, map[string]any) {
	if !provider.HasReliableSources(res) {
		return http.StatusUnprocessableEntity, map[string]any{
			"error":   "证据不足，无法可靠回答：模型没有引用任何可核验片段。请换一种问法或查看转录稿。",
			"answer":  "",
			"sources": []provider.Source{},
		}
	}
	return http.StatusOK, map[string]any{
		"answer":  res.Answer,
		"sources": res.Sources,
	}
}

// cardView 把验证过的 KnowledgeCard 转换成模板友好结构（时间范围由程序从 Citation 解析）。
func cardView(c provider.KnowledgeCard, segments []provider.Segment) map[string]any {
	chapterView := make([]map[string]any, 0, len(c.Chapters))
	for _, ch := range c.Chapters {
		start, end, ok := citationSpan(ch.Citations, segments)
		if !ok {
			continue
		}
		chapterView = append(chapterView, map[string]any{
			"title": ch.Title, "gist": ch.Gist, "startTime": start, "endTime": end,
		})
	}
	quoteView := make([]map[string]any, 0, len(c.Quotes))
	for _, q := range c.Quotes {
		start, end, ok := citationSpan(q.Citations, segments)
		if !ok {
			continue
		}
		quoteView = append(quoteView, map[string]any{"text": q.Text, "startTime": start, "endTime": end})
	}
	keyPointView := make([]map[string]any, 0, len(c.KeyPoints))
	for _, kp := range c.KeyPoints {
		keyPointView = append(keyPointView, map[string]any{"content": kp.Content, "description": kp.Description})
	}
	return map[string]any{
		"title":     c.Title,
		"summary":   c.Summary.Text,
		"chapters":  chapterView,
		"keyPoints": keyPointView,
		"quotes":    quoteView,
		"tags":      c.Tags,
	}
}

// citationSpan 取 citations 的最小时间范围（程序解析，ADR-0008）。
func citationSpan(citations []string, segments []provider.Segment) (float64, float64, bool) {
	if len(citations) == 0 {
		return 0, 0, false
	}
	var start, end float64
	first := true
	for _, c := range citations {
		s, e, ok := provider.ResolveCitationRange(c, segments)
		if !ok {
			continue
		}
		if first || s < start {
			start = s
		}
		if first || e > end {
			end = e
		}
		first = false
	}
	return start, end, !first
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// saveUploadFile 将上传的音频保存到临时目录，供 worker 后续读取。
// 约定路径：<tempDir>/uploads/<uploadID>
func saveUploadFile(tempDir, uploadID string, src interface{ Read([]byte) (int, error) }) error {
	dir := filepath.Join(tempDir, "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst, err := os.Create(filepath.Join(dir, uploadID))
	if err != nil {
		return err
	}
	defer dst.Close()
	buf := make([]byte, 32<<20) // 32MB buffer
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}

// ---- KeyPoint 全局视图 + Owner 标注体系（ADR-0017）----

func (srv *Server) handleKeyPoints(w http.ResponseWriter, r *http.Request) {
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	const perPage = 20
	kps, total, err := srv.store.ListKeyPoints(r.Context(), page, perPage)
	if err != nil {
		http.Error(w, "加载失败", http.StatusInternalServerError)
		return
	}
	totalPages := (total + perPage - 1) / perPage
	srv.tmpl.Render(w, "keypoints.html", map[string]any{
		"KeyPoints":  kps,
		"Page":       page,
		"TotalPages": totalPages,
		"Total":      total,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
		"CSRF":       auth.CSRFValue(r),
	})
}

func (srv *Server) handleKeyPointsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	kps, total, err := srv.store.SearchKeyPoints(r.Context(), q, 1, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type result struct {
		ID          string  `json:"id"`
		SourceType  string  `json:"source_type"`
		SourceID    string  `json:"source_id"`
		SourceTitle string  `json:"source_title"`
		Content     string  `json:"content"`
		Description string  `json:"description"`
		TimeStart   float64 `json:"time_start"`
		TimeEnd     float64 `json:"time_end"`
	}
	results := make([]result, 0, len(kps))
	for _, kp := range kps {
		results = append(results, result{
			ID: kp.ID, SourceType: string(kp.SourceType), SourceID: kp.SourceID,
			SourceTitle: kp.SourceTitle, Content: kp.Content, Description: kp.Description,
			TimeStart: kp.TimeStart, TimeEnd: kp.TimeEnd,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "total": total})
}

// ---- 知识图谱（Episode + Tag 共现可视化）----

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
