package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const KnowledgeEmbeddingJobVersion = "knowledge-embedding-v1"

type KnowledgeEmbeddingJobInput struct {
	UnknownRetry  bool                        `json:"unknown_retry,omitempty"`
	RetryOfID     string                      `json:"retry_of_id,omitempty"`
	RetryRevision int                         `json:"retry_revision,omitempty"`
	Version       string                      `json:"version"`
	Kind          string                      `json:"kind"`
	Config        provider.EmbeddingConfig    `json:"config"`
	Windows       []EmbeddingWindow           `json:"windows,omitempty"`
	Estimate      *provider.KnowledgeEstimate `json:"estimate"`
	OriginJobID   string                      `json:"origin_job_id,omitempty"`
}

func (in KnowledgeEmbeddingJobInput) Inputs() []string {
	if in.Kind == "preflight" {
		return []string{"个人学习连接预检：保留来源与适用条件。"}
	}
	out := make([]string, len(in.Windows))
	for i, w := range in.Windows {
		out[i] = w.Input
	}
	return out
}
func (in KnowledgeEmbeddingJobInput) Operation() string { return "embedding_" + in.Kind }

// ReserveKnowledgeEmbeddingBatch freezes a complete input and an input-only
// price. Stable receipts cover every terminal state, so the scheduler cannot
// silently recreate a failed/unknown/stopped operation.
func (s *Store) ReserveKnowledgeEmbeddingBatch(ctx context.Context, id string) (*models.ProcessingJob, bool, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, false, err
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if len(windows) == 0 {
		return nil, false, nil
	}
	estimate, err := s.FreezeEmbeddingEstimate(ctx, cfg.EmbeddingConfig, windowInputs(windows))
	if err != nil {
		return nil, false, err
	}
	in := KnowledgeEmbeddingJobInput{Version: KnowledgeEmbeddingJobVersion, Kind: "content", Config: cfg.EmbeddingConfig, Windows: windows, Estimate: estimate}
	identity, _ := json.Marshal([]any{cfg.ID, cfg.Revision, windows})
	return s.reserveKnowledgeEmbeddingJob(ctx, "batch:"+fmt.Sprintf("%x", sha256.Sum256(identity)), in)
}
func windowInputs(windows []EmbeddingWindow) []string {
	out := make([]string, len(windows))
	for i, w := range windows {
		out[i] = w.Input
	}
	return out
}

// ReserveKnowledgeEmbeddingPreflight is an explicit Owner operation; it uses no
// source text and does not enable a measured configuration when it succeeds.
func (s *Store) ReserveKnowledgeEmbeddingPreflight(ctx context.Context, key string, cfg provider.EmbeddingConfig) (*models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil {
		return nil, false, ErrInvalidEditorialState
	}
	in := KnowledgeEmbeddingJobInput{Version: KnowledgeEmbeddingJobVersion, Kind: "preflight", Config: cfg}
	var err error
	in.Estimate, err = s.FreezeEmbeddingEstimate(ctx, cfg, in.Inputs())
	if err != nil {
		return nil, false, err
	}
	return s.reserveKnowledgeEmbeddingJob(ctx, "preflight:"+key, in)
}

