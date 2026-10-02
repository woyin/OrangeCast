package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// RerankCandidate binds a current object without storing its body.
// Full bodies are loaded only at the authorized paid boundary.
type RerankCandidate struct {
	Key      string
	Revision int
	Hash     string
}

// KnowledgeRerankInput freezes the explicit paid ranking scope and estimate.
type KnowledgeRerankInput struct {
	Version     string
	Config      provider.RerankConfig
	Request     KnowledgeRetrieveQuery
	Candidates  []RerankCandidate
	Fingerprint string
	Estimate    *provider.KnowledgeEstimate
}

func rerankHash(v any) string { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }

func (s *Store) prepareRerank(ctx context.Context, req KnowledgeRetrieveQuery, cfg provider.RerankConfig) (KnowledgeRerankInput, KnowledgeRetrieveResult, error) {
	in := KnowledgeRerankInput{Version: "knowledge-rerank-v1", Config: cfg}
	req.Rerank = nil
	req.Search.Page, req.Search.PerPage = 1, 30
	in.Request = req
	if req.Purpose != RetrieveLocal && req.Purpose != "" || req.Search.IncludeDrafts || req.Search.IncludeHistory || strings.TrimSpace(req.Search.Text) == "" || cfg.ID == "" {
		return in, KnowledgeRetrieveResult{}, ErrInvalidEditorialState
	}
	result, err := s.retrieveKnowledge(ctx, req, false)
	if err != nil {
		return in, result, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return in, result, err
	}
	defer tx.Rollback()
	for _, hit := range result.Hits {
		var body string
		var rev int
		err = tx.QueryRowContext(ctx, `SELECT d.revision,d.title||char(10)||char(10)||d.body FROM knowledge_search_docs d WHERE d.key=:key AND `+rerankDocumentQualified(), sql.Named("key", hit.Key), sql.Named("provider", cfg.Provider)).Scan(&rev, &body)
		if err != nil {
			return in, result, err
		}
		if rev != hit.Revision {
			return in, result, ErrConflict
		}
		in.Candidates = append(in.Candidates, RerankCandidate{Key: hit.Key, Revision: rev, Hash: rerankHash(body)})
	}
	in.Fingerprint = rerankHash([]any{in.Version, cfg, in.Request, in.Candidates, result.Method})
	return in, result, nil
}

