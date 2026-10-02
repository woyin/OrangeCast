package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// KnowledgeWritingPlan freezes the actual selected write request, never a recall recipe.
type KnowledgeWritingPlan struct {
	ArticleID      string                           `json:"article_id"`
	ParentRevision int                              `json:"parent_revision"`
	Revision       int                              `json:"revision"`
	Hash           string                           `json:"hash"`
	Request        provider.KnowledgeArticleRequest `json:"request"`
	State          string                           `json:"state"`
	JobID          string                           `json:"job_id"`
	Provider       string                           `json:"provider"`
	Model          string                           `json:"model"`
	Automated      bool                             `json:"automated"`
}

func createKnowledgeWritingPlan(ctx context.Context, tx *sql.Tx, articleID string, req provider.KnowledgeArticleRequest, name, model string, automatic bool) error {
	if req.Topic == nil || strings.TrimSpace(req.Topic.Outline) == "" {
		return ErrConflict
	}
	req.Stage = "write"
	var err error
	model, err = freezeKnowledgeEstimate(ctx, tx, name, model, &req)
	if err != nil {
		return err
	}
	var parentRevision int
	if err = tx.QueryRowContext(ctx, `SELECT working_revision FROM knowledge_articles WHERE id=?`, articleID).Scan(&parentRevision); err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	var revision int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM knowledge_writing_plans WHERE article_id=?`, articleID).Scan(&revision); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_writing_plans(article_id,parent_revision,revision,hash,request_json,provider,model,automated) VALUES(?,?,?,?,?,?,?,?)`, articleID, parentRevision, revision, hex.EncodeToString(sum[:]), string(body), name, model, automatic)
	return err
}

func readKnowledgeWritingPlan(ctx context.Context, q reviewReader, id string) (*KnowledgeWritingPlan, error) {
	p := &KnowledgeWritingPlan{}
	var body string
	err := q.QueryRowContext(ctx, `SELECT article_id,parent_revision,revision,hash,request_json,state,job_id,provider,model,automated FROM knowledge_writing_plans WHERE article_id=? ORDER BY revision DESC LIMIT 1`, id).Scan(&p.ArticleID, &p.ParentRevision, &p.Revision, &p.Hash, &body, &p.State, &p.JobID, &p.Provider, &p.Model, &p.Automated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(body))
	if hex.EncodeToString(sum[:]) != p.Hash {
		return nil, ErrConflict
	}
	err = json.Unmarshal([]byte(body), &p.Request)
	return p, err
}

// GetKnowledgeWritingPlan reads the latest immutable plan and verifies its stored request hash.
func (s *Store) GetKnowledgeWritingPlan(ctx context.Context, id string) (*KnowledgeWritingPlan, error) {
	return readKnowledgeWritingPlan(ctx, s.DB, id)
}

// EditKnowledgeWritingPlan creates an immutable new revision with a fresh estimate.
func (s *Store) EditKnowledgeWritingPlan(ctx context.Context, id string, revision int, hash, outline string) (*KnowledgeWritingPlan, error) {
	return s.EditKnowledgeWritingPlanPurpose(ctx, id, revision, hash, outline, "")
}

