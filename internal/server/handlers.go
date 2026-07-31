package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/breestealth/wisepod/internal/auth"
	"github.com/breestealth/wisepod/internal/models"
	"github.com/breestealth/wisepod/internal/provider"
	"github.com/breestealth/wisepod/internal/rss"
	"github.com/breestealth/wisepod/internal/store"
)

// ---- 认证 ----

func (srv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "register.html", map[string]any{})
		return
	}
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	if err := auth.ValidateEmail(email); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "邮箱格式无效"})
		return
	}
	if err := auth.ValidatePassword(pw); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "密码至少 8 位"})
		return
	}
	if err := auth.EnsureUnusedEmail(r.Context(), srv.store, email); err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": err.Error()})
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	u, err := srv.store.CreateUser(r.Context(), email, hash)
	if err != nil {
		srv.tmpl.Render(w, "register.html", map[string]any{"Error": "注册失败"})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID); err != nil {
		http.Error(w, "内部错误", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (srv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "login.html", map[string]any{})
		return
	}
	email := auth.NormalizeEmail(r.FormValue("email"))
	pw := r.FormValue("password")
	u, err := srv.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误"})
		return
	}
	ok, err := auth.VerifyPassword(pw, u.PasswordHash)
	if err != nil || !ok {
		srv.tmpl.Render(w, "login.html", map[string]any{"Error": "邮箱或密码错误"})
		return
	}
	if err := auth.SetSessionCookie(w, r, srv.store, u.ID); err != nil {
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
	userID, _ := auth.UserIDFromContext(r.Context())
	ps, err := srv.store.ListPodcasts(r.Context(), userID)
	if err != nil {
		http.Error(w, "加载失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "podcasts.html", map[string]any{"Podcasts": ps})
}

func (srv *Server) handlePodcastNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{})
		return
	}
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	podcast, eps, err := rss.FetchFeed(feedURL)
	if err != nil {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{"Error": "抓取/解析 feed 失败: " + err.Error()})
		return
	}
	userID, _ := auth.UserIDFromContext(r.Context())
	p, err := srv.store.CreatePodcast(r.Context(), userID, feedURL, podcast.Title, podcast.Description, podcast.ImageURL)
	if err != nil {
		srv.tmpl.Render(w, "podcast_new.html", map[string]any{"Error": "订阅失败（可能已订阅）"})
		return
	}
	if _, err := srv.store.MergeEpisodes(r.Context(), p.ID, userID, eps); err != nil {
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
	userID, _ := auth.UserIDFromContext(r.Context())
	p, err := srv.store.GetPodcastByID(r.Context(), path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if p.UserID != userID {
		http.NotFound(w, r)
		return
	}
	eps, _ := srv.store.ListEpisodes(r.Context(), path)
	srv.tmpl.Render(w, "podcast_detail.html", map[string]any{"Podcast": p, "Episodes": eps})
}

// ---- Uploads ----

func (srv *Server) handleUploads(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	us, _ := srv.store.ListUploads(r.Context(), userID)
	srv.tmpl.Render(w, "uploads.html", map[string]any{"Uploads": us})
}

func (srv *Server) handleUploadNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{})
		return
	}
	// 限制大小 200MB
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "上传失败: " + err.Error()})
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "请选择音频文件"})
		return
	}
	defer file.Close()
	if !isAllowedAudio(header.Filename, header.Header.Get("Content-Type")) {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "仅支持 mp3/m4a/wav"})
		return
	}
	userID, _ := auth.UserIDFromContext(r.Context())
	up, err := srv.store.CreateUpload(r.Context(), userID, header.Filename, header.Header.Get("Content-Type"), header.Size)
	if err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "保存失败"})
		return
	}
	// 音频持久化到临时目录（worker 从这里读，处理后保留以便重试）
	if err := saveUploadFile(srv.cfg.TempDir, up.ID, file); err != nil {
		srv.tmpl.Render(w, "upload_new.html", map[string]any{"Error": "存储音频失败"})
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
	sourceType := models.SourceType(parts[0])
	sourceID := parts[1]
	userID, _ := auth.UserIDFromContext(r.Context())

	audioURL := ""
	if sourceType == models.SourceEpisode {
		ep, err := srv.store.GetEpisodeByID(r.Context(), sourceID)
		if err != nil || ep.UserID != userID {
			http.NotFound(w, r)
			return
		}
		audioURL = ep.AudioURL
	} else {
		up, err := srv.store.GetUploadByID(r.Context(), sourceID)
		if err != nil || up.UserID != userID {
			http.NotFound(w, r)
			return
		}
		// upload 音频通过内部端点提供
		audioURL = "/api/audio/" + sourceID
	}

	t, _ := srv.store.GetTranscript(r.Context(), userID, sourceType, sourceID)
	a, _ := srv.store.GetAnalysis(r.Context(), userID, sourceType, sourceID)

	var segments []provider.Segment
	title := "处理中…"
	summary := ""
	var card map[string]any
	if t != nil {
		segments = parseSegments(t.SegmentsJSON)
	}
	if a != nil {
		title = a.Title
		summary = a.Summary
		card = parseCard(a.ContentJSON)
	}

	data := map[string]any{
		"Title":    title,
		"Summary":  summary,
		"AudioURL": audioURL,
		"Segments": segments,
		"Card":     card,
	}
	srv.tmpl.Render(w, "source_detail.html", data)
}