func rerankDocuments(ctx context.Context, tx *sql.Tx, in KnowledgeRerankInput) ([]string, error) {
	if in.Version != "knowledge-rerank-v1" || len(in.Candidates) < 1 || len(in.Candidates) > 30 {
		return nil, ErrInvalidEditorialState
	}
	docs := make([]string, len(in.Candidates))
	seen := map[string]bool{}
	for i, c := range in.Candidates {
		if seen[c.Key] {
			return nil, ErrInvalidEditorialState
		}
		seen[c.Key] = true
		var rev int
		err := tx.QueryRowContext(ctx, `SELECT d.revision,d.title||char(10)||char(10)||d.body FROM knowledge_search_docs d WHERE d.key=:key AND `+rerankDocumentQualified(), sql.Named("key", c.Key), sql.Named("provider", in.Config.Provider)).Scan(&rev, &docs[i])
		if err != nil {
			return nil, err
		}
		if rev != c.Revision || rerankHash(docs[i]) != c.Hash {
			return nil, ErrConflict
		}
	}
	if _, err := provider.EstimateRerankInputs(in.Request.Search.Text, docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ReserveKnowledgeRerank is an explicit paid preparation; GET never calls it.
// All attempts for one frozen candidate set share one job, including unknown
// remote results. Refreshing the page cannot replay a possibly charged call.
func (s *Store) ReserveKnowledgeRerank(ctx context.Context, req KnowledgeRetrieveQuery, cfg provider.RerankConfig) (*models.ProcessingJob, bool, error) {
	in, _, err := s.prepareRerank(ctx, req, cfg)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE job_type='knowledge_rerank' AND intent_id=? LIMIT 1`, in.Fingerprint).Scan(&id)
	if err == nil {
		tx.Rollback()
		j, e := s.GetJob(ctx, id)
		return j, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	docs, err := rerankDocuments(ctx, tx, in)
	if err != nil {
		return nil, false, err
	}
	in.Estimate, err = provider.EstimateRerankInputs(req.Search.Text, docs)
	if err != nil {
		return nil, false, err
	}
	// Freeze this exact model's price; never borrow the embedding model rate.
	var price int64
	var updated string
	err = tx.QueryRowContext(ctx, `SELECT input_cents_per_million,updated_at FROM model_prices WHERE provider=? AND model=?`, cfg.Provider, cfg.Model).Scan(&price, &updated)
	if err == nil {
		if price < 0 || price > (math.MaxInt64-999999)/int64(in.Estimate.InputTokens) {
			return nil, false, ErrInvalidEditorialState
		}
		in.Estimate.PriceKnown = true
		in.Estimate.InputCentsPerMillion = price
		in.Estimate.PriceUpdatedAt = updated
		amount, _ := in.Estimate.CostForUnits(in.Estimate.InputTokens, 0)
		in.Estimate.CostCents = &amount
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	raw, _ := json.Marshal(in)
	id = uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,config_version,configured_provider,configured_model) VALUES(?,'knowledge_index',?,'knowledge_rerank','queued',?,?,?,?,?)`, id, cfg.ID, in.Fingerprint, string(raw), in.Version, cfg.Provider, cfg.Model)
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	j, err := s.GetJob(ctx, id)
	return j, true, err
}

// StartKnowledgeRerank rechecks permission and revision in the same transaction
// as the durable remote boundary. It returns only the frozen authorized inputs.
func (s *Store) StartKnowledgeRerank(ctx context.Context, id string, in KnowledgeRerankInput) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, id); err != nil {
		return nil, err
	}
	docs, err := rerankDocuments(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET remote_call_started=1 WHERE id=? AND status='running' AND remote_call_started=0`, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, ErrConflict
	}
	return docs, tx.Commit()
}

// HoldRerankBudget reserves the frozen input-only model price before sending.
func (s *Store) HoldRerankBudget(ctx context.Context, id string, in KnowledgeRerankInput) error {
	if in.Estimate == nil {
		return ErrBudgetIncomplete
	}
	_, err := s.holdBudget(ctx, id, "knowledge_rerank", false, in.Config.Provider, in.Config.Model, in.Estimate.InputTokens, 0, in.Estimate)
	return err
}

// RecordRerankReceipt records supplier usage independently of price availability.
func (s *Store) RecordRerankReceipt(ctx context.Context, id string, in KnowledgeRerankInput, r *provider.RerankResult) error {
	if r == nil || r.InputTokens < 0 || in.Estimate == nil {
		return ErrInvalidEditorialState
	}
	var amount any
	if r.UsageKnown && !r.UnverifiedModel && r.Model == in.Config.Model {
		if cost, known := in.Estimate.CostForUnits(r.InputTokens, 0); known {
			amount = float64(cost)
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_records(id,operation,provider,model,input_units,output_units,estimated_cost,receipt_id,attempt_id,unit_kind,units_known)VALUES(?,'knowledge_rerank',?,?,?,0,?,?,?,'input_tokens',?)`, uuid.NewString(), in.Config.Provider, r.Model, r.InputTokens, amount, id+":knowledge_rerank", id+":response", r.UsageKnown)
	return err
}

