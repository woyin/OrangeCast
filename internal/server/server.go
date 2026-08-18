package server

import (
	"context"
	"sync"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/queue"
	"github.com/woyin/orangecast/internal/rss"
	"github.com/woyin/orangecast/internal/store"
)

// Server 装配所有依赖与路由。
type Server struct {
	cfg           *config.Config
	store         *store.Store
	worker        *queue.Worker
	refresher     *rss.Refresher
	selector      *provider.Selector
	bundleFor     func(provider.TaskConfig) (*provider.ProviderBundle, error) // 可注入（测试用），默认走 selector
	fetchFeed     func(string) (*models.Podcast, []models.Episode, error)     // 可注入（测试用），默认走 rss.FetchFeed
	fetchDocument func(context.Context, string) (string, string, error)
	tmpl          *Templates
	loginLimiter  *auth.RateLimiter
	autoRefill    bool
	refillMu      sync.Mutex
	refilling     map[string]bool
	refillErrors  map[string]string
}

// New 装配 Server 全部依赖：模板、登录限流、默认 bundleFor/fetchFeed。
// bundleFor 默认走 selector；测试可注入 srv.bundleFor 提供 fake provider。
func New(cfg *config.Config, s *store.Store, worker *queue.Worker, refresher *rss.Refresher, selector *provider.Selector) (*Server, error) {
	tmpl, err := NewTemplates()
	if err != nil {
		return nil, err
	}
	// 登录限流：每 IP 每 5 分钟最多 20 次尝试（ADR-0013）
	limiter := auth.NewRateLimiter(20, 5*time.Minute)
	srv := &Server{cfg: cfg, store: s, worker: worker, refresher: refresher, selector: selector, tmpl: tmpl, loginLimiter: limiter, autoRefill: true, refilling: map[string]bool{}, refillErrors: map[string]string{}}
	// 默认 bundle 解析走 selector；测试可注入 srv.bundleFor 提供 fake provider。
	srv.bundleFor = func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return selector.BundleForTask(tc)
	}
	srv.fetchFeed = rss.FetchFeed
	srv.fetchDocument = fetchWebDocument
	return srv, nil
}
