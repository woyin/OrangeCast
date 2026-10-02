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

// LegacyStudyTaskVersion identifies the single-source input/checkpoint contract,
// separate from question-study tasks and historic synchronous messages.
const LegacyStudyTaskVersion = "legacy-study-task-v1"

// LegacyStudyJobInput freezes real source/version identities, bounded history,
// QA connection/model, stage estimate and the private candidate awaiting review.
// OriginJobID preserves the paid receipt identity during known-response recovery.
type LegacyStudyJobInput struct {
	Version          string                       `json:"version"`
	Stage            string                       `json:"stage"`
	TurnID           string                       `json:"turn_id"`
	SessionID        string                       `json:"session_id"`
	SourceType       models.SourceType            `json:"source_type"`
	SourceID         string                       `json:"source_id"`
	SnapshotID       string                       `json:"snapshot_id"`
	ContentVersionID string                       `json:"content_version_id"`
	Scope            provider.LegacyStudyScope    `json:"scope"`
	Config           provider.QuestionStudyConfig `json:"config"`
	Estimate         *provider.KnowledgeEstimate  `json:"estimate"`
	Answer           *provider.StudyChatMessage   `json:"answer,omitempty"`
	OriginJobID      string                       `json:"origin_job_id,omitempty"`
}

// Operation returns the stage-specific receipt/budget category, keeping generation
// and review charges distinguishable even when they share a QA model.
func (in LegacyStudyJobInput) Operation() string { return "legacy_study_" + in.Stage }

// Model returns this stage's frozen QA model rather than resolving live settings.
func (in LegacyStudyJobInput) Model() string {
	if in.Stage == "review" {
		return in.Config.ReviewModel
	}
	return in.Config.GenerationModel
}

// LegacyStudyTurn exposes persisted turn/task identities and the current session
// revision. Answer is present only for the atomically adopted visible message.
type LegacyStudyTurn struct {
	ID              string           `json:"turn_id"`
	SessionID       string           `json:"session_id"`
	Revision        int              `json:"revision"`
	State           string           `json:"state"`
	GenerationJobID string           `json:"generation_job_id"`
	CheckJobID      string           `json:"check_job_id"`
	Feedback        string           `json:"scope_feedback,omitempty"`
	Answer          *StudyMessageRow `json:"-"`
}

