package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ListeningCapture belongs to the playing source, regardless of the viewed page.
// Authentication material is deliberately absent from this persisted value.
type ListeningCapture struct {
	SourceType string            `json:"sourceType"`
	SourceID   string            `json:"sourceId"`
	Title      string            `json:"title"`
	Anchor     models.NoteAnchor `json:"anchor"`
}

type ReflectionAnswers struct {
	Remember  string `json:"remember"`
	Uncertain string `json:"uncertain"`
	Apply     string `json:"apply"`
}

func (a ReflectionAnswers) normalized() (ReflectionAnswers, error) {
	a.Remember, a.Uncertain, a.Apply = strings.TrimSpace(a.Remember), strings.TrimSpace(a.Uncertain), strings.TrimSpace(a.Apply)
	if len(a.Remember)+len(a.Uncertain)+len(a.Apply) > 40000 {
		return a, ErrInvalidEditorialState
	}
	return a, nil
}

// Content is deterministic and records Owner understanding, never source claims.
func (a ReflectionAnswers) Content() string {
	var parts []string
	for _, p := range []struct{ label, text string }{{"我记住了什么", a.Remember}, {"我还不确定什么", a.Uncertain}, {"我准备如何使用", a.Apply}} {
		if p.text != "" {
			parts = append(parts, p.label+"\n"+p.text)
		}
	}
	return strings.Join(parts, "\n\n")
}

type ListeningReflection struct {
	ID               string            `json:"id"`
	Capture          ListeningCapture  `json:"capture"`
	Answers          ReflectionAnswers `json:"answers"`
	QuestionID       string            `json:"question_id"`
	QuestionRevision int               `json:"question_revision"`
	Revision         int               `json:"revision"`
	State            string            `json:"state"`
	SavedNoteID      string            `json:"saved_note_id"`
	ExpiresAt        string            `json:"expires_at"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

const reflectionColumns = `id,capture_json,answers_json,question_id,question_revision,revision,state,saved_note_id,expires_at,created_at,updated_at`

func scanReflection(row interface{ Scan(...any) error }) (*ListeningReflection, error) {
	r := &ListeningReflection{}
	var capture, answers string
	err := row.Scan(&r.ID, &capture, &answers, &r.QuestionID, &r.QuestionRevision, &r.Revision, &r.State, &r.SavedNoteID, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(capture), &r.Capture); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(answers), &r.Answers); err != nil {
		return nil, err
	}
	if r.State == "draft" && r.ExpiresAt <= time.Now().UTC().Format("2006-01-02 15:04:05") {
		r.State = "expired"
		r.Capture = ListeningCapture{}
		r.Answers = ReflectionAnswers{}
	}
	return r, nil
}

func reflectionHash(v any) string {
	b, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (s *Store) GetListeningReflection(ctx context.Context, id string) (*ListeningReflection, error) {
	return scanReflection(s.DB.QueryRowContext(ctx, `SELECT `+reflectionColumns+` FROM listening_reflections WHERE id=?`, id))
}

// ListListeningReflections is private history; drafts never enter material selection.
func (s *Store) ListListeningReflections(ctx context.Context) ([]*ListeningReflection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+reflectionColumns+` FROM listening_reflections WHERE state IN('draft','saved') ORDER BY updated_at DESC,id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ListeningReflection{}
	for rows.Next() {
		r, e := scanReflection(rows)
		if e != nil {
			return nil, e
		}
		if r.State != "expired" {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// StartListeningReflection binds the client UUID to immutable playback and question identity.
func (s *Store) StartListeningReflection(ctx context.Context, id string, c ListeningCapture, question string, questionRevision int) (*ListeningReflection, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrInvalidEditorialState
	}
	if (question == "") != (questionRevision == 0) || len(c.Title) > 2000 || len(c.Anchor.SegmentIDs) > 100 {
		return nil, ErrInvalidEditorialState
	}
	hash := reflectionHash(struct {
		Capture  ListeningCapture
		Question string
		Revision int
	}{c, question, questionRevision})
	var oldHash string
	err := s.DB.QueryRowContext(ctx, `SELECT start_hash FROM listening_reflections WHERE id=?`, id).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			return nil, ErrConflict
		}
		return s.GetListeningReflection(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if (c.SourceType != "episode" && c.SourceType != "upload") || c.Anchor.NoPosition || c.Anchor.SnapshotID == "" || c.Anchor.Version < 1 || c.Anchor.Mode == "" {
		return nil, ErrInvalidEditorialState
	}
	anchor, _ := json.Marshal(c.Anchor)
	refs, _ := json.Marshal(c.Anchor.SegmentIDs)
	note, err := s.prepareOwnerNote(ctx, models.OwnerNote{SourceType: c.SourceType, SourceID: c.SourceID, Kind: "owner_reflection", Content: "听后整理草稿", AnchorJSON: string(anchor), ReferencesJSON: string(refs)})
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(note.AnchorJSON), &c.Anchor); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(c)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT start_hash FROM listening_reflections WHERE id=?`, id).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			return nil, ErrConflict
		}
		return scanReflection(tx.QueryRowContext(ctx, `SELECT `+reflectionColumns+` FROM listening_reflections WHERE id=?`, id))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = checkReflectionCapture(ctx, tx, c); err != nil {
		return nil, err
	}
	if err = checkReflectionQuestion(ctx, tx, question, questionRevision); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM listening_reflections WHERE state='draft' AND expires_at>datetime('now')`).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 20 {
		return nil, ErrInvalidEditorialState
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO listening_reflections(id,source_type,source_id,capture_json,question_id,question_revision,start_hash)VALUES(?,?,?,?,?,?,?)`, id, c.SourceType, c.SourceID, string(raw), question, questionRevision, hash)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetListeningReflection(ctx, id)
}

func checkReflectionCapture(ctx context.Context, tx *sql.Tx, c ListeningCapture) error {
	if err := checkQuestionSource(ctx, tx, c.SourceType, c.SourceID); err != nil {
		return err
	}
	var status, sha string
	err := tx.QueryRowContext(ctx, `SELECT status,audio_sha256 FROM source_snapshots WHERE id=? AND source_type=? AND source_id=? AND content_version=?`, c.Anchor.SnapshotID, c.SourceType, c.SourceID, c.Anchor.Version).Scan(&status, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "active" || (c.Anchor.AudioSHA256 != "" && sha != c.Anchor.AudioSHA256) {
		return ErrConflict
	}
	if c.Anchor.AudioSHA256 != "" {
		var currentSHA, currentStatus string
		err = tx.QueryRowContext(ctx, `SELECT sha256,status FROM evidence_audio WHERE source_type=? AND source_id=?`, c.SourceType, c.SourceID).Scan(&currentSHA, &currentStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if currentSHA != c.Anchor.AudioSHA256 || currentStatus != "ready" {
			return ErrConflict
		}
	}
	return nil
}

func checkReflectionQuestion(ctx context.Context, tx *sql.Tx, id string, revision int) error {
	if id == "" {
		return nil
	}
	q, err := scanLearningQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE id=?`, id))
	if err != nil {
		return err
	}
	if q.Revision != revision || q.Status != "active" {
		return ErrConflict
	}
	return nil
}