// EditKnowledgeWritingPlanPurpose freezes a changed mode as a new revision.
func (s *Store) EditKnowledgeWritingPlanPurpose(ctx context.Context, id string, revision int, hash, outline, mode string) (*KnowledgeWritingPlan, error) {
	outline = strings.TrimSpace(outline)
	if outline == "" || len([]rune(outline)) > 12000 {
		return nil, ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := readKnowledgeWritingPlan(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Revision != revision || p.Hash != hash || p.State != "pending" {
		return nil, ErrConflict
	}
	var parentMatches bool
	if err = tx.QueryRowContext(ctx, `SELECT status='awaiting_plan' AND working_revision=? FROM knowledge_articles WHERE id=?`, p.ParentRevision, id).Scan(&parentMatches); err != nil {
		return nil, err
	}
	if !parentMatches {
		return nil, ErrConflict
	}
	p.Request.Topic.Outline = outline
	if mode != "" {
		purpose, e := provider.FreezeWritingPurpose(mode)
		if e != nil {
			return nil, e
		}
		p.Request.WritingPurpose = &purpose
		p.Request.PromptVersion = provider.KnowledgeArticlePurposePromptVersion
	}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_writing_plans SET state='superseded' WHERE article_id=? AND revision=? AND hash=? AND state='pending'`, id, revision, hash)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrConflict
	}
	if err = createKnowledgeWritingPlan(ctx, tx, id, p.Request, p.Provider, p.Model, p.Automated); err != nil {
		return nil, err
	}
	p, err = readKnowledgeWritingPlan(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}

// ConfirmKnowledgeWritingPlan atomically admits precisely one write task for an exact revision.
func (s *Store) ConfirmKnowledgeWritingPlan(ctx context.Context, id string, revision int, hash string) (*KnowledgeWritingPlan, error) {
	p, err := s.GetKnowledgeWritingPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Revision != revision || p.Hash != hash {
		return nil, ErrConflict
	}
	if p.State == "confirmed" {
		return p, nil
	}
	a, err := s.GetKnowledgeArticle(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = s.CheckKnowledgeMaterials(ctx, a.ProfileID, p.Provider, p.Request.Materials); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	latest, err := readKnowledgeWritingPlan(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if latest.Revision != revision || latest.Hash != hash {
		return nil, ErrConflict
	}
	if latest.State == "confirmed" {
		return latest, nil
	}
	if latest.State != "pending" {
		return nil, ErrConflict
	}
	if err = checkWritingPlanMaterials(ctx, tx, p.Provider, p.Request.Materials); err != nil {
		return nil, err
	}
	var blocked bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_controls WHERE paused=1 AND ((kind='lane' AND target='knowledge') OR (kind='direction' AND target=?))) OR EXISTS(SELECT 1 FROM processing_jobs WHERE source_type='knowledge_article' AND source_id=? AND stop_requested=1)`, "knowledge_article:"+id, id).Scan(&blocked)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrRunControlled
	}
	if err = checkQuestionAdmission(ctx, tx, p.Request.Question, p.Automated, true); err != nil {
		return nil, err
	}
	var budget sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT monthly_budget_cents FROM settings WHERE id=1`).Scan(&budget); err != nil {
		return nil, err
	}
	if budget.Valid {
		e := p.Request.Estimate
		if e == nil || !e.PriceKnown || e.CostCents == nil {
			return nil, ErrBudgetUnpriced
		}
		var used int64
		err = tx.QueryRowContext(ctx, `SELECT (SELECT COALESCE(SUM(cost_cents),0) FROM editorial_usage_records WHERE created_at>=datetime('now','start of month'))+(SELECT COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE created_at>=datetime('now','start of month'))+(SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status IN ('held','pending_remote') AND created_at>=datetime('now','start of month'))`).Scan(&used)
		if err != nil {
			return nil, err
		}
		if used+*e.CostCents > budget.Int64 {
			return nil, ErrBudgetExhausted
		}
	}
	refreshed := p.Request
	if _, err = freezeKnowledgeEstimate(ctx, tx, p.Provider, p.Model, &refreshed); err != nil {
		return nil, err
	}
	oldEstimate, _ := json.Marshal(p.Request.Estimate)
	newEstimate, _ := json.Marshal(refreshed.Estimate)
	if string(oldEstimate) != string(newEstimate) {
		return nil, ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status='write',stage='write',reason='',updated_at=datetime('now') WHERE id=? AND status='awaiting_plan' AND working_revision=?`, id, p.ParentRevision)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrConflict
	}
	// enqueue repeats estimate freezing for the queue admission boundary; no selection or model call occurs.
	if err = enqueueKnowledgeStage(ctx, tx, id, "write", p.Provider, p.Model, p.Request, p.Automated); err != nil {
		return nil, err
	}
	var jobID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_type='knowledge_article' AND source_id=? AND job_type='knowledge_article' AND intent_id=? ORDER BY rowid DESC LIMIT 1`, id, "knowledge-article:"+id+":write").Scan(&jobID); err != nil {
		return nil, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE knowledge_writing_plans SET state='confirmed',job_id=? WHERE article_id=? AND revision=? AND hash=? AND state='pending'`, jobID, id, revision, hash)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrConflict
	}

	p.State = "confirmed"
	p.JobID = jobID
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}