// GetLegacyStudyTurn reads a turn with its current session revision and adopted
// answer; deleted sessions/turns return ErrNotFound and never reconstruct history.
func (s *Store) GetLegacyStudyTurn(ctx context.Context, id string) (*LegacyStudyTurn, error) {
	t := &LegacyStudyTurn{}
	var answer string
	err := s.DB.QueryRowContext(ctx, `SELECT t.id,t.session_id,s.revision,t.state,COALESCE(t.generation_job_id,''),COALESCE(t.check_job_id,''),t.feedback,COALESCE(t.answer_message_id,'') FROM legacy_study_turns t JOIN study_sessions s ON s.id=t.session_id WHERE t.id=?`, id).Scan(&t.ID, &t.SessionID, &t.Revision, &t.State, &t.GenerationJobID, &t.CheckJobID, &t.Feedback, &answer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if answer != "" {
		t.Answer, err = s.GetStudyMessage(ctx, answer)
	}
	return t, err
}

// LegacyStudyRequest looks up the original command receipt without creating or
// calling a task. The returned job is the receipt's job, not necessarily a later
// review/recovery job; callers inspect the turn's current task identities.
func (s *Store) LegacyStudyRequest(ctx context.Context, key string) (*LegacyStudyTurn, *models.ProcessingJob, error) {
	var turn, job string
	err := s.DB.QueryRowContext(ctx, `SELECT turn_id,job_id FROM legacy_study_requests WHERE request_key=?`, key).Scan(&turn, &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	t, err := s.GetLegacyStudyTurn(ctx, turn)
	if err != nil {
		return nil, nil, err
	}
	j, err := s.GetJob(ctx, job)
	return t, j, err
}
func legacyStudyHash(values ...any) string {
	raw, _ := json.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func validLegacyConfig(cfg provider.QuestionStudyConfig) bool {
	fingerprint, err := hex.DecodeString(cfg.ConnectionID)
	return err == nil && len(fingerprint) == 32 && (cfg.Provider == "groq" || cfg.Provider == "openai") && len(cfg.ConnectionID) == 64 && cfg.GenerationModel != "" && len(cfg.GenerationModel) <= 200 && !strings.ContainsAny(cfg.GenerationModel, "\r\n") && cfg.ReviewModel == cfg.GenerationModel && !cfg.IndependentModel
}

// SubmitLegacyStudyTurn freezes one real source and bounded historic messages.
// The command receipt is read before current configuration or material access.
func (s *Store) SubmitLegacyStudyTurn(ctx context.Context, sourceType models.SourceType, sourceID, sessionID string, expected int, question, key string, cfg provider.QuestionStudyConfig) (*LegacyStudyTurn, *models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 || strings.TrimSpace(question) == "" || len(question) > 8192 || !validLegacyConfig(cfg) || (sourceType != models.SourceEpisode && sourceType != models.SourceUpload) {
		return nil, nil, false, ErrInvalidEditorialState
	}
	hash := legacyStudyHash(sourceType, sourceID, sessionID, expected, question)
	var previous string
	err := s.DB.QueryRowContext(ctx, `SELECT payload_hash FROM legacy_study_requests WHERE request_key=?`, key).Scan(&previous)
	if err == nil {
		if previous != hash {
			return nil, nil, false, ErrConflict
		}
		t, j, e := s.LegacyStudyRequest(ctx, key)
		return t, j, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, err
	}
	// Permission is checked before reading the transcript, including local_only.
	if err = checkQuestionStudySource(ctx, s.DB, QuestionStudySource{string(sourceType), sourceID}, cfg.Provider); err != nil {
		return nil, nil, false, err
	}
	snapshot, err := s.FreezeSourceSnapshot(ctx, sourceType, sourceID)
	if err != nil {
		return nil, nil, false, err
	}
	av, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
	if err != nil {
		return nil, nil, false, err
	}
	if av.ID != snapshot.ContentVersionID || len(av.Payload) > 2*1024*1024 {
		return nil, nil, false, ErrConflict
	}
	var tp provider.TranscriptPayload
	if json.Unmarshal([]byte(av.Payload), &tp) != nil || len(tp.Segments) == 0 || len(tp.Segments) > 10000 {
		return nil, nil, false, ErrInvalidEditorialState
	}
	scope := provider.LegacyStudyScope{Question: question, Segments: tp.Segments}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var oldHash, oldTurn, oldJob string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,turn_id,job_id FROM legacy_study_requests WHERE request_key=?`, key).Scan(&oldHash, &oldTurn, &oldJob)
	if err == nil {
		if oldHash != hash {
			return nil, nil, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, nil, false, err
		}
		t, j, e := s.LegacyStudyRequest(ctx, key)
		return t, j, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, err
	}
	if sessionID == "" {
		if expected != 1 {
			return nil, nil, false, ErrConflict
		}
		sessionID = uuid.NewString()
		title := []rune(question)
		if len(title) > 40 {
			title = title[:40]
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO study_sessions(id,source_type,source_id,title)VALUES(?,?,?,?)`, sessionID, sourceType, sourceID, string(title))
		if err != nil {
			return nil, nil, false, err
		}
	}
	var boundType, boundID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT source_type,source_id,revision FROM study_sessions WHERE id=?`, sessionID).Scan(&boundType, &boundID, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, ErrNotFound
	}
	if err != nil {
		return nil, nil, false, err
	}
	if boundType != string(sourceType) || boundID != sourceID || revision != expected {
		return nil, nil, false, ErrConflict
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_study_turns WHERE session_id=? AND state IN('queued','running','response_saved','checking','unknown'))`, sessionID).Scan(&pending); err != nil {
		return nil, nil, false, err
	}
	if pending {
		return nil, nil, false, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT role,content,reference_segment_ids FROM (SELECT rowid,role,content,reference_segment_ids FROM study_messages WHERE session_id=? AND suppressed=0 ORDER BY rowid DESC LIMIT 6) ORDER BY rowid`, sessionID)
	if err != nil {
		return nil, nil, false, err
	}
	for rows.Next() {
		var m provider.StudyChatMessage
		var refs string
		if err = rows.Scan(&m.Role, &m.Content, &refs); err != nil {
			rows.Close()
			return nil, nil, false, err
		}
		if len(m.Content) > 16384 {
			rows.Close()
			return nil, nil, false, ErrInvalidEditorialState
		}
		_ = json.Unmarshal([]byte(refs), &m.ReferenceSegmentIDs)
		scope.History = append(scope.History, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, false, err
	}
	estimate, err := provider.EstimateLegacyStudy(scope, "generate", nil)
	if err != nil {
		return nil, nil, false, err
	}
	if err = freezeQuestionStudyPrice(ctx, tx, cfg.Provider, cfg.GenerationModel, estimate); err != nil {
		return nil, nil, false, err
	}
	turnID, jobID := uuid.NewString(), uuid.NewString()
	in := LegacyStudyJobInput{Version: LegacyStudyTaskVersion, Stage: "generate", TurnID: turnID, SessionID: sessionID, SourceType: sourceType, SourceID: sourceID, SnapshotID: snapshot.ID, ContentVersionID: av.ID, Scope: scope, Config: cfg, Estimate: estimate}
	if err = checkLegacyStudyScope(ctx, tx, in); err != nil {
		return nil, nil, false, err
	}
	raw, _ := json.Marshal(in)
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,config_version,configured_provider,configured_model,run_lane)VALUES(?,'study_chat',?,'question_study','queued',?,?,?,?,?,'study')`, jobID, sessionID, "legacy-study:"+turnID+":generate", string(raw), in.Version, cfg.Provider, in.Model())
	if err != nil {
		return nil, nil, false, err
	}
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return nil, nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO legacy_study_turns(id,session_id,request_key,generation_job_id)VALUES(?,?,?,?)`, turnID, sessionID, key, jobID)
	if err != nil {
		return nil, nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO legacy_study_requests(request_key,payload_hash,turn_id,job_id)VALUES(?,?,?,?)`, key, hash, turnID, jobID)
	if err != nil {
		return nil, nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO study_messages(id,session_id,role,content)VALUES(?,?,'user',?)`, uuid.NewString(), sessionID, question)
	if err != nil {
		return nil, nil, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=?`, sessionID)
	if err != nil {
		return nil, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	t, j, e := s.LegacyStudyRequest(ctx, key)
	return t, j, true, e
}
func checkLegacyStudyScope(ctx context.Context, q reviewReader, in LegacyStudyJobInput) error {
	if in.Version != LegacyStudyTaskVersion || !validLegacyConfig(in.Config) || (in.Stage != "generate" && in.Stage != "review") || in.Estimate == nil {
		return ErrInvalidEditorialState
	}
	if in.SourceType != models.SourceEpisode && in.SourceType != models.SourceUpload {
		return ErrInvalidEditorialState
	}
	if _, err := provider.LegacyStudyMessages(in.Scope, in.Stage, in.Answer); err != nil {
		return ErrInvalidEditorialState
	}
	if err := checkQuestionStudySource(ctx, q, QuestionStudySource{string(in.SourceType), in.SourceID}, in.Config.Provider); err != nil {
		return err
	}
	var valid bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM source_snapshots s JOIN artifact_versions a ON a.id=s.content_version_id WHERE s.id=? AND s.source_type=? AND s.source_id=? AND s.status!='purged' AND s.content_version_id=? AND a.source_type=s.source_type AND a.source_id=s.source_id AND a.kind='transcript' AND a.version=(SELECT current_transcript_version FROM `+sourceTable(in.SourceType)+` WHERE id=s.source_id))`, in.SnapshotID, in.SourceType, in.SourceID, in.ContentVersionID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

// MarkLegacyStudyCallStarted atomically rechecks source permissions/version and
// run controls before crossing the paid boundary, then marks the active task/turn.
// A stopped, stale or superseded task returns a conflict without authorizing a call.
func (s *Store) MarkLegacyStudyCallStarted(ctx context.Context, jobID string, in LegacyStudyJobInput) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkLegacyStudyScope(ctx, tx, in); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET remote_call_started=1 WHERE id=? AND status='running' AND remote_call_started=0 AND EXISTS(SELECT 1 FROM legacy_study_turns WHERE id=? AND session_id=? AND CASE WHEN ?='review' THEN check_job_id ELSE generation_job_id END=?)`, jobID, in.TurnID, in.SessionID, in.Stage, jobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE legacy_study_turns SET state=CASE WHEN ?='review' THEN 'checking' ELSE 'running' END WHERE id=?`, in.Stage, in.TurnID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SaveLegacyStudyCheckpoint stores a response only when its frozen input still
// matches an active task and no stop was requested. Purge/stop conflicts prevent
// late bodies from attaching; callers can still record numeric usage separately.
func (s *Store) SaveLegacyStudyCheckpoint(ctx context.Context, jobID, frozen, checkpoint string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json=? WHERE id=? AND job_type='question_study' AND source_type='study_chat' AND input_snapshot_json=? AND status IN('queued','running') AND stop_requested=0`, checkpoint, jobID, frozen)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// RecordLegacyStudyReceipt deduplicates numeric usage by the original paid task.
// Cost remains NULL unless usage, returned model and the frozen price are known;
// the receipt does not depend on a surviving source/session or adopt its body.
func (s *Store) RecordLegacyStudyReceipt(ctx context.Context, origin string, in LegacyStudyJobInput, response *provider.QuestionStudyResponse) error {
	if in.Estimate == nil || response == nil || response.InputUnits < 0 || response.OutputUnits < 0 {
		return ErrInvalidEditorialState
	}
	var amount any
	if response.UsageKnown && !response.UnverifiedModel && response.Model == in.Model() {
		if cost, known := in.Estimate.CostForUnits(response.InputUnits, response.OutputUnits); known {
			amount = float64(cost)
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_records(id,operation,provider,model,input_units,output_units,estimated_cost,receipt_id,attempt_id,unit_kind,units_known)VALUES(?,?,?,?,?,?,?,?,?,'tokens',?)`, uuid.NewString(), in.Operation(), in.Config.Provider, response.Model, response.InputUnits, response.OutputUnits, amount, origin+":"+in.Operation(), origin+":response", response.UsageKnown)
	return err
}

// CommitLegacyStudyGeneration keeps the generated body private until an independent check.
func (s *Store) CommitLegacyStudyGeneration(ctx context.Context, jobID string, in LegacyStudyJobInput, result *provider.StudyChatResult) error {
	if in.Stage != "generate" || result == nil {
		return ErrInvalidEditorialState
	}
	if result.Answer != nil {
		raw, _ := json.Marshal(struct {
			Answer string   `json:"answer"`
			IDs    []string `json:"referenceSegmentIds"`
		}{result.Answer.Content, result.Answer.ReferenceSegmentIDs})
		parsed, err := provider.ParseLegacyStudyAnswer(in.Scope, string(raw))
		if err != nil || parsed.Answer == nil {
			return ErrInvalidEditorialState
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkLegacyStudyScope(ctx, tx, in); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT generation_job_id=? AND session_id=? AND state!='accepted' FROM legacy_study_turns WHERE id=?`, jobID, in.SessionID, in.TurnID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrConflict
	}
	if result.Answer == nil {
		_, err = tx.ExecContext(ctx, `UPDATE legacy_study_turns SET state='insufficient',feedback=? WHERE id=?`, result.ScopeFeedback, in.TurnID)
	} else {
		review := in
		review.Stage = "review"
		review.Answer = result.Answer
		review.OriginJobID = ""
		review.Estimate, err = provider.EstimateLegacyStudy(in.Scope, "review", result.Answer)
		if err != nil {
			return err
		}
		if err = freezeQuestionStudyPrice(ctx, tx, in.Config.Provider, review.Model(), review.Estimate); err != nil {
			return err
		}
		raw, _ := json.Marshal(review)
		checkID := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,config_version,configured_provider,configured_model,run_lane)VALUES(?,'study_chat',?,'question_study','queued',?,?,?,?,?,'study')`, checkID, in.SessionID, "legacy-study:"+in.TurnID+":review", string(raw), in.Version, in.Config.Provider, review.Model())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE legacy_study_turns SET state='response_saved',check_job_id=? WHERE id=?`, checkID, in.TurnID)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_state='complete',result_json=? WHERE id=?`, fmt.Sprintf(`{"turn_id":%q,"stage":"generate"}`, in.TurnID), jobID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CommitLegacyStudyReview rechecks scope and controls, then atomically appends the
// message, advances the turn/session and completes the task. Related=false keeps
// the candidate suppressed; related=true alone makes it visible in history.
func (s *Store) CommitLegacyStudyReview(ctx context.Context, jobID string, in LegacyStudyJobInput, check provider.ReferenceCheckResult) error {
	if in.Stage != "review" || in.Answer == nil {
		return ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkLegacyStudyScope(ctx, tx, in); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT check_job_id=? AND session_id=? AND state NOT IN('accepted','insufficient') FROM legacy_study_turns WHERE id=?`, jobID, in.SessionID, in.TurnID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrConflict
	}
	messageID := uuid.NewString()
	refs, _ := json.Marshal(in.Answer.ReferenceSegmentIDs)
	_, err = tx.ExecContext(ctx, `INSERT INTO study_messages(id,session_id,role,content,reference_segment_ids,suppressed)VALUES(?,?,'assistant',?,?,?)`, messageID, in.SessionID, in.Answer.Content, string(refs), !check.Related)
	if err != nil {
		return err
	}
	state, feedback := "accepted", ""
	if !check.Related {
		state = "insufficient"
		feedback = "这个回答似乎脱离了本集内容，请尝试更贴近本集内容的问题。"
		messageID = ""
	}
	_, err = tx.ExecContext(ctx, `UPDATE legacy_study_turns SET state=?,feedback=?,answer_message_id=NULLIF(?,'') WHERE id=?`, state, feedback, messageID, in.TurnID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=?`, in.SessionID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_state='complete',result_json=? WHERE id=?`, fmt.Sprintf(`{"turn_id":%q,"stage":"review"}`, in.TurnID), jobID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// FailLegacyStudy leaves accepted history intact and marks an unfinished active
// turn unknown only after an unstopped paid call without a checkpoint; known
// response failures or stopped tasks become blocked without automatic resending.
func (s *Store) FailLegacyStudy(ctx context.Context, jobID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE legacy_study_turns SET state=CASE WHEN EXISTS(SELECT 1 FROM processing_jobs WHERE id=? AND remote_call_started=1 AND checkpoint_json='' AND stop_requested=0) THEN 'unknown' ELSE 'blocked' END WHERE (generation_job_id=? OR check_job_id=?) AND state NOT IN('accepted','insufficient')`, jobID, jobID, jobID)
	return err
}

// RetryLegacyStudy requires explicit new-attempt consent for an unknown remote
// result; a known response is rebound to a new task and keeps its paid receipt.
func (s *Store) RetryLegacyStudy(ctx context.Context, id, key string, expected int, allowUnknown bool) (*models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 {
		return nil, false, ErrInvalidEditorialState
	}
	receiptKey := "retry:" + key
	hash := legacyStudyHash(id, expected, allowUnknown)
	var stored string
	err := s.DB.QueryRowContext(ctx, `SELECT payload_hash FROM legacy_study_requests WHERE request_key=?`, receiptKey).Scan(&stored)
	if err == nil {
		if stored != hash {
			return nil, false, ErrConflict
		}
		_, job, e := s.LegacyStudyRequest(ctx, receiptKey)
		return job, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var previous, previousJob string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,job_id FROM legacy_study_requests WHERE request_key=?`, receiptKey).Scan(&previous, &previousJob)
	if err == nil {
		if previous != hash {
			return nil, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		job, e := s.GetJob(ctx, previousJob)
		return job, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var status, frozen, checkpoint string
	var revision int
	var started bool
	err = tx.QueryRowContext(ctx, `SELECT status,input_snapshot_json,checkpoint_json,control_revision,remote_call_started FROM processing_jobs WHERE id=? AND source_type='study_chat'`, id).Scan(&status, &frozen, &checkpoint, &revision, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if status != "failed" || revision != expected {
		return nil, false, ErrConflict
	}
	var in LegacyStudyJobInput
	if json.Unmarshal([]byte(frozen), &in) != nil || in.Version != LegacyStudyTaskVersion {
		return nil, false, ErrInvalidEditorialState
	}
	if err = checkLegacyStudyScope(ctx, tx, in); err != nil {
		return nil, false, err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state!='accepted' AND CASE WHEN ?='review' THEN check_job_id ELSE generation_job_id END=? FROM legacy_study_turns WHERE id=?`, in.Stage, id, in.TurnID).Scan(&active); err != nil {
		return nil, false, err
	}
	if !active {
		return nil, false, ErrConflict
	}
	var saved QuestionStudyCheckpoint
	if checkpoint != "" {
		if json.Unmarshal([]byte(checkpoint), &saved) != nil || saved.Version != in.Version || saved.JobID != id || saved.InputHash != fmt.Sprintf("%x", sha256.Sum256([]byte(frozen))) || saved.Response == nil {
			return nil, false, ErrConflict
		}
		if in.OriginJobID == "" {
			in.OriginJobID = id
		}
	} else {
		if started && !allowUnknown {
			return nil, false, fmt.Errorf("%w: 远端结果未知，新尝试可能再次计费", ErrConflict)
		}
		in.OriginJobID = ""
		in.Estimate, err = provider.EstimateLegacyStudy(in.Scope, in.Stage, in.Answer)
		if err != nil {
			return nil, false, err
		}
		if err = freezeQuestionStudyPrice(ctx, tx, in.Config.Provider, in.Model(), in.Estimate); err != nil {
			return nil, false, err
		}
	}
	jobID := uuid.NewString()
	raw, _ := json.Marshal(in)
	restored := ""
	if checkpoint != "" {
		saved.JobID = jobID
		saved.InputHash = fmt.Sprintf("%x", sha256.Sum256(raw))
		encoded, _ := json.Marshal(saved)
		restored = string(encoded)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,checkpoint_json,config_version,configured_provider,configured_model,run_lane)VALUES(?,'study_chat',?,'question_study','queued',?,?,?,?,?,?,'study')`, jobID, in.SessionID, "legacy-study:retry:"+key, string(raw), restored, in.Version, in.Config.Provider, in.Model())
	if err != nil {
		return nil, false, err
	}
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET control_revision=control_revision+1 WHERE id=?`, id)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE legacy_study_turns SET generation_job_id=CASE WHEN ?='generate' THEN ? ELSE generation_job_id END,check_job_id=CASE WHEN ?='review' THEN ? ELSE check_job_id END,state='queued',feedback='' WHERE id=?`, in.Stage, jobID, in.Stage, jobID, in.TurnID)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE study_sessions SET revision=revision+1 WHERE id=?`, in.SessionID)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO legacy_study_requests(request_key,payload_hash,turn_id,job_id)VALUES(?,?,?,?)`, receiptKey, hash, in.TurnID, jobID)
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	job, err := s.GetJob(ctx, jobID)
	return job, true, err
}
