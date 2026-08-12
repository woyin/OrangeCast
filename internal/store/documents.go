package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// CreatePastedDocument snapshots Owner-provided text as an immutable EvidenceDocument.
func (s *Store) CreatePastedDocument(ctx context.Context, title, content string) (*models.Document, error) {
	title, content = strings.TrimSpace(title), strings.TrimSpace(content)
	if title == "" || content == "" {
		return nil, ErrInvalidEditorialState
	}
	sum := sha256.Sum256([]byte(content))
	id := uuid.NewString()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO documents (id,title,content,content_sha256) VALUES (?,?,?,?)`, id, title, content, hex.EncodeToString(sum[:])); err != nil {
		return nil, err
	}
	return s.GetDocument(ctx, id)
}

func (s *Store) GetDocument(ctx context.Context, id string) (*models.Document, error) {
	d := &models.Document{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,title,origin_kind,origin_url,content,content_sha256,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents WHERE id=?`, id).Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) ListDocuments(ctx context.Context) ([]*models.Document, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,title,origin_kind,origin_url,content,content_sha256,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Document
	for rows.Next() {
		d := &models.Document{}
		if err := rows.Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
