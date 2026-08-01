package store

import (
	"context"

	"github.com/woyin/orangecast/internal/models"
)

// UpdateEpisodeStatus 更新单集处理状态。
func (s *Store) UpdateEpisodeStatus(ctx context.Context, id string, status models.EpisodeProcessingStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE episodes SET processing_status = ? WHERE id = ?`, string(status), id)
	return err
}

func (s *Store) UpdateUploadStatus(ctx context.Context, id string, status models.EpisodeProcessingStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE uploads SET processing_status = ? WHERE id = ?`, string(status), id)
	return err
}
