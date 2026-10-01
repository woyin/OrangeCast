package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// LearningQuestion is an Owner-controlled goal, not a statement of fact.
type LearningQuestion struct {
	ID, Body, Goal, ThemeID, TargetDate, Status, CreatedAt, UpdatedAt string
	Revision                                                          int
}

// LearningQuestionRelation preserves organization identity independently of material content.
type LearningQuestionRelation struct {
	provider.LearningQuestionLink
	State, Origin, CreatedAt string
}

// LearningQuestionOperation records explicit changes without inferring mastery.
type LearningQuestionOperation struct {
	Revision                  int
	Action, Detail, CreatedAt string
}

// LearningQuestionChange uses one question revision for both content and membership edits.
type LearningQuestionChange struct {
	Action, Body, Goal, ThemeID, TargetDate, Status string
	Link                                            provider.LearningQuestionLink
}

func validLearningQuestionStatus(status string) bool {
	return status == "active" || status == "paused" || status == "resolved" || status == "archived"
}
func normalizeLearningQuestion(q *LearningQuestion) error {
	q.Body, q.Goal, q.ThemeID, q.TargetDate = strings.TrimSpace(q.Body), strings.TrimSpace(q.Goal), strings.TrimSpace(q.ThemeID), strings.TrimSpace(q.TargetDate)
	if q.Body == "" || len([]rune(q.Body)) > 2000 || len([]rune(q.Goal)) > 5000 || !validLearningQuestionStatus(q.Status) {
		return ErrInvalidEditorialState
	}
	if q.TargetDate != "" {
		if _, err := time.Parse("2006-01-02", q.TargetDate); err != nil {
			return ErrInvalidEditorialState
		}
	}
	return nil
}

const questionColumns = "id,body,goal,COALESCE(theme_id,''),target_date,status,revision,created_at,updated_at"

func scanLearningQuestion(row interface{ Scan(...any) error }) (*LearningQuestion, error) {
	q := &LearningQuestion{}
	err := row.Scan(&q.ID, &q.Body, &q.Goal, &q.ThemeID, &q.TargetDate, &q.Status, &q.Revision, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return q, err
}

// CreateLearningQuestion starts active; only explicit Owner edits may resolve it.
func (s *Store) CreateLearningQuestion(ctx context.Context, q LearningQuestion) (*LearningQuestion, error) {
	q.ID, q.Status = uuid.NewString(), "active"
	if err := normalizeLearningQuestion(&q); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkQuestionTheme(ctx, tx, q.ThemeID); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO learning_questions(id,body,goal,theme_id,target_date)VALUES(?,?,?,NULLIF(?,''),?)`, q.ID, q.Body, q.Goal, q.ThemeID, q.TargetDate)
	if err != nil {
		return nil, err
	}
	if err = questionOperation(ctx, tx, q.ID, 1, "create", ""); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLearningQuestion(ctx, q.ID)
}
func checkQuestionTheme(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM themes WHERE id=?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func questionOperation(ctx context.Context, tx *sql.Tx, id string, rev int, action, detail string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO learning_question_operations(question_id,revision,action,detail)VALUES(?,?,?,?)`, id, rev, action, detail)
	return err
}