// ---- 搜索 ----

func (srv *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []store.SearchHit
	if q != "" {
		hits, _ = srv.store.SearchSource(r.Context(), userID, q)
	}
	srv.tmpl.Render(w, "search.html", map[string]any{"Query": q, "Hits": hits})
}

// ---- 设置 ----

func (srv *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	if r.Method == http.MethodPost {
		provider := r.FormValue("active_provider")
		if provider == "groq" || provider == "openai" {
			_ = srv.store.UpdateActiveProvider(r.Context(), userID, provider)
		}
		http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
		return
	}
	st, _ := srv.store.GetOrCreateSettings(r.Context(), userID)
	srv.tmpl.Render(w, "settings.html", map[string]any{
		"ActiveProvider": st.ActiveProvider,
		"Saved":          r.URL.Query().Get("saved") == "1",	})
}

// ---- API ----

func (srv *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	userID, _ := auth.UserIDFromContext(r.Context())
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	job, err := srv.store.EnqueueJob(r.Context(), userID, sourceType, sourceID, models.JobTranscribe)
	if err != nil {
		http.Error(w, "入队失败", http.StatusInternalServerError)
		return
	}
	if job != nil {
		srv.worker.Process(job)
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

func (srv *Server) handleQA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	userID, _ := auth.UserIDFromContext(r.Context())
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	question := r.FormValue("question")

	t, err := srv.store.GetTranscript(r.Context(), userID, sourceType, sourceID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "无转录稿"})
		return
	}
	st, _ := srv.store.GetOrCreateSettings(r.Context(), userID)
	bundle, err := srv.selector.Bundle(st.ActiveProvider)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	res, err := bundle.QA.Answer(question, parseSegments(t.SegmentsJSON))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "回答失败"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAudio 提供 upload 音频文件（episode 直接用外链 audio_url，无需此端点）。
func (srv *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())
	uploadID := strings.TrimPrefix(r.URL.Path, "/api/audio/")
	up, err := srv.store.GetUploadByID(r.Context(), uploadID)
	if err != nil || up.UserID != userID {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(srv.cfg.TempDir, "uploads", uploadID)
	w.Header().Set("Content-Type", up.ContentType)
	http.ServeFile(w, r, path)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// parseSegments 解析 segments_json（[{start,end,text}]）为播放器联动所需结构。
func parseSegments(raw string) []provider.Segment {
	if raw == "" {
		return nil
	}
	var segs []provider.Segment
	if err := json.Unmarshal([]byte(raw), &segs); err != nil {
		return nil
	}
	return segs
}

// parseCard 解析 KnowledgeCard JSON 为模板可用的 map。
func parseCard(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
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
