package server

import (
	"net/http"

	"github.com/breestealth/wisepod/internal/auth"
	"github.com/breestealth/wisepod/internal/config"
	"github.com/breestealth/wisepod/internal/provider"
	"github.com/breestealth/wisepod/internal/queue"
	"github.com/breestealth/wisepod/internal/rss"
	"github.com/breestealth/wisepod/internal/store"
)

// Server 装配所有依赖与路由。
type Server struct {
	cfg       *config.Config
	store     *store.Store
	worker    *queue.Worker
	refresher *rss.Refresher
	selector  *provider.Selector
	tmpl      *Templates
}

func New(cfg *config.Config, s *store.Store, worker *queue.Worker, refresher *rss.Refresher, selector *provider.Selector) (*Server, error) {
	tmpl, err := NewTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, store: s, worker: worker, refresher: refresher, selector: selector, tmpl: tmpl}, nil
}

// Router 装配全部路由。
func (srv *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// 静态资源（嵌入）
	staticFS, err := StaticFS()
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// 公开路由
	mux.HandleFunc("/login", srv.handleLogin)
	mux.HandleFunc("/register", srv.handleRegister)
	mux.HandleFunc("/logout", srv.handleLogout)

	// 受保护路由
	authMw := auth.RequireAuth(srv.store)
	protected := http.NewServeMux()
	protected.HandleFunc("/dashboard", srv.handleDashboard)
	protected.HandleFunc("/podcasts", srv.handlePodcasts)
	protected.HandleFunc("/podcasts/new", srv.handlePodcastNew)
	protected.HandleFunc("/podcasts/", srv.handlePodcastDetail)
	protected.HandleFunc("/uploads", srv.handleUploads)
	protected.HandleFunc("/uploads/new", srv.handleUploadNew)
	protected.HandleFunc("/sources/", srv.handleSourceDetail)
	protected.HandleFunc("/search", srv.handleSearch)
	protected.HandleFunc("/settings", srv.handleSettings)
	protected.HandleFunc("/api/qa", srv.handleQA)
	protected.HandleFunc("/api/process", srv.handleProcess)
	protected.HandleFunc("/api/audio/", srv.handleAudio)

	mux.Handle("/", authMw(protected))
	return mux
}
