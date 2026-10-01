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
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// ReviewRuleVersion names the personal calendar-day self-assessment policy.
const ReviewRuleVersion = "review-calendar-v1"

// ReviewSchedule records an Owner-controlled reminder, not a mastery score.
type ReviewSchedule struct {
	ItemID, Question, DueUTC, Status, Timezone, RuleVersion, LastAnsweredUTC, LastPresentedUTC, QuestionID string
	Revision, Streak                                                                                       int
	Available                                                                                              bool
	Warning                                                                                                string
}

// ReviewSession identifies one resumable, explicitly started short review.
type ReviewSession struct {
	ID, RequestKey, Status, Timezone, CreatedAt, EndedAt string
	Revision                                             int
}

// ReviewSessionItem preserves the selected question and its original evidence.
type ReviewSessionItem struct {
	SessionID, ItemID, Question, AnswerBasis, InputJSON, MaterialIDsJSON, State, Answer, Assessment, NoteID, Warning string
	Position, ItemRevision, Revision                                                                                 int
	Revealed, Available                                                                                              bool
}
type reviewReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// NextReviewDue applies calendar days in the Owner's zone, never fixed 24h spans.
func NextReviewDue(now time.Time, zone, assessment string, previousStreak int) (time.Time, int, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, 0, err
	}
	days, streak := 0, 0
	switch assessment {
	case "revisit":
		days = 1
	case "partial":
		days = 3
	case "explain":
		streak = previousStreak + 1
		days = 7
		if streak >= 2 {
			days = 14
		}
	default:
		return time.Time{}, 0, ErrInvalidEditorialState
	}
	return now.In(loc).AddDate(0, 0, days).UTC(), streak, nil
}
func reviewTime(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// reviewEvidence permits explicitly labelled historical versions, but excludes
// withdrawn/deleted materials. Local review does not require external AI permission.
func reviewEvidence(ctx context.Context, q reviewReader, input, ids string) (string, bool, error) {
	var req provider.KnowledgeArticleRequest
	var wanted []string
	if json.Unmarshal([]byte(input), &req) != nil || json.Unmarshal([]byte(ids), &wanted) != nil {
		return "冻结依据损坏", false, ErrInvalidEditorialState
	}
	selected := selectedKnowledgeMaterials(req.Materials, wanted)
	if len(selected) == 0 || len(selected) != len(wanted) {
		return "原依据不可用", false, nil
	}
	warning := ""
	for _, m := range selected {
		st := models.SourceType(m.SourceType)
		if !validSourceType(st) {
			return "来源身份无效", false, nil
		}
		var archived sql.NullString
		err := q.QueryRowContext(ctx, `SELECT archived_at FROM `+sourceTable(st)+` WHERE id=?`, m.SourceID).Scan(&archived)
		if errors.Is(err, sql.ErrNoRows) || archived.Valid {
			return "原来源已清理或归档", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if m.Kind == "keypoint" {
			var version int
			var content, production, evidence, quality, stale string
			err = q.QueryRowContext(ctx, `SELECT card_version,content,production_status,evidence_status,quality_status,COALESCE(stale_at,'') FROM keypoint_index WHERE id=? AND source_type=? AND source_id=?`, m.ID, m.SourceType, m.SourceID).Scan(&version, &content, &production, &evidence, &quality, &stale)
			if errors.Is(err, sql.ErrNoRows) {
				return "原重点已删除", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if stale != "" || evidence == "stale" || production == string(models.KeyPointDismissed) || quality != string(models.KeyPointReady) && quality != string(models.KeyPointOwnerConfirmed) {
				return "原重点已撤回或失效", false, nil
			}
			if version != m.Version || content != m.Content {
				warning = "题目基于历史材料；当前版本已变化，请核对原依据。"
			}
		} else {
			var revision int
			var content, kind string
			err = q.QueryRowContext(ctx, `SELECT revision,content,kind FROM owner_notes WHERE id=? AND source_type=? AND source_id=?`, m.ID, m.SourceType, m.SourceID).Scan(&revision, &content, &kind)
			if errors.Is(err, sql.ErrNoRows) {
				return "原笔记已删除", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if revision != m.Version || content != m.Content || kind != m.Kind {
				warning = "题目基于历史笔记；当前版本已变化，请核对原依据。"
			}
		}
		if m.SnapshotID != "" {
			var status, sourceID, sourceType string
			var version int
			err = q.QueryRowContext(ctx, `SELECT status,source_id,source_type,content_version FROM source_snapshots WHERE id=?`, m.SnapshotID).Scan(&status, &sourceID, &sourceType, &version)
			if errors.Is(err, sql.ErrNoRows) || status == "purged" || sourceID != m.SourceID || sourceType != m.SourceType {
				return "冻结来源已清理或身份不符", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if st == models.SourceEpisode || st == models.SourceUpload {
				var current sql.NullInt64
				if err = q.QueryRowContext(ctx, `SELECT current_transcript_version FROM `+sourceTable(st)+` WHERE id=?`, m.SourceID).Scan(&current); err != nil {
					return "", false, err
				}
				if current.Valid && int(current.Int64) != version {
					warning = "题目保留原转录版本；当前原音依据已变化，请核对历史内容。"
				}
			}
		}
	}
	return warning, true, nil
}
func scanReviewSchedule(row interface{ Scan(...any) error }) (ReviewSchedule, error) {
	var v ReviewSchedule
	err := row.Scan(&v.ItemID, &v.Question, &v.DueUTC, &v.Status, &v.Timezone, &v.RuleVersion, &v.Streak, &v.Revision, &v.LastAnsweredUTC, &v.LastPresentedUTC, &v.QuestionID)
	return v, err
}

const reviewScheduleColumns = `s.item_id,i.question,s.due_utc,s.status,s.timezone,s.rule_version,s.streak,s.revision,s.last_answered_utc,s.last_presented_utc,COALESCE(s.question_id,'')`

// ListReviewSchedules is a paged read; it never generates questions or advances due dates.
func (s *Store) ListReviewSchedules(ctx context.Context, status string, offset int) ([]ReviewSchedule, error) {
	if offset < 0 || status != "" && status != "active" && status != "paused" && status != "ended" {
		return nil, ErrInvalidEditorialState
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+reviewScheduleColumns+` FROM review_schedules s JOIN learning_review_items i ON i.id=s.item_id WHERE (?='' OR s.status=?) ORDER BY s.due_utc,s.item_id LIMIT 51 OFFSET ?`, status, status, offset)
	if err != nil {
		return nil, err
	}
	var out []ReviewSchedule
	for rows.Next() {
		v, e := scanReviewSchedule(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		var input, ids string
		if err = s.DB.QueryRowContext(ctx, `SELECT b.input_json,i.material_ids_json FROM learning_review_items i JOIN learning_review_batches b ON b.id=i.batch_id WHERE i.id=?`, out[i].ItemID).Scan(&input, &ids); err != nil {
			return nil, err
		}
		out[i].Warning, out[i].Available, err = reviewEvidence(ctx, s.DB, input, ids)
		if err != nil {
			return nil, err
		}
		if out[i].QuestionID != "" {
			var state string
			err = s.DB.QueryRowContext(ctx, `SELECT status FROM learning_questions WHERE id=?`, out[i].QuestionID).Scan(&state)
			if err != nil {
				return nil, err
			}
			if state == "paused" || state == "archived" {
				out[i].Available = false
				out[i].Warning = "关联学习问题已暂停或归档"
			}
		}
	}
	return out, nil
}

// GetReviewSessionSize reads the preferred size without starting a session.
func (s *Store) GetReviewSessionSize(ctx context.Context) (int, error) {
	var size int
	err := s.DB.QueryRowContext(ctx, `SELECT session_size FROM review_schedule_settings WHERE id=1`).Scan(&size)
	return size, err
}

// SetReviewSessionSize changes only future sessions, within one to five items.
func (s *Store) SetReviewSessionSize(ctx context.Context, size int) error {
	if size < 1 || size > 5 {
		return ErrInvalidEditorialState
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE review_schedule_settings SET session_size=? WHERE id=1`, size)
	return err
}
func scanReviewSession(row interface{ Scan(...any) error }) (*ReviewSession, error) {
	v := &ReviewSession{}
	err := row.Scan(&v.ID, &v.RequestKey, &v.Status, &v.Revision, &v.Timezone, &v.CreatedAt, &v.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

const reviewSessionColumns = `id,request_key,status,revision,timezone,created_at,ended_at`

// GetReviewSession reads current or historical session metadata.
func (s *Store) GetReviewSession(ctx context.Context, id string) (*ReviewSession, error) {
	return scanReviewSession(s.DB.QueryRowContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions WHERE id=?`, id))
}

// ActiveReviewSession returns the sole unfinished session, or nil.
func (s *Store) ActiveReviewSession(ctx context.Context) (*ReviewSession, error) {
	v, err := scanReviewSession(s.DB.QueryRowContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions WHERE status='active'`))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return v, err
}

// dueReviewCandidates closes its rows before evidence checking, supporting the
// Store's single connection and the same admission transaction for start/withdraw.
func dueReviewCandidates(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, now time.Time) ([]ReviewSessionItem, error) {
	rows, err := q.QueryContext(ctx, `SELECT i.id,i.question,i.answer_basis,b.input_json,i.material_ids_json,i.revision FROM review_schedules s JOIN learning_review_items i ON i.id=s.item_id JOIN learning_review_batches b ON b.id=i.batch_id WHERE s.status='active' AND s.due_utc<=? AND b.status='ready' AND NOT EXISTS(SELECT 1 FROM learning_questions l WHERE l.id=s.question_id AND l.status IN('paused','archived')) ORDER BY s.last_presented_utc,s.due_utc,s.last_answered_utc,s.item_id`, reviewTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewSessionItem
	for rows.Next() {
		var v ReviewSessionItem
		if err = rows.Scan(&v.ItemID, &v.Question, &v.AnswerBasis, &v.InputJSON, &v.MaterialIDsJSON, &v.ItemRevision); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DueReviewCount excludes paused questions and unavailable original evidence.
func (s *Store) DueReviewCount(ctx context.Context, now time.Time) (int, error) {
	items, err := dueReviewCandidates(ctx, s.DB, now)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, v := range items {
		_, ok, e := reviewEvidence(ctx, s.DB, v.InputJSON, v.MaterialIDsJSON)
		if e != nil {
			return 0, e
		}
		if ok {
			count++
		}
	}
	return count, nil
}

// StartReviewSession freezes at most five already-generated questions. A retry
// returns its original session; a new request continues the sole unfinished one.
func (s *Store) StartReviewSession(ctx context.Context, requestKey string, size int, now time.Time) (*ReviewSession, error) {
	if _, err := uuid.Parse(requestKey); err != nil || size < 1 || size > 5 {
		return nil, ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	existing, err := scanReviewSession(tx.QueryRowContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions WHERE request_key=? OR status='active' ORDER BY request_key=? DESC LIMIT 1`, requestKey, requestKey))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var zone string
	if err = tx.QueryRowContext(ctx, `SELECT timezone FROM learning_review_settings WHERE id=1`).Scan(&zone); err != nil {
		return nil, err
	}
	if _, err = time.LoadLocation(zone); err != nil {
		return nil, err
	}
	candidates, err := dueReviewCandidates(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	selected := []ReviewSessionItem{}
	for _, v := range candidates {
		_, ok, e := reviewEvidence(ctx, tx, v.InputJSON, v.MaterialIDsJSON)
		if e != nil {
			return nil, e
		}
		if ok {
			selected = append(selected, v)
			if len(selected) == size {
				break
			}
		}
	}
	if len(selected) == 0 {
		return nil, ErrNotFound
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO review_sessions(id,request_key,timezone,created_at)VALUES(?,?,?,?)`, id, requestKey, zone, reviewTime(now)); err != nil {
		return nil, err
	}
	for i, v := range selected {
		var req provider.KnowledgeArticleRequest
		var ids []string
		json.Unmarshal([]byte(v.InputJSON), &req)
		json.Unmarshal([]byte(v.MaterialIDsJSON), &ids)
		req.Materials = selectedKnowledgeMaterials(req.Materials, ids)
		if _, err = tx.ExecContext(ctx, `INSERT INTO review_session_items(session_id,item_id,position,question,answer_basis,input_json,material_ids_json,item_revision)VALUES(?,?,?,?,?,?,?,?)`, id, v.ItemID, i+1, v.Question, v.AnswerBasis, jsonString(req), v.MaterialIDsJSON, v.ItemRevision); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE review_schedules SET last_presented_utc=? WHERE item_id=?`, reviewTime(now), v.ItemID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetReviewSession(ctx, id)
}

// ListReviewSessionItems reads frozen questions and labels historical or withdrawn evidence.
func (s *Store) ListReviewSessionItems(ctx context.Context, sessionID string) ([]ReviewSessionItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT x.session_id,x.item_id,x.position,x.question,x.answer_basis,x.input_json,x.material_ids_json,x.item_revision,x.revision,x.state,x.revealed,x.answer,x.assessment,COALESCE(a.note_id,'') FROM review_session_items x LEFT JOIN learning_review_answers a ON a.item_id=x.item_id AND a.revision=x.item_revision WHERE x.session_id=? ORDER BY x.position`, sessionID)
	if err != nil {
		return nil, err
	}
	var out []ReviewSessionItem
	for rows.Next() {
		var v ReviewSessionItem
		if err = rows.Scan(&v.SessionID, &v.ItemID, &v.Position, &v.Question, &v.AnswerBasis, &v.InputJSON, &v.MaterialIDsJSON, &v.ItemRevision, &v.Revision, &v.State, &v.Revealed, &v.Answer, &v.Assessment, &v.NoteID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Warning, out[i].Available, err = reviewEvidence(ctx, s.DB, out[i].InputJSON, out[i].MaterialIDsJSON)
		if err != nil {
			return nil, err
		}
		var status string
		var questionStatus sql.NullString
		if err = s.DB.QueryRowContext(ctx, `SELECT s.status,q.status FROM review_schedules s LEFT JOIN learning_questions q ON q.id=s.question_id WHERE s.item_id=?`, out[i].ItemID).Scan(&status, &questionStatus); err != nil {
			return nil, err
		}
		if status != "active" || questionStatus.String == "paused" || questionStatus.String == "archived" {
			out[i].Available = false
			out[i].Warning = "题目安排或关联问题已暂停／结束；历史解释仍可查看。"
		}

		var currentRevision int
		if err = s.DB.QueryRowContext(ctx, `SELECT revision FROM learning_review_items WHERE id=?`, out[i].ItemID).Scan(&currentRevision); err != nil {
			return nil, err
		}
		if out[i].State == "pending" && currentRevision != out[i].ItemRevision {
			out[i].Available = false
			out[i].Warning = "题目已在另一处更新；结束此会话后可重新选择题目，原草稿仍保留。"
		}

	}
	return out, nil
}
func reviewActionFingerprint(value any) string {
	hash := sha256.Sum256([]byte(jsonString(value)))
	return hex.EncodeToString(hash[:])
}
func reviewActionSeen(ctx context.Context, tx *sql.Tx, key, kind, target, action, hash string) (bool, error) {
	if _, err := uuid.Parse(key); err != nil {
		return false, ErrInvalidEditorialState
	}
	var k, t, a, h string
	err := tx.QueryRowContext(ctx, `SELECT target_type,target_id,action,payload_hash FROM review_owner_actions WHERE request_key=?`, key).Scan(&k, &t, &a, &h)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if k != kind || t != target || a != action || h != hash {
		return false, ErrConflict
	}
	return true, nil
}
func recordReviewAction(ctx context.Context, tx *sql.Tx, key, kind, target, action, hash string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO review_owner_actions(request_key,target_type,target_id,action,payload_hash,created_at)VALUES(?,?,?,?,?,?)`, key, kind, target, action, hash, reviewTime(now))
	return err
}

// scheduleReviewAnswerTx shares the answer/history transaction with both weekly
// and daily explanations. Revealing an answer never increments the streak.
func scheduleReviewAnswerTx(ctx context.Context, tx *sql.Tx, item, assessment string, now time.Time) error {
	var zone string
	var streak int
	if err := tx.QueryRowContext(ctx, `SELECT timezone FROM learning_review_settings WHERE id=1`).Scan(&zone); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT streak FROM review_schedules WHERE item_id=?`, item).Scan(&streak); err != nil {
		return err
	}
	due, next, err := NextReviewDue(now, zone, assessment, streak)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_schedules SET due_utc=?,timezone=?,rule_version=?,streak=?,last_answered_utc=?,revision=revision+1 WHERE item_id=?`, reviewTime(due), zone, ReviewRuleVersion, next, reviewTime(now), item)
	return err
}

func postponeReviewTx(ctx context.Context, tx *sql.Tx, item string, now time.Time) error {
	var zone string
	if err := tx.QueryRowContext(ctx, `SELECT timezone FROM learning_review_settings WHERE id=1`).Scan(&zone); err != nil {
		return err
	}
	due, _, err := NextReviewDue(now, zone, "revisit", 0)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_schedules SET due_utc=?,timezone=?,revision=revision+1 WHERE item_id=?`, reviewTime(due), zone, item)
	return err
}

// AnswerReviewSession checks the session item and global answer revisions together,
// commits answer history plus its next due date, and records replay-safe actions.
func (s *Store) AnswerReviewSession(ctx context.Context, sessionID, item, action, answer, assessment, key string, expected int, now time.Time) error {
	if expected < 1 || len([]rune(answer)) > 10000 || action != "answer" && action != "reveal" && action != "later" {
		return ErrInvalidEditorialState
	}
	if action == "answer" && (strings.TrimSpace(answer) == "" || assessment != "explain" && assessment != "partial" && assessment != "revisit") {
		return ErrInvalidEditorialState
	}
	hash := reviewActionFingerprint([]any{expected, answer, assessment})
	target := sessionID + ":" + item
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen, err := reviewActionSeen(ctx, tx, key, "session_item", target, action, hash)
	if err != nil || seen {
		return err
	}
	var v ReviewSessionItem
	var sessionStatus, scheduleStatus string
	var questionState sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT x.input_json,x.material_ids_json,x.item_revision,x.revision,x.state,r.status,s.status,l.status FROM review_session_items x JOIN review_sessions r ON r.id=x.session_id JOIN review_schedules s ON s.item_id=x.item_id LEFT JOIN learning_questions l ON l.id=s.question_id WHERE x.session_id=? AND x.item_id=?`, sessionID, item).Scan(&v.InputJSON, &v.MaterialIDsJSON, &v.ItemRevision, &v.Revision, &v.State, &sessionStatus, &scheduleStatus, &questionState)
	if err != nil {
		return err
	}
	if sessionStatus != "active" || v.State != "pending" || v.Revision != expected || scheduleStatus != "active" || questionState.String == "paused" || questionState.String == "archived" {
		return ErrConflict
	}
	_, available, err := reviewEvidence(ctx, tx, v.InputJSON, v.MaterialIDsJSON)
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("%w: 原依据已撤回，不能提交为当前有效回顾", ErrConflict)
	}
	state := "pending"
	reveal := true
	switch action {
	case "answer":
		res, e := tx.ExecContext(ctx, `UPDATE learning_review_items SET answer=?,assessment=?,state='answered',revealed=1,note_id='',revision=revision+1,updated_at=? WHERE id=? AND revision=?`, answer, assessment, now.UTC().Format("2006-01-02 15:04:05"), item, v.ItemRevision)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrConflict
		}
		v.ItemRevision++
		if _, err = tx.ExecContext(ctx, `INSERT INTO learning_review_answers(id,item_id,revision,answer,assessment,state)VALUES(?,?,?,?,?,'answered')`, uuid.NewString(), item, v.ItemRevision, answer, assessment); err != nil {
			return err
		}
		if err = scheduleReviewAnswerTx(ctx, tx, item, assessment, now); err != nil {
			return err
		}
		state = "answered"
	case "later":
		var zone string
		if err = tx.QueryRowContext(ctx, `SELECT timezone FROM learning_review_settings WHERE id=1`).Scan(&zone); err != nil {
			return err
		}
		due, _, e := NextReviewDue(now, zone, "revisit", 0)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE review_schedules SET due_utc=?,timezone=?,revision=revision+1 WHERE item_id=?`, reviewTime(due), zone, item); err != nil {
			return err
		}
		state = "later"
		reveal = false
	}
	if _, err = tx.ExecContext(ctx, `UPDATE review_session_items SET state=?,revealed=MAX(revealed,?),answer=?,assessment=?,item_revision=?,revision=revision+1 WHERE session_id=? AND item_id=?`, state, reveal, answer, assessment, v.ItemRevision, sessionID, item); err != nil {
		return err
	}
	if err = recordReviewAction(ctx, tx, key, "session_item", target, action, hash, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE review_sessions SET status=CASE WHEN NOT EXISTS(SELECT 1 FROM review_session_items WHERE session_id=? AND state='pending') THEN 'complete' ELSE status END,revision=revision+1 WHERE id=?`, sessionID, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// EndReviewSession leaves unanswered schedules due, allowing an explicit new
// short session; last-presented ordering prevents one abandoned item dominating.
func (s *Store) EndReviewSession(ctx context.Context, id, key string, expected int, now time.Time) error {
	hash := reviewActionFingerprint(expected)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen, err := reviewActionSeen(ctx, tx, key, "session", id, "end", hash)
	if err != nil || seen {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE review_sessions SET status='ended',ended_at=?,revision=revision+1 WHERE id=? AND status='active' AND revision=?`, reviewTime(now), id, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if err = recordReviewAction(ctx, tx, key, "session", id, "end", hash, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ReviewScheduleChange carries one explicit reminder or question association change.
type ReviewScheduleChange struct {
	Action, LocalDue, QuestionID string
	QuestionRevision             int
}

// ChangeReviewSchedule commits a replay-safe Owner action under the displayed revision.
func (s *Store) ChangeReviewSchedule(ctx context.Context, item, key string, expected int, c ReviewScheduleChange, now time.Time) error {
	if expected < 1 {
		return ErrInvalidEditorialState
	}
	hash := reviewActionFingerprint([]any{expected, c})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen, err := reviewActionSeen(ctx, tx, key, "schedule", item, c.Action, hash)
	if err != nil || seen {
		return err
	}
	var status string
	var revision int
	if err = tx.QueryRowContext(ctx, `SELECT status,revision FROM review_schedules WHERE item_id=?`, item).Scan(&status, &revision); err != nil {
		return err
	}
	if revision != expected {
		return ErrConflict
	}
	var zone string
	if err = tx.QueryRowContext(ctx, `SELECT timezone FROM learning_review_settings WHERE id=1`).Scan(&zone); err != nil {
		return err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return err
	}
	switch c.Action {
	case "pause":
		status = "paused"
	case "resume":
		status = "active"
	case "end":
		status = "ended"
	case "due", "later":
		var due time.Time
		if c.Action == "due" {
			due, err = time.ParseInLocation("2006-01-02T15:04", c.LocalDue, loc)
			if err != nil || due.In(loc).Format("2006-01-02T15:04") != c.LocalDue {
				return ErrInvalidEditorialState
			}
		} else {
			due = now.In(loc).AddDate(0, 0, 1)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE review_schedules SET due_utc=?,timezone=? WHERE item_id=?`, reviewTime(due), zone, item); err != nil {
			return err
		}
	case "question":
		if c.QuestionID != "" {
			var r int
			if err = tx.QueryRowContext(ctx, `SELECT revision FROM learning_questions WHERE id=?`, c.QuestionID).Scan(&r); err != nil {
				return err
			}
			if r != c.QuestionRevision {
				return ErrConflict
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE review_schedules SET question_id=NULLIF(?,'') WHERE item_id=?`, c.QuestionID, item); err != nil {
			return err
		}
	default:
		return ErrInvalidEditorialState
	}
	if _, err = tx.ExecContext(ctx, `UPDATE review_schedules SET status=?,revision=revision+1 WHERE item_id=?`, status, item); err != nil {
		return err
	}
	if err = recordReviewAction(ctx, tx, key, "schedule", item, c.Action, hash, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ListReviewSessions pages durable session history in stable creation order.
func (s *Store) ListReviewSessions(ctx context.Context, offset int) ([]*ReviewSession, error) {
	if offset < 0 {
		return nil, ErrInvalidEditorialState
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions ORDER BY created_at DESC,id DESC LIMIT 51 OFFSET ?`, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReviewSession
	for rows.Next() {
		v, e := scanReviewSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReviewActivityCounts distinguishes paid question batches from explicit daily
// answers; revealing and retrying a response do not inflate answer counts.
func (s *Store) ReviewActivityCounts(ctx context.Context) (int, int, error) {
	var batches, answers int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM learning_review_batches WHERE status='ready'),(SELECT count(*) FROM review_owner_actions WHERE target_type='session_item' AND action='answer')`).Scan(&batches, &answers)
	return batches, answers, err
}
