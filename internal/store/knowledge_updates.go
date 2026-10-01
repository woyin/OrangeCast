package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// KnowledgeUpdateSettings is a separate, initially disabled automation preference.
type KnowledgeUpdateSettings struct {
	Enabled    bool
	DailyLimit int
}

// KnowledgeUpdateProposal preserves an exact parent, material changes and all subsequent decisions.
type KnowledgeUpdateProposal struct {
	ArticleTitle                                                                                    string
	ID, ArticleID, ParentHash, QuestionID, InputHash, InputJSON, Provider, Model, PromptVersion     string
	State, Reason, AnalysisJSON, ChangesJSON, JobID, OwnerAction, OwnerReason, CreatedAt, UpdatedAt string
	ParentRevision, PassedRevision, QuestionRevision, GeneratedRevision                             int
	Automated                                                                                       bool
	LastSeq                                                                                         int64
}

const knowledgeUpdateColumns = `id,article_id,parent_revision,parent_hash,passed_revision,question_id,question_revision,input_hash,input_json,provider,model,prompt_version,state,reason,analysis_json,changes_json,generated_revision,job_id,automated,last_seq,owner_action,owner_reason,created_at,updated_at,(SELECT title FROM knowledge_articles a WHERE a.id=article_id)`

func scanKnowledgeUpdate(row interface{ Scan(...any) error }) (*KnowledgeUpdateProposal, error) {
	p := &KnowledgeUpdateProposal{}
	err := row.Scan(&p.ID, &p.ArticleID, &p.ParentRevision, &p.ParentHash, &p.PassedRevision, &p.QuestionID, &p.QuestionRevision, &p.InputHash, &p.InputJSON, &p.Provider, &p.Model, &p.PromptVersion, &p.State, &p.Reason, &p.AnalysisJSON, &p.ChangesJSON, &p.GeneratedRevision, &p.JobID, &p.Automated, &p.LastSeq, &p.OwnerAction, &p.OwnerReason, &p.CreatedAt, &p.UpdatedAt, &p.ArticleTitle)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}

// GetKnowledgeUpdateSettings reads an independent opt-in without changing article discovery settings.
func (s *Store) GetKnowledgeUpdateSettings(ctx context.Context) (KnowledgeUpdateSettings, error) {
	var v KnowledgeUpdateSettings
	err := s.DB.QueryRowContext(ctx, `SELECT enabled,daily_limit FROM knowledge_update_settings WHERE id=1`).Scan(&v.Enabled, &v.DailyLimit)
	return v, err
}

