package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const QuestionStudyTaskVersion = "question-study-task-v1"

type QuestionStudyJobInput struct {
	Version      string                       `json:"version"`
	Stage        string                       `json:"stage"`
	TurnID       string                       `json:"turn_id"`
	Scope        provider.QuestionStudyScope  `json:"scope"`
	Config       provider.QuestionStudyConfig `json:"config"`
	Estimate     *provider.KnowledgeEstimate  `json:"estimate"`
	ReviewIntent string                       `json:"review_intent"`
	OriginJobID  string                       `json:"origin_job_id,omitempty"`
}

func (in QuestionStudyJobInput) Operation() string { return "question_study_" + in.Stage }
func (in QuestionStudyJobInput) Model() string {
	if in.Stage == "review" {
		return in.Config.ReviewModel
	}
	return in.Config.GenerationModel
}

type QuestionStudyCheckpoint struct {
	Version   string                          `json:"version"`
	JobID     string                          `json:"job_id"`
	InputHash string                          `json:"input_hash"`
	Response  *provider.QuestionStudyResponse `json:"response"`
}

func freezeQuestionStudyPrice(ctx context.Context, tx *sql.Tx, name, model string, estimate *provider.KnowledgeEstimate) error {
	err := tx.QueryRowContext(ctx, `SELECT input_cents_per_million,output_cents_per_million,updated_at FROM model_prices WHERE provider=? AND model=?`, name, model).Scan(&estimate.InputCentsPerMillion, &estimate.OutputCentsPerMillion, &estimate.PriceUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	estimate.PriceKnown = err == nil
	if cost, known := estimate.CostForUnits(estimate.InputTokens, estimate.OutputTokens); known {
		estimate.CostCents = &cost
	}
	return nil
}
func validateQuestionStudyConfig(cfg provider.QuestionStudyConfig) error {
	fingerprint, err := hex.DecodeString(cfg.ConnectionID)
	if err != nil || len(fingerprint) != 32 || cfg.Provider != "pod" || cfg.GenerationModel == "" || cfg.ReviewModel == "" || len(cfg.GenerationModel) > 200 || len(cfg.ReviewModel) > 200 || strings.ContainsAny(cfg.GenerationModel+cfg.ReviewModel, "\r\n") || cfg.IndependentModel != (cfg.GenerationModel != cfg.ReviewModel) {
		return ErrInvalidEditorialState
	}
	return nil
}

// Submit stores the command receipt, session revision, frozen input and task in
// one transaction. A repeated command reuses the original frozen scope even if
// materials have subsequently changed.
func (s *Store) SubmitQuestionStudyTurn(ctx context.Context, sessionID string, expected int, input, key string, selected []string, cfg provider.QuestionStudyConfig) (*QuestionStudyTurn, *models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 {
		return nil, nil, false, ErrInvalidEditorialState
	}
	if err := validateQuestionStudyConfig(cfg); err != nil {
		return nil, nil, false, err
	}
	selected = append([]string(nil), selected...)
	sort.Strings(selected)
	command, _ := json.Marshal([]any{sessionID, expected, input, selected, cfg})
	hash := fmt.Sprintf("%x", sha256.Sum256(command))
	if turn, job, err := s.questionStudyRequest(ctx, key, hash); err == nil {
		return turn, job, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, nil, false, err
	}
	scope, err := s.FreezeQuestionStudyScope(ctx, sessionID, input, cfg.Provider, selected)
	if err != nil {
		return nil, nil, false, err
	}
	estimate, err := provider.EstimateQuestionStudy(*scope)
	if err != nil {
		return nil, nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var previousHash, turnID, jobID string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,turn_id,job_id FROM question_study_requests WHERE request_key=?`, key).Scan(&previousHash, &turnID, &jobID)
	if err == nil {
		if hash != previousHash {
			return nil, nil, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, nil, false, err
		}
		turn, err := s.GetQuestionStudyTurn(ctx, turnID)
		if err != nil {
			return nil, nil, false, err
		}
		job, err := s.GetJob(ctx, jobID)
		return turn, job, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, err
	}
	if err = checkQuestionAdmission(ctx, tx, scope.Question, false, true); err != nil {
		return nil, nil, false, err
	}
	if err = checkQuestionStudyScope(ctx, tx, *scope, cfg.Provider); err != nil {
		return nil, nil, false, err
	}
	if err = freezeQuestionStudyPrice(ctx, tx, cfg.Provider, cfg.GenerationModel, estimate); err != nil {
		return nil, nil, false, err
	}
	frozenScope, _ := json.Marshal(scope)
	var sources []QuestionStudySource
	seen := map[QuestionStudySource]bool{}
	for _, material := range scope.Materials {
		source := QuestionStudySource{material.SourceType, material.SourceID}
		if !seen[source] {
			sources = append(sources, source)
			seen[source] = true
		}
	}
	for _, history := range scope.History {
		for _, dependency := range history.SourceDependencies {
			source := QuestionStudySource{dependency.SourceType, dependency.SourceID}
			if !seen[source] {
				sources = append(sources, source)
				seen[source] = true
			}
		}
	}
	for _, source := range sources {
		if err = checkQuestionStudySource(ctx, tx, source, cfg.Provider); err != nil {
			return nil, nil, false, err
		}
	}
	turn, _, err := reserveQuestionStudyTurn(ctx, tx, sessionID, expected, input, key, string(frozenScope), sources)
	if err != nil {
		return nil, nil, false, err
	}
	jobID = uuid.NewString()
	in := QuestionStudyJobInput{Version: QuestionStudyTaskVersion, Stage: "generate", TurnID: turn.ID, Scope: *scope, Config: cfg, Estimate: estimate, ReviewIntent: "question-study:" + turn.ID + ":review"}
	raw, _ := json.Marshal(in)
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,config_version,configured_provider,configured_model,run_lane)VALUES(?,'question_study',?,'question_study','queued',?,?,?,?,?,'study')`, jobID, scope.Question.ID, "question-study:"+turn.ID+":generate", string(raw), in.Version, cfg.Provider, cfg.GenerationModel)
	if err != nil {
		return nil, nil, false, err
	}
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return nil, nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_turns SET generation_job_id=? WHERE id=?`, jobID, turn.ID); err != nil {
		return nil, nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO question_study_requests(request_key,payload_hash,turn_id,job_id)VALUES(?,?,?,?)`, key, hash, turn.ID, jobID); err != nil {
		return nil, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	turn, err = s.GetQuestionStudyTurn(ctx, turn.ID)
	if err != nil {
		return nil, nil, false, err
	}
	job, err := s.GetJob(ctx, jobID)
	return turn, job, true, err
}
func (s *Store) questionStudyRequest(ctx context.Context, key, hash string) (*QuestionStudyTurn, *models.ProcessingJob, error) {
	var stored, turnID, jobID string
	err := s.DB.QueryRowContext(ctx, `SELECT payload_hash,turn_id,job_id FROM question_study_requests WHERE request_key=?`, key).Scan(&stored, &turnID, &jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if stored != hash {
		return nil, nil, ErrConflict
	}
	turn, err := s.GetQuestionStudyTurn(ctx, turnID)
	if err != nil {
		return nil, nil, err
	}
	job, err := s.GetJob(ctx, jobID)
	return turn, job, err
}

// Recheck membership and current material identities on the same connection used
// for admission/adoption. Provider authorization is checked before reading bodies.
func checkQuestionStudyScope(ctx context.Context, q reviewReader, scope provider.QuestionStudyScope, name string) error {
	if _, err := provider.QuestionStudyMessages(scope); err != nil {
		return ErrInvalidEditorialState
	}
	var questionExists int
	if err := q.QueryRowContext(ctx, `SELECT 1 FROM learning_questions WHERE id=?`, scope.Question.ID).Scan(&questionExists); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	for _, material := range scope.Materials {
		if !validSourceType(models.SourceType(material.SourceType)) {
			return ErrInvalidEditorialState
		}
		var allowed bool
		err := q.QueryRowContext(ctx, `SELECT archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots WHERE source_type=? AND source_id=? AND status='purged') AND (model_data_policy='external_allowed' OR (model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(approved_providers_json) WHERE lower(trim(value))=lower(trim(?))))) FROM `+sourceTable(models.SourceType(material.SourceType))+` WHERE id=?`, material.SourceType, material.SourceID, name, material.SourceID).Scan(&allowed)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		var body, kind, objectID, sourceType, sourceID string
		var revision int
		err = q.QueryRowContext(ctx, `SELECT substr(body,1,10241),kind,object_id,revision,source_type,source_id FROM knowledge_search_docs WHERE key=? AND visibility='current'`, material.Key).Scan(&body, &kind, &objectID, &revision, &sourceType, &sourceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if revision != material.Revision || body != material.Content || kind != material.Kind || sourceType != material.SourceType || sourceID != material.SourceID {
			return ErrConflict
		}
		var confirmed bool
		err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM learning_question_links l WHERE l.question_id=? AND l.state='confirmed' AND l.origin!='generation' AND ((l.kind IN('source','evidence') AND l.source_type=? AND l.source_id=?) OR (l.kind='note' AND ? IN('source_note','owner_reflection') AND l.object_id=?) OR (l.kind='keypoint' AND ?='keypoint' AND l.object_id=?) OR (l.kind='article' AND EXISTS(SELECT 1 FROM knowledge_articles a,json_each(a.input_json,'$.materials') m WHERE a.id=l.object_id AND json_extract(m.value,'$.id')=?))))`, scope.Question.ID, material.SourceType, material.SourceID, kind, objectID, kind, objectID, objectID).Scan(&confirmed)
		if err != nil {
			return err
		}
		if !confirmed {
			return ErrConflict
		}
		if kind == "source_note" || kind == "owner_reflection" || kind == "keypoint" {
			var raw, anchor string
			if kind == "keypoint" {
				err = q.QueryRowContext(ctx, `SELECT citations_json,'' FROM keypoint_index WHERE id=?`, objectID).Scan(&raw, &anchor)
			} else {
				err = q.QueryRowContext(ctx, `SELECT CASE WHEN kind='owner_reflection' THEN references_json ELSE citations_json END,anchor_json FROM owner_notes WHERE id=?`, objectID).Scan(&raw, &anchor)
			}
			if err != nil {
				return err
			}
			var refs []string
			if json.Unmarshal([]byte(raw), &refs) != nil || len(refs) != len(material.Segments) {
				return ErrConflict
			}
			for i, id := range refs {
				if material.Segments[i].SegmentID != id {
					return ErrConflict
				}
			}
			if strings.TrimSpace(anchor) != "" {
				var capture models.NoteAnchor
				if json.Unmarshal([]byte(anchor), &capture) != nil {
					return ErrConflict
				}
				if capture.SnapshotID != "" && capture.SnapshotID != material.SnapshotID && len(refs) > 0 {
					return ErrConflict
				}
			}
		}
		if material.SnapshotID != "" {
			var valid bool
			err = q.QueryRowContext(ctx, `SELECT status!='purged' AND source_type=? AND source_id=? FROM source_snapshots WHERE id=?`, material.SourceType, material.SourceID, material.SnapshotID).Scan(&valid)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !valid {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		}
		if kind == "keypoint" {
			var valid bool
			err = q.QueryRowContext(ctx, `SELECT stale_at IS NULL AND evidence_status!='stale' AND production_status!='dismissed' AND quality_status IN('ready','owner_confirmed') FROM keypoint_index WHERE id=?`, objectID).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return ErrConflict
			}
		}
	}
	return nil
}
func (s *Store) MarkQuestionStudyCallStarted(ctx context.Context, jobID string, in QuestionStudyJobInput) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkQuestionStudyDependencies(ctx, tx, in.TurnID, in.Config.Provider); err != nil {
		return err
	}
	if err = checkQuestionStudyScope(ctx, tx, in.Scope, in.Config.Provider); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET remote_call_started=1 WHERE id=? AND status='running' AND remote_call_started=0 AND EXISTS(SELECT 1 FROM question_study_turns WHERE id=? AND generation_job_id=? AND purged=0)`, jobID, in.TurnID, jobID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_turns SET state='running',updated_at=datetime('now') WHERE id=?`, in.TurnID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SaveQuestionStudyCheckpoint(ctx context.Context, id, frozen, checkpoint string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json=? WHERE id=? AND job_type='question_study' AND input_snapshot_json=? AND status IN('queued','running')`, checkpoint, id, frozen)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) RecordQuestionStudyReceipt(ctx context.Context, origin string, in QuestionStudyJobInput, response *provider.QuestionStudyResponse) error {
	if response == nil || response.InputUnits < 0 || response.OutputUnits < 0 {
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
func (s *Store) CommitQuestionStudyGeneration(ctx context.Context, jobID string, in QuestionStudyJobInput, answer provider.QuestionStudyAnswer) error {
	if err := provider.ValidateQuestionStudyAnswer(in.Scope, answer); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return err
	}
	if err = checkQuestionStudyDependencies(ctx, tx, in.TurnID, in.Config.Provider); err != nil {
		return err
	}
	if err = checkQuestionStudyScope(ctx, tx, in.Scope, in.Config.Provider); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE question_study_turns SET state='response_saved',updated_at=datetime('now') WHERE id=? AND generation_job_id=? AND purged=0`, in.TurnID, jobID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	raw, _ := json.Marshal(map[string]string{"turn_id": in.TurnID, "stage": "generate", "review_intent": in.ReviewIntent})
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, string(raw), jobID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FailQuestionStudyGeneration(ctx context.Context, jobID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE question_study_turns SET state=CASE WHEN EXISTS(SELECT 1 FROM processing_jobs WHERE id=? AND remote_call_started=1 AND checkpoint_json='') THEN 'unknown' ELSE 'blocked' END,updated_at=datetime('now') WHERE generation_job_id=? AND purged=0 AND state!='accepted'`, jobID, jobID)
	return err
}

// Explicit recovery reuses a known response's paid origin. Without a response,
// crossing the remote boundary requires separate consent to a new paid attempt.
func (s *Store) RetryQuestionStudyGeneration(ctx context.Context, id, key string, expected int, allowUnknown bool) (*models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 {
		return nil, false, ErrInvalidEditorialState
	}
	command, _ := json.Marshal([]any{id, expected, allowUnknown})
	hash := fmt.Sprintf("%x", sha256.Sum256(command))
	receiptKey := "retry:" + key
	if _, job, err := s.questionStudyRequest(ctx, receiptKey, hash); err == nil {
		return job, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var stored, existingJob string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,job_id FROM question_study_requests WHERE request_key=?`, receiptKey).Scan(&stored, &existingJob)
	if err == nil {
		if stored != hash {
			return nil, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		job, err := s.GetJob(ctx, existingJob)
		return job, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var status, frozen, checkpoint string
	var revision int
	var started bool
	err = tx.QueryRowContext(ctx, `SELECT status,input_snapshot_json,checkpoint_json,control_revision,remote_call_started FROM processing_jobs WHERE id=? AND job_type='question_study'`, id).Scan(&status, &frozen, &checkpoint, &revision, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if status != "failed" || revision != expected {
		return nil, false, ErrConflict
	}
	var in QuestionStudyJobInput
	if json.Unmarshal([]byte(frozen), &in) != nil || in.Version != QuestionStudyTaskVersion || in.Stage != "generate" || in.Scope.Question == nil {
		return nil, false, ErrInvalidEditorialState
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT purged=0 AND generation_job_id=? FROM question_study_turns WHERE id=?`, id, in.TurnID).Scan(&active); err != nil {
		return nil, false, err
	}
	if !active {
		return nil, false, ErrConflict
	}
	if err = checkQuestionStudyDependencies(ctx, tx, in.TurnID, in.Config.Provider); err != nil {
		return nil, false, err
	}
	if err = checkQuestionStudyScope(ctx, tx, in.Scope, in.Config.Provider); err != nil {
		return nil, false, err
	}
	var saved QuestionStudyCheckpoint
	if checkpoint != "" {
		inputHash := fmt.Sprintf("%x", sha256.Sum256([]byte(frozen)))
		if json.Unmarshal([]byte(checkpoint), &saved) != nil || saved.Version != in.Version || saved.JobID != id || saved.InputHash != inputHash || saved.Response == nil {
			return nil, false, ErrConflict
		}
		if in.OriginJobID == "" {
			in.OriginJobID = id
		}
	} else {
		if started && !allowUnknown {
			return nil, false, fmt.Errorf("%w: 远端结果未知，重发可能再次计费，需要明确确认", ErrConflict)
		}
		in.OriginJobID = ""
		in.Estimate, err = provider.EstimateQuestionStudy(in.Scope)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,checkpoint_json,config_version,configured_provider,configured_model,run_lane)VALUES(?,'question_study',?,'question_study','queued',?,?,?,?,?,?,'study')`, jobID, in.Scope.Question.ID, "question-study:retry:"+key, string(raw), restored, in.Version, in.Config.Provider, in.Model())
	if err != nil {
		return nil, false, err
	}
	if err = checkRunControl(ctx, tx, jobID); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET control_revision=control_revision+1 WHERE id=?`, id); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_turns SET generation_job_id=?,state='queued',updated_at=datetime('now') WHERE id=?`, jobID, in.TurnID); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_study_sessions SET revision=revision+1,updated_at=datetime('now') WHERE id=(SELECT session_id FROM question_study_turns WHERE id=?)`, in.TurnID); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO question_study_requests(request_key,payload_hash,turn_id,job_id)VALUES(?,?,?,?)`, receiptKey, hash, in.TurnID, jobID); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	job, err := s.GetJob(ctx, jobID)
	return job, true, err
}

func checkQuestionStudySource(ctx context.Context, q reviewReader, source QuestionStudySource, name string) error {
	if !validSourceType(models.SourceType(source.SourceType)) {
		return ErrInvalidEditorialState
	}
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots WHERE source_type=? AND source_id=? AND status='purged') AND (model_data_policy='external_allowed' OR (model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(approved_providers_json) WHERE lower(trim(value))=lower(trim(?))))) FROM `+sourceTable(models.SourceType(source.SourceType))+` WHERE id=?`, source.SourceType, source.SourceID, name, source.SourceID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return ErrConflict
	}
	return err
}
func checkQuestionStudyDependencies(ctx context.Context, q reviewReader, turnID, name string) error {
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(json_group_array(json_object('source_type',source_type,'source_id',source_id)),'[]') FROM question_study_sources WHERE turn_id=?`, turnID).Scan(&raw); err != nil {
		return err
	}
	var sources []provider.QuestionStudySourceDependency
	if json.Unmarshal([]byte(raw), &sources) != nil || len(sources) == 0 || len(sources) > 96 {
		return ErrInvalidEditorialState
	}
	for _, source := range sources {
		if err := checkQuestionStudySource(ctx, q, QuestionStudySource{source.SourceType, source.SourceID}, name); err != nil {
			return err
		}
	}
	return nil
}
