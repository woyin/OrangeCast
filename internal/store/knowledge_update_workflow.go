package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func checkUpdateAutomation(ctx context.Context, tx *sql.Tx, automatic bool) error {
	if !automatic {
		return nil
	}
	var enabled bool
	var limit, count int
	if err := tx.QueryRowContext(ctx, `SELECT enabled,daily_limit FROM knowledge_update_settings WHERE id=1`).Scan(&enabled, &limit); err != nil {
		return err
	}
	if !enabled {
		return ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_update_proposals WHERE automated=1 AND date(analysis_admitted_at)=date('now')`).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrAutoDailyLimitReached
	}
	return nil
}
func checkUpdateParent(ctx context.Context, tx *sql.Tx, p *KnowledgeUpdateProposal) error {
	var current int
	var hash string
	err := tx.QueryRowContext(ctx, `SELECT a.working_revision,r.content_hash FROM knowledge_articles a JOIN knowledge_article_revisions r ON r.article_id=a.id AND r.revision=a.working_revision WHERE a.id=?`, p.ArticleID).Scan(&current, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != p.ParentRevision || hash != p.ParentHash {
		return ErrConflict
	}
	return nil
}

// CheckKnowledgeUpdateExecution rechecks both current sources and all old-prose dependencies before a paid step.
func (s *Store) CheckKnowledgeUpdateExecution(ctx context.Context, p *KnowledgeUpdateProposal, req provider.KnowledgeArticleRequest, automatic bool) error {
	if req.Stage == "update_propose" {
		if p.State != "pending" && p.State != "analysing" && p.State != "failed" {
			return ErrConflict
		}
	} else if p.State != "pending" && p.State != "accepted" && p.State != "generating" && p.State != "needs_review" && p.State != "failed" {
		return ErrConflict
	}
	a, err := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if err != nil {
		return err
	}
	parent, err := s.GetKnowledgeRevision(ctx, p.ArticleID, p.ParentRevision)
	if err != nil {
		return err
	}
	if parent.ContentHash != p.ParentHash {
		return ErrConflict
	}
	if err = s.CheckKnowledgeUpdateParentPolicy(ctx, a, parent); err != nil {
		return err
	}
	if err = s.CheckLearningQuestionExecution(ctx, req.Question, automatic); err != nil {
		return err
	}
	if err = s.CheckKnowledgeMaterials(ctx, a.ProfileID, p.Provider, req.Materials); err != nil {
		return err
	}
	if automatic {
		settings, e := s.GetKnowledgeUpdateSettings(ctx)
		if e != nil {
			return e
		}
		if !settings.Enabled {
			return ErrConflict
		}
	}
	return nil
}

// QueueKnowledgeUpdateAnalysis admits one analysis using the existing article queue and frozen budget estimates.
func (s *Store) QueueKnowledgeUpdateAnalysis(ctx context.Context, id string, automatic bool) error {
	p, err := s.GetKnowledgeUpdateProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.State == "analysing" || p.AnalysisJSON != "{}" {
		return nil
	}
	if p.State != "pending" {
		return ErrConflict
	}
	var req provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(p.InputJSON), &req); err != nil {
		return err
	}
	if err = s.CheckKnowledgeUpdateExecution(ctx, p, req, automatic); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkUpdateParent(ctx, tx, p); err != nil {
		return err
	}
	if err = checkQuestionAdmission(ctx, tx, req.Question, automatic, true); err != nil {
		return err
	}
	if err = checkUpdateAutomation(ctx, tx, automatic); err != nil {
		return err
	}
	if automatic {
		var unknown int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM processing_jobs WHERE source_type='knowledge_article' AND source_id=? AND result_state='unknown')`, p.ArticleID).Scan(&unknown); err != nil {
			return err
		}
		if unknown != 0 {
			return fmt.Errorf("%w: 此文章有远端未知结果，需Owner核对后明确重试", ErrConflict)
		}
	}
	var other int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_update_proposals WHERE article_id=? AND id!=? AND state IN ('analysing','generating'))`, p.ArticleID, id).Scan(&other); err != nil {
		return err
	}
	if other != 0 {
		return ErrConflict
	}
	req.Stage = "update_propose"
	model, err := freezeKnowledgeEstimate(ctx, tx, p.Provider, p.Model, &req)
	if err != nil {
		return err
	}
	jobID := uuid.NewString()
	revision := p.ParentRevision
	input := KnowledgeStageInput{ArticleID: p.ArticleID, Stage: req.Stage, Request: req, ExpectedRevision: &revision, UpdateProposalID: p.ID}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='analysing',reason='更新分析已入队，响应与用量见执行记录',job_id=?,automated=?,input_json=?,analysis_admitted_at=datetime('now'),updated_at=datetime('now') WHERE id=? AND state='pending' AND analysis_json='{}'`, jobID, automatic, jsonString(req), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,is_automated,intent_id,input_snapshot_json,config_version,configured_provider,configured_model) VALUES(?,'knowledge_article',?,'knowledge_article','queued',?,?,?,?,?,?)`, jobID, p.ArticleID, automatic, "knowledge-update:"+id+":analysis", jsonString(input), p.PromptVersion, p.Provider, model); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_article_runs(job_id,article_id,parent_revision,stage)VALUES(?,?,?,'update_propose')`, jobID, p.ArticleID, p.ParentRevision); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitKnowledgeUpdateAnalysis saves the decision and marks stale parents without changing the article.