// CommitKnowledgeRerank adopts scores only while frozen materials remain allowed.
func (s *Store) CommitKnowledgeRerank(ctx context.Context, id string, in KnowledgeRerankInput, r *provider.RerankResult) error {
	if r == nil || r.Model != in.Config.Model || len(r.Scores) != len(in.Candidates) {
		return ErrInvalidEditorialState
	}
	for _, v := range r.Scores {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ErrInvalidEditorialState
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, id); err != nil {
		return err
	}
	if _, err = rerankDocuments(ctx, tx, in); err != nil {
		return err
	}
	raw, _ := json.Marshal(r.Scores)
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_rerank_cache(fingerprint,job_id,scores_json)VALUES(?,?,?) ON CONFLICT(fingerprint)DO NOTHING`, in.Fingerprint, id, string(raw)); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_state='complete',result_json=? WHERE id=? AND status='running'`, string(raw), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Store) retrieveReranked(ctx context.Context, req KnowledgeRetrieveQuery, evaluation bool) (KnowledgeRetrieveResult, error) {
	cfg := *req.Rerank
	req.Rerank = nil
	page, size := req.Search.Page, req.Search.PerPage
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	if page > 10000 {
		return KnowledgeRetrieveResult{}, ErrInvalidEditorialState
	}
	offset := (page - 1) * size
	if offset >= 30 {
		return s.retrieveKnowledge(ctx, req, evaluation)
	}
	// The same first 30 objects are reordered as a unit; the tail retains its
	// original order. Page two therefore cannot repeat page one's moved items.
	wide := req
	wide.Search.Page = 1
	wide.Search.PerPage = max(30, offset+size)
	out, err := s.retrieveKnowledge(ctx, wide, evaluation)
	if err != nil {
		return out, err
	}
	in, base, err := s.prepareRerank(ctx, req, cfg)
	if err == nil && len(base.Hits) > 0 {
		var raw string
		err = s.DB.QueryRowContext(ctx, `SELECT scores_json FROM knowledge_rerank_cache WHERE fingerprint=?`, in.Fingerprint).Scan(&raw)
		var scores []float64
		if err == nil && json.Unmarshal([]byte(raw), &scores) == nil && len(scores) == len(base.Hits) && len(out.Hits) >= len(scores) {
			consistent := true
			for i, h := range base.Hits {
				if out.Hits[i].Key != h.Key || out.Hits[i].Revision != h.Revision {
					consistent = false
				}
			}
			if !consistent {
				out.RerankDegradation = "候选已变化，保留本地排序。"
			} else {
				byKey := map[string]float64{}
				for i, h := range base.Hits {
					byKey[h.Key] = scores[i]
				}
				prefix := out.Hits[:len(scores)]
				sort.SliceStable(prefix, func(i, j int) bool { return byKey[prefix[i].Key] > byKey[prefix[j].Key] })
				out.RerankApplied = true
			}
		}
	}
	if !out.RerankApplied {
		out.RerankDegradation = "重排缓存未准备或已失效，保留本地检索排序。"
	}
	start := min(offset, len(out.Hits))
	end := min(offset+size, len(out.Hits))
	out.Hits = out.Hits[start:end]
	out.Page, out.PerPage = page, size
	return out, nil
}

func rerankDocumentQualified() string {
	clause := strings.ReplaceAll(embeddingDocumentQualified(), understandingSendSQL("d", ":provider", true), understandingSendSQL("d", ":provider", false))
	for _, kind := range []string{"episode", "upload", "document"} {
		clause = strings.ReplaceAll(clause, " JOIN knowledge_embedding_sources chosen ON chosen.config_id=:config AND chosen.source_type='"+kind+"' AND chosen.source_id=es.id", "")
	}
	return clause
}

// ResumeKnowledgeRerank explicitly resumes local adoption or an unstarted call.
// Unknown remote results remain blocked, even if the page is submitted again.
func (s *Store) ResumeKnowledgeRerank(ctx context.Context, id string, expected int) error {
	if expected < 1 {
		return ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var snapshot, checkpoint, status string
	var remote bool
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT input_snapshot_json,checkpoint_json,status,remote_call_started,control_revision FROM processing_jobs WHERE id=? AND job_type='knowledge_rerank'`, id).Scan(&snapshot, &checkpoint, &status, &remote, &revision)
	if err != nil {
		return err
	}
	if revision != expected || status != "failed" || remote && checkpoint == "" {
		return ErrConflict
	}
	var in KnowledgeRerankInput
	if json.Unmarshal([]byte(snapshot), &in) != nil {
		return ErrInvalidEditorialState
	}
	if err = checkRunControl(ctx, tx, id); err != nil {
		return err
	}
	if _, err = rerankDocuments(ctx, tx, in); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET status='queued',result_state='',result_json='',last_error='',lease_until=NULL,heartbeat_at=NULL,control_revision=control_revision+1,updated_at=datetime('now') WHERE id=? AND status='failed' AND control_revision=?`, id, expected)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
