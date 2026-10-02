package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// Query normalization changes no lexical meaning beyond case and whitespace.
func normalizedEmbeddingQuery(query string) (string, string, error) {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 200 {
		return "", "", ErrInvalidEditorialState
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if normalized == "" {
		return "", "", ErrInvalidEditorialState
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("query-normalized-v1\x00"+normalized)))
	return normalized, hash, nil
}

func validateKnowledgeQueryEmbedding(ctx context.Context, q reviewReader, in KnowledgeEmbeddingJobInput) error {
	normalized, hash, err := normalizedEmbeddingQuery(in.Query)
	if err != nil || normalized != in.Query || hash != in.QueryHash {
		return ErrInvalidEditorialState
	}
	var epoch int64
	if err = q.QueryRowContext(ctx, `SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&epoch); err != nil {
		return err
	}
	if epoch != in.DeleteEpoch {
		return ErrConflict
	}
	var allowed bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_embedding_configs WHERE id=? AND enabled=1 AND revision=? AND dimensions=? AND model=? AND connection_id=?)`, in.Config.ID, in.ScopeRevision, in.Config.Dimensions, in.Config.Model, in.Config.ConnectionID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrConflict
	}
	cfg := &KnowledgeEmbeddingConfig{EmbeddingConfig: in.Config}
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_embedding_vectors v JOIN knowledge_search_docs d ON d.key=v.doc_key WHERE v.config_id=:config AND v.revision=d.revision AND v.dimensions=:dimensions AND `+embeddingDocumentQualified()+`)`, append(embeddingSQLArgs(cfg), sql.Named("dimensions", in.Config.Dimensions))...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: 没有可用的授权向量，查询不会外发", ErrInvalidEditorialState)
	}
	return nil
}

// ReserveKnowledgeQueryEmbedding is explicitly invoked. The deterministic
// normalized query/config/epoch identity covers pending AND terminal jobs.
func (s *Store) ReserveKnowledgeQueryEmbedding(ctx context.Context, id, query string) (*models.ProcessingJob, bool, error) {
	return s.reserveKnowledgeQueryEmbedding(ctx, id, query, nil)
}
func (s *Store) reserveKnowledgeQueryEmbedding(ctx context.Context, id, query string, expectedEpoch *int64) (*models.ProcessingJob, bool, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, false, err
	}
	normalized, hash, err := normalizedEmbeddingQuery(query)
	if err != nil {
		return nil, false, err
	}
	var epoch int64
	if err = s.DB.QueryRowContext(ctx, `SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&epoch); err != nil {
		return nil, false, err
	}
	if expectedEpoch != nil && epoch != *expectedEpoch {
		return nil, false, ErrConflict
	}
	in := KnowledgeEmbeddingJobInput{Version: KnowledgeEmbeddingJobVersion, Kind: "query", Config: cfg.EmbeddingConfig, Query: normalized, QueryHash: hash, DeleteEpoch: epoch, ScopeRevision: cfg.Revision}
	in.Estimate, err = s.FreezeEmbeddingEstimate(ctx, cfg.EmbeddingConfig, in.Inputs())
	if err != nil {
		return nil, false, err
	}
	if err = validateKnowledgeQueryEmbedding(ctx, s.DB, in); err != nil {
		return nil, false, err
	}
	return s.reserveKnowledgeEmbeddingJob(ctx, "", in)
}

