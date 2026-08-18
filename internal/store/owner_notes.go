package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func validOwnerNoteKind(kind string) bool {
	return kind == "source_note" || kind == "owner_reflection"
}

// CreateOwnerNote keeps source-faithful notes distinct from the Owner's own
// reflections. A source note must resolve to cited segments in that source.
func (s *Store) CreateOwnerNote(ctx context.Context, note models.OwnerNote) (*models.OwnerNote, error) {
	note.ID = uuid.NewString()
	note.SourceType, note.SourceID = strings.TrimSpace(note.SourceType), strings.TrimSpace(note.SourceID)
	note.Kind, note.Content = strings.TrimSpace(note.Kind), strings.TrimSpace(note.Content)
	if !validSourceType(models.SourceType(note.SourceType)) || note.SourceID == "" || !validOwnerNoteKind(note.Kind) || note.Content == "" {
		return nil, fmt.Errorf("%w: invalid owner note", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, models.SourceType(note.SourceType), note.SourceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	if note.CitationsJSON == "" {
		note.CitationsJSON = "[]"
	}
	if note.ReferencesJSON == "" {
		note.ReferencesJSON = "[]"
	}
	var citations []string
	if err := json.Unmarshal([]byte(note.CitationsJSON), &citations); err != nil {
		return nil, fmt.Errorf("%w: invalid note citations", ErrInvalidEditorialState)
	}
	if note.Kind == "source_note" {
		if len(citations) == 0 {
			return nil, fmt.Errorf("%w: source note needs citations", ErrInvalidEditorialState)
		}
		valid, err := s.ValidateSourceCitations(ctx, models.SourceType(note.SourceType), note.SourceID, citations)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: note citation does not resolve inside source", ErrInvalidEditorialState)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO owner_notes (id,source_type,source_id,kind,content,citations_json,references_json) VALUES (?,?,?,?,?,?,?)`, note.ID, note.SourceType, note.SourceID, note.Kind, note.Content, note.CitationsJSON, note.ReferencesJSON); err != nil {
		return nil, err
	}
	return s.GetOwnerNote(ctx, note.ID)
}

// GetOwnerNote retrieves one Owner note by stable identifier.
func (s *Store) GetOwnerNote(ctx context.Context, id string) (*models.OwnerNote, error) {
	note := &models.OwnerNote{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,kind,content,citations_json,references_json,created_at,updated_at FROM owner_notes WHERE id=?`, id).Scan(&note.ID, &note.SourceType, &note.SourceID, &note.Kind, &note.Content, &note.CitationsJSON, &note.ReferencesJSON, &note.CreatedAt, &note.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return note, err
}

// ListOwnerNotes lists the durable notes for exactly one Source.
func (s *Store) ListOwnerNotes(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.OwnerNote, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,kind,content,citations_json,references_json,created_at,updated_at FROM owner_notes WHERE source_type=? AND source_id=? ORDER BY created_at DESC,id DESC`, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.OwnerNote
	for rows.Next() {
		note := &models.OwnerNote{}
		if err := rows.Scan(&note.ID, &note.SourceType, &note.SourceID, &note.Kind, &note.Content, &note.CitationsJSON, &note.ReferencesJSON, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, rows.Err()
}

// ListRightsConstraints lists active and inactive external-reuse restrictions for one Source.
func (s *Store) ListRightsConstraints(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.RightsConstraint, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,constraint_kind,details,active,created_at FROM rights_constraints WHERE source_type=? AND source_id=? ORDER BY active DESC,created_at DESC,id DESC`, string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.RightsConstraint
	for rows.Next() {
		constraint := &models.RightsConstraint{}
		var active int
		if err := rows.Scan(&constraint.ID, &constraint.SourceType, &constraint.SourceID, &constraint.ConstraintKind, &constraint.Details, &active, &constraint.CreatedAt); err != nil {
			return nil, err
		}
		constraint.Active = active != 0
		out = append(out, constraint)
	}
	return out, rows.Err()
}

// UpsertRightsConstraint records restrictions on external reuse without
// revoking the Owner's internal learning or creative use of a Source.
func (s *Store) UpsertRightsConstraint(ctx context.Context, sourceType models.SourceType, sourceID, kind, details string, active bool) error {
	kind, details = strings.TrimSpace(kind), strings.TrimSpace(details)
	if !validSourceType(sourceType) || strings.TrimSpace(sourceID) == "" || kind == "" || details == "" {
		return fmt.Errorf("%w: invalid rights constraint", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, sourceType, sourceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO rights_constraints (id,source_type,source_id,constraint_kind,details,active) VALUES (?,?,?,?,?,?) ON CONFLICT(source_type,source_id,constraint_kind) DO UPDATE SET details=excluded.details,active=excluded.active`, uuid.NewString(), string(sourceType), sourceID, kind, details, boolToInt(active))
	return err
}
