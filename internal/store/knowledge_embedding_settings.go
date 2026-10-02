package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// A report cannot survive a different server executable or target machine.
// Hash once: retrieval never shells out, reads source files, or repeatedly hashes code.
var embeddingBuildIdentity struct {
	sync.Once
	value string
	err   error
}

func embeddingRuntimeIdentity() (string, error) {
	embeddingBuildIdentity.Do(func() {
		path, err := os.Executable()
		if err != nil {
			embeddingBuildIdentity.err = err
			return
		}
		f, err := os.Open(path)
		if err != nil {
			embeddingBuildIdentity.err = err
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			embeddingBuildIdentity.err = err
			return
		}
		hostname, err := os.Hostname()
		if err != nil {
			embeddingBuildIdentity.err = err
			return
		}
		embeddingBuildIdentity.value = fmt.Sprintf("%x:%s:%s:%s:%s:%d:%s", h.Sum(nil), runtime.Version(), runtime.GOOS, runtime.GOARCH, hostname, runtime.NumCPU(), embeddingMachineCPUIdentity())
	})
	return embeddingBuildIdentity.value, embeddingBuildIdentity.err
}

// EmbeddingSettingsCommand changes Owner scope, capacity and exact input-only price atomically.
type EmbeddingSettingsCommand struct {
	ConfigID                 string            `json:"config_id"`
	ExpectedRevision         int               `json:"expected_revision"`
	IndexAuthorized          bool              `json:"index_authorized"`
	WindowCapacity           int               `json:"window_capacity"`
	InputCentsPerMillion     *int64            `json:"input_cents_per_million"`
	UnderstandingSnapshotIDs []string          `json:"understanding_snapshot_ids"`
	Sources                  []EmbeddingSource `json:"sources"`
}

// EmbeddingQualityReport is a frozen measurement artifact, never a generated model opinion.
// Import requires Owner-attested real measurements; synthetic fixtures establish only gate behavior.
type EmbeddingQualityReport struct {
	ID                      string  `json:"id"`
	Identity                string  `json:"identity"`
	QueryManifestSHA256     string  `json:"query_manifest_sha256"`
	CorpusManifestSHA256    string  `json:"corpus_manifest_sha256"`
	RelevanceManifestSHA256 string  `json:"relevance_manifest_sha256"`
	SourceRevision          string  `json:"source_revision"`
	Machine                 string  `json:"machine"`
	Method                  string  `json:"method"`
	Fusion                  string  `json:"fusion"`
	RealMeasurements        bool    `json:"real_measurements"`
	PerformanceDimensions   int     `json:"performance_dimensions"`
	Windows10k              int     `json:"windows_10k"`
	Windows50k              int     `json:"windows_50k"`
	Queries                 int     `json:"queries"`
	FTSRecall10             float64 `json:"fts_recall10"`
	HybridRecall10          float64 `json:"hybrid_recall10"`
	RewriteFTSRecall10      float64 `json:"rewrite_fts_recall10"`
	RewriteHybridRecall10   float64 `json:"rewrite_hybrid_recall10"`
	Cold10kP95MS            float64 `json:"cold_10k_p95_ms"`
	Cold50kP95MS            float64 `json:"cold_50k_p95_ms"`
	Warm10kP95MS            float64 `json:"warm_10k_p95_ms"`
	Warm50kP95MS            float64 `json:"warm_50k_p95_ms"`
	PeakRSSBytes            int64   `json:"peak_rss_bytes"`
	EstimatedCents          int64   `json:"estimated_cents"`
	EstimatedCostKnown      bool    `json:"estimated_cost_known"`
	ActualCostKnown         bool    `json:"actual_cost_known"`
	ActualCents             int64   `json:"actual_cents"`
	Samples                 int     `json:"samples"`
	Passed                  bool    `json:"passed"`
}

