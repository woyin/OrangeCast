// podcast_opml.go OPML 导入需要的两项仓储能力：按 feed 地址查重、标记订阅来自 OPML。
// opml_present 只是展示来源，不改变摄取策略或处理深度；自动处理仍由 Owner 逐条开启。
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/woyin/orangecast/internal/models"
)

// GetPodcastByFeedURL 按 feed 地址读取订阅；不存在返回 ErrNotFound。
func (s *Store) GetPodcastByFeedURL(ctx context.Context, feedURL string) (*models.Podcast, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT id FROM podcasts WHERE feed_url = ?", feedURL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetPodcastByID(ctx, id)
}

// MarkPodcastOPMLPresent 记录该订阅随 OPML 清单导入，便于 Owner 区分来源。
func (s *Store) MarkPodcastOPMLPresent(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE podcasts SET opml_present = 1 WHERE id = ?", id)
	return err
}