func (s *Store) reserveKnowledgeEmbeddingJob(ctx context.Context, key string, in KnowledgeEmbeddingJobInput, checkpoints ...string) (*models.ProcessingJob, bool, error) {
	if in.Version != KnowledgeEmbeddingJobVersion || (in.Kind != "content" && in.Kind != "preflight") || in.Config.Provider == "" || in.Config.Model == "" || in.Estimate == nil {
		return nil, false, ErrInvalidEditorialState
	}
	payload, _ := json.Marshal([]any{in.Version, in.Kind, in.Config, in.Windows, in.RetryOfID, in.RetryRevision, in.OriginJobID, in.UnknownRetry})
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	raw, _ := json.Marshal(in)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var prior, priorHash string
	err = tx.QueryRowContext(ctx, `SELECT job_id,payload_hash FROM knowledge_embedding_requests WHERE request_key=?`, key).Scan(&prior, &priorHash)
	if err == nil {
		if priorHash != hash {
			return nil, false, ErrConflict
		}
		tx.Rollback()
		job, err := s.GetJob(ctx, prior)
		return job, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if in.RetryOfID != "" {
		updated, e := tx.ExecContext(ctx, `UPDATE processing_jobs SET control_revision=control_revision+1 WHERE id=? AND status='failed' AND control_revision=?`, in.RetryOfID, in.RetryRevision)
		if e != nil {
			return nil, false, e
		}
		n, _ := updated.RowsAffected()
		if n != 1 {
			return nil, false, ErrConflict
		}
	}
	if in.Kind == "content" && len(checkpoints) == 0 {
		if err = validateEmbeddingWindows(ctx, tx, in); err != nil {
			return nil, false, err
		}
	}
	id := uuid.NewString()
	checkpoint := ""
	if len(checkpoints) == 1 {
		var cp embeddingStoredCheckpoint
		if json.Unmarshal([]byte(checkpoints[0]), &cp) != nil {
			return nil, false, ErrInvalidEditorialState
		}
		cp.JobID = id
		cp.InputFingerprint = fmt.Sprintf("%x", sha256.Sum256(raw))
		b, _ := json.Marshal(cp)
		checkpoint = string(b)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,configured_provider,configured_model,config_version,checkpoint_json)VALUES(?,'knowledge_index',?,'knowledge_embedding','queued',?,?,?,?,?,?)`, id, in.Config.ID, key, string(raw), in.Config.Provider, in.Config.Model, in.Version, checkpoint); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_requests(request_key,payload_hash,job_id)VALUES(?,?,?)`, key, hash, id); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	job, err := s.GetJob(ctx, id)
	return job, true, err
}

