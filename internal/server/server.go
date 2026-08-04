package server

import (
	"net/http"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/store"
)

// Server 装配所有依赖与路由。
type Server struct {
	cfg          *config.Config
	store        *store.Store
	worker       *queue.Worker
	refresher    *rss.Refresher
	selector     *provider.Selector
	tmpl         *Templates
	loginLimiter *auth.RateLimiter
}

func New(cfg *config.Config, s *store.Store, worker *queue.Worker, refresher *rss.Refresher, selector *provider.Selector) (*Server, error) {
	tmpl, err := NewTemplates()
	if err != nil {
		return nil, err
	}
	// 登录限流：每 IP 每 5 分钟最多 20 次尝试（ADR-0013）
	limiter := auth.NewRateLimiter(20, 5*time.Minute)
	return &Server{cfg: cfg, store: s, worker: worker, refresher: refresher, selector: selector, tmpl: tmpl, loginLimiter: limiter}, nil
}

// Router 装配全部路由。
//
// 中间件顺序（由内向外）：
//
//	受保护路由：RequireAuth（未登录 401/303）→ CSRFProtect（状态变更校验 token）
//	公开路由（登录/认领/登出）：CSRFProtect（防登录 CSRF，ADR-0013）
//
// 因此未认证的 /api POST 先得到 401，认证后缺 token 的 POST 得到 403。
func (srv *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// 静态资源（嵌入）
	staticFS, err := StaticFS()
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// 公开路由（认领与登录）：包 CSRF，防登录 CSRF。
	mux.Handle("/login", auth.CSRFProtect(http.HandlerFunc(srv.handleLogin)))
	mux.Handle("/register", auth.CSRFProtect(http.HandlerFunc(srv.handleRegister)))
	mux.Handle("/logout", auth.CSRFProtect(http.HandlerFunc(srv.handleLogout)))

	// 受保护路由：认证 → CSRF。
	authMw := auth.RequireAuth(srv.store)
	protected := http.NewServeMux()
	protected.HandleFunc("/dashboard", srv.handleDashboard)
	protected.HandleFunc("/progress", srv.handleProgress)
	protected.HandleFunc("/api/progress", srv.handleProgressAPI)
	protected.HandleFunc("/podcasts", srv.handlePodcasts)
	protected.HandleFunc("/podcasts/new", srv.handlePodcastNew)
	protected.HandleFunc("/podcasts/", srv.handlePodcastDetail)
	protected.HandleFunc("/uploads", srv.handleUploads)
	protected.HandleFunc("/uploads/new", srv.handleUploadNew)
	protected.HandleFunc("/sources/", srv.handleSourceDetail) // /sources/{type}/{id}[/dj|/download|/versions]
	protected.HandleFunc("/search", srv.handleSearch)
	protected.HandleFunc("/settings", srv.handleSettings)
	protected.HandleFunc("/api/qa", srv.handleQA)
	protected.HandleFunc("/api/process", srv.handleProcess)
	protected.HandleFunc("/api/audio/", srv.handleAudio)
	mux.Handle("/", authMw(auth.CSRFProtect(protected)))
	return mux
}
