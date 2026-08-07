package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ArtifactKind 产物类型：转录或知识卡片（ADR-0011 不可变版本）。
type ArtifactKind string

const (
	KindTranscript    ArtifactKind = "transcript"
	KindKnowledgeCard ArtifactKind = "knowledge_card"
	KindHighlight     ArtifactKind = "highlight"
)

// CreateArtifactVersion 创建不可变产物版本并返回版本号。
// 版本号 = 该 source+kind 已有最大版本 + 1；并发下由 UNIQUE(source,kind,version) 兜底。
func (s *Store) CreateArtifactVersion(ctx context.Context, sourceType models.SourceType, sourceID string, kind ArtifactKind, providerName, model, promptVersion, jobID, payload string) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var maxVer int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM artifact_versions WHERE source_type=? AND source_id=? AND kind=?`,
		string(sourceType), sourceID, string(kind)).Scan(&maxVer); err != nil {
		return 0, err
	}
	version := maxVer + 1
	_, err = tx.ExecContext(ctx,
		`INSERT INTO artifact_versions (id, source_type, source_id, kind, version, provider, model, prompt_version, job_id, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), string(sourceType), sourceID, string(kind), version, providerName, model, promptVersion, jobID, payload)
	if err != nil {
		return 0, fmt.Errorf("创建产物版本: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

// GetArtifactVersion 读取指定版本。
func (s *Store) GetArtifactVersion(ctx context.Context, sourceType models.SourceType, sourceID string, kind ArtifactKind, version int) (*models.ArtifactVersion, error) {
	a := &models.ArtifactVersion{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, kind, version, provider, model, prompt_version, job_id, payload, created_at
		 FROM artifact_versions WHERE source_type=? AND source_id=? AND kind=? AND version=?`,
		string(sourceType), sourceID, string(kind), version).
		Scan(&a.ID, &a.SourceType, &a.SourceID, &a.Kind, &a.Version, &a.Provider, &a.Model, &a.PromptVersion, &a.JobID, &a.Payload, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListArtifactVersions 列出全部版本（新→旧），供查看与回退。
func (s *Store) ListArtifactVersions(ctx context.Context, sourceType models.SourceType, sourceID string, kind ArtifactKind) ([]*models.ArtifactVersion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, kind, version, provider, model, prompt_version, job_id, payload, created_at
		 FROM artifact_versions WHERE source_type=? AND source_id=? AND kind=?
		 ORDER BY version DESC`,
		string(sourceType), sourceID, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArtifactVersion
	for rows.Next() {
		a := &models.ArtifactVersion{}
		if err := rows.Scan(&a.ID, &a.SourceType, &a.SourceID, &a.Kind, &a.Version, &a.Provider, &a.Model, &a.PromptVersion, &a.JobID, &a.Payload, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetCurrentVersion 把 Source 的当前版本指针指向指定版本（回退/切换）。
func (s *Store) SetCurrentVersion(ctx context.Context, sourceType models.SourceType, sourceID string, kind ArtifactKind, version int) error {
	col := "current_card_version"
	if kind == KindTranscript {
		col = "current_transcript_version"
	}
	table := "episodes"
	if sourceType == models.SourceUpload {
		table = "uploads"
	}
	// 校验版本确实存在，防止指向不存在版本
	if _, err := s.GetArtifactVersion(ctx, sourceType, sourceID, kind, version); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET %s = ? WHERE id = ?`, table, col), version, sourceID)
	return err
}

// GetCurrentVersion 读取 Source 当前采用的版本；未设置返回 ErrNotFound。
func (s *Store) GetCurrentVersion(ctx context.Context, sourceType models.SourceType, sourceID string, kind ArtifactKind) (*models.ArtifactVersion, error) {
	col := "current_card_version"
	if kind == KindTranscript {
		col = "current_transcript_version"
	}
	table := "episodes"
	if sourceType == models.SourceUpload {
		table = "uploads"
	}
	var version sql.NullInt64
	if err := s.DB.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT %s FROM %s WHERE id=?`, col, table), sourceID).Scan(&version); err != nil {
		return nil, err
	}
	if !version.Valid {
		return nil, ErrNotFound
	}
	return s.GetArtifactVersion(ctx, sourceType, sourceID, kind, int(version.Int64))
}