// Recheck mutable source permissions and material identity in the same admission transaction.
func checkWritingPlanMaterials(ctx context.Context, tx *sql.Tx, name string, materials []provider.KnowledgeMaterial) error {
	for _, m := range materials {
		if m.Kind == "understanding" {
			if err := checkUnderstandingMaterialTransaction(ctx, tx, name, m); err != nil {
				return err
			}
			continue
		}
		st := models.SourceType(m.SourceType)
		if !validSourceType(st) {
			return ErrConflict
		}
		var archived sql.NullString
		var policy, approved string
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT archived_at,model_data_policy,approved_providers_json FROM %s WHERE id=?`, sourceTable(st)), m.SourceID).Scan(&archived, &policy, &approved); err != nil {
			return err
		}
		allowed := policy == "external_allowed"
		if policy == "approved_providers_only" {
			var names []string
			if err := json.Unmarshal([]byte(approved), &names); err != nil {
				return err
			}
			for _, n := range names {
				if name != "" && strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(name)) {
					allowed = true
				}
			}
		}
		if archived.Valid || !allowed {
			return ErrConflict
		}
		if m.SnapshotID != "" {
			var sourceID, sourceType, kind, status string
			var version int
			if err := tx.QueryRowContext(ctx, `SELECT source_id,source_type,kind,status,content_version FROM source_snapshots WHERE id=?`, m.SnapshotID).Scan(&sourceID, &sourceType, &kind, &status, &version); err != nil {
				return err
			}
			if sourceID != m.SourceID || sourceType != m.SourceType || status == "purged" {
				return ErrConflict
			}
			if kind == "audio" && m.Kind != "owner_reflection" {
				var current sql.NullInt64
				if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT current_transcript_version FROM %s WHERE id=?`, sourceTable(st)), m.SourceID).Scan(&current); err != nil {
					return err
				}
				if !current.Valid || current.Int64 != int64(version) {
					return ErrConflict
				}
			}
		}

		if m.Kind == "keypoint" {
			var content, description, quality, production string
			var version int
			var stale sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT content,description,card_version,quality_status,production_status,stale_at FROM keypoint_index WHERE id=?`, m.ID).Scan(&content, &description, &version, &quality, &production, &stale); err != nil {
				return err
			}
			if content != m.Content || description != m.Description || version != m.Version || stale.Valid || production == "dismissed" || (quality != "ready" && quality != "owner_confirmed") {
				return ErrConflict
			}
		} else {
			var content, kind string
			var revision int
			if err := tx.QueryRowContext(ctx, `SELECT content,kind,revision FROM owner_notes WHERE id=?`, m.ID).Scan(&content, &kind, &revision); err != nil {
				return err
			}
			if content != m.Content || kind != m.Kind || revision != m.Version {
				return ErrConflict
			}
		}
	}
	return nil
}

// Plans can recall sources not present in the original article input and before
// a body revision exists. Resolve those edges before deleting jobs and articles
// so paid numeric usage survives while all frozen source bodies are removed.
const knowledgeArticlePurgeReferences = `
 SELECT ka.id FROM knowledge_articles ka,json_each(ka.input_json,'$.materials') m
 WHERE json_extract(m.value,'$.source_type')=?1 AND json_extract(m.value,'$.source_id')=?2
 UNION SELECT article_id FROM knowledge_article_material_refs WHERE source_type=?1 AND source_id=?2
 UNION SELECT p.article_id FROM knowledge_writing_plans p,json_each(p.request_json,'$.materials') m
 WHERE json_extract(m.value,'$.source_type')=?1 AND json_extract(m.value,'$.source_id')=?2
 UNION SELECT p.article_id FROM knowledge_writing_plans p,json_each(p.request_json,'$.materials') m,json_each(m.value,'$.understanding_references') r
 WHERE json_extract(r.value,'$.source_type')=?1 AND json_extract(r.value,'$.source_id')=?2
 UNION SELECT p.article_id FROM knowledge_writing_plans p,json_each(p.request_json,'$.materials') m,json_each(m.value,'$.understanding_references') r
 JOIN knowledge_article_material_refs ar ON ar.article_id=json_extract(r.value,'$.object_id') AND ar.revision=json_extract(r.value,'$.version')
 WHERE json_extract(r.value,'$.kind')='article' AND ar.source_type=?1 AND ar.source_id=?2`
