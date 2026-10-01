package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
)

// KnowledgeRevision is immutable content; review and evidence state are separate.
type KnowledgeRevision struct {
	ArticleID                                                                                                    string
	Revision, ParentRevision                                                                                     int
	Title, InputJSON, BlocksJSON, ContentHash, Origin, Provider, Model, PromptVersion, EvidenceStatus, CreatedAt string
	Passed                                                                                                       bool
}

// KnowledgeReview is a verdict bound to one exact content fingerprint.
type KnowledgeReview struct {
	ID, JobID, ContentHash, IssuesJSON, Provider, Model, PromptVersion, CreatedAt string
	Revision                                                                      int
	Passed                                                                        bool
}

// KnowledgeFeedback is explicit Owner feedback on a particular revision.
type KnowledgeFeedback struct {
	ID, Category, Comment, CreatedAt string
	Revision                         int
}

const knowledgeRevisionColumns = `article_id,revision,parent_revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version,evidence_status,created_at`

func scanKnowledgeRevision(row interface{ Scan(...any) error }) (*KnowledgeRevision, error) {
	v := &KnowledgeRevision{}
	err := row.Scan(&v.ArticleID, &v.Revision, &v.ParentRevision, &v.Title, &v.InputJSON, &v.BlocksJSON, &v.ContentHash, &v.Origin, &v.Provider, &v.Model, &v.PromptVersion, &v.EvidenceStatus, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// GetKnowledgeRevision retrieves content and its exact review disposition.
func (s *Store) GetKnowledgeRevision(ctx context.Context, id string, revision int) (*KnowledgeRevision, error) {
	v, err := scanKnowledgeRevision(s.DB.QueryRowContext(ctx, `SELECT `+knowledgeRevisionColumns+` FROM knowledge_article_revisions WHERE article_id=? AND revision=?`, id, revision))
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_article_reviews WHERE article_id=? AND revision=? AND content_hash=? AND passed=1)`, id, revision, v.ContentHash).Scan(&v.Passed)
	return v, err
}

// ListKnowledgeRevisions lists immutable bodies including superseded branches.
func (s *Store) ListKnowledgeRevisions(ctx context.Context, id string) ([]*KnowledgeRevision, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knowledgeRevisionColumns+` FROM knowledge_article_revisions WHERE article_id=? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnowledgeRevision
	for rows.Next() {
		v, err := scanKnowledgeRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func appendKnowledgeRevision(ctx context.Context, tx *sql.Tx, id string, parent int, title string, req provider.KnowledgeArticleRequest, blocks []provider.KnowledgeBlock, origin, name, model, version string) (int, error) {
	input, _ := json.Marshal(req)
	body, _ := json.Marshal(blocks)
	materials, _ := json.Marshal(req.Materials)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(title+"\x00"+string(body)+"\x00"+string(materials))))
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM knowledge_article_revisions WHERE article_id=?`, id).Scan(&revision); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_article_revisions(article_id,revision,parent_revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, revision, parent, title, string(input), string(body), hash, origin, name, model, version)
	return revision, err
}

// SaveKnowledgeDraft creates a new unreviewed working version with optimistic concurrency.
func (s *Store) SaveKnowledgeDraft(ctx context.Context, id string, expected int, title string, blocks []provider.KnowledgeBlock, req provider.KnowledgeArticleRequest) (*KnowledgeRevision, error) {
	article, err := s.GetKnowledgeArticle(ctx, id)
	if err != nil {
		return nil, err
	}
	req.PromptVersion = provider.KnowledgeArticlePromptVersion
	if req.Topic == nil {
		var topic provider.KnowledgeTopic
		if json.Unmarshal([]byte(article.TopicJSON), &topic) == nil && topic.Title != "" {
			req.Topic = &topic
		}
	}
	if article.WorkingRevision != expected {
		return nil, ErrConflict
	}
	if err := provider.ValidateKnowledgeBlocks(title, blocks, req.Materials); err != nil {
		return nil, err
	}
	if err := s.CheckKnowledgeMaterials(ctx, article.ProfileID, article.Provider, req.Materials); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	blocks, err = assignKnowledgeBlockIDs(ctx, tx, id, expected, blocks, true)
	if err != nil {
		return nil, err
	}
	req.Blocks = blocks
	// Reserve CAS before creating the body. A new draft preserves passed_revision.
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status='needs_review',stage='manual',reason='手动修改尚未审校',title=?,blocks_json=?,input_json=?,updated_at=datetime('now') WHERE id=? AND working_revision=?`, title, jsonString(blocks), jsonString(req), id, expected)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrConflict
	}
	revision, err := appendKnowledgeRevision(ctx, tx, id, expected, title, req, blocks, "owner", article.Provider, article.Model, provider.KnowledgeArticlePromptVersion)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET working_revision=? WHERE id=?`, revision, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKnowledgeRevision(ctx, id, revision)
}
func jsonString(v any) string { raw, _ := json.Marshal(v); return string(raw) }

// QueueKnowledgeRevision binds a bounded directed revision or review to the expected draft.
func (s *Store) QueueKnowledgeRevision(ctx context.Context, id string, expected int, instructions, reviewModel string, revise bool) error {
	article, err := s.GetKnowledgeArticle(ctx, id)
	if err != nil {
		return err
	}
	if article.WorkingRevision != expected || expected < 1 {
		return ErrConflict
	}
	revision, err := s.GetKnowledgeRevision(ctx, id, expected)
	if err != nil {
		return err
	}
	var req provider.KnowledgeArticleRequest
	if err := json.Unmarshal([]byte(revision.InputJSON), &req); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(revision.BlocksJSON), &req.Blocks); err != nil {
		return err
	}
	if len([]rune(instructions)) > 4000 {
		return ErrInvalidEditorialState
	}
	req.PromptVersion = provider.KnowledgeArticlePromptVersion
	req.Instructions = strings.TrimSpace(instructions)
	req.ReviewModel = reviewModel
	ensureKnowledgeStageConfigs(&req, article.Model)
	for _, stage := range []string{"review", "review_final"} {
		cfg := req.StageConfigs[stage]
		if reviewModel != "" {
			cfg.Model = reviewModel
			req.StageConfigs[stage] = cfg
		}
	}
	if err := s.CheckKnowledgeMaterials(ctx, article.ProfileID, article.Provider, req.Materials); err != nil {
		return err
	}
	stage := "review_final"
	if revise {
		stage = "revise"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status=?,stage=?,reason='',review_model=? WHERE id=? AND working_revision=? AND status NOT IN ('discover','select','write','review','revise','review_final')`, stage, stage, reviewModel, id, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if err := enqueueKnowledgeStage(ctx, tx, id, stage, article.Provider, article.Model, req, false); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordKnowledgeFeedback never changes writing preferences implicitly.