func (s *Store) CommitKnowledgeUpdateAnalysis(ctx context.Context, job *models.ProcessingJob, input KnowledgeStageInput, result *provider.KnowledgeArticleResult) error {
	execution, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	if execution.ResultState == models.JobResultComplete {
		return nil
	}
	if err := provider.ValidateKnowledgeResult(input.Request, result); err != nil {
		return err
	}
	p, err := s.GetKnowledgeUpdateProposal(ctx, input.UpdateProposalID)
	if err != nil {
		return err
	}
	if p.ArticleID != input.ArticleID || input.Request.Update == nil || input.Request.Update.ProposalID != p.ID {
		return ErrConflict
	}
	if err = s.CheckKnowledgeUpdateExecution(ctx, p, input.Request, job.Automated); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var resultState string
	if err = tx.QueryRowContext(ctx, `SELECT result_state FROM processing_jobs WHERE id=?`, job.ID).Scan(&resultState); err != nil {
		return err
	}
	if resultState == models.JobResultComplete {
		return nil
	}
	state, reason := "pending", result.Update.Reason
	if err = checkUpdateParent(ctx, tx, p); err != nil {
		if !errors.Is(err, ErrConflict) {
			return err
		}
		state, reason = "parent_changed", "父稿已变化，原分析与用量保留；需要基于新稿建立提案"
	} else {
		switch result.Update.Decision {
		case "insufficient":
			state = "insufficient"
		case "no_change":
			state = "no_change"
		case "new_direction":
			state = "new_direction"
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state=?,reason=?,analysis_json=?,updated_at=datetime('now') WHERE id=? AND job_id=? AND state IN ('analysing','failed')`, state, reason, jsonString(result.Update), p.ID, job.ID); err != nil {
		return err
	}
	if state == "new_direction" && result.Update.NewDirection != nil {
		// Keep a different direction separate from an update to the same article.
		req := input.Request
		req.Stage = "discover"
		req.Blocks = nil
		req.Update = nil
		req.Estimate = nil
		req.Topic = nil
		req.History = nil
		hash := fmt.Sprintf("update-direction:%s", p.ID)
		batchID := "update-direction:" + p.ID
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_discovery_batches(id,profile_id,input_hash,input_json,scope_json,provider,model,prompt_version,automated,status) SELECT ?,profile_id,?,?,?,?,?,?,?,'complete' FROM knowledge_articles WHERE id=?`, batchID, hash, jsonString(req), req.ScopeJSON, p.Provider, p.Model, p.PromptVersion, false, p.ArticleID); err != nil {
			return err
		}
		if _, err = saveKnowledgeCandidates(ctx, tx, batchID, []provider.KnowledgeTopic{*result.Update.NewDirection}, nil); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_article_runs SET result_json=? WHERE job_id=?`, jsonString(result), job.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, jsonString(result), job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkKnowledgeUpdateParentChanged preserves response facts while rejecting application to a newer draft.
func (s *Store) MarkKnowledgeUpdateParentChanged(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='parent_changed',reason='父稿已变化，请基于新稿建立提案',updated_at=datetime('now') WHERE id=? AND state NOT IN ('completed','ignored')`, id)
	return err
}

