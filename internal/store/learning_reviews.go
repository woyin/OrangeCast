package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"time"
)

// LearningReviewSettings configures future weekly runs, independently of articles.
type LearningReviewSettings struct {
	Enabled              bool
	Timezone             string
	Weekday              int
	ClockTime, EnabledAt string
}

// LearningReviewBatch freezes the original week, materials and paid execution.
type LearningReviewBatch struct {
	ID, ProfileID, WeekKey, Timezone, StartUTC, EndUTC, InputJSON, Provider, Model, PromptVersion, Status, Reason, JobID, CreatedAt string
	Automated                                                                                                                       bool
}

// LearningReviewItem keeps a question separate from the Owner's explanation.
type LearningReviewItem struct {
	ID, BatchID, Question, AnswerBasis, MaterialIDsJSON, State, Answer, Assessment, NoteID, UpdatedAt string
	Position, Revision                                                                                int
	Revealed                                                                                          bool
}

// LearningReviewAnswer is an immutable Owner answer version.
type LearningReviewAnswer struct {
	Revision                                     int
	Answer, Assessment, State, CreatedAt, NoteID string
}

// GetLearningReviewSettings reads the independent opt-in and time zone.
func (s *Store) GetLearningReviewSettings(ctx context.Context) (LearningReviewSettings, error) {
	var v LearningReviewSettings
	err := s.DB.QueryRowContext(ctx, `SELECT enabled,timezone,weekday,clock_time,enabled_at FROM learning_review_settings WHERE id=1`).Scan(&v.Enabled, &v.Timezone, &v.Weekday, &v.ClockTime, &v.EnabledAt)
	return v, err
}