func (s *Store) RecordKnowledgeFeedback(ctx context.Context, id string, revision int, category, comment string) error {
	if category != "useful" && category != "shallow" && category != "duplicate" && category != "misattribution" {
		return ErrInvalidEditorialState
	}
	if len([]rune(comment)) > 2000 {
		return ErrInvalidEditorialState
	}
	if _, err := s.GetKnowledgeRevision(ctx, id, revision); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_article_feedback(id,article_id,revision,category,comment) VALUES(?,?,?,?,?)`, uuid.NewString(), id, revision, category, strings.TrimSpace(comment))
	return err
}

// ListKnowledgeFeedback lists feedback without applying its preference suggestions.
func (s *Store) ListKnowledgeFeedback(ctx context.Context, id string) ([]KnowledgeFeedback, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,revision,category,comment,created_at FROM knowledge_article_feedback WHERE article_id=? ORDER BY created_at DESC,id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeFeedback
	for rows.Next() {
		var v KnowledgeFeedback
		if err := rows.Scan(&v.ID, &v.Revision, &v.Category, &v.Comment, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListKnowledgeReviews exposes the actual frozen model for each verdict.
func (s *Store) ListKnowledgeReviews(ctx context.Context, id string) ([]KnowledgeReview, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,revision,job_id,content_hash,passed,issues_json,provider,model,prompt_version,created_at FROM knowledge_article_reviews WHERE article_id=? ORDER BY created_at DESC,id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeReview
	for rows.Next() {
		var v KnowledgeReview
		if err := rows.Scan(&v.ID, &v.Revision, &v.JobID, &v.ContentHash, &v.Passed, &v.IssuesJSON, &v.Provider, &v.Model, &v.PromptVersion, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// KnowledgeExecution exposes stage identity and receipt-backed known/unknown cost.
type KnowledgeExecution struct {
	JobID, Stage, Status, Provider, Model, PromptVersion, CreatedAt, Cost string
	ParentRevision, InputUnits, OutputUnits                               int
	Estimate                                                              *provider.KnowledgeEstimate
}

// ListKnowledgeExecutions includes failures and retries, retaining their own receipts.
func (s *Store) ListKnowledgeExecutions(ctx context.Context, id string) ([]KnowledgeExecution, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT j.id,COALESCE(r.stage,json_extract(j.input_snapshot_json,'$.stage'),''),j.status,j.configured_provider,j.configured_model,j.config_version,j.created_at,COALESCE(r.parent_revision,0),COALESCE(SUM(u.input_units),0),COALESCE(SUM(u.output_units),0),SUM(u.estimated_cost),COUNT(u.id),SUM(CASE WHEN u.estimated_cost IS NULL THEN 1 ELSE 0 END),json_extract(j.input_snapshot_json,'$.request.estimate') FROM processing_jobs j LEFT JOIN knowledge_article_runs r ON r.job_id=j.id LEFT JOIN usage_records u ON u.attempt_id=j.id WHERE j.source_type='knowledge_article' AND j.source_id=? GROUP BY j.id ORDER BY j.created_at,j.rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeExecution
	for rows.Next() {
		var v KnowledgeExecution
		var cost sql.NullFloat64
		var count, unknown int
		var estimate sql.NullString
		if err := rows.Scan(&v.JobID, &v.Stage, &v.Status, &v.Provider, &v.Model, &v.PromptVersion, &v.CreatedAt, &v.ParentRevision, &v.InputUnits, &v.OutputUnits, &cost, &count, &unknown, &estimate); err != nil {
			return nil, err
		}
		if estimate.Valid && json.Unmarshal([]byte(estimate.String), &v.Estimate) != nil {
			return nil, ErrInvalidEditorialState
		}
		v.Cost = "未知"
		if count > 0 && unknown == 0 && cost.Valid {
			v.Cost = fmt.Sprintf("%.4f", cost.Float64)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// KnowledgeMaterialUsages resolves exact material/source identities to frozen revisions.
func (s *Store) KnowledgeMaterialUsages(ctx context.Context, sourceType, sourceID, materialID string) ([]MaterialUsage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT r.article_id,r.revision,r.title FROM knowledge_article_material_refs m JOIN knowledge_article_revisions r ON r.article_id=m.article_id AND r.revision=m.revision WHERE (?!='' AND m.material_id=?) OR (?!='' AND m.source_type=? AND m.source_id=?) ORDER BY r.revision DESC`, materialID, materialID, sourceID, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialUsage
	for rows.Next() {
		var id, title string
		var revision int
		if err := rows.Scan(&id, &revision, &title); err != nil {
			return nil, err
		}
		out = append(out, MaterialUsage{Kind: "knowledge_article", Title: title, Detail: fmt.Sprintf("自动文章 v%d", revision), Link: fmt.Sprintf("/knowledge-articles/%s?revision=%d", id, revision)})
	}
	return out, rows.Err()
}