// AcceptKnowledgeUpdate creates a directed revision of the same article after CAS and policy checks.
func (s *Store) AcceptKnowledgeUpdate(ctx context.Context, id string, automatic bool) error {
	p, err := s.GetKnowledgeUpdateProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.State == "generating" || p.State == "needs_review" || p.State == "completed" {
		return nil
	}
	if p.State != "pending" || p.AnalysisJSON == "{}" {
		return ErrConflict
	}
	var req provider.KnowledgeArticleRequest
	var analysis provider.KnowledgeUpdateAnalysis
	if json.Unmarshal([]byte(p.InputJSON), &req) != nil || json.Unmarshal([]byte(p.AnalysisJSON), &analysis) != nil {
		return ErrInvalidEditorialState
	}
	if analysis.Decision != "update" {
		return ErrConflict
	}
	if err = s.CheckKnowledgeUpdateExecution(ctx, p, req, automatic); err != nil {
		return err
	}
	if err = provider.ValidateKnowledgeUpdate(req, &analysis); err != nil {
		return err
	}
	req.Update.Analysis = &analysis
	req.Instructions = "仅按已接受的更新判断修改父稿。每个新事实和引语必须由当前materials支持，保留/补充/删除/反驳分别对应判断中的理由；不能使用父稿作为缺失证据。"
	req.Estimate = nil
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkUpdateParent(ctx, tx, p); err != nil {
		tx.Rollback()
		if errors.Is(err, ErrConflict) {
			_ = s.MarkKnowledgeUpdateParentChanged(ctx, p.ID)
		}
		return err
	}
	if err = checkQuestionAdmission(ctx, tx, req.Question, automatic, automatic); err != nil {
		return err
	}
	if automatic {
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM knowledge_update_settings WHERE id=1`).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return ErrConflict
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='accepted',owner_action=?,updated_at=datetime('now') WHERE id=? AND state='pending'`, map[bool]string{true: "automation_accepted", false: "accepted"}[automatic], id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	res, err = tx.ExecContext(ctx, `UPDATE knowledge_articles SET status='revise',stage='revise',reason='依据增量更新，原通过版保留' WHERE id=? AND working_revision=? AND status NOT IN ('discover','select','write','review','revise','review_final')`, p.ArticleID, p.ParentRevision)
	if err != nil {
		return err
	}
	n, err = res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if err = enqueueKnowledgeStage(ctx, tx, p.ArticleID, "revise", p.Provider, p.Model, req, automatic); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='generating' WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetKnowledgeUpdateDisposition records ignore/defer actions without changing underlying material or article bodies.
func (s *Store) SetKnowledgeUpdateDisposition(ctx context.Context, id, action, reason string) error {
	if action != "ignore" && action != "defer" {
		return ErrInvalidEditorialState
	}
	if len([]rune(reason)) > 2000 {
		return ErrInvalidEditorialState
	}
	state := "ignored"
	if action == "defer" {
		state = "deferred"
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE knowledge_update_proposals SET deferred_state=CASE WHEN state='deferred' THEN deferred_state ELSE state END,state=?,owner_action=?,owner_reason=?,updated_at=datetime('now') WHERE id=? AND state IN ('pending','insufficient','new_direction','no_change','deferred')`, state, action, strings.TrimSpace(reason), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// RetryKnowledgeUpdateAnalysis is an explicit new attempt, reusing only valid known response checkpoints.
func (s *Store) RetryKnowledgeUpdateAnalysis(ctx context.Context, id string) error {
	p, err := s.GetKnowledgeUpdateProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.State != "failed" || p.JobID == "" {
		return ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkUpdateParent(ctx, tx, p); err != nil {
		return err
	}
	var snapshot, checkpoint string
	if err = tx.QueryRowContext(ctx, `SELECT input_snapshot_json,checkpoint_json FROM processing_jobs WHERE id=? AND status='failed'`, p.JobID).Scan(&snapshot, &checkpoint); err != nil {
		return err
	}
	var prior KnowledgeStageInput
	var cached struct {
		Result *provider.KnowledgeArticleResult `json:"result"`
	}
	if checkpoint != "" && (json.Unmarshal([]byte(snapshot), &prior) != nil || json.Unmarshal([]byte(checkpoint), &cached) != nil || provider.ValidateKnowledgeResult(prior.Request, cached.Result) != nil) {
		checkpoint = ""
	}
	next := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,checkpoint_json,config_version,configured_provider,configured_model) SELECT ?,source_type,source_id,job_type,'queued',intent_id,input_snapshot_json,?,config_version,configured_provider,configured_model FROM processing_jobs WHERE id=?`, next, checkpoint, p.JobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_article_runs(job_id,article_id,parent_revision,stage)VALUES(?,?,?,'update_propose')`, next, p.ArticleID, p.ParentRevision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='analysing',job_id=?,automated=0,reason='',updated_at=datetime('now') WHERE id=? AND state='failed'`, next, id); err != nil {
		return err
	}
	return tx.Commit()
}

func knowledgeUpdateID(req provider.KnowledgeArticleRequest) string {
	if req.Update != nil {
		return req.Update.ProposalID
	}
	return ""
}
func updateKnowledgeProposalStage(ctx context.Context, tx *sql.Tx, id, state string, revision int) error {
	_, err := tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state=?,generated_revision=CASE WHEN ?='parent_changed' THEN generated_revision ELSE ? END,reason=CASE WHEN ?='parent_changed' THEN '执行期间父稿已变化，响应与用量保留，新结果未选择为当前工作稿' ELSE reason END,updated_at=datetime('now') WHERE id=? AND state NOT IN ('completed','ignored','insufficient')`, state, state, revision, state, id)
	return err
}
func (s *Store) failKnowledgeUpdateJob(ctx context.Context, jobID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='failed',reason=?,updated_at=datetime('now') WHERE id=COALESCE((SELECT json_extract(CASE WHEN json_valid(input_snapshot_json) THEN input_snapshot_json ELSE '{}' END,'$.update_proposal_id') FROM processing_jobs WHERE id=?),'') AND state IN ('analysing','accepted','generating','needs_review','failed')`, reason, jobID)
	return err
}

// ResumeKnowledgeUpdate returns a deferred decision to its original state; it
// neither refreshes its frozen input nor spends money.
func (s *Store) ResumeKnowledgeUpdate(ctx context.Context, id string) error {
	p, err := s.GetKnowledgeUpdateProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.State != "deferred" {
		return ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkUpdateParent(ctx, tx, p); err != nil {
		return err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT deferred_state FROM knowledge_update_proposals WHERE id=? AND state='deferred'`, id).Scan(&state); err != nil {
		return err
	}
	if state != "pending" && state != "insufficient" && state != "new_direction" && state != "no_change" {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state=?,owner_action='resume',updated_at=datetime('now') WHERE id=? AND state='deferred'`, state, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// RetryKnowledgeUpdate chooses the failed analysis or the existing revision
// recovery path. All earlier attempts and receipts remain intact.
func (s *Store) RetryKnowledgeUpdate(ctx context.Context, id string) error {
	p, err := s.GetKnowledgeUpdateProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.State != "failed" {
		return ErrConflict
	}
	if p.AnalysisJSON == "{}" {
		return s.RetryKnowledgeUpdateAnalysis(ctx, id)
	}
	a, err := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if err != nil {
		return err
	}
	if a.Status != "failed" {
		return ErrConflict
	}
	// The article retry verifies and restores the proposal in the same transaction.
	return s.RetryKnowledgeArticle(ctx, p.ArticleID)
}
