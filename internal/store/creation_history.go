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