// KnowledgeEvidenceState combines stored invalidation with an authoritative policy/content check.
func (s *Store) KnowledgeEvidenceState(ctx context.Context, article *KnowledgeArticleRecord, v *KnowledgeRevision) (string, string, error) {
	if v.EvidenceStatus != "valid" {
		return v.EvidenceStatus, "材料已变化或已删除；旧审校不再放行下载。", nil
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	if err := json.Unmarshal([]byte(v.InputJSON), &req); err != nil {
		return "", "", err
	}
	if err := json.Unmarshal([]byte(v.BlocksJSON), &blocks); err != nil {
		return "", "", err
	}
	used := map[string]bool{}
	for _, b := range blocks {
		for _, id := range b.MaterialIDs {
			used[id] = true
		}
	}
	var materials []provider.KnowledgeMaterial
	for _, m := range req.Materials {
		if used[m.ID] {
			materials = append(materials, m)
		}
	}
	err := s.CheckKnowledgeMaterials(ctx, article.ProfileID, article.Provider, materials)
	if err == nil {
		return "valid", "", nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrSnapshotInvalidated) {
		return "unavailable", err.Error(), nil
	}
	if errors.Is(err, ErrConflict) {
		return "outdated", err.Error(), nil
	}
	return "", "", err
}

// FailKnowledgeArticleRun cannot attach a superseded attempt's failure to a new draft.
func (s *Store) FailKnowledgeArticleRun(ctx context.Context, jobID, reason string) error {
	var articleID, stage string
	var expected int
	err := s.DB.QueryRowContext(ctx, `SELECT article_id,stage,parent_revision FROM knowledge_article_runs WHERE job_id=?`, jobID).Scan(&articleID, &stage, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		execution, getErr := s.GetJobExecution(ctx, jobID)
		if getErr != nil {
			return getErr
		}
		var input KnowledgeStageInput
		if decodeErr := json.Unmarshal([]byte(execution.InputSnapshotJSON), &input); decodeErr != nil {
			return decodeErr
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT source_id FROM processing_jobs WHERE id=? AND source_type='knowledge_article'`, jobID).Scan(&articleID); err != nil {
			return err
		}
		stage = input.Stage
		expected = 0
		if len(input.Request.Blocks) > 0 {
			expected = 1
		}
		if input.ExpectedRevision != nil {
			expected = *input.ExpectedRevision
		}
	} else if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET status='failed',reason=?,updated_at=datetime('now') WHERE id=? AND stage=? AND working_revision=? AND status NOT IN ('ready','needs_review','insufficient')`, reason, articleID, stage, expected)
	return err
}

// assignKnowledgeBlockIDs preserves only confirmed parent identities for Owner edits.
// AI-produced IDs and duplicate/new Owner IDs always receive application-owned identities.
func assignKnowledgeBlockIDs(ctx context.Context, tx *sql.Tx, id string, parent int, blocks []provider.KnowledgeBlock, owner bool) ([]provider.KnowledgeBlock, error) {
	allowed := map[string]bool{}
	if owner && parent > 0 {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT blocks_json FROM knowledge_article_revisions WHERE article_id=? AND revision=?`, id, parent).Scan(&raw); err != nil {
			return nil, err
		}
		var old []provider.KnowledgeBlock
		if err := json.Unmarshal([]byte(raw), &old); err != nil {
			return nil, err
		}
		for _, b := range old {
			if b.ID != "" {
				allowed[b.ID] = true
			}
		}
	}
	out := append([]provider.KnowledgeBlock(nil), blocks...)
	used := map[string]bool{}
	for i := range out {
		if !allowed[out[i].ID] || used[out[i].ID] {
			out[i].ID = uuid.NewString()
		}
		used[out[i].ID] = true
	}
	return out, nil
}
