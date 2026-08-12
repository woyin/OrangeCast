package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// CreatePodcast 创建播客订阅。feed_url 全局唯一（单 Owner 实例，ADR-0007）。
func (s *Store) CreatePodcast(ctx context.Context, feedURL, title, description, imageURL string) (*models.Podcast, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO podcasts (id, feed_url, title, description, image_url) VALUES (?, ?, ?, ?, ?)`,
		id, feedURL, title, description, imageURL)
	if err != nil {
		return nil, fmt.Errorf("创建播客: %w", err)
	}
	return s.GetPodcastByID(ctx, id)
}

// GetPodcastByID 按 ID 查询单个播客订阅。
func (s *Store) GetPodcastByID(ctx context.Context, id string) (*models.Podcast, error) {
	p := &models.Podcast{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at, ingestion_policy
		 FROM podcasts WHERE id = ?`, id).
		Scan(&p.ID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt, &p.IngestionPolicy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListPodcasts 列出全部订阅。
func (s *Store) ListPodcasts(ctx context.Context) ([]*models.Podcast, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at, ingestion_policy
		 FROM podcasts ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Podcast
	for rows.Next() {
		p := &models.Podcast{}
		if err := rows.Scan(&p.ID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt, &p.IngestionPolicy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdatePodcastFetched 记录刷新时间。
func (s *Store) UpdatePodcastFetched(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE podcasts SET last_fetched_at = datetime('now') WHERE id = ?`, id)
	return err
}

// SetPodcastIngestionPolicy changes how newly discovered episodes enter processing.
func (s *Store) SetPodcastIngestionPolicy(ctx context.Context, id string, policy models.IngestionPolicy) error {
	if policy != models.IngestionManual && policy != models.IngestionAllNew && policy != models.IngestionFiltered {
		return fmt.Errorf("invalid ingestion policy %q", policy)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE podcasts SET ingestion_policy=? WHERE id=?`, string(policy), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUnprocessedEpisodes returns candidates that a policy may elect to queue.
func (s *Store) ListUnprocessedEpisodes(ctx context.Context, podcastID string) ([]*models.Episode, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE podcast_id=? AND processing_status='unprocessed' ORDER BY published_at DESC, created_at DESC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Episode
	for rows.Next() {
		episode := &models.Episode{}
		if err := rows.Scan(&episode.ID, &episode.PodcastID, &episode.GUID, &episode.Title, &episode.Description, &episode.AudioURL, &episode.DurationSeconds, &episode.PublishedAt, &episode.ProcessingStatus, &episode.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, episode)
	}
	return out, rows.Err()
}

// ListPodcastsForRefresh 按 last_fetched_at ASC 取一批用于 cron 刷新。
func (s *Store) ListPodcastsForRefresh(ctx context.Context, limit int) ([]*models.Podcast, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at, ingestion_policy
		 FROM podcasts ORDER BY last_fetched_at IS NOT NULL, last_fetched_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Podcast
	for rows.Next() {
		p := &models.Podcast{}
		if err := rows.Scan(&p.ID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt, &p.IngestionPolicy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MergeEpisodes 插入新 episode（按 guid 去重），返回新增数量。已存在的 guid 被忽略。
func (s *Store) MergeEpisodes(ctx context.Context, podcastID string, eps []models.Episode) (int, error) {
	if len(eps) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	inserted := 0
	for _, e := range eps {
		e.ID = uuid.NewString()
		e.PodcastID = podcastID
		e.ProcessingStatus = models.StatusUnprocessed
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO episodes (id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.PodcastID, e.GUID, e.Title, e.Description, e.AudioURL, e.DurationSeconds, e.PublishedAt, string(e.ProcessingStatus))
		if err != nil {
			return inserted, fmt.Errorf("插入 episode: %w", err)
		}
		// INSERT OR IGNORE 无法直接判断是否插入；用 changes 间接统计。
		var changes int
		if err := tx.QueryRowContext(ctx, "SELECT changes()").Scan(&changes); err != nil {
			return inserted, err
		}
		inserted += changes
	}
	return inserted, tx.Commit()
}

// ListEpisodes 返回某播客下的全部单集。
func (s *Store) ListEpisodes(ctx context.Context, podcastID string) ([]*models.Episode, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE podcast_id = ? ORDER BY published_at DESC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Episode
	for rows.Next() {
		e := &models.Episode{}
		if err := rows.Scan(&e.ID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.AudioURL, &e.DurationSeconds, &e.PublishedAt, &e.ProcessingStatus, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEpisodeByID 按 ID 查询单个播客单集。
func (s *Store) GetEpisodeByID(ctx context.Context, id string) (*models.Episode, error) {
	e := &models.Episode{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE id = ?`, id).
		Scan(&e.ID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.AudioURL, &e.DurationSeconds, &e.PublishedAt, &e.ProcessingStatus, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// CreateUpload 记录手动上传的音频元数据（音频文件本身走临时落盘，不持久化）。
func (s *Store) CreateUpload(ctx context.Context, filename, contentType string, size int64) (*models.Upload, error) {
	u := &models.Upload{
		ID: uuid.NewString(), OriginalFilename: filename, ContentType: contentType, SizeBytes: size,
		ProcessingStatus: models.StatusUnprocessed,
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO uploads (id, original_filename, content_type, size_bytes, processing_status)
		 VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.OriginalFilename, u.ContentType, u.SizeBytes, string(u.ProcessingStatus))
	if err != nil {
		return nil, fmt.Errorf("创建 upload: %w", err)
	}
	return u, nil
}

// GetUploadByID 按 ID 查询单个手动上传。
func (s *Store) GetUploadByID(ctx context.Context, id string) (*models.Upload, error) {
	u := &models.Upload{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, original_filename, content_type, size_bytes, duration_seconds, processing_status, created_at
		 FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.OriginalFilename, &u.ContentType, &u.SizeBytes, &u.DurationSeconds, &u.ProcessingStatus, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ListUploads 返回全部手动上传单。
func (s *Store) ListUploads(ctx context.Context) ([]*models.Upload, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, original_filename, content_type, size_bytes, duration_seconds, processing_status, created_at
		 FROM uploads ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Upload
	for rows.Next() {
		u := &models.Upload{}
		if err := rows.Scan(&u.ID, &u.OriginalFilename, &u.ContentType, &u.SizeBytes, &u.DurationSeconds, &u.ProcessingStatus, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListEpisodesPaginated 分页查询单集（page 从 1 开始，每页 perPage 条）。
// 返回当前页数据与总数。
func (s *Store) ListEpisodesPaginated(ctx context.Context, podcastID string, page, perPage int) ([]*models.Episode, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}
	var total int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM episodes WHERE podcast_id = ?`, podcastID).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * perPage
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE podcast_id = ? ORDER BY published_at IS NULL, published_at DESC LIMIT ? OFFSET ?`,
		podcastID, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*models.Episode
	for rows.Next() {
		e := &models.Episode{}
		if err := rows.Scan(&e.ID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.AudioURL, &e.DurationSeconds, &e.PublishedAt, &e.ProcessingStatus, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