// KnowledgeQueryEmbedding is a read-only cache lookup. Cache misses never queue
// or bill. Source policy/version predicates are re-evaluated before returning.
func (s *Store) KnowledgeQueryEmbedding(ctx context.Context, id, query string) ([]float32, error) {
	cfg, err := s.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	normalized, hash, err := normalizedEmbeddingQuery(query)
	if err != nil {
		return nil, err
	}
	var epoch int64
	if err = s.DB.QueryRowContext(ctx, `SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&epoch); err != nil {
		return nil, err
	}
	in := KnowledgeEmbeddingJobInput{Kind: "query", Config: cfg.EmbeddingConfig, Query: normalized, QueryHash: hash, DeleteEpoch: epoch, ScopeRevision: cfg.Revision}
	if err = validateKnowledgeQueryEmbedding(ctx, s.DB, in); err != nil {
		return nil, err
	}
	var blob []byte
	var dimensions int
	if err = s.DB.QueryRowContext(ctx, `SELECT vector,dimensions FROM knowledge_query_embeddings WHERE config_id=? AND query_hash=? AND delete_epoch=? AND scope_revision=? AND expires_at>datetime('now')`, id, hash, epoch, cfg.Revision).Scan(&blob, &dimensions); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	vector, err := decodeEmbeddingVector(blob, dimensions)
	if err != nil || dimensions != cfg.Dimensions {
		return nil, ErrInvalidEditorialState
	}
	return vector, nil
}

func decodeEmbeddingVector(blob []byte, dimensions int) ([]float32, error) {
	if dimensions < 1 || dimensions > provider.EmbeddingMaxDimensions || len(blob) != dimensions*4 {
		return nil, ErrInvalidEditorialState
	}
	out := make([]float32, dimensions)
	var norm float64
	for i := range out {
		v := math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, ErrInvalidEditorialState
		}
		out[i] = v
		norm += float64(v) * float64(v)
	}
	if math.Abs(norm-1) > .0001 {
		return nil, ErrInvalidEditorialState
	}
	return out, nil
}
func (s *Store) commitKnowledgeQueryEmbedding(ctx context.Context, id string, in KnowledgeEmbeddingJobInput, result *provider.EmbeddingResult) error {
	if provider.ValidateEmbeddingResult(result, in.Config.Model, 1, in.Config.Dimensions) != nil {
		return ErrInvalidEditorialState
	}
	blob := make([]byte, 4*result.Dimensions)
	for i, v := range result.Vectors[0] {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(v))
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, id); err != nil {
		return err
	}
	if err = validateKnowledgeQueryEmbedding(ctx, tx, in); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_query_embeddings(config_id,job_id,query_hash,delete_epoch,scope_revision,dimensions,vector)VALUES(?,?,?,?,?,?,?) ON CONFLICT(config_id,query_hash,delete_epoch)DO UPDATE SET job_id=excluded.job_id,vector=excluded.vector,dimensions=excluded.dimensions,scope_revision=excluded.scope_revision,created_at=datetime('now'),expires_at=datetime('now','+30 days')`, in.Config.ID, id, in.QueryHash, in.DeleteEpoch, in.ScopeRevision, in.Config.Dimensions, blob); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_query_embeddings WHERE rowid IN(SELECT rowid FROM knowledge_query_embeddings ORDER BY created_at DESC,rowid DESC LIMIT -1 OFFSET 1000)`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json='{"query_ready":true}',result_state='complete',updated_at=datetime('now') WHERE id=? AND status IN('running','queued') AND result_state!='complete'`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// ReserveKnowledgeQueryEmbeddingRequest Owner request identity freezes the cache epoch as well as query/config. A lost
// acknowledgement followed by an edited query cannot start a different paid job.
func (s *Store) ReserveKnowledgeQueryEmbeddingRequest(ctx context.Context, id, query, key string) (*models.ProcessingJob, bool, error) {
	if _, err := uuid.Parse(key); err != nil {
		return nil, false, ErrInvalidEditorialState
	}
	_, hash, err := normalizedEmbeddingQuery(query)
	if err != nil {
		return nil, false, err
	}
	var epoch int64
	if err = s.DB.QueryRowContext(ctx, `SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1`).Scan(&epoch); err != nil {
		return nil, false, err
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_query_requests(request_key,config_id,query_hash,delete_epoch)VALUES(?,?,?,?)`, key, id, hash, epoch); err != nil {
		return nil, false, err
	}
	var frozenID, frozenHash string
	var jobID sql.NullString
	if err = s.DB.QueryRowContext(ctx, `SELECT config_id,query_hash,delete_epoch,job_id FROM knowledge_query_requests WHERE request_key=?`, key).Scan(&frozenID, &frozenHash, &epoch, &jobID); err != nil {
		return nil, false, err
	}
	if frozenID != id || frozenHash != hash {
		return nil, false, ErrConflict
	}
	if jobID.Valid {
		job, e := s.GetJob(ctx, jobID.String)
		return job, false, e
	}
	job, created, err := s.reserveKnowledgeQueryEmbedding(ctx, id, query, &epoch)
	if err != nil {
		return nil, false, err
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE knowledge_query_requests SET job_id=? WHERE request_key=? AND job_id IS NULL`, job.ID, key); err != nil {
		return nil, false, err
	}
	return job, created, nil
}

// FindKnowledgeQueryEmbeddingJob restores a pending Owner operation by reading
// its normalized identity. Navigation/GET never creates or resumes a paid task.
func (s *Store) FindKnowledgeQueryEmbeddingJob(ctx context.Context, id, query string) (*models.ProcessingJob, error) {
	_, hash, err := normalizedEmbeddingQuery(query)
	if err != nil {
		return nil, err
	}
	var jobID string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_type='knowledge_index' AND source_id=? AND json_extract(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.kind')='query' AND json_extract(input_snapshot_json,'$.query_hash')=? AND json_extract(input_snapshot_json,'$.delete_epoch')=(SELECT delete_epoch FROM knowledge_embedding_state WHERE id=1) ORDER BY rowid DESC LIMIT 1`, id, hash).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, jobID)
}