// SetLearningReviewSettings validates an IANA zone and future schedule.
func (s *Store) SetLearningReviewSettings(ctx context.Context, v LearningReviewSettings) error {
	if _, err := time.LoadLocation(v.Timezone); err != nil {
		return fmt.Errorf("请输入有效时区，例如 Asia/Shanghai 或 UTC")
	}
	if _, err := time.Parse("15:04", v.ClockTime); err != nil || v.Weekday < 0 || v.Weekday > 6 {
		return ErrInvalidEditorialState
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE learning_review_settings SET enabled_at=CASE WHEN enabled=0 AND ?=1 THEN datetime('now') ELSE enabled_at END,enabled=?,timezone=?,weekday=?,clock_time=?,updated_at=datetime('now') WHERE id=1`, v.Enabled, v.Enabled, v.Timezone, v.Weekday, v.ClockTime)
	return err
}

// LearningReviewWindow returns Monday-to-Monday boundaries in the selected zone.
func LearningReviewWindow(now time.Time, zone string) (string, time.Time, time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	n := now.In(loc)
	start := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(int(n.Weekday())+6)%7)
	return start.Format("2006-01-02"), start.UTC(), start.AddDate(0, 0, 7).UTC(), nil
}

// LearningReviewDue compares the configured local weekday and wall-clock time.
func LearningReviewDue(now time.Time, v LearningReviewSettings) bool {
	if !v.Enabled {
		return false
	}
	loc, err := time.LoadLocation(v.Timezone)
	if err != nil {
		return false
	}
	n := now.In(loc)
	day, wanted := (int(n.Weekday())+6)%7, (v.Weekday+6)%7
	return day > wanted || day == wanted && n.Format("15:04") >= v.ClockTime
}

// ReserveLearningReview reuses the week even when settings or materials later change.
func (s *Store) ReserveLearningReview(ctx context.Context, profile, name, model string, now time.Time, automatic bool) (*LearningReviewBatch, bool, error) {
	prefs, err := s.GetLearningReviewSettings(ctx)
	if err != nil {
		return nil, false, err
	}
	key, start, end, err := LearningReviewWindow(now, prefs.Timezone)
	if err != nil {
		return nil, false, err
	}
	var existing string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM learning_review_batches WHERE profile_id=? AND week_key=?`, profile, key).Scan(&existing)
	if err == nil {
		v, e := s.GetLearningReviewBatch(ctx, existing)
		return v, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if automatic && !LearningReviewDue(now, prefs) {
		return nil, false, nil
	}
	// Recent changed materials plus deferred review evidence; never load the full library.
	rows, err := s.DB.QueryContext(ctx, `SELECT material_id FROM knowledge_learning_changes WHERE created_at>=? AND created_at<? GROUP BY material_id ORDER BY MAX(seq) DESC LIMIT 20`, start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if e := rows.Scan(&id); e != nil {
			rows.Close()
			return nil, false, e
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	req, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, name, KnowledgeScope{MaterialIDs: ids}, false)
	if err != nil {
		return nil, false, err
	}
	if len(ids) == 0 {
		req.Materials = nil
	}
	deferred, err := s.DB.QueryContext(ctx, `SELECT b.input_json,i.material_ids_json FROM learning_review_items i JOIN learning_review_batches b ON b.id=i.batch_id WHERE b.profile_id=? AND b.week_key!=? AND i.state IN ('pending','later') ORDER BY b.created_at DESC LIMIT 5`, profile, key)
	if err != nil {
		return nil, false, err
	}
	type pendingEvidence struct{ input, ids string }
	var pending []pendingEvidence
	for deferred.Next() {
		var v pendingEvidence
		if e := deferred.Scan(&v.input, &v.ids); e != nil {
			deferred.Close()
			return nil, false, e
		}
		pending = append(pending, v)
	}
	err = deferred.Err()
	deferred.Close()
	if err != nil {
		return nil, false, err
	}
	for _, v := range pending {
		var old provider.KnowledgeArticleRequest
		var wanted []string
		if json.Unmarshal([]byte(v.input), &old) != nil || json.Unmarshal([]byte(v.ids), &wanted) != nil {
			return nil, false, ErrInvalidEditorialState
		}
		for _, m := range selectedKnowledgeMaterials(old.Materials, wanted) {
			fresh, e := s.knowledgeMaterial(ctx, profile, name, m.ID)
			if e != nil {
				return nil, false, e
			}
			if fresh != nil {
				fresh.RetrievalReason = "待回看：上周未处理的依据"
				req.Materials = append(req.Materials, *fresh)
			}
		}
	}
	req.Materials = boundKnowledgeMaterials(req.Materials, 20)
	if len(req.Materials) == 0 {
		return nil, false, nil
	}
	for i, m := range req.Materials {
		if m.Kind != "keypoint" {
			versions, e := s.ListOwnerNoteRevisions(ctx, m.ID)
			if e != nil {
				return nil, false, e
			}
			if len(versions) > 1 {
				req.Materials[i].PreviousContent = versions[1].Content
			}
		}
	}
	req.Stage = "weekly_review"
	req.History = nil
	req.Topic = nil
	req.DiscoveryBatchID = ""
	req.PromptVersion = provider.KnowledgeArticlePromptVersion
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	// Read identity and opt-in again under the admission transaction.
	err = tx.QueryRowContext(ctx, `SELECT id FROM learning_review_batches WHERE profile_id=? AND week_key=?`, profile, key).Scan(&existing)
	if err == nil {
		tx.Rollback()
		v, e := s.GetLearningReviewBatch(ctx, existing)
		return v, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if automatic {
		var enabled bool
		if e := tx.QueryRowContext(ctx, `SELECT enabled FROM learning_review_settings WHERE id=1`).Scan(&enabled); e != nil {
			return nil, false, e
		}
		if !enabled {
			return nil, false, nil
		}
	}
	id, jobID := uuid.NewString(), uuid.NewString()
	ensureKnowledgeStageConfigs(&req, model)
	model, err = freezeKnowledgeEstimate(ctx, tx, name, model, &req)
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO learning_review_batches(id,profile_id,week_key,timezone,start_utc,end_utc,input_json,provider,model,prompt_version,job_id,automated)VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, profile, key, prefs.Timezone, start.Format(time.RFC3339), end.Format(time.RFC3339), jsonString(req), name, model, req.PromptVersion, jobID, automatic); err != nil {
		return nil, false, err
	}
	input := jsonString(LearningReviewStageInput{BatchID: id, Request: req})
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,is_automated,intent_id,input_snapshot_json,config_version,configured_provider,configured_model)VALUES(?,'learning_review',?,'weekly_review','queued',?,?,?,?,?,?)`, jobID, id, automatic, "learning-review:"+id, input, req.PromptVersion, name, model); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	v, err := s.GetLearningReviewBatch(ctx, id)
	return v, true, err
}

// LearningReviewStageInput freezes a batch input for the durable queue.
type LearningReviewStageInput struct {
	BatchID string                           `json:"batch_id"`
	Request provider.KnowledgeArticleRequest `json:"request"`
}

func scanLearningReview(row interface{ Scan(...any) error }) (*LearningReviewBatch, error) {
	v := &LearningReviewBatch{}
	err := row.Scan(&v.ID, &v.ProfileID, &v.WeekKey, &v.Timezone, &v.StartUTC, &v.EndUTC, &v.InputJSON, &v.Provider, &v.Model, &v.PromptVersion, &v.Status, &v.Reason, &v.JobID, &v.Automated, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

const learningReviewColumns = `id,profile_id,week_key,timezone,start_utc,end_utc,input_json,provider,model,prompt_version,status,reason,job_id,automated,created_at`

// GetLearningReviewBatch retrieves immutable week inputs and current execution state.
func (s *Store) GetLearningReviewBatch(ctx context.Context, id string) (*LearningReviewBatch, error) {
	return scanLearningReview(s.DB.QueryRowContext(ctx, `SELECT `+learningReviewColumns+` FROM learning_review_batches WHERE id=?`, id))
}

// ListLearningReviewBatches returns the recent weekly history.
func (s *Store) ListLearningReviewBatches(ctx context.Context) ([]*LearningReviewBatch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+learningReviewColumns+` FROM learning_review_batches ORDER BY created_at DESC LIMIT 52`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LearningReviewBatch
	for rows.Next() {
		v, e := scanLearningReview(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListLearningReviewItems returns ordered questions without deriving a mastery score.
func (s *Store) ListLearningReviewItems(ctx context.Context, id string) ([]LearningReviewItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,batch_id,position,question,answer_basis,material_ids_json,state,answer,assessment,revision,note_id,revealed,updated_at FROM learning_review_items WHERE batch_id=? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LearningReviewItem
	for rows.Next() {
		var v LearningReviewItem
		if e := rows.Scan(&v.ID, &v.BatchID, &v.Position, &v.Question, &v.AnswerBasis, &v.MaterialIDsJSON, &v.State, &v.Answer, &v.Assessment, &v.Revision, &v.NoteID, &v.Revealed, &v.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CommitLearningReview atomically persists validated questions and completion.
func (s *Store) CommitLearningReview(ctx context.Context, jobID, batchID string, result *provider.KnowledgeArticleResult) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT result_state FROM processing_jobs WHERE id=? AND source_id=? AND source_type='learning_review'`, jobID, batchID).Scan(&state); err != nil {
		return err
	}
	if state == models.JobResultComplete {
		return nil
	}
	for i, q := range result.Questions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO learning_review_items(id,batch_id,position,question,answer_basis,material_ids_json)VALUES(?,?,?,?,?,?)`, fmt.Sprintf("%s:%d", batchID, i+1), batchID, i+1, q.Question, q.AnswerBasis, jsonString(q.MaterialIDs)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE learning_review_batches SET status='ready',reason=? WHERE id=?`, result.Reason, batchID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, jsonString(result), jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// FailLearningReview records an actionable failure without destroying frozen inputs.
func (s *Store) FailLearningReview(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE learning_review_batches SET status='failed',reason=? WHERE id=? AND status!='ready'`, reason, id)
	return err
}

// AnswerLearningReview saves an immutable Owner explanation version with CAS.
func (s *Store) AnswerLearningReview(ctx context.Context, id, answer, assessment, action string, expected int) error {
	return s.AnswerLearningReviewAt(ctx, id, answer, assessment, action, expected, time.Now())
}

// AnswerLearningReviewAt shares a fixed clock with daily review scheduling.
func (s *Store) AnswerLearningReviewAt(ctx context.Context, id, answer, assessment, action string, expected int, now time.Time) error {
	if len([]rune(answer)) > 10000 || expected < 0 {
		return ErrInvalidEditorialState
	}
	state := "answered"
	reveal := true
	switch action {
	case "answer":
		if strings.TrimSpace(answer) == "" || (assessment != "explain" && assessment != "partial" && assessment != "revisit") {
			return ErrInvalidEditorialState
		}
	case "later":
		state = "later"
		reveal = false
	case "reveal":
		state = "pending"
	default:
		return ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE learning_review_items SET answer=CASE WHEN ?='answer' THEN ? ELSE answer END,assessment=CASE WHEN ?='answer' THEN ? ELSE assessment END,state=CASE WHEN ?='reveal' AND state='answered' THEN state ELSE ? END,note_id=CASE WHEN ?1='answer' THEN '' ELSE note_id END,revealed=MAX(revealed,?),revision=revision+1,updated_at=datetime('now') WHERE id=? AND revision=?`, action, answer, action, assessment, action, state, reveal, id, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO learning_review_answers(id,item_id,revision,answer,assessment,state) SELECT ?,id,revision,answer,assessment,state FROM learning_review_items WHERE id=?`, uuid.NewString(), id); err != nil {
		return err
	}
	if action == "answer" {
		if err = scheduleReviewAnswerTx(ctx, tx, id, assessment, now); err != nil {
			return err
		}
	}
	if action == "later" {
		if err = postponeReviewTx(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LearningReviewAnswerHistory exposes only recorded answer versions.
func (s *Store) LearningReviewAnswerHistory(ctx context.Context, id string) ([]LearningReviewAnswer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT revision,answer,assessment,state,created_at,note_id FROM learning_review_answers WHERE item_id=? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LearningReviewAnswer
	for rows.Next() {
		var v LearningReviewAnswer
		if e := rows.Scan(&v.Revision, &v.Answer, &v.Assessment, &v.State, &v.CreatedAt, &v.NoteID); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RetryLearningReview makes an explicit new attempt, preserving valid checkpoints and audit.
func (s *Store) RetryLearningReview(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prior, status string
	if err = tx.QueryRowContext(ctx, `SELECT job_id,status FROM learning_review_batches WHERE id=?`, id).Scan(&prior, &status); err != nil {
		return err
	}
	if status != "failed" {
		return ErrConflict
	}
	var snapshot, checkpoint string
	if err = tx.QueryRowContext(ctx, `SELECT input_snapshot_json,checkpoint_json FROM processing_jobs WHERE id=? AND status='failed'`, prior).Scan(&snapshot, &checkpoint); err != nil {
		return err
	}
	var input LearningReviewStageInput
	var cached struct {
		Result *provider.KnowledgeArticleResult `json:"result"`
	}
	if checkpoint != "" && (json.Unmarshal([]byte(snapshot), &input) != nil || json.Unmarshal([]byte(checkpoint), &cached) != nil || provider.ValidateKnowledgeResult(input.Request, cached.Result) != nil) {
		checkpoint = ""
	}
	next := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,checkpoint_json,config_version,configured_provider,configured_model) SELECT ?,source_type,source_id,job_type,'queued',intent_id,input_snapshot_json,?,config_version,configured_provider,configured_model FROM processing_jobs WHERE id=? AND status='failed'`, next, checkpoint, prior); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE learning_review_batches SET status='queued',reason='',job_id=? WHERE id=?`, next, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveLearningReviewNote retains the weekly current-answer CAS interface.
func (s *Store) SaveLearningReviewNote(ctx context.Context, itemID string, expected int) (*models.OwnerNote, error) {
	var revision int
	var state string
	if err := s.DB.QueryRowContext(ctx, `SELECT revision,state FROM learning_review_items WHERE id=?`, itemID).Scan(&revision, &state); err != nil {
		return nil, err
	}
	if revision != expected || state != "answered" {
		return nil, ErrConflict
	}
	return s.saveLearningReviewAnswerNote(ctx, itemID, expected, true)
}

// SaveLearningReviewAnswerNote addresses an immutable answer version, so an old
// session's explicit note can be saved even after another answer is submitted.
func (s *Store) SaveLearningReviewAnswerNote(ctx context.Context, itemID string, revision int) (*models.OwnerNote, error) {
	return s.saveLearningReviewAnswerNote(ctx, itemID, revision, false)
}

func (s *Store) saveLearningReviewAnswerNote(ctx context.Context, itemID string, revision int, requireCurrent bool) (*models.OwnerNote, error) {
	var batchID, answer, ids, input, state, noteID string
	err := s.DB.QueryRowContext(ctx, `SELECT i.batch_id,a.answer,i.material_ids_json,b.input_json,a.state,a.note_id FROM learning_review_answers a JOIN learning_review_items i ON i.id=a.item_id JOIN learning_review_batches b ON b.id=i.batch_id WHERE a.item_id=? AND a.revision=?`, itemID, revision).Scan(&batchID, &answer, &ids, &input, &state, &noteID)
	if err != nil {
		return nil, err
	}
	if state != "answered" || strings.TrimSpace(answer) == "" {
		return nil, ErrConflict
	}
	if noteID != "" {
		return s.GetOwnerNote(ctx, noteID)
	}
	var req provider.KnowledgeArticleRequest
	var wanted []string
	if json.Unmarshal([]byte(input), &req) != nil || json.Unmarshal([]byte(ids), &wanted) != nil {
		return nil, ErrInvalidEditorialState
	}
	selected := selectedKnowledgeMaterials(req.Materials, wanted)
	if len(selected) == 0 {
		return nil, ErrInvalidEditorialState
	}
	m := selected[0]
	anchor := models.NoteAnchor{SnapshotID: m.SnapshotID, Position: m.Position, NoPosition: m.NoPosition, SegmentIDs: []string{}}
	if m.SnapshotID != "" {
		snap, _, _, e := s.SnapshotContent(ctx, m.SnapshotID)
		if e != nil {
			return nil, e
		}
		if snap.SourceID != m.SourceID || string(snap.SourceType) != m.SourceType {
			return nil, ErrConflict
		}
		anchor.Version = snap.ContentVersion
	}
	n, err := s.prepareOwnerNote(ctx, models.OwnerNote{SourceType: m.SourceType, SourceID: m.SourceID, Kind: "owner_reflection", Content: answer, CitationsJSON: "[]", ReferencesJSON: "[]", AnchorJSON: jsonString(anchor)})
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if requireCurrent {
		var current int
		var currentState string
		if err = tx.QueryRowContext(ctx, `SELECT revision,state FROM learning_review_items WHERE id=?`, itemID).Scan(&current, &currentState); err != nil {
			return nil, err
		}
		if current != revision || currentState != "answered" {
			return nil, ErrConflict
		}
	}

	if err = tx.QueryRowContext(ctx, `SELECT note_id FROM learning_review_answers WHERE item_id=? AND revision=? AND state='answered'`, itemID, revision).Scan(&noteID); err != nil {
		return nil, err
	}
	if noteID != "" {
		tx.Rollback()
		return s.GetOwnerNote(ctx, noteID)
	}
	if !validSourceType(models.SourceType(m.SourceType)) {
		return nil, ErrInvalidEditorialState
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+sourceTable(models.SourceType(m.SourceType))+` WHERE id=?`, m.SourceID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists != 1 {
		return nil, ErrNotFound
	}
	noteID = uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO owner_notes(id,source_type,source_id,kind,content,citations_json,references_json,anchor_json)VALUES(?,?,?,'owner_reflection',?,'[]','[]',?)`, noteID, n.SourceType, n.SourceID, n.Content, n.AnchorJSON); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE learning_review_answers SET note_id=? WHERE item_id=? AND revision=?`, noteID, itemID, revision); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE learning_review_items SET note_id=? WHERE id=? AND revision=?`, noteID, itemID, revision); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOwnerNote(ctx, noteID)
}
