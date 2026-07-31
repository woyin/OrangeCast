package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/google/uuid"
)

// CreatePodcast 创建播客订阅。(user_id, feed_url) 唯一。
func (s *Store) CreatePodcast(ctx context.Context, userID, feedURL, title, description, imageURL string) (*models.Podcast, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO podcasts (id, user_id, feed_url, title, description, image_url) VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, feedURL, title, description, imageURL)
	if err != nil {
		return nil, fmt.Errorf("创建播客: %w", err)
	}
	return s.GetPodcastByID(ctx, id)
}

func (s *Store) GetPodcastByID(ctx context.Context, id string) (*models.Podcast, error) {
	p := &models.Podcast{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at
		 FROM podcasts WHERE id = ?`, id).
		Scan(&p.ID, &p.UserID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListPodcasts 列出用户所有订阅。
func (s *Store) ListPodcasts(ctx context.Context, userID string) ([]*models.Podcast, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at
		 FROM podcasts WHERE user_id = ? ORDER BY title`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Podcast
	for rows.Next() {
		p := &models.Podcast{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt); err != nil {
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

// ListPodcastsForRefresh 按 last_fetched_at ASC 取一批用于 cron 刷新。
func (s *Store) ListPodcastsForRefresh(ctx context.Context, limit int) ([]*models.Podcast, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, feed_url, title, COALESCE(description,''), COALESCE(image_url,''), last_fetched_at, created_at
		 FROM podcasts ORDER BY last_fetched_at IS NOT NULL, last_fetched_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Podcast
	for rows.Next() {
		p := &models.Podcast{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.FeedURL, &p.Title, &p.Description, &p.ImageURL, &p.LastFetchedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MergeEpisodes 插入新 episode（按 guid 去重），返回新增数量。已存在的 guid 被忽略。
func (s *Store) MergeEpisodes(ctx context.Context, podcastID, userID string, eps []models.Episode) (int, error) {
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
		e.UserID = userID
		e.ProcessingStatus = models.StatusUnprocessed
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO episodes (id, user_id, podcast_id, guid, title, description, audio_url, duration_seconds, published_at, processing_status)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.UserID, e.PodcastID, e.GUID, e.Title, e.Description, e.AudioURL, e.DurationSeconds, e.PublishedAt, string(e.ProcessingStatus))
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

func (s *Store) ListEpisodes(ctx context.Context, podcastID string) ([]*models.Episode, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE podcast_id = ? ORDER BY published_at DESC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Episode
	for rows.Next() {
		e := &models.Episode{}
		if err := rows.Scan(&e.ID, &e.UserID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.AudioURL, &e.DurationSeconds, &e.PublishedAt, &e.ProcessingStatus, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetEpisodeByID(ctx context.Context, id string) (*models.Episode, error) {
	e := &models.Episode{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, podcast_id, guid, title, COALESCE(description,''), audio_url, duration_seconds, published_at, processing_status, created_at
		 FROM episodes WHERE id = ?`, id).
		Scan(&e.ID, &e.UserID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.AudioURL, &e.DurationSeconds, &e.PublishedAt, &e.ProcessingStatus, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// CreateUpload 记录手动上传的音频元数据（音频文件本身走临时落盘，不持久化）。
func (s *Store) CreateUpload(ctx context.Context, userID, filename, contentType string, size int64) (*models.Upload, error) {
	u := &models.Upload{
		ID: uuid.NewString(), UserID: userID,
		OriginalFilename: filename, ContentType: contentType, SizeBytes: size,
		ProcessingStatus: models.StatusUnprocessed,
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO uploads (id, user_id, original_filename, content_type, size_bytes, processing_status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.UserID, u.OriginalFilename, u.ContentType, u.SizeBytes, string(u.ProcessingStatus))
	if err != nil {
		return nil, fmt.Errorf("创建 upload: %w", err)
	}
	return u, nil
}

func (s *Store) GetUploadByID(ctx context.Context, id string) (*models.Upload, error) {
	u := &models.Upload{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, original_filename, content_type, size_bytes, duration_seconds, processing_status, created_at
		 FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.UserID, &u.OriginalFilename, &u.ContentType, &u.SizeBytes, &u.DurationSeconds, &u.ProcessingStatus, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) ListUploads(ctx context.Context, userID string) ([]*models.Upload, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, original_filename, content_type, size_bytes, duration_seconds, processing_status, created_at
		 FROM uploads WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Upload
	for rows.Next() {
		u := &models.Upload{}
		if err := rows.Scan(&u.ID, &u.UserID, &u.OriginalFilename, &u.ContentType, &u.SizeBytes, &u.DurationSeconds, &u.ProcessingStatus, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
