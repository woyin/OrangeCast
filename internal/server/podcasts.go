// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：Podcast 订阅与 Upload 管理）。
package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
)

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
	podcast, eps, err := srv.fetchFeed(feedURL)
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
	page := pageParam(r)
	const perPage = 10
	eps, total, _ := srv.store.ListEpisodesPaginated(r.Context(), path, page, perPage)
	batchEnqueued := r.URL.Query().Get("enqueued")
	batchSkipped := r.URL.Query().Get("skipped")
	batchDone := ""
	if batchEnqueued != "" || batchSkipped != "" {
		batchDone = "1"
	}
	srv.tmpl.Render(w, "podcast_detail.html", map[string]any{
		"Podcast": p, "Episodes": eps, "CSRF": auth.CSRFValue(r),
		"BatchDone": batchDone, "BatchEnqueued": batchEnqueued, "BatchSkipped": batchSkipped,
		"Page": page, "TotalPages": totalPages(total, perPage), "Total": total,
		"PrevPage": page - 1, "NextPage": page + 1,
	})
}

// handlePodcastIngestionPolicy updates the Owner-selected automatic ingestion policy.
func (srv *Server) handlePodcastIngestionPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	podcastID := strings.TrimSpace(r.FormValue("podcast_id"))
	policy := models.IngestionPolicy(strings.TrimSpace(r.FormValue("ingestion_policy")))
	if err := srv.store.SetPodcastIngestionPolicyWithFilters(r.Context(), podcastID, policy, r.FormValue("include_keywords"), r.FormValue("exclude_keywords")); err != nil {
		http.Error(w, "更新自动摄取策略失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/podcasts/"+podcastID, http.StatusSeeOther)
}
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
