package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func validCreationHistoryStatus(status string) bool {
	return status == "published" || status == "unpublished"
}

// CreateCreationHistory imports a published or unpublished work for duplicate detection.
func (s *Store) CreateCreationHistory(ctx context.Context, work models.CreationHistory) (*models.CreationHistory, error) {
	work.ID = uuid.NewString()
	work.Status = strings.TrimSpace(work.Status)
	work.Title = strings.TrimSpace(work.Title)
	work.CreationForm = strings.TrimSpace(work.CreationForm)
	if work.CreationForm == "" {
		work.CreationForm = "article"
	}
	if work.EditorialProfileID == "" || work.Title == "" || !validCreationHistoryStatus(work.Status) {
		return nil, fmt.Errorf("%w: invalid creation history", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, work.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,core_claim,audience,content,source_url) VALUES (?,?,?,?,?,?,?,?,?)`, work.ID, work.EditorialProfileID, work.Status, work.CreationForm, work.Title, work.CoreClaim, work.Audience, work.Content, work.SourceURL)
	if err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, work.ID)
}

// GetCreationHistory retrieves imported or internal history.
func (s *Store) GetCreationHistory(ctx context.Context, id string) (*models.CreationHistory, error) {
	work := &models.CreationHistory{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,status,creation_form,title,core_claim,audience,content,source_url,created_at,updated_at FROM creation_history WHERE id=?`, id).Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return work, err
}

// ListCreationHistory lists a profile's durable historical work, newest first.
func (s *Store) ListCreationHistory(ctx context.Context, profileID string) ([]*models.CreationHistory, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,status,creation_form,title,core_claim,audience,content,source_url,created_at,updated_at FROM creation_history WHERE editorial_profile_id=? ORDER BY created_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.CreationHistory{}
	for rows.Next() {
		work := &models.CreationHistory{}
		if err := rows.Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// FindCreationHistoryCandidates performs a transparent lexical prefilter. It is
// only a reminder when history lacks full content; hard-duplicate decisions stay Owner-reviewable.
func (s *Store) FindCreationHistoryCandidates(ctx context.Context, profileID, claim string) ([]*models.CreationHistory, error) {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return nil, nil
	}
	terms := strings.Fields(claim)
	if len(terms) == 0 {
		terms = []string{claim}
	}
	like := "%" + terms[0] + "%"
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,status,creation_form,title,core_claim,audience,content,source_url,created_at,updated_at FROM creation_history WHERE editorial_profile_id=? AND (core_claim LIKE ? OR title LIKE ? OR content LIKE ?) ORDER BY created_at DESC`, profileID, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.CreationHistory{}
	for rows.Next() {
		work := &models.CreationHistory{}
		if err := rows.Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// RecordDigestHistory 将确切精读修订登记为创作历史（G08 / ADR-0024 §5）：
//   - 无专门画像时复用默认画像，不创建品牌副本；
//   - status 只能是 published（Owner 明确登记发布）或 unpublished；
//   - digest_id 唯一：同修订重复登记幂等返回既有记录（导出/预览不调用本方法）；
//   - digest_content 复制登记时正文——来源删除后历史身份与已登记文本仍可审计
//     （依据有效性由来源快照的审计状态说明，不私存被删原文）。
func (s *Store) RecordDigestHistory(ctx context.Context, digestID, status string) (*models.CreationHistory, error) {
	if status != "published" && status != "unpublished" {
		return nil, fmt.Errorf("%w: invalid history status %q", ErrInvalidEditorialState, status)
	}
	d, err := s.GetEpisodeDigest(ctx, digestID)
	if err != nil {
		return nil, err
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		return nil, err
	}
	// 幂等：同修订已有记录直接返回。
	var existingID string
	err = s.DB.QueryRowContext(ctx,
		`SELECT id FROM creation_history WHERE digest_id=?`, digestID).Scan(&existingID)
	if err == nil {
		return s.GetCreationHistory(ctx, existingID)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	blocks, err := s.ListDigestBlocks(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	for _, b := range blocks {
		body.WriteString(b.Text + "\n\n")
	}
	work := models.CreationHistory{
		EditorialProfileID: profile.ID, Status: status, CreationForm: "episode_digest",
		Title: d.Title, Content: body.String(),
	}
	created, err := s.CreateCreationHistory(ctx, work)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE creation_history SET digest_id=?, digest_content=? WHERE id=?`,
		d.ID, body.String(), created.ID); err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, created.ID)
}
