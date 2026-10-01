package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// QuestionStudySession has its own revision; editing a question does not rewrite
// the identity or accepted history of an existing session.
type QuestionStudySession struct {
	ID, QuestionID, CreatedAt, UpdatedAt string
	Revision                             int
}
type QuestionStudySource struct{ SourceType, SourceID string }

type QuestionStudyTurn struct {
	ID, SessionID, RequestKey, PayloadHash, State, OwnerInput, FrozenJSON, AcceptedJSON string
	GenerationJobID, CheckJobID, CreatedAt, UpdatedAt                                   string
	Ordinal                                                                             int
	Purged                                                                              bool
}

func (s *Store) StartQuestionStudySession(ctx context.Context, questionID, key string) (*QuestionStudySession, error) {
	if _, err := uuid.Parse(key); err != nil {
		return nil, ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id, existingQuestion string
	err = tx.QueryRowContext(ctx, `SELECT id,question_id FROM question_study_sessions WHERE request_key=?`, key).Scan(&id, &existingQuestion)
	if err == nil {
		if existingQuestion != questionID {
			return nil, ErrConflict
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	} else {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM learning_questions WHERE id=?`, questionID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
		id = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO question_study_sessions(id,question_id,request_key)VALUES(?,?,?)`, id, questionID, key); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetQuestionStudySession(ctx, id)
}
func (s *Store) GetQuestionStudySession(ctx context.Context, id string) (*QuestionStudySession, error) {
	out := &QuestionStudySession{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,question_id,revision,created_at,updated_at FROM question_study_sessions WHERE id=?`, id).Scan(&out.ID, &out.QuestionID, &out.Revision, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

const questionStudyTurnColumns = `id,session_id,ordinal,request_key,payload_hash,state,owner_input,frozen_json,accepted_json,COALESCE(generation_job_id,''),COALESCE(check_job_id,''),purged,created_at,updated_at`

func scanQuestionStudyTurn(row interface{ Scan(...any) error }) (*QuestionStudyTurn, error) {
	out := &QuestionStudyTurn{}
	err := row.Scan(&out.ID, &out.SessionID, &out.Ordinal, &out.RequestKey, &out.PayloadHash, &out.State, &out.OwnerInput, &out.FrozenJSON, &out.AcceptedJSON, &out.GenerationJobID, &out.CheckJobID, &out.Purged, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}
func (s *Store) GetQuestionStudyTurn(ctx context.Context, id string) (*QuestionStudyTurn, error) {
	return scanQuestionStudyTurn(s.DB.QueryRowContext(ctx, `SELECT `+questionStudyTurnColumns+` FROM question_study_turns WHERE id=?`, id))
}

// reserveQuestionStudyTurn runs inside the eventual admission transaction. The
// caller supplies already-validated frozen scope; this is not an HTTP command.
func reserveQuestionStudyTurn(ctx context.Context, tx *sql.Tx, sessionID string, expected int, input, key, frozen string, sources []QuestionStudySource) (*QuestionStudyTurn, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 || !utf8.ValidString(input) || strings.TrimSpace(input) == "" || len(input) > 8192 || !json.Valid([]byte(frozen)) || len(frozen) > 128*1024 || len(sources) > 8 {
		return nil, false, ErrInvalidEditorialState
	}
	payload, _ := json.Marshal([]any{sessionID, expected, input, frozen, sources})
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	previous, err := scanQuestionStudyTurn(tx.QueryRowContext(ctx, `SELECT `+questionStudyTurnColumns+` FROM question_study_turns WHERE request_key=?`, key))
	if err == nil {
		if previous.PayloadHash != hash {
			return nil, false, ErrConflict
		}
		return previous, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	var revision, count, pending int
	err = tx.QueryRowContext(ctx, `SELECT revision,(SELECT COUNT(*) FROM question_study_turns WHERE session_id=s.id),(SELECT COUNT(*) FROM question_study_turns WHERE session_id=s.id AND purged=0 AND state IN('queued','running','response_saved','checking','unknown','blocked')) FROM question_study_sessions s WHERE id=?`, sessionID).Scan(&revision, &count, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if revision != expected || count >= 12 || pending > 0 {
		return nil, false, ErrConflict
	}
	seen := map[QuestionStudySource]bool{}
	for _, source := range sources {
		table := ""
		switch source.SourceType {
		case "episode":
			table = "episodes"
		case "upload":
			table = "uploads"
		case "document":
			table = "documents"
		default:
			return nil, false, ErrInvalidEditorialState
		}
		if seen[source] || source.SourceID == "" {
			return nil, false, ErrInvalidEditorialState
		}
		seen[source] = true
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id=? AND archived_at IS NULL`, source.SourceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		} else if err != nil {
			return nil, false, err
		}
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO question_study_turns(id,session_id,ordinal,request_key,payload_hash,owner_input,frozen_json)VALUES(?,?,?,?,?,?,?)`, id, sessionID, count+1, key, hash, input, frozen); err != nil {
		return nil, false, err
	}
	for _, source := range sources {
		if _, err = tx.ExecContext(ctx, `INSERT INTO question_study_sources(turn_id,source_type,source_id)VALUES(?,?,?)`, id, source.SourceType, source.SourceID); err != nil {
			return nil, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=? AND revision=?`, sessionID, expected); err != nil {
		return nil, false, err
	}
	out, err := scanQuestionStudyTurn(tx.QueryRowContext(ctx, `SELECT `+questionStudyTurnColumns+` FROM question_study_turns WHERE id=?`, id))
	return out, true, err
}

// Accepted history never exposes generation/checkpoint bodies or unreviewed text.
func (s *Store) QuestionStudyHistory(ctx context.Context, sessionID string) ([]QuestionStudyTurn, error) {
	if _, err := s.GetQuestionStudySession(ctx, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+questionStudyTurnColumns+` FROM question_study_turns WHERE session_id=? AND state='accepted' AND purged=0 ORDER BY ordinal LIMIT 12`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuestionStudyTurn{}
	for rows.Next() {
		turn, err := scanQuestionStudyTurn(rows)
		if err != nil {
			return nil, err
		}
		turn.FrozenJSON = ""
		out = append(out, *turn)
	}
	return out, rows.Err()
}
