// highlight_clips.go 高光片段真实音频区间仓储（学习补齐：可播放、可分发的片段）。
// 区间起止由源音频与 Segment 时间计算，AI 不得自估；同一源音频与区间只缓存一份，
// 第二次请求直接命中，不再调用 ffmpeg。文件落在独立目录，与 EvidenceAudio 物理隔离。
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// GetHighlightClip 按源音频身份与区间读取已缓存的片段。
func (s *Store) GetHighlightClip(ctx context.Context, audioSHA256 string, start, end float64, format string) (*models.HighlightClip, error) {
	c := &models.HighlightClip{}
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, audio_sha256, start_seconds, end_seconds, format, rel_path, bytes, status, created_at "+
			"FROM highlight_clips WHERE audio_sha256=? AND start_seconds=? AND end_seconds=? AND format=?",
		audioSHA256, start, end, format).
		Scan(&c.ID, &c.AudioSHA256, &c.Start, &c.End, &c.Format, &c.RelPath, &c.Bytes, &c.Status, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CreateHighlightClip 记录一条已生成的片段；同键并发写入命中已有行时返回已有记录。
func (s *Store) CreateHighlightClip(ctx context.Context, audioSHA256 string, start, end float64, format, relPath string, bytes int64) (*models.HighlightClip, error) {
	if _, err := s.DB.ExecContext(ctx,
		"INSERT OR IGNORE INTO highlight_clips (id, audio_sha256, start_seconds, end_seconds, format, rel_path, bytes) "+
			"VALUES (?,?,?,?,?,?,?)",
		uuid.NewString(), audioSHA256, start, end, format, relPath, bytes); err != nil {
		return nil, err
	}
	return s.GetHighlightClip(ctx, audioSHA256, start, end, format)
}

// DeleteHighlightClipsForSource 删除某来源音频下的全部片段记录（Purge 时使用）。
func (s *Store) DeleteHighlightClipsForSource(ctx context.Context, audioSHA256 string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT rel_path FROM highlight_clips WHERE audio_sha256=?", audioSHA256)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM highlight_clips WHERE audio_sha256=?", audioSHA256); err != nil {
		return nil, err
	}
	return paths, nil
}
