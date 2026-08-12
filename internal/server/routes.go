package server

import (
	"net/http"

	"github.com/woyin/orangecast/internal/auth"
)

// Router 装配全部路由。
//
// 路由装配是 Server 的外部接口：所有 URL、HTTP 方法、处理器和中间件
// 顺序都在这里保持稳定；具体 handler 的实现位于各职责文件中。
//
// 中间件顺序（由内向外）：
//
//	受保护路由：RequireAuth（未登录 401/303）→ CSRFProtect（状态变更校验）
//	公开路由（登录/认领/登出）：CSRFProtect（防登录 CSRF）
//
// 因此未认证的 /api POST 先得到 401，认证后缺 token 的 POST 得到 403。
func (srv *Server) Router() http.Handler {
	mux := http.NewServeMux()
	srv.registerStaticRoutes(mux)
	srv.registerPublicRoutes(mux)
	mux.Handle("/", auth.RequireAuth(srv.store)(auth.CSRFProtect(srv.protectedRoutes())))
	return mux
}

func (srv *Server) registerStaticRoutes(mux *http.ServeMux) {
	staticFS, err := StaticFS()
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
}

func (srv *Server) registerPublicRoutes(mux *http.ServeMux) {
	mux.Handle("/login", auth.CSRFProtect(http.HandlerFunc(srv.handleLogin)))
	mux.Handle("/register", auth.CSRFProtect(http.HandlerFunc(srv.handleRegister)))
	mux.Handle("/logout", auth.CSRFProtect(http.HandlerFunc(srv.handleLogout)))
}

func (srv *Server) protectedRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard", srv.handleDashboard)
	mux.HandleFunc("/workbench", srv.handleWorkbench)
	mux.HandleFunc("/themes", srv.handleThemes)
	mux.HandleFunc("/themes/create", srv.handleThemeCreate)
	mux.HandleFunc("/themes/status", srv.handleThemeStatus)
	mux.HandleFunc("/themes/keypoints", srv.handleThemeKeyPoint)
	mux.HandleFunc("/themes/scout", srv.handleScoutRun)
	mux.HandleFunc("/workbench/profiles", srv.handleEditorialProfileCreate)
	mux.HandleFunc("/workbench/sources", srv.handleEditorialSourceScope)
	mux.HandleFunc("/workbench/proposals", srv.handleArticleProposalCreate)
	mux.HandleFunc("/workbench/proposals/status", srv.handleArticleProposalStatus)
	mux.HandleFunc("/workbench/briefs", srv.handleArticleBriefCreate)
	mux.HandleFunc("/workbench/briefs/confirm", srv.handleArticleBriefConfirm)
	mux.HandleFunc("/workbench/drafts", srv.handleArticleDraftCreate)
	mux.HandleFunc("/workbench/drafts/", srv.handleArticleDraftDetail)
	mux.HandleFunc("/workbench/revisions", srv.handleArticleRevisionCreate)
	mux.HandleFunc("/workbench/revisions/", srv.handlePublicationPackage)
	mux.HandleFunc("/workbench/reviews", srv.handleArticleReviewCreate)
	mux.HandleFunc("/workbench/reviews/evidence", srv.handleEvidenceReviewRun)
	mux.HandleFunc("/workbench/reviews/style", srv.handleStyleReviewRun)
	mux.HandleFunc("/workbench/write", srv.handleArticleWriterRun)
	mux.HandleFunc("/workbench/revise", srv.handleArticleRevisionWriterRun)
	mux.HandleFunc("/progress", srv.handleProgress)
	mux.HandleFunc("/api/progress", srv.handleProgressAPI)
	mux.HandleFunc("/podcasts", srv.handlePodcasts)
	mux.HandleFunc("/podcasts/new", srv.handlePodcastNew)
	mux.HandleFunc("/api/podcasts/ingestion-policy", srv.handlePodcastIngestionPolicy)
	mux.HandleFunc("/podcasts/", srv.handlePodcastDetail)
	mux.HandleFunc("/uploads", srv.handleUploads)
	mux.HandleFunc("/uploads/new", srv.handleUploadNew)
	mux.HandleFunc("/sources/", srv.handleSourceDetail) // /sources/{type}/{id}[/dj|/download|/versions]
	mux.HandleFunc("/search", srv.handleSearch)
	mux.HandleFunc("/keypoints", srv.handleKeyPoints)
	mux.HandleFunc("/graph", srv.handleGraph)
	mux.HandleFunc("/api/graph", srv.handleGraphAPI)
	mux.HandleFunc("/api/keypoints/search", srv.handleKeyPointsSearch)
	mux.HandleFunc("/api/keypoints/status", srv.handleKeyPointStatus)
	mux.HandleFunc("/api/annotation", srv.handleAnnotation)
	mux.HandleFunc("/api/pin", srv.handlePin)
	mux.HandleFunc("/api/collection", srv.handleCollection)
	mux.HandleFunc("/api/collection/item", srv.handleCollectionItem)
	mux.HandleFunc("/settings", srv.handleSettings)
	mux.HandleFunc("/api/qa", srv.handleEvidenceQA)                       // 向后兼容别名
	mux.HandleFunc("/api/evidence-qa", srv.handleEvidenceQA)              // 规范路径（ADR-0018）
	mux.HandleFunc("/api/paraphrase", srv.handleParaphrase)               // Paraphrase（GeneratedDerivative，ADR-0018 R2）
	mux.HandleFunc("/api/study-chat", srv.handleStudyChat)                // StudyChat（GeneratedDerivative，ADR-0018 R3）
	mux.HandleFunc("/api/study-chat/history", srv.handleStudyChatHistory) // StudyChat 历史回看
	mux.HandleFunc("/api/process", srv.handleProcess)
	mux.HandleFunc("/api/purge", srv.handlePurge) // Purge（ADR-0012）：完整删除 Source
	mux.HandleFunc("/api/process-batch", srv.handleProcessBatch)
	mux.HandleFunc("/api/audio/", srv.handleAudio)
	mux.HandleFunc("/api/narration/", srv.handleNarration) // Narration 解说音轨（ADR-0019）
	return mux
}