// These readers are deliberately transactional at the paid-call boundary.
func validateEmbeddingWindows(ctx context.Context, tx *sql.Tx, in KnowledgeEmbeddingJobInput) error {
	var enabled bool
	var revision, dimensions int
	var model, connection string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,revision,dimensions,model,connection_id FROM knowledge_embedding_configs WHERE id=?`, in.Config.ID).Scan(&enabled, &revision, &dimensions, &model, &connection); err != nil {
		return err
	}
	if !enabled || in.Config.Dimensions != dimensions || in.Config.Model != model || in.Config.ConnectionID != connection {
		return ErrConflict
	}
	if len(in.Windows) < 1 || len(in.Windows) > provider.EmbeddingMaxBatch {
		return ErrInvalidEditorialState
	}
	cfg := &KnowledgeEmbeddingConfig{EmbeddingConfig: in.Config, Enabled: enabled, Revision: revision}
	for _, window := range in.Windows {
		if window.ScopeRevision != revision {
			return ErrConflict
		}
		args := embeddingSQLArgs(cfg)
		args = append(args, sql.Named("key", window.DocKey))
		var d embeddingDocument
		if err := tx.QueryRowContext(ctx, `SELECT d.key,d.revision,d.title,d.body,d.kind,d.object_id,d.source_type,d.source_id FROM knowledge_search_docs d WHERE d.key=:key AND `+embeddingDocumentQualified(), args...).Scan(&d.key, &d.revision, &d.title, &d.body, &d.kind, &d.objectID, &d.sourceType, &d.sourceID); err != nil {
			return err
		}
		matches, err := embeddingSourcesMatch(ctx, tx, d, window.Sources)
		if err != nil {
			return err
		}
		if !matches {
			return ErrConflict
		}
		current, err := embeddingWindows(d.key, d.revision, d.title, d.body, revision)
		if err != nil || window.WindowNo < 0 || window.WindowNo >= len(current) || current[window.WindowNo].ContentHash != window.ContentHash || current[window.WindowNo].Input != window.Input {
			return ErrConflict
		}
	}
	return nil
}

func (s *Store) MarkKnowledgeEmbeddingCallStarted(ctx context.Context, id string, in KnowledgeEmbeddingJobInput) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, id); err != nil {
		return err
	}
	if in.Kind == "content" {
		if err = validateEmbeddingWindows(ctx, tx, in); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET remote_call_started=1,updated_at=datetime('now') WHERE id=? AND status IN('queued','running') AND remote_call_started=0`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// CommitKnowledgeEmbeddingResponse saves the application result together with
// the index transaction. Paid checkpoints and receipts exist before this call.
func (s *Store) CommitKnowledgeEmbeddingResponse(ctx context.Context, id string, in KnowledgeEmbeddingJobInput, result *provider.EmbeddingResult) error {
	if in.Kind == "content" {
		_, err := s.adoptKnowledgeEmbeddings(ctx, in.Config.ID, in.Windows, result, id)
		return err
	}
	if in.Kind != "preflight" || provider.ValidateEmbeddingResult(result, in.Config.Model, 1, in.Config.Dimensions) != nil {
		return ErrInvalidEditorialState
	}
	measured, err := in.Config.WithDimensions(result.Dimensions)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_configs(id,connection_id,provider,model,dimensions,unit)VALUES(?,?,?,?,?,?) ON CONFLICT(id)DO NOTHING`, measured.ID, measured.ConnectionID, measured.Provider, measured.Model, measured.Dimensions, measured.Unit); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"config_id": measured.ID, "dimensions": measured.Dimensions, "indexing_enabled": false})
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete',updated_at=datetime('now') WHERE id=? AND status IN('running','queued') AND result_state!='complete'`, string(raw), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// RecordEmbeddingReceipt stores actual unit availability separately from price.
func (s *Store) RecordEmbeddingReceipt(ctx context.Context, origin string, in KnowledgeEmbeddingJobInput, result *provider.EmbeddingResult) error {
	if result == nil || result.InputTokens < 0 || result.Model != in.Config.Model {
		return ErrInvalidEditorialState
	}
	cost, known := int64(0), false
	if result.UsageKnown {
		cost, known = in.Estimate.CostForUnits(result.InputTokens, 0)
	}
	var amount any
	if known {
		amount = float64(cost)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_records(id,operation,provider,model,input_units,output_units,estimated_cost,receipt_id,attempt_id,unit_kind,units_known)VALUES(?,?,?,?,?,0,?,?,?,'input_tokens',?)`, uuid.NewString(), in.Operation(), in.Config.Provider, result.Model, result.InputTokens, amount, origin+":"+in.Operation(), origin+":response", result.UsageKnown)
	return err
}

// EnabledKnowledgeEmbeddingConfigs only enumerates matching, Owner-enabled
// connections. A different endpoint/model cannot pick up an old automatic scope.
func (s *Store) EnabledKnowledgeEmbeddingConfigs(ctx context.Context, route provider.EmbeddingConfig) ([]*KnowledgeEmbeddingConfig, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,connection_id,provider,model,dimensions,unit,enabled,revision FROM knowledge_embedding_configs WHERE enabled=1 AND connection_id=? AND model=? AND (?=0 OR dimensions=?) AND NOT EXISTS(SELECT 1 FROM run_controls c WHERE c.kind='lane' AND c.target='index' AND c.paused=1) ORDER BY updated_at,id LIMIT 8`, route.ConnectionID, route.Model, route.Dimensions, route.Dimensions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnowledgeEmbeddingConfig
	for rows.Next() {
		c := &KnowledgeEmbeddingConfig{}
		if err = rows.Scan(&c.ID, &c.ConnectionID, &c.Provider, &c.Model, &c.Dimensions, &c.Unit, &c.Enabled, &c.Revision); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// The persisted shape is shared by recovery without exposing a queue dependency.
type embeddingStoredCheckpoint struct {
	Version          string                    `json:"version"`
	JobID            string                    `json:"job_id"`
	InputFingerprint string                    `json:"input_fingerprint"`
	Result           *provider.EmbeddingResult `json:"result"`
}

// RetryKnowledgeEmbeddingJob is an explicit CAS operation. Known responses carry
// their original paid identity; unknown results require a separate affirmative flag.
func (s *Store) RetryKnowledgeEmbeddingJob(ctx context.Context, id, key string, expected int, allowUnknown bool) (*models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil || expected < 1 {
		return nil, false, ErrInvalidEditorialState
	}
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if job.JobType != models.JobKnowledgeEmbedding {
		return nil, false, ErrInvalidEditorialState
	}
	ex, err := s.GetJobExecution(ctx, id)
	if err != nil {
		return nil, false, err
	}
	var in KnowledgeEmbeddingJobInput
	if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil || in.Version != KnowledgeEmbeddingJobVersion {
		return nil, false, ErrInvalidEditorialState
	}
	// Read-only replay detection precedes CAS so a lost acknowledgement can recover.
	var old string
	err = s.DB.QueryRowContext(ctx, `SELECT job_id FROM knowledge_embedding_requests WHERE request_key=?`, "retry:"+key).Scan(&old)
	if err == nil {
		var raw string
		if err = s.DB.QueryRowContext(ctx, `SELECT input_snapshot_json FROM processing_jobs WHERE id=?`, old).Scan(&raw); err != nil {
			return nil, false, err
		}
		var prior KnowledgeEmbeddingJobInput
		if json.Unmarshal([]byte(raw), &prior) != nil || prior.RetryOfID != id || prior.RetryRevision != expected || prior.UnknownRetry != (ex.CheckpointJSON == "" && ex.RemoteCallStarted && allowUnknown) {
			return nil, false, ErrConflict
		}
		replay, e := s.GetJob(ctx, old)
		return replay, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if job.Status != models.StatusFailed {
		return nil, false, ErrConflict
	}
	in.RetryOfID = id
	in.RetryRevision = expected
	in.UnknownRetry = ex.CheckpointJSON == "" && ex.RemoteCallStarted && allowUnknown
	if ex.CheckpointJSON != "" {
		var cp embeddingStoredCheckpoint
		if json.Unmarshal([]byte(ex.CheckpointJSON), &cp) != nil || cp.JobID != id || cp.Version != in.Version || cp.InputFingerprint != fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON))) || provider.ValidateEmbeddingResult(cp.Result, in.Config.Model, len(in.Inputs()), in.Config.Dimensions) != nil {
			return nil, false, ErrInvalidEditorialState
		}
		if in.OriginJobID == "" {
			in.OriginJobID = id
		}
		return s.reserveKnowledgeEmbeddingJob(ctx, "retry:"+key, in, ex.CheckpointJSON)
	}
	if ex.RemoteCallStarted && !allowUnknown {
		return nil, false, ErrConflict
	}
	// Fresh dispatch freezes today's exact price instead of pretending an old
	// unknown/unpriced operation has become a known free call.
	in.OriginJobID = ""
	in.Estimate, err = s.FreezeEmbeddingEstimate(ctx, in.Config, in.Inputs())
	if err != nil {
		return nil, false, err
	}
	return s.reserveKnowledgeEmbeddingJob(ctx, "retry:"+key, in)
}

// Source purge can race a supplier response. The frozen-input comparison ensures
// a redacted task never regains a derived vector checkpoint after purge.
func (s *Store) SaveKnowledgeEmbeddingCheckpoint(ctx context.Context, id, frozenInput, checkpoint string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET checkpoint_json=?,updated_at=datetime('now') WHERE id=? AND job_type='knowledge_embedding' AND input_snapshot_json=? AND status IN('queued','running')`, checkpoint, id, frozenInput)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
