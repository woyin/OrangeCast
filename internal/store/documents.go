package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const maxDocumentContentBytes = 4 << 20

// CreatePastedDocument snapshots Owner-provided text as an immutable EvidenceDocument.
func (s *Store) CreatePastedDocument(ctx context.Context, title, content string) (*models.Document, error) {
	return s.createDocument(ctx, title, "pasted", "", content)
}

// CreateWebDocument persists the fetched text, never a live webpage reference.
func (s *Store) CreateWebDocument(ctx context.Context, title, sourceURL, content string) (*models.Document, error) {
	return s.createDocument(ctx, title, "url", sourceURL, content)
}

func (s *Store) CreatePDFDocument(ctx context.Context, title, filename, content string) (*models.Document, error) {
	return s.createDocument(ctx, title, "pdf", filename, content)
}

func (s *Store) createDocument(ctx context.Context, title, originKind, originURL, content string) (*models.Document, error) {
	return s.createDocumentVersion(ctx, "", 1, title, originKind, originURL, content)
}

func (s *Store) createDocumentVersion(ctx context.Context, seriesID string, version int, title, originKind, originURL, content string) (*models.Document, error) {
	title, content = strings.TrimSpace(title), strings.TrimSpace(content)
	if title == "" || content == "" || len(content) > maxDocumentContentBytes {
		return nil, ErrInvalidEditorialState
	}
	sum := sha256.Sum256([]byte(content))
	id := uuid.NewString()
	if seriesID == "" {
		seriesID = id
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents (id,title,origin_kind,origin_url,content,content_sha256,series_id,version) VALUES (?,?,?,?,?,?,?,?)`, id, title, originKind, originURL, content, hex.EncodeToString(sum[:]), seriesID, version); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_search(document_id,title,content) VALUES (?,?,?)`, id, title, content); err != nil {
		return nil, err
	}
	document := &models.Document{ID: id, Title: title, Content: content}
	card := documentKnowledgeCard(document)
	payload, _ := json.Marshal(card)
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_knowledge_cards(document_id,payload) VALUES (?,?)`, id, string(payload)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDocument(ctx, id)
}

// CreateDocumentVersion creates a new immutable snapshot in the same logical
// series; existing Citation anchors continue to point at the older snapshot.
func (s *Store) CreateDocumentVersion(ctx context.Context, documentID, title, content string) (*models.Document, error) {
	previous, err := s.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}
	var next int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM documents WHERE series_id=?`, previous.SeriesID).Scan(&next); err != nil {
		return nil, err
	}
	return s.createDocumentVersion(ctx, previous.SeriesID, next, title, "revision", previous.OriginURL, content)
}

func (s *Store) GetDocument(ctx context.Context, id string) (*models.Document, error) {
	d := &models.Document{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,title,origin_kind,origin_url,content,content_sha256,series_id,version,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents WHERE id=?`, id).Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.SeriesID, &d.Version, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) ListDocuments(ctx context.Context) ([]*models.Document, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,title,origin_kind,origin_url,content,content_sha256,series_id,version,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Document
	for rows.Next() {
		d := &models.Document{}
		if err := rows.Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.SeriesID, &d.Version, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ListDocumentVersions(ctx context.Context, seriesID string) ([]*models.Document, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,title,origin_kind,origin_url,content,content_sha256,series_id,version,production_use,model_data_policy,archived_at,created_at,updated_at FROM documents WHERE series_id=? ORDER BY version DESC`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Document
	for rows.Next() {
		d := &models.Document{}
		if err := rows.Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.SeriesID, &d.Version, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDocumentKnowledgeCard(ctx context.Context, documentID string) (*provider.KnowledgeCard, error) {
	var payload string
	if err := s.DB.QueryRowContext(ctx, `SELECT payload FROM document_knowledge_cards WHERE document_id=?`, documentID).Scan(&payload); err != nil {
		return nil, err
	}
	var card provider.KnowledgeCard
	if err := json.Unmarshal([]byte(payload), &card); err != nil {
		return nil, err
	}
	return &card, nil
}

func documentKnowledgeCard(document *models.Document) *provider.KnowledgeCard {
	segments := DocumentSegments(document)
	card := &provider.KnowledgeCard{Title: document.Title}
	if len(segments) > 0 {
		card.Summary = provider.CitedText{Text: truncateDocumentText(segments[0].Text, 240), Citations: []string{segments[0].ID}}
	}
	for _, segment := range segments {
		if len(card.KeyPoints) == 8 {
			break
		}
		card.KeyPoints = append(card.KeyPoints, provider.KeyPoint{Content: truncateDocumentText(segment.Text, 160), Citations: []string{segment.ID}})
	}
	return card
}

func truncateDocumentText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func (s *Store) SearchDocuments(ctx context.Context, query string, limit int) ([]*models.Document, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.id,d.title,d.origin_kind,d.origin_url,d.content,d.content_sha256,d.series_id,d.version,d.production_use,d.model_data_policy,d.archived_at,d.created_at,d.updated_at FROM document_search ds JOIN documents d ON d.id=ds.document_id WHERE document_search MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Document
	for rows.Next() {
		d := &models.Document{}
		if err := rows.Scan(&d.ID, &d.Title, &d.OriginKind, &d.OriginURL, &d.Content, &d.ContentSHA256, &d.SeriesID, &d.Version, &d.ProductionUse, &d.ModelDataPolicy, &d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DocumentSegments deterministically derives citation anchors from paragraph order.
// Because Document content never mutates, ids remain stable for its whole lifetime.
func DocumentSegments(doc *models.Document) []models.DocumentSegment {
	parts := strings.Split(strings.ReplaceAll(doc.Content, "\r\n", "\n"), "\n\n")
	out := make([]models.DocumentSegment, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, models.DocumentSegment{ID: doc.ID + "-p" + fmt.Sprintf("%04d", len(out)+1), Position: len(out) + 1, Text: text})
		}
	}
	return out
}