// SetKnowledgeUpdateSettings saves the explicit update preference and bounded daily admission limit.
func (s *Store) SetKnowledgeUpdateSettings(ctx context.Context, v KnowledgeUpdateSettings) error {
	if v.DailyLimit < 1 || v.DailyLimit > 10 {
		return ErrInvalidEditorialState
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_update_settings SET enabled=?,daily_limit=? WHERE id=1`, v.Enabled, v.DailyLimit)
	return err
}

// GetKnowledgeUpdateProposal reads a frozen proposal without calling a model.
func (s *Store) GetKnowledgeUpdateProposal(ctx context.Context, id string) (*KnowledgeUpdateProposal, error) {
	return scanKnowledgeUpdate(s.DB.QueryRowContext(ctx, `SELECT `+knowledgeUpdateColumns+` FROM knowledge_update_proposals WHERE id=?`, id))
}

// ListKnowledgeUpdateProposals lists bounded article-specific or inbox proposals.
func (s *Store) ListKnowledgeUpdateProposals(ctx context.Context, articleID, state string) ([]*KnowledgeUpdateProposal, error) {
	if len(articleID) > 200 || len(state) > 40 {
		return nil, ErrInvalidEditorialState
	}
	columns := strings.Replace(knowledgeUpdateColumns, ",input_json,", ",'{}' AS input_json,", 1)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM knowledge_update_proposals WHERE (?='' OR article_id=?) AND (?='' OR state=?) ORDER BY created_at DESC,id DESC LIMIT 100`, articleID, articleID, state, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*KnowledgeUpdateProposal{}
	for rows.Next() {
		p, e := scanKnowledgeUpdate(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PendingAutomaticKnowledgeUpdates is independent of the inbox display limit.
func (s *Store) PendingAutomaticKnowledgeUpdates(ctx context.Context) ([]*KnowledgeUpdateProposal, error) {
	columns := strings.Replace(knowledgeUpdateColumns, ",input_json,", ",'{}' AS input_json,", 1)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM knowledge_update_proposals WHERE state='pending' AND owner_action='' ORDER BY created_at,id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnowledgeUpdateProposal
	for rows.Next() {
		p, e := scanKnowledgeUpdate(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// KnowledgeArticleForQuestion finds a current article answering the same Owner
// goal. A changed purpose remains a separate direction discovery.
func (s *Store) KnowledgeArticleForQuestion(ctx context.Context, profileID, questionID string) (*KnowledgeArticleRecord, error) {
	q, err := s.GetLearningQuestion(ctx, questionID)
	if err != nil {
		return nil, err
	}
	var id string
	err = s.DB.QueryRowContext(ctx, `SELECT a.id FROM knowledge_articles a JOIN knowledge_article_revisions r ON r.article_id=a.id AND r.revision=a.working_revision JOIN learning_question_links l ON l.kind='article' AND l.object_id=a.id AND l.question_id=? AND l.state='confirmed'
	 WHERE a.profile_id=? AND json_extract(CASE WHEN json_valid(r.input_json) THEN r.input_json ELSE '{}' END,'$.learning_question.id')=? AND json_extract(CASE WHEN json_valid(r.input_json) THEN r.input_json ELSE '{}' END,'$.learning_question.body')=? AND json_extract(CASE WHEN json_valid(r.input_json) THEN r.input_json ELSE '{}' END,'$.learning_question.goal')=? ORDER BY a.updated_at DESC,a.id DESC LIMIT 1`, questionID, profileID, questionID, q.Body, q.Goal).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetKnowledgeArticle(ctx, id)
}

// CollectKnowledgeUpdateSignals advances a bounded local outbox using material/source/question indexes.
func (s *Store) CollectKnowledgeUpdateSignals(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cursor int64
	if err = tx.QueryRowContext(ctx, `SELECT last_seq FROM knowledge_update_cursor WHERE id=1`).Scan(&cursor); err != nil {
		return err
	}
	type signal struct {
		seq                            int64
		kind, id, sourceType, sourceID string
	}
	var signals []signal
	rows, err := tx.QueryContext(ctx, `SELECT seq,kind,material_id,source_type,source_id FROM knowledge_learning_changes WHERE seq>? ORDER BY seq LIMIT 200`, cursor)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v signal
		if err = rows.Scan(&v.seq, &v.kind, &v.id, &v.sourceType, &v.sourceID); err != nil {
			rows.Close()
			return err
		}
		signals = append(signals, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range signals {
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_update_pending_articles(article_id,last_seq)
   SELECT id,? FROM knowledge_articles WHERE working_revision>0 AND (
    id IN(SELECT article_id FROM knowledge_article_material_index WHERE material_id=? OR (source_type=? AND source_id=? AND source_id!=''))
    OR id IN(SELECT qa.object_id FROM learning_question_links qa WHERE qa.kind='article' AND qa.state='confirmed' AND (qa.question_id=? AND ?='learning_question' OR qa.question_id IN(SELECT question_id FROM learning_question_links WHERE state='confirmed' AND (object_id=? OR source_type=? AND source_id=? AND source_id!='')))))
   ON CONFLICT(article_id) DO UPDATE SET last_seq=MAX(last_seq,excluded.last_seq)`, v.seq, v.id, v.sourceType, v.sourceID, v.id, v.kind, v.id, v.sourceType, v.sourceID)
		if err != nil {
			return err
		}
		cursor = v.seq
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_update_cursor SET last_seq=? WHERE id=1`, cursor); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingKnowledgeUpdateArticles returns oldest signaled articles for bounded background inspection.
func (s *Store) PendingKnowledgeUpdateArticles(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT article_id FROM knowledge_update_pending_articles ORDER BY last_seq,article_id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func updateParentBlocks(parent *KnowledgeRevision) ([]provider.KnowledgeBlock, error) {
	var blocks []provider.KnowledgeBlock
	if err := json.Unmarshal([]byte(parent.BlocksJSON), &blocks); err != nil {
		return nil, err
	}
	if len(parent.BlocksJSON) > 100000 {
		return nil, fmt.Errorf("父稿超过100KB，请先缩小更新范围")
	}
	for i := range blocks {
		if blocks[i].ID == "" {
			blocks[i].ID = fmt.Sprintf("legacy:%d:%s", i, parent.ContentHash[:min(16, len(parent.ContentHash))])
		}
	}
	return blocks, nil
}
func materialUpdateChanges(old, current []provider.KnowledgeMaterial) []provider.KnowledgeMaterialChange {
	previous := map[string]provider.KnowledgeMaterial{}
	for _, m := range old {
		previous[m.ID] = m
	}
	var changes []provider.KnowledgeMaterialChange
	for _, m := range current {
		o, exists := previous[m.ID]
		delete(previous, m.ID)
		kind, reason := "added", "当前合格的新材料，是否有实质用途需判断"
		if exists {
			if o.Version == m.Version && o.SnapshotID == m.SnapshotID && o.Content == m.Content && o.Description == m.Description {
				continue
			}
			kind, reason = "changed", "材料版本、内容或确切依据发生变化，不继承旧引语有效性"
		}
		changes = append(changes, provider.KnowledgeMaterialChange{MaterialID: m.ID, Kind: kind, BeforeVersion: o.Version, AfterVersion: m.Version, BeforeSnapshot: o.SnapshotID, AfterSnapshot: m.SnapshotID, Reason: reason})
	}
	for _, m := range previous {
		changes = append(changes, provider.KnowledgeMaterialChange{MaterialID: m.ID, Kind: "unavailable", BeforeVersion: m.Version, BeforeSnapshot: m.SnapshotID, Reason: "未进入当前合格范围；不是模型认定无用"})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].MaterialID < changes[j].MaterialID })
	return changes
}

// CheckKnowledgeUpdateParentPolicy checks the source dependencies in old prose, even after selecting new material.
func (s *Store) CheckKnowledgeUpdateParentPolicy(ctx context.Context, article *KnowledgeArticleRecord, parent *KnowledgeRevision) error {
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	if json.Unmarshal([]byte(parent.InputJSON), &req) != nil || json.Unmarshal([]byte(parent.BlocksJSON), &blocks) != nil {
		return ErrInvalidEditorialState
	}
	used := map[string]bool{}
	for _, b := range blocks {
		for _, id := range b.MaterialIDs {
			used[id] = true
		}
	}
	seen := map[string]bool{}
	for _, m := range req.Materials {
		if !used[m.ID] {
			continue
		}
		seen[m.ID] = true
		allowed, err := s.CanUseSourceForPublication(ctx, article.ProfileID, models.SourceType(m.SourceType), m.SourceID)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: 父稿依赖的来源已归档或撤回用途权限", ErrConflict)
		}
		allowed, err = s.CanSendSourceToProvider(ctx, models.SourceType(m.SourceType), m.SourceID, article.Provider)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: 父稿依赖的来源禁止外发；不能通过旧正文绕过", ErrConflict)
		}
		if m.Kind == "keypoint" {
			if _, err = s.GetKeyPoint(ctx, m.ID); err != nil {
				return fmt.Errorf("父稿重点已清理: %w", err)
			}
		} else if _, err = s.GetOwnerNote(ctx, m.ID); err != nil {
			return fmt.Errorf("父稿笔记已清理: %w", err)
		}
		if m.SnapshotID != "" {
			snapshot, _, _, err := s.SnapshotContent(ctx, m.SnapshotID)
			if err != nil {
				return err
			}
			if snapshot.SourceID != m.SourceID || string(snapshot.SourceType) != m.SourceType {
				return ErrConflict
			}
		}
	}
	for id := range used {
		if !seen[id] {
			return fmt.Errorf("%w: 父稿材料映射缺失", ErrConflict)
		}
	}
	return nil
}

func (s *Store) currentKnowledgeUpdateMaterial(ctx context.Context, a *KnowledgeArticleRecord, id string) (*provider.KnowledgeMaterial, error) {
	note, err := s.GetOwnerNote(ctx, id)
	if errors.Is(err, ErrNotFound) || err == nil && note.Kind != "source_note" {
		return s.knowledgeMaterial(ctx, a.ProfileID, a.Provider, id)
	}
	if err != nil {
		return nil, err
	}
	// Resolve current source evidence before checking source-version validity;
	// the regular material reader deliberately refuses stale source assertions.
	m := &provider.KnowledgeMaterial{ID: note.ID, Kind: note.Kind, SourceType: note.SourceType, SourceID: note.SourceID, Version: note.Revision, Content: note.Content}
	if err = json.Unmarshal([]byte(note.CitationsJSON), &m.Citations); err != nil {
		return nil, err
	}
	ok, err := s.prepareKnowledgeMaterial(ctx, a.ProfileID, a.Provider, m)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	_, audio, documents, err := s.SnapshotContent(ctx, m.SnapshotID)
	if err != nil {
		return nil, err
	}
	for _, seg := range audio {
		if seg.ID == m.Citations[0] {
			m.Position = seg.Start
			break
		}
	}
	for _, seg := range documents {
		if seg.ID == m.Citations[0] {
			m.Position = float64(seg.Position)
			break
		}
	}
	m.RetrievalReason = "来源笔记的原锚点仍保留；本次对照当前引用片段，旧总结是否仍成立需要重新判断"
	if err = s.CheckKnowledgeMaterials(ctx, a.ProfileID, a.Provider, []provider.KnowledgeMaterial{*m}); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrSnapshotInvalidated) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

func (s *Store) freezeKnowledgeUpdate(ctx context.Context, a *KnowledgeArticleRecord, parent *KnowledgeRevision, models map[string]string) (provider.KnowledgeArticleRequest, error) {
	var old provider.KnowledgeArticleRequest
	if err := json.Unmarshal([]byte(parent.InputJSON), &old); err != nil {
		return old, err
	}
	blocks, err := updateParentBlocks(parent)
	if err != nil {
		return old, err
	}
	scope := KnowledgeScope{}
	if old.ScopeJSON != "" {
		if err = json.Unmarshal([]byte(old.ScopeJSON), &scope); err != nil {
			return old, err
		}
	}
	scope.Question = nil
	scope.ExploreHistory = false
	scope.HistoryCursor = 0
	scope.ExpectedQuestionRevision = 0
	scope.MaterialIDs = nil
	if old.Question != nil {
		scope.QuestionID = old.Question.ID
	}
	req, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, a.ProfileID, a.Provider, scope, false)
	if err != nil {
		return old, err
	}
	if old.Topic != nil {
		req, err = s.RecallKnowledgeMaterials(ctx, a.ProfileID, a.Provider, req, *old.Topic)
		if err != nil {
			return req, err
		}
	}
	byID := map[string]bool{}
	var pool []provider.KnowledgeMaterial
	for _, previous := range old.Materials {
		m, e := s.currentKnowledgeUpdateMaterial(ctx, a, previous.ID)
		if e != nil {
			return req, e
		}
		if m == nil || !questionAllowsMaterial(req.Question, *m) {
			continue
		}
		question := ""
		if old.Topic != nil {
			question = old.Topic.Question + " " + old.Topic.Thesis
		}
		if e := s.compactKnowledgeEvidence(ctx, m, question); e != nil {
			return req, e
		}
		pool = append(pool, *m)
		byID[m.ID] = true
	}
	for _, m := range req.Materials {
		if !byID[m.ID] {
			pool = append(pool, m)
			byID[m.ID] = true
		}
	}
	// Give actual increments space before unchanged context. A full old pool
	// must not hide a newly admitted counterexample from the model.
	oldByID := map[string]provider.KnowledgeMaterial{}
	for _, m := range old.Materials {
		oldByID[m.ID] = m
	}
	sort.SliceStable(pool, func(i, j int) bool {
		changed := func(m provider.KnowledgeMaterial) bool {
			o, ok := oldByID[m.ID]
			return !ok || o.Version != m.Version || o.SnapshotID != m.SnapshotID || o.Content != m.Content || o.Description != m.Description
		}
		return changed(pool[i]) && !changed(pool[j])
	})
	req.Materials = boundKnowledgeMaterials(pool, 20)
	req.Exclusions = append(req.Exclusions, knowledgeExclusions(pool, req.Materials)...)
	admitted := map[string]bool{}
	for _, m := range req.Materials {
		admitted[m.ID] = true
	}
	var exclusions []provider.KnowledgeExclusion
	for _, e := range req.Exclusions {
		if !admitted[e.MaterialID] {
			exclusions = append(exclusions, e)
		}
	}
	req.Exclusions = exclusions
	for i := range req.Candidates {
		c := &req.Candidates[i]
		if admitted[c.MaterialID] {
			c.State, c.Reason = "admitted", "本次更新的合格冻结材料"
		} else if c.State == "admitted" {
			c.State, c.Reason = "skipped", "更新优先新增或变化材料，容量内未纳入"
		}
	}
	if req.Coverage != nil {
		req.Coverage.AdmittedCount = len(req.Materials)
	}
	req.Stage, req.PromptVersion, req.Topic, req.Blocks, req.History = "update_propose", provider.KnowledgeArticlePromptVersion, old.Topic, blocks, nil
	req.StageConfigs = provider.FreezeKnowledgeStageConfigs(models)
	cfg := req.StageConfigs["discover"]
	cfg.MaxOutputTokens = max(cfg.MaxOutputTokens, 4096)
	req.StageConfigs["update_propose"] = cfg
	req.ReviewModel = models["review"]
	req.Update = &provider.KnowledgeUpdateContext{ParentRevision: parent.Revision, ParentHash: parent.ContentHash, ParentTitle: parent.Title, Changes: materialUpdateChanges(old.Materials, req.Materials)}
	return req, nil
}

// ReserveKnowledgeUpdateProposal freezes a changed input once; it does not start a paid call.
func (s *Store) ReserveKnowledgeUpdateProposal(ctx context.Context, articleID string, expected int, models map[string]string) (*KnowledgeUpdateProposal, bool, error) {
	a, err := s.GetKnowledgeArticle(ctx, articleID)
	if err != nil {
		return nil, false, err
	}
	if expected < 1 || a.WorkingRevision != expected {
		return nil, false, ErrConflict
	}
	parent, err := s.GetKnowledgeRevision(ctx, articleID, expected)
	if err != nil {
		return nil, false, err
	}
	req, err := s.freezeKnowledgeUpdate(ctx, a, parent, models)
	questionMissing := false
	if errors.Is(err, ErrNotFound) {
		var old provider.KnowledgeArticleRequest
		if json.Unmarshal([]byte(parent.InputJSON), &old) == nil && old.Question != nil {
			if _, e := s.GetLearningQuestion(ctx, old.Question.ID); errors.Is(e, ErrNotFound) {
				questionMissing = true
				req = old
				req.Blocks, err = updateParentBlocks(parent)
				req.Stage, req.PromptVersion, req.Estimate, req.History = "update_propose", provider.KnowledgeArticlePromptVersion, nil, nil
				req.StageConfigs = provider.FreezeKnowledgeStageConfigs(models)
				cfg := req.StageConfigs["discover"]
				cfg.MaxOutputTokens = max(4096, cfg.MaxOutputTokens)
				req.StageConfigs["update_propose"] = cfg
				req.Update = &provider.KnowledgeUpdateContext{ParentRevision: expected, ParentHash: parent.ContentHash, ParentTitle: parent.Title}
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	state, reason := "pending", "材料变化待核对，尚未调用模型"
	if len(req.Materials) < 2 {
		state, reason = "insufficient", "当前合格材料不足两项"
	}
	if len(req.Update.Changes) == 0 && (req.Question == nil || !questionPurposeChanged(parent.InputJSON, req.Question)) {
		state, reason = "no_change", "当前问题及材料没有变化，不调用模型"
	}
	if e := s.CheckKnowledgeUpdateParentPolicy(ctx, a, parent); e != nil {
		state, reason = "insufficient", e.Error()
	}
	if questionMissing {
		state, reason = "insufficient", "原学习问题已删除，不能自动扩大材料范围；冻结旧输入仅供本地查看"
	}
	// A route/prompt change alone must not revive an ignored or insufficient
	// decision. Fingerprint actual material identities/content and Owner scope
	// separately from the complete configured input hash.
	materialFacts := make([]provider.KnowledgeMaterial, 0, len(req.Materials))
	for _, m := range req.Materials {
		materialFacts = append(materialFacts, provider.KnowledgeMaterial{ID: m.ID, Kind: m.Kind, SourceType: m.SourceType, SourceID: m.SourceID, SnapshotID: m.SnapshotID, Version: m.Version, Content: m.Content, Description: m.Description})
	}
	sort.Slice(materialFacts, func(i, j int) bool { return materialFacts[i].ID < materialFacts[j].ID })
	var questionFacts *provider.FrozenLearningQuestion
	if req.Question != nil {
		q := *req.Question
		q.Revision = 0
		q.TargetDate = ""
		q.ThemeID = ""
		questionFacts = &q
	}
	blockedReason := ""
	if state == "insufficient" {
		blockedReason = reason
	}
	materialHash := fmt.Sprintf("%x", sha256.Sum256([]byte(jsonString(struct {
		Materials     []provider.KnowledgeMaterial
		Question      *provider.FrozenLearningQuestion
		BlockedReason string
	}{materialFacts, questionFacts, blockedReason}))))
	hashReq := req
	hashReq.Update.ProposalID = ""
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(articleID+"\x00"+jsonString(hashReq))))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var actual int
	if err = tx.QueryRowContext(ctx, `SELECT working_revision FROM knowledge_articles WHERE id=?`, articleID).Scan(&actual); err != nil {
		return nil, false, err
	}
	if actual != expected {
		return nil, false, ErrConflict
	}
	if !questionMissing {
		err = checkQuestionAdmission(ctx, tx, req.Question, false, true)
	}
	if err != nil {
		return nil, false, err
	}
	var last int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM knowledge_learning_changes`).Scan(&last); err != nil {
		return nil, false, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM knowledge_update_proposals WHERE input_hash=? OR (article_id=? AND parent_hash=? AND material_fingerprint=? AND state IN ('insufficient','no_change','ignored','deferred')) ORDER BY created_at DESC,id DESC LIMIT 1`, hash, articleID, parent.ContentHash, materialHash).Scan(&existing)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_update_pending_articles WHERE article_id=? AND last_seq<=?`, articleID, last); err != nil {
			return nil, false, err
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		p, e := s.GetKnowledgeUpdateProposal(ctx, existing)
		return p, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	id := uuid.NewString()
	req.Update.ProposalID = id
	questionID, questionRev := "", 0
	if req.Question != nil {
		questionID, questionRev = req.Question.ID, req.Question.Revision
	}
	cfg := req.StageConfigs["update_propose"]
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_update_proposals(id,article_id,parent_revision,parent_hash,passed_revision,question_id,question_revision,input_hash,material_fingerprint,input_json,provider,model,prompt_version,state,reason,changes_json,last_seq)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, articleID, expected, parent.ContentHash, a.PassedRevision, questionID, questionRev, hash, materialHash, jsonString(req), a.Provider, cfg.Model, req.PromptVersion, state, reason, jsonString(req.Update.Changes), last); err != nil {
		return nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_update_pending_articles WHERE article_id=? AND last_seq<=?`, articleID, last); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	p, e := s.GetKnowledgeUpdateProposal(ctx, id)
	return p, true, e
}
func questionPurposeChanged(raw string, current *provider.FrozenLearningQuestion) bool {
	var req provider.KnowledgeArticleRequest
	if json.Unmarshal([]byte(raw), &req) != nil {
		return true
	}
	if req.Question == nil || current == nil {
		return req.Question != nil || current != nil
	}
	return req.Question.ID != current.ID || req.Question.Body != current.Body || req.Question.Goal != current.Goal
}
