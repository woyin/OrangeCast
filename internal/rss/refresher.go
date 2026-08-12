// Package rss 实现 Podcast feed 抓取、解析（gofeed）与 30 分钟周期刷新调度。
// feed.go 提供 FetchFeed（复用 safehttp SSRF 防护客户端）与 parseFeed；
// refresher.go 用 robfig/cron 定时拉取新 Episode 并 MergeEpisodes 入库。
package rss

import (
	"context"
	"log"

	"github.com/robfig/cron/v3"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// Refresher 定时刷新所有播客 feed 的调度器。
type Refresher struct {
	cron      *cron.Cron
	store     *store.Store
	fetchFeed func(string) (*models.Podcast, []models.Episode, error) // 可注入（测试用），默认走 FetchFeed
}

// NewRefresher 创建刷新器，每 30 分钟跑一次。
func NewRefresher(s *store.Store) *Refresher {
	r := &Refresher{cron: cron.New(), store: s, fetchFeed: FetchFeed}
	r.cron.AddFunc("*/30 * * * *", r.runScheduled)
	return r
}

// Start 启动定时刷新调度。
func (r *Refresher) Start() { r.cron.Start() }

// Stop 停止定时刷新调度。
func (r *Refresher) Stop() { r.cron.Stop() }

// runScheduled cron 定时调用的入口：刷新失败仅记录日志，不中断调度。
func (r *Refresher) runScheduled() {
	if err := r.RefreshAll(context.Background()); err != nil {
		log.Printf("cron 刷新失败: %v", err)
	}
}

// RefreshAll 按 last_fetched_at ASC 取一批（25 个）刷新。
// 新 Episode 是否自动入队由 Podcast 的 IngestionPolicy 决定；默认 manual 不产生 AI 调用。
func (r *Refresher) RefreshAll(ctx context.Context) error {
	podcasts, err := r.store.ListPodcastsForRefresh(ctx, 25)
	if err != nil {
		return err
	}
	for _, p := range podcasts {
		if err := r.refreshOne(ctx, p); err != nil {
			log.Printf("刷新播客 %s (%s) 失败: %v", p.ID, p.Title, err)
			continue
		}
	}
	return nil
}

func (r *Refresher) refreshOne(ctx context.Context, p *models.Podcast) error {
	_, eps, err := r.fetchFeed(p.FeedURL)
	if err != nil {
		return err
	}
	knownCandidates := make(map[string]struct{})
	if p.IngestionPolicy == string(models.IngestionAllNew) {
		candidates, err := r.store.ListUnprocessedEpisodes(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, episode := range candidates {
			knownCandidates[episode.ID] = struct{}{}
		}
	}
	if _, err := r.store.MergeEpisodes(ctx, p.ID, eps); err != nil {
		return err
	}
	if p.IngestionPolicy == string(models.IngestionAllNew) {
		candidates, err := r.store.ListUnprocessedEpisodes(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, episode := range candidates {
			if _, existed := knownCandidates[episode.ID]; existed {
				continue
			}
			if _, err := r.store.EnqueueJob(ctx, models.SourceEpisode, episode.ID, models.JobTranscribe); err != nil {
				return err
			}
		}
	}
	return r.store.UpdatePodcastFetched(ctx, p.ID)
}
