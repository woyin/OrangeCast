package server

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/linkimport"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/notesync"
	"github.com/woyin/orangecast/internal/notify"
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
	readwise      *notesync.Readwise                                        // 可注入（测试用）；Token 为空时视为未配置
	importLink    func(context.Context, string) (*linkimport.Result, error) // 可注入（测试用），默认走 linkimport
	tmpl          *Templates
	loginLimiter  *auth.RateLimiter
	discoveryMu   sync.Mutex
	reviewClock   func() time.Time
}

// New 装配 Server 全部依赖：模板、登录限流、默认 bundleFor/fetchFeed。
// bundleFor 默认走 selector；测试可注入 srv.bundleFor 提供 fake provider。
func New(cfg *config.Config, s *store.Store, worker *queue.Worker, refresher *rss.Refresher, selector *provider.Selector) (*Server, error) {
	if err := s.BeginOfflineInstance(context.Background()); err != nil {
		return nil, err
	}
	tmpl, err := NewTemplates()
	if err != nil {
		return nil, err
	}
	// 登录限流：每 IP 每 5 分钟最多 20 次尝试（ADR-0013）
	limiter := auth.NewRateLimiter(20, 5*time.Minute)
	srv := &Server{cfg: cfg, store: s, worker: worker, refresher: refresher, selector: selector, tmpl: tmpl, loginLimiter: limiter}
	queue.ConfigureLearningExports(worker, s, filepath.Join(cfg.DataDir, "learning-exports"))
	configureNotifications(worker, cfg)
	// 默认 bundle 解析走 selector；测试可注入 srv.bundleFor 提供 fake provider。
	srv.bundleFor = func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return selector.BundleForTask(tc)
	}
	srv.fetchFeed = rss.FetchFeed
	srv.fetchDocument = fetchWebDocument
	importer := &linkimport.Importer{YtDlp: cfg.YtDlpBinary, TempDir: filepath.Join(cfg.TempDir, "link-import")}
	srv.importLink = importer.Fetch
	srv.readwise = &notesync.Readwise{Token: cfg.ReadwiseToken}
	return srv, nil
}

// configureNotifications 在配置了 Webhook 或 Telegram 时，让 worker 在来源处理完成后推送通知。
func configureNotifications(worker *queue.Worker, cfg *config.Config) {
	if hook := notificationHook(cfg); worker != nil && hook != nil {
		worker.WithOnProcessed(hook)
	}
}

// notificationHook 构造“处理完成”回调；没有任何通知渠道时返回 nil。
// 推送放在独立 goroutine 里，失败只记日志，绝不拖慢或中断处理流水线。
func notificationHook(cfg *config.Config) func(context.Context, models.SourceType, string, string, string) {
	n := &notify.Notifier{WebhookURL: cfg.NotifyWebhookURL, TelegramToken: cfg.TelegramBotToken, TelegramChatID: cfg.TelegramChatID}
	if !n.Enabled() {
		return nil
	}
	return func(_ context.Context, sourceType models.SourceType, sourceID, title, summary string) {
		ev := notify.Event{Title: title, Summary: summary, URL: strings.TrimSuffix(cfg.PublicURL, "/") + "/sources/" + string(sourceType) + "/" + sourceID}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := n.Send(ctx, ev); err != nil {
				log.Printf("处理完成通知失败: %v", err)
			}
		}()
	}
}