// UpdateKnowledgeEmbeddingSettings applies scope, explicit understanding versions, capacity and input price under one config CAS.
// A successful change clears old vectors, advances epochs and disables semantic retrieval; nil price means unknown.
func (s *Store) UpdateKnowledgeEmbeddingSettings(ctx context.Context, cmd EmbeddingSettingsCommand) (*KnowledgeEmbeddingConfig, error) {
	if cmd.ExpectedRevision < 1 || cmd.WindowCapacity < 1 || cmd.WindowCapacity > KnowledgeEmbeddingCapacity || len(cmd.Sources) > 1000 || (cmd.InputCentsPerMillion != nil && (*cmd.InputCentsPerMillion < 0 || *cmd.InputCentsPerMillion > math.MaxInt64/8192)) {
		return nil, ErrInvalidEditorialState
	}
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, cmd.ConfigID)
	if err != nil {
		return nil, err
	}
	seen := map[EmbeddingSource]bool{}
	for _, source := range cmd.Sources {
		if !validSourceType(models.SourceType(source.SourceType)) || source.SourceID == "" || len(source.SourceID) > 200 || seen[source] {
			return nil, ErrInvalidEditorialState
		}
		seen[source] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_embedding_configs SET enabled=?,semantic_enabled=0,window_capacity=?,revision=revision+1,updated_at=datetime('now') WHERE id=? AND revision=?`, cmd.IndexAuthorized, cmd.WindowCapacity, cmd.ConfigID, cmd.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_embedding_sources WHERE config_id=?`, cmd.ConfigID); err != nil {
		return nil, err
	}
	for _, source := range cmd.Sources {
		var exists int
		err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id=? AND archived_at IS NULL`, sourceTable(models.SourceType(source.SourceType))), source.SourceID).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if exists != 1 {
			return nil, ErrNotFound
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_sources(config_id,source_type,source_id)VALUES(?,?,?)`, cmd.ConfigID, source.SourceType, source.SourceID); err != nil {
			return nil, err
		}
	}
	if err = setEmbeddingUnderstandings(ctx, tx, cmd.ConfigID, cmd.UnderstandingSnapshotIDs); err != nil {
		return nil, err
	}
	if cmd.InputCentsPerMillion == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM model_prices WHERE provider=? AND model=?`, cfg.Provider, cfg.Model)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO model_prices(provider,model,input_cents_per_million,output_cents_per_million)VALUES(?,?,?,0) ON CONFLICT(provider,model)DO UPDATE SET input_cents_per_million=excluded.input_cents_per_million,output_cents_per_million=0,updated_at=datetime('now')`, cfg.Provider, cfg.Model, *cmd.InputCentsPerMillion)
	}
	if err != nil {
		return nil, err
	}
	for _, q := range []string{`DELETE FROM knowledge_embedding_vectors WHERE config_id=?`, `DELETE FROM knowledge_embedding_events WHERE config_id=?`, `INSERT INTO knowledge_embedding_events(config_id,doc_key,action)SELECT ?,key,'upsert' FROM knowledge_search_docs WHERE visibility='current'`} {
		if _, err = tx.ExecContext(ctx, q, cmd.ConfigID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_embedding_state SET delete_epoch=delete_epoch+1,index_epoch=index_epoch+1 WHERE id=1`); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKnowledgeEmbeddingConfig(ctx, cmd.ConfigID)
}

// EmbeddingQualityIdentity binds immutable route, revision, source scope, corpus/vector events and price.
// Epochs also invalidate a report on material, source-policy or vector repairs without reading bodies.
func embeddingQualityIdentity(ctx context.Context, q reviewReader, id string) (string, error) {
	var cfg KnowledgeEmbeddingConfig
	err := q.QueryRowContext(ctx, `SELECT id,connection_id,provider,model,dimensions,unit,enabled,revision,window_capacity,profile FROM knowledge_embedding_configs WHERE id=?`, id).Scan(&cfg.ID, &cfg.ConnectionID, &cfg.Provider, &cfg.Model, &cfg.Dimensions, &cfg.Unit, &cfg.Enabled, &cfg.Revision, &cfg.WindowCapacity, &cfg.Profile)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var index, del int64
	if err = q.QueryRowContext(ctx, `SELECT index_epoch,delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&index, &del); err != nil {
		return "", err
	}
	var price sql.NullInt64
	var updated sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT (SELECT input_cents_per_million FROM model_prices WHERE provider=? AND model=?),(SELECT updated_at FROM model_prices WHERE provider=? AND model=?)`, cfg.Provider, cfg.Model, cfg.Provider, cfg.Model).Scan(&price, &updated); err != nil {
		return "", err
	}
	// Scope is fully covered by its CAS revision; document and permission mutations advance epochs.
	var understandings string
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(group_concat(snapshot_id,','),'') FROM (SELECT snapshot_id FROM knowledge_embedding_understandings WHERE config_id=? ORDER BY snapshot_id)`, id).Scan(&understandings); err != nil {
		return "", err
	}
	runtimeIdentity, err := embeddingRuntimeIdentity()
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal([]any{runtimeIdentity, understandings, cfg.ID, cfg.ConnectionID, cfg.Provider, cfg.Model, cfg.Dimensions, cfg.Unit, cfg.Enabled, cfg.Revision, cfg.WindowCapacity, index, del, price, updated, KnowledgeFusionVersion})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// KnowledgeEmbeddingQualityIdentity returns the current code/machine, corpus/event, scope/model and price fingerprint.
// It is read-only and never queues work or reads source bodies.
func (s *Store) KnowledgeEmbeddingQualityIdentity(ctx context.Context, id string) (string, error) {
	return embeddingQualityIdentity(ctx, s.DB, id)
}
func validQualityHash(v string) bool { b, e := hex.DecodeString(v); return e == nil && len(b) == 32 }
func validRecall(v float64) bool     { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
func positiveMeasure(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }

func validateEmbeddingQualityReport(r EmbeddingQualityReport) error {
	if !validQualityHash(r.Identity) || !validQualityHash(r.QueryManifestSHA256) || !validQualityHash(r.CorpusManifestSHA256) || !validQualityHash(r.RelevanceManifestSHA256) || !r.RealMeasurements || r.PerformanceDimensions != 2048 || r.Windows10k != 10000 || r.Windows50k != 50000 || r.Queries != 40 || r.Samples < 20 || strings.TrimSpace(r.SourceRevision) == "" || len(r.SourceRevision) > 200 || strings.TrimSpace(r.Machine) == "" || len(r.Machine) > 1000 || strings.TrimSpace(r.Method) == "" || len(r.Method) > 4000 || r.Fusion != KnowledgeFusionVersion || r.PeakRSSBytes <= 0 || !r.EstimatedCostKnown || !r.ActualCostKnown || r.EstimatedCents < 0 || r.ActualCents < 0 {
		return ErrInvalidEditorialState
	}
	for _, v := range []float64{r.FTSRecall10, r.HybridRecall10, r.RewriteFTSRecall10, r.RewriteHybridRecall10} {
		if !validRecall(v) {
			return ErrInvalidEditorialState
		}
	}
	for _, v := range []float64{r.Cold10kP95MS, r.Cold50kP95MS, r.Warm10kP95MS, r.Warm50kP95MS} {
		if !positiveMeasure(v) {
			return ErrInvalidEditorialState
		}
	}
	return nil
}
func embeddingQualityMetricsPassed(r EmbeddingQualityReport) bool {
	return r.HybridRecall10 >= r.FTSRecall10 && r.RewriteHybridRecall10-r.RewriteFTSRecall10 >= .1-1e-9 && r.Cold10kP95MS <= 150 && r.Cold50kP95MS <= 500
}

// SaveKnowledgeEmbeddingQualityReport validates completeness, then computes pass/fail itself.
func (s *Store) SaveKnowledgeEmbeddingQualityReport(ctx context.Context, id string, r EmbeddingQualityReport) (*EmbeddingQualityReport, error) {
	if err := validateEmbeddingQualityReport(r); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	identity, err := embeddingQualityIdentity(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if identity != r.Identity {
		return nil, ErrConflict
	}
	r.ID = uuid.NewString()
	r.Passed = embeddingQualityMetricsPassed(r)
	payload, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_embedding_quality_reports(id,config_id,identity,report_json,passed)VALUES(?,?,?,?,?)`, r.ID, id, r.Identity, string(payload), r.Passed); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_embedding_configs SET semantic_enabled=0 WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &r, nil
}

// KnowledgeEmbeddingQualityGate returns a read-only diagnostic; stale reports cannot authorize retrieval.
func (s *Store) KnowledgeEmbeddingQualityGate(ctx context.Context, id string) (*EmbeddingQualityReport, string, error) {
	identity, err := s.KnowledgeEmbeddingQualityIdentity(ctx, id)
	if err != nil {
		return nil, "", err
	}
	var payload string
	var storedPassed bool
	err = s.DB.QueryRowContext(ctx, `SELECT report_json,passed FROM knowledge_embedding_quality_reports WHERE config_id=? ORDER BY rowid DESC LIMIT 1`, id).Scan(&payload, &storedPassed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "尚无真实质量与同机冷性能报告；继续使用 FTS。", nil
	}
	if err != nil {
		return nil, "", err
	}
	var report EmbeddingQualityReport
	if err = json.Unmarshal([]byte(payload), &report); err != nil {
		return nil, "", err
	}
	if report.Identity != identity {
		return &report, "来源、索引、配置或价格已变化；原报告失效，继续使用 FTS。", nil
	}
	if validateEmbeddingQualityReport(report) != nil {
		return &report, "质量报告不完整或损坏；继续使用 FTS。", nil
	}
	if !storedPassed || !report.Passed || !embeddingQualityMetricsPassed(report) {
		return &report, "质量或冷检索性能未达到门槛；继续使用 FTS。", nil
	}
	return &report, "", nil
}

// SetKnowledgeSemanticEnabled changes only search activation under the displayed config revision.
// Enabling requires an authorized index and the latest passing report for the exact current identity; disabling needs no report.
func (s *Store) SetKnowledgeSemanticEnabled(ctx context.Context, id string, expected int, enabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if enabled {
		identity, e := embeddingQualityIdentity(ctx, tx, id)
		if e != nil {
			return e
		}
		var payload string
		e = tx.QueryRowContext(ctx, `SELECT report_json FROM knowledge_embedding_quality_reports WHERE config_id=? AND identity=? AND passed=1 AND rowid=(SELECT MAX(rowid) FROM knowledge_embedding_quality_reports WHERE config_id=?)`, id, identity, id).Scan(&payload)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrInvalidEditorialState
		}
		if e != nil {
			return e
		}
		var report EmbeddingQualityReport
		if json.Unmarshal([]byte(payload), &report) != nil || validateEmbeddingQualityReport(report) != nil || report.Identity != identity || !report.Passed || !embeddingQualityMetricsPassed(report) {
			return ErrInvalidEditorialState
		}

	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_embedding_configs SET semantic_enabled=? WHERE id=? AND revision=? AND (enabled=1 OR ?=0)`, enabled, id, expected, enabled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// EmbeddingScopePreview exposes permission metadata only; scope selection never grants external permission.
type EmbeddingScopePreview struct {
	EmbeddingSource
	Policy  string `json:"policy"`
	MaySend bool   `json:"may_send"`
	Reason  string `json:"reason"`
}

// PreviewKnowledgeEmbeddingScope returns current source permission metadata for a proposed range without granting it.
// Missing, purged, archived and unapproved sources remain in the preview with MaySend=false.
func (s *Store) PreviewKnowledgeEmbeddingScope(ctx context.Context, id string, sources []EmbeddingSource) ([]EmbeddingScopePreview, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(sources) > 1000 {
		return nil, ErrInvalidEditorialState
	}
	out := make([]EmbeddingScopePreview, 0, len(sources))
	for _, source := range sources {
		if !validSourceType(models.SourceType(source.SourceType)) || source.SourceID == "" || len(source.SourceID) > 200 {
			return nil, ErrInvalidEditorialState
		}
		var policy string
		var allowed bool
		err = s.DB.QueryRowContext(ctx, `SELECT model_data_policy,archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots WHERE source_type=? AND source_id=? AND status='purged') AND (model_data_policy='external_allowed' OR (model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(approved_providers_json) THEN approved_providers_json ELSE '[]' END) WHERE lower(trim(value))=lower(trim(?))))) FROM `+sourceTable(models.SourceType(source.SourceType))+` WHERE id=?`, source.SourceType, source.SourceID, cfg.Provider, source.SourceID).Scan(&policy, &allowed)
		v := EmbeddingScopePreview{EmbeddingSource: source, Policy: policy, MaySend: allowed, Reason: "当前允许此独立 embedding 连接索引。"}
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
			v.MaySend = false
			v.Reason = "来源已不存在，不会外发。"
		} else if err != nil {
			return nil, err
		} else if !allowed {
			v.Reason = "当前策略、归档或撤回状态不允许此连接，不会外发。"
		}
		out = append(out, v)
	}
	return out, nil
}

// embeddingQualifiedSourcesCTE computes the <=1000 Owner-selected permissions once,
// rather than repeating provider/snapshot checks for every 2048-dimensional window.
func embeddingQualifiedSourcesCTE() string {
	var parts []string
	for _, v := range []struct{ kind, table string }{{"episode", "episodes"}, {"upload", "uploads"}, {"document", "documents"}} {
		parts = append(parts, `SELECT chosen.source_type,chosen.source_id FROM knowledge_embedding_sources chosen JOIN `+v.table+` es ON es.id=chosen.source_id WHERE chosen.config_id=:config AND chosen.source_type='`+v.kind+`' AND es.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots p WHERE p.source_type=chosen.source_type AND p.source_id=chosen.source_id AND p.status='purged') AND (es.model_data_policy='external_allowed' OR (es.model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(es.approved_providers_json) THEN es.approved_providers_json ELSE '[]' END) ap WHERE lower(trim(ap.value))=lower(trim(:provider)))))`)
	}
	return `WITH embedding_allowed_sources AS MATERIALIZED (` + strings.Join(parts, " UNION ALL ") + `) `
}
func embeddingMatrixDocumentQualified() string {
	clause := embeddingDocumentQualified()
	for _, alias := range []string{"d", "m", "ur", "am"} {
		clause = strings.ReplaceAll(clause, embeddingSourceQualified(alias), `EXISTS(SELECT 1 FROM embedding_allowed_sources allowed WHERE allowed.source_type=`+alias+`.source_type AND allowed.source_id=`+alias+`.source_id)`)
	}
	return clause
}

// EmbeddingUnderstandingChoice contains the immutable current identity, never reference bodies.
type EmbeddingUnderstandingChoice struct {
	SnapshotID, Question, Policy string
	Selected, MaySend            bool
}

// ListEmbeddingUnderstandingChoices returns at most 500 current immutable understanding identities for Owner selection.
// MaySend reflects current own/all-reference permission, not inclusion in an index or permission for another provider.
func (s *Store) ListEmbeddingUnderstandingChoices(ctx context.Context, id string) ([]EmbeddingUnderstandingChoice, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT us.id,q.body,us.model_data_policy,EXISTS(SELECT 1 FROM knowledge_embedding_understandings chosen WHERE chosen.config_id=? AND chosen.snapshot_id=us.id) FROM understanding_snapshots us JOIN understanding_heads h ON h.current_snapshot_id=us.id JOIN learning_questions q ON q.id=us.question_id ORDER BY us.created_at DESC,us.id LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	var out []EmbeddingUnderstandingChoice
	for rows.Next() {
		var v EmbeddingUnderstandingChoice
		if err = rows.Scan(&v.SnapshotID, &v.Question, &v.Policy, &v.Selected); err != nil {
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
		var allowed bool
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_search_docs d WHERE d.kind='understanding' AND d.object_id=? AND `+understandingSendSQL("d", ":provider", false)+`)`, out[i].SnapshotID, sql.Named("provider", cfg.Provider)).Scan(&allowed)
		if err != nil {
			return nil, err
		}
		out[i].MaySend = allowed
	}
	return out, nil
}