// GetLearningQuestion retrieves the current Owner revision.
func (s *Store) GetLearningQuestion(ctx context.Context, id string) (*LearningQuestion, error) {
	return scanLearningQuestion(s.DB.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE id=?`, id))
}

// ListLearningQuestions bounds local literal search and status filtering.
func (s *Store) ListLearningQuestions(ctx context.Context, text, status string) ([]*LearningQuestion, error) {
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 200 || status != "" && !validLearningQuestionStatus(status) {
		return nil, ErrInvalidEditorialState
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE (?='' OR status=?) AND (?='' OR instr(lower(body||' '||goal),lower(?))>0) ORDER BY updated_at DESC,id LIMIT 100`, status, status, text, text)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LearningQuestion{}
	for rows.Next() {
		q, e := scanLearningQuestion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ListLearningQuestionRelations returns both confirmed membership and non-authorizing suggestions.
func (s *Store) ListLearningQuestionRelations(ctx context.Context, id string) ([]LearningQuestionRelation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,object_id,source_type,source_id,version,state,origin,created_at FROM learning_question_links WHERE question_id=? ORDER BY state,kind,object_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearningQuestionRelation{}
	for rows.Next() {
		var v LearningQuestionRelation
		if err = rows.Scan(&v.Kind, &v.ObjectID, &v.SourceType, &v.SourceID, &v.Version, &v.State, &v.Origin, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListLearningQuestionOperations returns the latest actual edits.
func (s *Store) ListLearningQuestionOperations(ctx context.Context, id string) ([]LearningQuestionOperation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT revision,action,detail,created_at FROM learning_question_operations WHERE question_id=? ORDER BY seq DESC LIMIT 20`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearningQuestionOperation{}
	for rows.Next() {
		var v LearningQuestionOperation
		if err = rows.Scan(&v.Revision, &v.Action, &v.Detail, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func questionSourceTable(kind string) string {
	switch kind {
	case "episode":
		return "episodes"
	case "upload":
		return "uploads"
	case "document":
		return "documents"
	}
	return ""
}
func checkQuestionSource(ctx context.Context, tx *sql.Tx, kind, id string) error {
	table := questionSourceTable(kind)
	if table == "" {
		return ErrInvalidEditorialState
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id=? AND archived_at IS NULL`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// resolveQuestionRelation runs inside the same transaction as membership changes.
func resolveQuestionRelation(ctx context.Context, tx *sql.Tx, l provider.LearningQuestionLink) (provider.LearningQuestionLink, error) {
	l.MaterialIDs = nil
	l.Version = 0
	l.ObjectID = strings.TrimSpace(l.ObjectID)
	var err error
	switch l.Kind {
	case "source":
		l.ObjectID = l.SourceType + ":" + l.SourceID
	case "note":
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,revision FROM owner_notes WHERE id=?`, l.ObjectID).Scan(&l.SourceType, &l.SourceID, &l.Version)
	case "keypoint":
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,card_version FROM keypoint_index WHERE id=?`, l.ObjectID).Scan(&l.SourceType, &l.SourceID, &l.Version)
	case "evidence":
		err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,content_version FROM source_snapshots WHERE id=? AND status!='purged'`, l.ObjectID).Scan(&l.SourceType, &l.SourceID, &l.Version)
	case "article":
		l.SourceType, l.SourceID = "", ""
		err = tx.QueryRowContext(ctx, `SELECT working_revision FROM knowledge_articles WHERE id=?`, l.ObjectID).Scan(&l.Version)
	default:
		return l, ErrInvalidEditorialState
	}
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	if l.SourceID != "" {
		err = checkQuestionSource(ctx, tx, l.SourceType, l.SourceID)
	}
	return l, err
}
func insertQuestionRelation(ctx context.Context, tx *sql.Tx, id string, l provider.LearningQuestionLink, state, origin string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO learning_question_links(question_id,kind,object_id,source_type,source_id,version,state,origin)VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(question_id,kind,object_id) DO UPDATE SET state=excluded.state,version=excluded.version,origin=excluded.origin`, id, l.Kind, l.ObjectID, l.SourceType, l.SourceID, l.Version, state, origin)
	return err
}

// ChangeLearningQuestion applies a CAS edit, link confirmation, state change or organization-only deletion.
func (s *Store) ChangeLearningQuestion(ctx context.Context, id string, expected int, c LearningQuestionChange) (*LearningQuestion, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q, err := scanLearningQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if expected < 1 || q.Revision != expected {
		return nil, ErrConflict
	}
	switch c.Action {
	case "edit":
		q.Body, q.Goal, q.ThemeID, q.TargetDate = c.Body, c.Goal, c.ThemeID, c.TargetDate
		if err = normalizeLearningQuestion(q); err != nil {
			return nil, err
		}
		if err = checkQuestionTheme(ctx, tx, q.ThemeID); err != nil {
			return nil, err
		}
	case "status":
		if !validLearningQuestionStatus(c.Status) {
			return nil, ErrInvalidEditorialState
		}
		q.Status = c.Status
	case "link", "suggest":
		l, e := resolveQuestionRelation(ctx, tx, c.Link)
		if e != nil {
			return nil, e
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM learning_question_links WHERE question_id=?`, id).Scan(&count); err != nil {
			return nil, err
		}
		var previous string
		e = tx.QueryRowContext(ctx, `SELECT state FROM learning_question_links WHERE question_id=? AND kind=? AND object_id=?`, id, l.Kind, l.ObjectID).Scan(&previous)
		state := "confirmed"
		if c.Action == "suggest" {
			state = "suggested"
		}
		if e == nil && (previous == state || previous == "confirmed" && state == "suggested") {
			tx.Rollback()
			return q, nil
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if count >= 200 && errors.Is(e, sql.ErrNoRows) {
			return nil, fmt.Errorf("一个问题最多关联200项")
		}
		if err = insertQuestionRelation(ctx, tx, id, l, state, "owner"); err != nil {
			return nil, err
		}
	case "unlink":
		res, e := tx.ExecContext(ctx, `DELETE FROM learning_question_links WHERE question_id=? AND kind=? AND object_id=?`, id, c.Link.Kind, c.Link.ObjectID)
		if e != nil {
			return nil, e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		if n == 0 {
			return nil, ErrNotFound
		}
	case "delete":
		if _, err = tx.ExecContext(ctx, `DELETE FROM learning_questions WHERE id=?`, id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, ErrInvalidEditorialState
	}
	q.Revision++
	if _, err = tx.ExecContext(ctx, `UPDATE learning_questions SET body=?,goal=?,theme_id=NULLIF(?,''),target_date=?,status=?,revision=?,updated_at=datetime('now') WHERE id=?`, q.Body, q.Goal, q.ThemeID, q.TargetDate, q.Status, q.Revision, id); err != nil {
		return nil, err
	}
	if err = questionOperation(ctx, tx, id, q.Revision, c.Action, c.Link.Kind+":"+c.Link.ObjectID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLearningQuestion(ctx, id)
}

// FreezeLearningQuestion freezes only confirmed organization scope, never suggestions or new permissions.
func (s *Store) FreezeLearningQuestion(ctx context.Context, id string, automatic bool) (*provider.FrozenLearningQuestion, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q, err := scanLearningQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM learning_questions WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if automatic && q.Status != "active" {
		return nil, ErrConflict
	}
	f := &provider.FrozenLearningQuestion{ID: q.ID, Revision: q.Revision, Body: q.Body, Goal: q.Goal, ThemeID: q.ThemeID, TargetDate: q.TargetDate, Links: []provider.LearningQuestionLink{}}
	rows, err := tx.QueryContext(ctx, `SELECT kind,object_id,source_type,source_id,version FROM learning_question_links WHERE question_id=? AND state='confirmed' AND origin!='generation' ORDER BY kind,object_id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l provider.LearningQuestionLink
		if err = rows.Scan(&l.Kind, &l.ObjectID, &l.SourceType, &l.SourceID, &l.Version); err != nil {
			rows.Close()
			return nil, err
		}
		f.Links = append(f.Links, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range f.Links {
		l := &f.Links[i]
		if l.Kind != "article" {
			continue
		}
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT input_json FROM knowledge_articles WHERE id=?`, l.ObjectID).Scan(&raw); err != nil {
			return nil, err
		}
		var req provider.KnowledgeArticleRequest
		if err = json.Unmarshal([]byte(raw), &req); err != nil {
			return nil, err
		}
		for _, m := range req.Materials {
			l.MaterialIDs = append(l.MaterialIDs, m.ID)
		}
	}
	return f, tx.Commit()
}

// CheckLearningQuestionExecution allows frozen old revisions while rechecking current automation eligibility.
func (s *Store) CheckLearningQuestionExecution(ctx context.Context, f *provider.FrozenLearningQuestion, automatic bool) error {
	if f == nil {
		return nil
	}
	q, err := s.GetLearningQuestion(ctx, f.ID)
	if err != nil {
		return err
	}
	if f.Revision < 1 || automatic && q.Status != "active" {
		return ErrConflict
	}
	return nil
}
func checkQuestionAdmission(ctx context.Context, tx *sql.Tx, f *provider.FrozenLearningQuestion, automatic bool, requireRevision bool) error {
	if f == nil {
		return nil
	}
	var revision int
	var status string
	err := tx.QueryRowContext(ctx, `SELECT revision,status FROM learning_questions WHERE id=?`, f.ID).Scan(&revision, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if f.Revision < 1 || requireRevision && revision != f.Revision || automatic && status != "active" {
		return ErrConflict
	}
	return nil
}
func attachGeneratedQuestionArticle(ctx context.Context, tx *sql.Tx, f *provider.FrozenLearningQuestion, articleID string) error {
	if f == nil {
		return nil
	}
	return insertQuestionRelation(ctx, tx, f.ID, provider.LearningQuestionLink{Kind: "article", ObjectID: articleID, Version: 0}, "confirmed", "generation")
}

// ListLearningQuestionArticles includes generated and explicitly attached articles without scanning history in memory.
func (s *Store) ListLearningQuestionArticles(ctx context.Context, id string) ([]*KnowledgeArticleRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knowledgeArticleColumns+` FROM knowledge_articles WHERE id IN(SELECT object_id FROM learning_question_links WHERE question_id=? AND kind='article' AND state='confirmed') ORDER BY created_at DESC,id LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*KnowledgeArticleRecord{}
	for rows.Next() {
		v, e := scanKnowledgeArticle(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateLearningQuestionNote atomically saves a personal reflection and its confirmed relation.
func (s *Store) CreateLearningQuestionNote(ctx context.Context, id string, expected int, note models.OwnerNote) (*models.OwnerNote, error) {
	note.Kind = "owner_reflection"
	note.CitationsJSON = "[]"
	prepared, err := s.prepareOwnerNote(ctx, note)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision, count int
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM learning_questions WHERE id=?`, id).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if expected < 1 || revision != expected {
		return nil, ErrConflict
	}
	if err = checkQuestionSource(ctx, tx, prepared.SourceType, prepared.SourceID); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM learning_question_links WHERE question_id=?`, id).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 200 {
		return nil, ErrInvalidEditorialState
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO owner_notes(id,source_type,source_id,kind,content,citations_json,references_json,anchor_json)VALUES(?,?,?,?,?,?,?,?)`, prepared.ID, prepared.SourceType, prepared.SourceID, prepared.Kind, prepared.Content, prepared.CitationsJSON, prepared.ReferencesJSON, prepared.AnchorJSON); err != nil {
		return nil, err
	}
	if err = insertQuestionRelation(ctx, tx, id, provider.LearningQuestionLink{Kind: "note", ObjectID: prepared.ID, SourceType: prepared.SourceType, SourceID: prepared.SourceID, Version: 1}, "confirmed", "owner"); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err = questionOperation(ctx, tx, id, revision+1, "note", prepared.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOwnerNote(ctx, prepared.ID)
}

// questionMaterialFilter bounds retrieval to frozen confirmed relationships before pagination.
func questionMaterialFilter(f *provider.FrozenLearningQuestion) (string, []any) {
	if f == nil {
		return "", nil
	}
	var clauses []string
	var args []any
	var ids []string
	seen := map[string]bool{}
	sources := map[string]bool{}
	for _, l := range f.Links {
		switch l.Kind {
		case "note", "keypoint":
			if !seen[l.ObjectID] {
				seen[l.ObjectID] = true
				ids = append(ids, l.ObjectID)
			}
		case "source", "evidence":
			key := l.SourceType + ":" + l.SourceID
			if !sources[key] {
				sources[key] = true
				clauses = append(clauses, "(d.source_type=? AND d.source_id=?)")
				args = append(args, l.SourceType, l.SourceID)
			}
		case "article":
			for _, id := range l.MaterialIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) > 0 {
		marks := make([]string, len(ids))
		for i, id := range ids {
			marks[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "d.object_id IN ("+strings.Join(marks, ",")+")")
	}
	if len(clauses) == 0 {
		return "0=1", nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args
}
func questionAllowsMaterial(f *provider.FrozenLearningQuestion, m provider.KnowledgeMaterial) bool {
	if f == nil {
		return true
	}
	for _, l := range f.Links {
		switch l.Kind {
		case "note", "keypoint":
			if l.ObjectID == m.ID {
				return true
			}
		case "source":
			if l.SourceType == m.SourceType && l.SourceID == m.SourceID {
				return true
			}
		case "evidence":
			if l.ObjectID == m.SnapshotID {
				return true
			}
		case "article":
			for _, id := range l.MaterialIDs {
				if id == m.ID {
					return true
				}
			}
		}
	}
	return false
}