// ChangeListeningReflection edits or cancels a private draft with payload-bound replay.
func (s *Store) ChangeListeningReflection(ctx context.Context, id, key, action string, expected int, answers ReflectionAnswers) (*ListeningReflection, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 || (action != "edit" && action != "cancel") {
		return nil, ErrInvalidEditorialState
	}
	a, err := answers.normalized()
	if err != nil {
		return nil, err
	}
	hash := reflectionHash(struct {
		ID, Action string
		Revision   int
		Answers    ReflectionAnswers
	}{id, action, expected, a})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if old, e := reflectionReplay(ctx, tx, key, id, hash); old != nil || e != nil {
		return old, e
	}
	r, err := scanReflection(tx.QueryRowContext(ctx, `SELECT `+reflectionColumns+` FROM listening_reflections WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if r.Revision != expected || r.State != "draft" {
		return nil, ErrConflict
	}
	r.Answers = a
	r.Revision++
	if action == "cancel" {
		r.State = "cancelled"
		r.Answers = ReflectionAnswers{}
		r.Capture = ListeningCapture{}
	}
	raw, _ := json.Marshal(r.Answers)
	capture, _ := json.Marshal(r.Capture)
	_, err = tx.ExecContext(ctx, `UPDATE listening_reflections SET answers_json=?,capture_json=?,state=?,revision=?,updated_at=datetime('now') WHERE id=?`, string(raw), string(capture), r.State, r.Revision, id)
	if err != nil {
		return nil, err
	}
	if err = recordReflectionAction(ctx, tx, key, id, hash, r); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

func reflectionReplay(ctx context.Context, tx *sql.Tx, key, id, hash string) (*ListeningReflection, error) {
	var priorID, priorHash, raw string
	err := tx.QueryRowContext(ctx, `SELECT reflection_id,payload_hash,result_json FROM listening_reflection_actions WHERE request_key=?`, key).Scan(&priorID, &priorHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if priorID != id || priorHash != hash {
		return nil, ErrConflict
	}
	// Purge removes replay content; expired drafts must not resurrect private text.
	current, err := scanReflection(tx.QueryRowContext(ctx, `SELECT `+reflectionColumns+` FROM listening_reflections WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if current.State == "expired" || current.State == "unavailable" || current.State == "cancelled" {
		return current, nil
	}
	var r ListeningReflection
	err = json.Unmarshal([]byte(raw), &r)
	return &r, err
}

func recordReflectionAction(ctx context.Context, tx *sql.Tx, key, id, hash string, r *ListeningReflection) error {
	raw, _ := json.Marshal(r)
	_, err := tx.ExecContext(ctx, `INSERT INTO listening_reflection_actions(request_key,reflection_id,payload_hash,result_json)VALUES(?,?,?,?)`, key, id, hash, string(raw))
	return err
}

// ExpireListeningReflections is explicit maintenance, never a side effect of GET.
func (s *Store) ExpireListeningReflections(ctx context.Context, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE listening_reflections SET state='expired',answers_json='{}',capture_json='{}',revision=revision+1 WHERE state='draft' AND expires_at<=?`, now.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM listening_reflection_actions WHERE reflection_id IN(SELECT id FROM listening_reflections WHERE state IN('expired','cancelled','unavailable'))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
