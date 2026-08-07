package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// StudySessionRow 学习会话容器（GeneratedDerivative，ADR-0018 R3）。
type StudySessionRow struct {
	ID         string
	SourceType models.SourceType
	SourceID   string
	Title      string
	CreatedAt  string
	UpdatedAt  string
}

// StudyMessageRow 学习会话内一轮消息。
type StudyMessageRow struct {
	ID                  string
	SessionID           string
	Role                string // user | assistant
	Content             string
	ReferenceSegmentIDs []string
	RelationKind        models.RelationKind
	Suppressed          bool
	CreatedAt           string
}

// CreateStudySession 新建一个学习会话。
func (s *Store) CreateStudySession(ctx context.Context, sourceType models.SourceType, sourceID, title string) (*StudySessionRow, error) {
	id := uuid.NewString()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO study_sessions (id, source_type, source_id, title) VALUES (?, ?, ?, ?)`,
		id, string(sourceType), sourceID, title); err != nil {
		return nil, fmt.Errorf("创建 study_session: %w", err)
	}
	return s.GetStudySession(ctx, id)
}

// GetStudySession 读取会话。
func (s *Store) GetStudySession(ctx context.Context, id string) (*StudySessionRow, error) {
	r := &StudySessionRow{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, title, created_at, updated_at FROM study_sessions WHERE id=?`, id).
		Scan(&r.ID, &r.SourceType, &r.SourceID, &r.Title, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListStudySessions 列出某 Source 的会话（最近在前）。
func (s *Store) ListStudySessions(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*StudySessionRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, title, created_at, updated_at
		 FROM study_sessions WHERE source_type=? AND source_id=? ORDER BY created_at DESC, rowid DESC`,
		string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*StudySessionRow
	for rows.Next() {
		r := &StudySessionRow{}
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.Title, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppendStudyMessage 向会话追加一轮消息（assistant 的 ReferenceSegmentIDs 通过 Reference 关联 Segment）。
func (s *Store) AppendStudyMessage(ctx context.Context, sessionID, role, content string, references []string, suppressed bool) (*StudyMessageRow, error) {
	id := uuid.NewString()
	refJSON, _ := json.Marshal(references)
	rk := models.RelationReference
	if role == "user" {
		rk = "" // user 消息无 Reference；列默认 'reference' 仅对 assistant 有意义
	}
	if rk == "" {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO study_messages (id, session_id, role, content, suppressed) VALUES (?, ?, ?, ?, ?)`,
			id, sessionID, role, content, boolToInt(suppressed)); err != nil {
			return nil, fmt.Errorf("写入 study_message: %w", err)
		}
	} else {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO study_messages (id, session_id, role, content, reference_segment_ids, relation_kind, suppressed) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, sessionID, role, content, string(refJSON), string(rk), boolToInt(suppressed)); err != nil {
			return nil, fmt.Errorf("写入 study_message: %w", err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE study_sessions SET updated_at = datetime('now') WHERE id = ?`, sessionID); err != nil {
		return nil, err
	}
	return s.GetStudyMessage(ctx, id)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetStudyMessage 读取单条消息。
func (s *Store) GetStudyMessage(ctx context.Context, id string) (*StudyMessageRow, error) {
	r := &StudyMessageRow{}
	var refJSON string
	var rk string
	var supp int
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, role, content, reference_segment_ids, relation_kind, suppressed, created_at FROM study_messages WHERE id=?`, id).
		Scan(&r.ID, &r.SessionID, &r.Role, &r.Content, &refJSON, &rk, &supp, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(refJSON), &r.ReferenceSegmentIDs)
	r.RelationKind = models.RelationKind(rk)
	r.Suppressed = supp == 1
	return r, nil
}

// ListStudyMessages 列出会话内全部消息（按时间升序，用于回放对话）。
// 默认只返回未被抑制的消息（呈现给 Owner）；includeSuppressed=true 时也返回被抑制的（调试/评测用）。
func (s *Store) ListStudyMessages(ctx context.Context, sessionID string, includeSuppressed bool) ([]*StudyMessageRow, error) {
	q := `SELECT id, session_id, role, content, reference_segment_ids, relation_kind, suppressed, created_at
	      FROM study_messages WHERE session_id=?`
	args := []any{sessionID}
	if !includeSuppressed {
		q += ` AND suppressed = 0`
	}
	q += ` ORDER BY created_at, rowid`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*StudyMessageRow
	for rows.Next() {
		r := &StudyMessageRow{}
		var refJSON string
		var rk string
		var supp int
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Role, &r.Content, &refJSON, &rk, &supp, &r.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(refJSON), &r.ReferenceSegmentIDs)
		r.RelationKind = models.RelationKind(rk)
		r.Suppressed = supp == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteStudySession 删除一个会话（及其全部消息，ON DELETE CASCADE）。
func (s *Store) DeleteStudySession(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM study_sessions WHERE id=?`, id)
	return err
}

// DeleteStudySessionsForSource Purge 时删除该 Source 的全部 StudySession。
func (s *Store) DeleteStudySessionsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM study_sessions WHERE source_type=? AND source_id=?`, string(sourceType), sourceID)
	return err
}
