package rss

import (
	"context"
	"log"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/breestealth/wisepod/internal/store"
	"github.com/robfig/cron/v3"
)

// Refresher 定时刷新所有播客 feed 的调度器。
type Refresher struct {
	cron  *cron.Cron
	store *store.Store
}

// NewRefresher 创建刷新器，每 30 分钟跑一次。
func NewRefresher(s *store.Store) *Refresher {
	r := &Refresher{cron: cron.New(), store: s}
	r.cron.AddFunc("*/30 * * * *", func() {
		if err := r.RefreshAll(context.Background()); err != nil {
			log.Printf("cron 刷新失败: %v", err)
		}
	})
	return r
}

func (r *Refresher) Start() { r.cron.Start() }
func (r *Refresher) Stop()  { r.cron.Stop() }

// RefreshAll 按 last_fetched_at ASC 取一批（25 个）刷新。
// 只插入新 episode，不自动触发 AI 处理（与原设计一致，手动处理）。
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
	_, eps, err := FetchFeed(p.FeedURL)
	if err != nil {
		return err
	}
	if _, err := r.store.MergeEpisodes(ctx, p.ID, p.UserID, eps); err != nil {
		return err
	}
	return r.store.UpdatePodcastFetched(ctx, p.ID)
}
