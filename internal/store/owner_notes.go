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
	var citations, references []string
	if err := json.Unmarshal([]byte(note.CitationsJSON), &citations); err != nil {
		return nil, fmt.Errorf("%w: invalid note citations", ErrInvalidEditorialState)
	}
	if err := json.Unmarshal([]byte(note.ReferencesJSON), &references); err != nil {
		return nil, fmt.Errorf("%w: invalid note references", ErrInvalidEditorialState)
	}
	if note.Kind == "source_note" {
		if len(citations) == 0 || len(references) > 0 {
			return nil, fmt.Errorf("%w: source note needs citations", ErrInvalidEditorialState)
		}
		valid, err := s.ValidateSourceCitations(ctx, models.SourceType(note.SourceType), note.SourceID, citations)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: note citation does not resolve inside source", ErrInvalidEditorialState)
		}
	} else {
		// K04：个人理解不得伪装为 Citation 支持的来源主张；Reference 若提供必须可解析。
		if len(citations) > 0 {
			return nil, fmt.Errorf("%w: owner reflection must not claim citations", ErrInvalidEditorialState)
		}
		if len(references) > 0 {
			valid, err := s.validateNoteReferences(ctx, &note, references)
			if err != nil {
				return nil, err
			}
			if !valid {
				return nil, fmt.Errorf("%w: note reference does not resolve inside source", ErrInvalidEditorialState)
			}
		}
	}
	anchor, err := s.prepareNoteAnchor(ctx, &note, citations, references)
	if err != nil {
		return nil, err
	}
	note.AnchorJSON = anchor
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO owner_notes (id,source_type,source_id,kind,content,citations_json,references_json,anchor_json) VALUES (?,?,?,?,?,?,?,?)`, note.ID, note.SourceType, note.SourceID, note.Kind, note.Content, note.CitationsJSON, note.ReferencesJSON, note.AnchorJSON); err != nil {
		return nil, err
	}
	return s.GetOwnerNote(ctx, note.ID)
}

// GetOwnerNote retrieves one Owner note by stable identifier.
func (s *Store) GetOwnerNote(ctx context.Context, id string) (*models.OwnerNote, error) {
	note := &models.OwnerNote{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,kind,content,citations_json,references_json,revision,created_at,updated_at,anchor_json FROM owner_notes WHERE id=?`, id).Scan(&note.ID, &note.SourceType, &note.SourceID, &note.Kind, &note.Content, &note.CitationsJSON, &note.ReferencesJSON, &note.Revision, &note.CreatedAt, &note.UpdatedAt, &note.AnchorJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return note, err
}

// ListOwnerNotes lists the durable notes for exactly one Source.
func (s *Store) ListOwnerNotes(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.OwnerNote, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,kind,content,citations_json,references_json,revision,created_at,updated_at,anchor_json FROM owner_notes WHERE source_type=? AND source_id=? ORDER BY created_at DESC,id DESC`, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.OwnerNote
	for rows.Next() {
		note := &models.OwnerNote{}
		if err := rows.Scan(&note.ID, &note.SourceType, &note.SourceID, &note.Kind, &note.Content, &note.CitationsJSON, &note.ReferencesJSON, &note.Revision, &note.CreatedAt, &note.UpdatedAt, &note.AnchorJSON); err != nil {
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

// UpdateOwnerNote 编辑笔记（K04）：乐观并发控制——expectedRevision 不匹配返回
// ErrConflict（过期编辑提示冲突）；kind 纪律与创建一致：SourceNote 只能 Citation、
// OwnerReflection 只能 Reference（个人理解不伪装为来源主张）。
func (s *Store) UpdateOwnerNote(ctx context.Context, noteID, content, citationsJSON, referencesJSON string, expectedRevision int) (*models.OwnerNote, error) {
	content = strings.TrimSpace(content)
	if content == "" || expectedRevision < 1 {
		return nil, fmt.Errorf("%w: invalid owner note update", ErrInvalidEditorialState)
	}
	current, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		return nil, err
	}
	if citationsJSON == "" {
		citationsJSON = "[]"
	}
	if referencesJSON == "" {
		referencesJSON = "[]"
	}
	var citations, references []string
	if err := json.Unmarshal([]byte(citationsJSON), &citations); err != nil {
		return nil, fmt.Errorf("%w: invalid note citations", ErrInvalidEditorialState)
	}
	if err := json.Unmarshal([]byte(referencesJSON), &references); err != nil {
		return nil, fmt.Errorf("%w: invalid note references", ErrInvalidEditorialState)
	}
	switch current.Kind {
	case "source_note":
		if len(citations) == 0 || len(references) > 0 {
			return nil, fmt.Errorf("%w: source note needs citations", ErrInvalidEditorialState)
		}
		valid, err := s.validateNoteReferences(ctx, current, citations)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: note citation does not resolve inside source", ErrInvalidEditorialState)
		}
	case "owner_reflection":
		if len(citations) > 0 {
			return nil, fmt.Errorf("%w: owner reflection must not claim citations", ErrInvalidEditorialState)
		}
		if len(references) > 0 {
			valid, err := s.validateNoteReferences(ctx, current, references)
			if err != nil {
				return nil, err
			}
			if !valid {
				return nil, fmt.Errorf("%w: note reference does not resolve inside source", ErrInvalidEditorialState)
			}
		}
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE owner_notes SET content=?, citations_json=?, references_json=?, revision=revision+1, updated_at=datetime('now')
		 WHERE id=? AND revision=?`,
		content, citationsJSON, referencesJSON, noteID, expectedRevision)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrConflict
	}
	return s.GetOwnerNote(ctx, noteID)
}

// DeleteOwnerNote 删除一条个人笔记（R23 挂账补齐）：
//   - 乐观并发：expectedRevision 不匹配当前版本返回 ErrConflict（K04 同契约）；
//   - 已被精读块引用的笔记属于已发布修订的血缘（digest_blocks.note_id），删除会
//     破坏精读笔记锚点与 FindUsageByNote 导航，明确拒绝；
//   - 笔记不属于任何来源主张体系，删除不需要级联 KeyPoint/来源。
func (s *Store) DeleteOwnerNote(ctx context.Context, noteID string, expectedRevision int) error {
	if expectedRevision < 1 {
		return fmt.Errorf("%w: invalid owner note delete", ErrInvalidEditorialState)
	}
	current, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return ErrConflict
	}
	var referenced int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM digest_blocks WHERE note_id=?`, noteID).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return fmt.Errorf("%w: 笔记已被 %d 个精读块引用，不能删除", ErrConflict, referenced)
	}
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM owner_notes WHERE id=? AND revision=?`, noteID, expectedRevision)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrConflict
	}
	return nil
}
