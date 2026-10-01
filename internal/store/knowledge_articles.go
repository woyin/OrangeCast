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
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// KnowledgeArticleSettings controls explicit automation and article preferences.
type KnowledgeArticleSettings struct {
	QuestionID                  string
	Enabled                     bool
	DailyLimit, DebounceMinutes int
	Audience, Style             string
}

// KnowledgeArticleRecord is a durable automatic article and its pipeline state.
type KnowledgeArticleRecord struct {
	DiscoveryBatchID, CandidateID                                                                string
	WorkingRevision, PassedRevision                                                              int
	ReviewModel                                                                                  string
	ID, ProfileID, InputHash, InputJSON, Status, Stage, Title, Thesis                            string
	TopicsJSON, TopicJSON, BlocksJSON, IssuesJSON, Reason, Provider, Model, CreatedAt, UpdatedAt string
}

// KnowledgeStageInput freezes the request and article identity for one queued stage.
type KnowledgeStageInput struct {
	UpdateProposalID string                           `json:"update_proposal_id,omitempty"`
	ExpectedRevision *int                             `json:"expected_revision,omitempty"`
	ArticleID        string                           `json:"article_id"`
	Stage            string                           `json:"stage"`
	Request          provider.KnowledgeArticleRequest `json:"request"`
}

// GetKnowledgeArticleSettings reads the singleton automatic-article policy.
func (s *Store) GetKnowledgeArticleSettings(ctx context.Context) (KnowledgeArticleSettings, error) {
	var v KnowledgeArticleSettings
	err := s.DB.QueryRowContext(ctx, `SELECT enabled,daily_limit,debounce_minutes,audience,style,question_id FROM knowledge_article_settings WHERE id=1`).Scan(&v.Enabled, &v.DailyLimit, &v.DebounceMinutes, &v.Audience, &v.Style, &v.QuestionID)
	return v, err
}

// SetKnowledgeArticleSettings updates the explicit opt-in and bounded frequency.
func (s *Store) SetKnowledgeArticleSettings(ctx context.Context, v KnowledgeArticleSettings) error {
	if v.DailyLimit < 1 || v.DailyLimit > 10 || v.DebounceMinutes < 0 || v.DebounceMinutes > 1440 || strings.TrimSpace(v.Audience) == "" || strings.TrimSpace(v.Style) == "" || len([]rune(v.Style)) > 2000 || len([]rune(v.Audience)) > 500 {
		return fmt.Errorf("每日篇数需为1–10，防抖为0–1440分钟，读者和风格不能为空或过长")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v.QuestionID != "" {
		var found int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM learning_questions WHERE id=?`, v.QuestionID).Scan(&found); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE knowledge_article_settings SET enabled=?,daily_limit=?,debounce_minutes=?,audience=?,style=?,question_id=?,updated_at=datetime('now') WHERE id=1`, v.Enabled, v.DailyLimit, v.DebounceMinutes, v.Audience, v.Style, v.QuestionID)
	if err == nil {
		err = tx.Commit()
	}
	return err
}

// BuildKnowledgeArticleRequest collects bounded, policy-eligible learning inputs.
func (s *Store) BuildKnowledgeArticleRequest(ctx context.Context, profileID, providerName string) (provider.KnowledgeArticleRequest, string, error) {
	prefs, err := s.GetKnowledgeArticleSettings(ctx)
	if err != nil {
		return provider.KnowledgeArticleRequest{}, "", err
	}
	req := provider.KnowledgeArticleRequest{PromptVersion: provider.KnowledgeArticlePromptVersion, Stage: "discover", Audience: prefs.Audience, Style: prefs.Style}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM keypoint_index WHERE quality_status IN ('ready','owner_confirmed') AND stale_at IS NULL AND evidence_status!='stale' AND production_status!='dismissed' ORDER BY created_at DESC,id LIMIT 40`)
	if err != nil {
		return req, "", err
	}
	var keyIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return req, "", err
		}
		keyIDs = append(keyIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return req, "", err
	}
	var latest string
	for _, id := range keyIDs {
		kp, err := s.GetKeyPoint(ctx, id)
		if err != nil {
			return req, "", err
		}
		eligible, err := s.IsKeyPointEligibleForProfile(ctx, profileID, id)
		if err != nil {
			return req, "", err
		}
		if !eligible {
			continue
		}
		m := provider.KnowledgeMaterial{ID: kp.ID, Kind: "keypoint", SourceType: string(kp.SourceType), SourceID: kp.SourceID, SourceTitle: kp.SourceTitle, Version: kp.CardVersion, Content: kp.Content, Description: kp.Description}
		if err := json.Unmarshal([]byte(kp.CitationsJSON), &m.Citations); err != nil {
			return req, "", err
		}
		ok, err := s.prepareKnowledgeMaterial(ctx, profileID, providerName, &m)
		if err != nil {
			return req, "", err
		}
		if !ok {
			continue
		}
		req.Materials = append(req.Materials, m)
		if kp.CreatedAt > latest {
			latest = kp.CreatedAt
		}
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT id FROM owner_notes ORDER BY updated_at DESC,id LIMIT 40`)
	if err != nil {
		return req, "", err
	}
	var noteIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return req, "", err
		}
		noteIDs = append(noteIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return req, "", err
	}
	for _, id := range noteIDs {
		n, err := s.GetOwnerNote(ctx, id)
		if err != nil {
			return req, "", err
		}
		m := provider.KnowledgeMaterial{ID: n.ID, Kind: n.Kind, SourceType: n.SourceType, SourceID: n.SourceID, Version: n.Revision, Content: n.Content}
		var anchor models.NoteAnchor
		if json.Unmarshal([]byte(n.AnchorJSON), &anchor) == nil {
			m.SnapshotID = anchor.SnapshotID
			m.Position = anchor.Position
			m.NoPosition = anchor.NoPosition
		}
		refs := n.CitationsJSON
		if n.Kind == "owner_reflection" {
			refs = n.ReferencesJSON
		}
		if err := json.Unmarshal([]byte(refs), &m.Citations); err != nil {
			return req, "", err
		}
		ok, err := s.prepareKnowledgeMaterial(ctx, profileID, providerName, &m)
		if err != nil {
			return req, "", err
		}
		if !ok {
			continue
		}
		req.Materials = append(req.Materials, m)
		if n.UpdatedAt > latest {
			latest = n.UpdatedAt
		}
	}
	// Bound complete JSON inputs, including evidence; no unbounded library prompts.
	bounded := make([]provider.KnowledgeMaterial, 0, len(req.Materials))
	size := 0
	// Prioritize personal notes so large libraries cannot starve them.
	sort.SliceStable(req.Materials, func(i, j int) bool { return req.Materials[i].Kind > req.Materials[j].Kind })
	for _, m := range req.Materials {
		b, _ := json.Marshal(m)
		if len(b) > 10000 || size+len(b) > 40000 {
			continue
		}
		size += len(b)
		bounded = append(bounded, m)
	}
	req.Materials = bounded
	sort.Slice(req.Materials, func(i, j int) bool { return req.Materials[i].ID < req.Materials[j].ID })
	rows, err = s.DB.QueryContext(ctx, `SELECT title,core_claim FROM (SELECT title,core_claim,updated_at AS at,id FROM creation_history WHERE editorial_profile_id=? UNION ALL SELECT title,thesis,updated_at AS at,id FROM knowledge_articles WHERE profile_id=? AND title!='') ORDER BY at DESC,id DESC LIMIT 100`, profileID, profileID)
	if err != nil {
		return req, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var h provider.KnowledgeTopic
		if err := rows.Scan(&h.Title, &h.Thesis); err != nil {
			return req, "", err
		}
		if len([]rune(h.Title)) > 200 || len([]rune(h.Thesis)) > 500 {
			continue
		}
		historyBytes := 0
		for _, item := range req.History {
			historyBytes += len(item.Title) + len(item.Thesis)
		}
		if historyBytes+len(h.Title)+len(h.Thesis) <= 8000 {
			req.History = append(req.History, h)
		}
	}
	return req, latest, rows.Err()
}

func (s *Store) prepareKnowledgeMaterial(ctx context.Context, profileID, name string, m *provider.KnowledgeMaterial) (bool, error) {
	st := models.SourceType(m.SourceType)
	usable, err := s.CanUseSourceForPublication(ctx, profileID, st, m.SourceID)
	if err != nil {
		return false, err
	}
	if !usable {
		return false, nil
	}
	allowed, err := s.CanSendSourceToProvider(ctx, st, m.SourceID, name)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, nil
	}
	if m.Kind == "owner_reflection" && len(m.Citations) == 0 {
		return true, nil
	}
	if len(m.Citations) == 0 {
		return false, nil
	}
	var snap *models.SourceSnapshot
	if m.SnapshotID != "" {
		snap, err = s.GetSourceSnapshot(ctx, m.SnapshotID)
	} else {
		snap, err = s.FreezeSourceSnapshot(ctx, st, m.SourceID)
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, audio, docs, err := s.SnapshotContent(ctx, snap.ID)
	if err != nil {
		return false, err
	}
	segs := map[string]string{}
	for _, seg := range audio {
		if m.Kind == "keypoint" && len(m.Citations) > 0 && seg.ID == m.Citations[0] {
			m.Position = seg.Start
		}
		segs[seg.ID] = seg.Text
	}
	for _, seg := range docs {
		if m.Kind == "keypoint" && len(m.Citations) > 0 && seg.ID == m.Citations[0] {
			m.Position = float64(seg.Position)
		}
		segs[seg.ID] = seg.Text
	}
	var evidence []string
	for _, id := range m.Citations {
		t, ok := segs[id]
		if !ok {
			return false, nil
		}
		evidence = append(evidence, "["+id+"] "+t)
	}
	m.SnapshotID = snap.ID
	m.SourceTitle = snap.Title
	m.Evidence = strings.Join(evidence, "\n")
	return true, nil
}

// CheckKnowledgeMaterials rechecks frozen material identities and live data policies.
func (s *Store) CheckKnowledgeMaterials(ctx context.Context, profileID, name string, materials []provider.KnowledgeMaterial) error {
	for _, m := range materials {
		usable, err := s.CanUseSourceForPublication(ctx, profileID, models.SourceType(m.SourceType), m.SourceID)
		if err != nil {
			return err
		}
		if !usable {
			return fmt.Errorf("%w: 材料来源已归档或不可用", ErrConflict)
		}
		allowed, err := s.CanSendSourceToProvider(ctx, models.SourceType(m.SourceType), m.SourceID, name)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: 材料策略已禁止发送至 %s", ErrConflict, name)
		}
		if m.Kind == "keypoint" {
			kp, err := s.GetKeyPoint(ctx, m.ID)
			if err != nil {
				return err
			}
			eligible, err := s.IsKeyPointEligibleForProfile(ctx, profileID, m.ID)
			if err != nil {
				return err
			}
			if !eligible || kp.StaleAt != "" || kp.EvidenceStatus == "stale" || kp.ProductionStatus == models.KeyPointDismissed || (kp.QualityStatus != models.KeyPointReady && kp.QualityStatus != models.KeyPointOwnerConfirmed) || kp.Content != m.Content || kp.Description != m.Description || kp.CardVersion != m.Version {
				return fmt.Errorf("%w: 重点已修改或失效，请基于新材料生成", ErrConflict)
			}
		} else {
			n, err := s.GetOwnerNote(ctx, m.ID)
			if err != nil {
				return err
			}
			if n.Revision != m.Version || n.Content != m.Content || n.Kind != m.Kind {
				return fmt.Errorf("%w: 笔记已修改或删除，请基于新材料生成", ErrConflict)
			}
		}
		if m.SnapshotID != "" {
			snap, _, _, err := s.SnapshotContent(ctx, m.SnapshotID)
			if err != nil {
				return err
			}
			if snap.SourceID != m.SourceID || string(snap.SourceType) != m.SourceType {
				return ErrConflict
			}
			// Personal reflections retain their genuine historical reference;
			// only source assertions require the current transcript version.
			if snap.Kind == models.SnapshotKindAudio && m.Kind != "owner_reflection" {
				current, err := s.GetCurrentVersion(ctx, snap.SourceType, snap.SourceID, KindTranscript)
				if err != nil {
					return err
				}
				if current.Version != snap.ContentVersion {
					return fmt.Errorf("%w: 来源转录版本已变化，冻结依据仍可阅读但需重新审校", ErrConflict)
				}
			}
		}
	}
	return nil
}

// ReserveKnowledgeArticle atomically admits one input snapshot and its first stage.
func (s *Store) ReserveKnowledgeArticle(ctx context.Context, profileID, name, model string, req provider.KnowledgeArticleRequest, automatic bool) (*KnowledgeArticleRecord, bool, error) {
	ensureKnowledgeStageConfigs(&req, model)
	if len(req.Materials) < 2 {
		return nil, false, fmt.Errorf("至少需要两项可用重点或笔记；先完成学习处理或记录笔记")
	}
	hashInput := req
	hashInput.History = nil
	raw, _ := json.Marshal(hashInput)
	sum := sha256.Sum256(append(raw, []byte(name+"\x00"+model+provider.KnowledgeArticlePromptVersion)...))
	hash := fmt.Sprintf("%x", sum)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := checkQuestionAdmission(ctx, tx, req.Question, automatic, true); err != nil {
		return nil, false, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM knowledge_articles WHERE profile_id=? AND input_hash=?`, profileID, hash).Scan(&existing)
	if err == nil {
		tx.Rollback()
		v, e := s.GetKnowledgeArticle(ctx, existing)
		return v, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles WHERE profile_id=? AND status IN ('discover','select','write','review','revise','review_final')`, profileID).Scan(&active); err != nil {
		return nil, false, err
	}
	if active > 0 {
		return nil, false, fmt.Errorf("已有文章正在生成，请等待当前流程完成")
	}
	if automatic {
		var limit, count int
		if err := tx.QueryRowContext(ctx, `SELECT daily_limit FROM knowledge_article_settings WHERE id=1`).Scan(&limit); err != nil {
			return nil, false, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles WHERE profile_id=? AND automated=1 AND date(created_at)=date('now')`, profileID).Scan(&count); err != nil {
			return nil, false, err
		}
		if count >= limit {
			return nil, false, fmt.Errorf("今日自动成稿上限已达到")
		}
	}
	id := uuid.NewString()
	input, _ := json.Marshal(req)
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,provider,model,automated,review_model,discovery_batch_id) VALUES(?,?,?,?,?,?,?,?,?)`, id, profileID, hash, string(input), name, model, automatic, req.ReviewModel, req.DiscoveryBatchID); err != nil {
		return nil, false, err
	}
	if err := attachGeneratedQuestionArticle(ctx, tx, req.Question, id); err != nil {
		return nil, false, err
	}
	if req.DiscoveryBatchID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_discovery_batches SET article_id=? WHERE id=? AND article_id=''`, id, req.DiscoveryBatchID); err != nil {
			return nil, false, err
		}
		if automatic && req.Question == nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_discovery_cursors(profile_id,last_seq) SELECT profile_id,last_seq FROM knowledge_discovery_batches WHERE id=? ON CONFLICT(profile_id) DO UPDATE SET last_seq=MAX(last_seq,excluded.last_seq)`, req.DiscoveryBatchID); err != nil {
				return nil, false, err
			}
		}
	}
	if err := enqueueKnowledgeStage(ctx, tx, id, "discover", name, model, req, automatic); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	v, err := s.GetKnowledgeArticle(ctx, id)
	return v, true, err
}

func enqueueKnowledgeStage(ctx context.Context, tx *sql.Tx, id, stage, name, model string, req provider.KnowledgeArticleRequest, automatic bool) error {
	req.Stage = stage
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT working_revision FROM knowledge_articles WHERE id=?`, id).Scan(&revision); err != nil {
		return err
	}
	var err error
	model, err = freezeKnowledgeEstimate(ctx, tx, name, model, &req)
	if err != nil {
		return err
	}
	input, _ := json.Marshal(KnowledgeStageInput{ArticleID: id, Stage: stage, Request: req, ExpectedRevision: &revision, UpdateProposalID: knowledgeUpdateID(req)})
	version := req.PromptVersion
	if version == "" {
		version = "knowledge-article-v1"
	}
	jobID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,is_automated,intent_id,input_snapshot_json,config_version,configured_provider,configured_model) VALUES(?,?,?,?,'queued',?,?,?,?,?,?)`, jobID, "knowledge_article", id, string(models.JobKnowledgeArticle), automatic, "knowledge-article:"+id+":"+stage, string(input), version, name, model)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_article_runs(job_id,article_id,parent_revision,stage) VALUES(?,?,?,?)`, jobID, id, revision, stage)
	return err
}

const knowledgeArticleColumns = `id,profile_id,input_hash,input_json,status,stage,title,thesis,topics_json,topic_json,blocks_json,issues_json,reason,provider,model,created_at,updated_at,working_revision,passed_revision,review_model,discovery_batch_id,candidate_id`

func scanKnowledgeArticle(row interface{ Scan(...any) error }) (*KnowledgeArticleRecord, error) {
	v := &KnowledgeArticleRecord{}
	err := row.Scan(&v.ID, &v.ProfileID, &v.InputHash, &v.InputJSON, &v.Status, &v.Stage, &v.Title, &v.Thesis, &v.TopicsJSON, &v.TopicJSON, &v.BlocksJSON, &v.IssuesJSON, &v.Reason, &v.Provider, &v.Model, &v.CreatedAt, &v.UpdatedAt, &v.WorkingRevision, &v.PassedRevision, &v.ReviewModel, &v.DiscoveryBatchID, &v.CandidateID)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// GetKnowledgeArticle retrieves an automatic article by stable identity.
func (s *Store) GetKnowledgeArticle(ctx context.Context, id string) (*KnowledgeArticleRecord, error) {
	return scanKnowledgeArticle(s.DB.QueryRowContext(ctx, `SELECT `+knowledgeArticleColumns+` FROM knowledge_articles WHERE id=?`, id))
}

// ListKnowledgeArticles returns recent automatic articles across profiles.
func (s *Store) ListKnowledgeArticles(ctx context.Context) ([]*KnowledgeArticleRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+knowledgeArticleColumns+` FROM knowledge_articles ORDER BY created_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnowledgeArticleRecord
	for rows.Next() {
		v, err := scanKnowledgeArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CommitKnowledgeStage atomically saves a step result and its successor job.
func (s *Store) CommitKnowledgeStage(ctx context.Context, job *models.ProcessingJob, input KnowledgeStageInput, result *provider.KnowledgeArticleResult) error {
	if input.Request.Update != nil {
		execution, e := s.GetJobExecution(ctx, job.ID)
		if e != nil {
			return e
		}
		if execution.ResultState == models.JobResultComplete {
			return nil
		}
		p, e := s.GetKnowledgeUpdateProposal(ctx, input.Request.Update.ProposalID)
		if e != nil {
			return e
		}
		if e = s.CheckKnowledgeUpdateExecution(ctx, p, input.Request, job.Automated); e != nil {
			return e
		}
	}

	v, err := s.GetKnowledgeArticle(ctx, input.ArticleID)
	if err != nil {
		return err
	}
	var req provider.KnowledgeArticleRequest
	if err := json.Unmarshal([]byte(v.InputJSON), &req); err != nil {
		return err
	}
	topics, topic, blocks, issues := v.TopicsJSON, v.TopicJSON, v.BlocksJSON, v.IssuesJSON
	status, next, reason, title, thesis := input.Stage, "", "", v.Title, v.Thesis
	switch input.Stage {
	case "discover":
		b, _ := json.Marshal(result.Topics)
		topics = string(b)
		chosen := provider.SelectKnowledgeTopic(result.Topics, req.History)
		if chosen == nil {
			status = "insufficient"
			reason = result.Reason
			if reason == "" {
				reason = "当前材料不足以支持不重复且质量达标的选题"
			}
		} else {
			title, thesis = chosen.Title, chosen.Thesis
			b, _ := json.Marshal(chosen)
			topic = string(b)
			req.Topic = chosen
			ids := map[string]bool{}
			for _, id := range chosen.MaterialIDs {
				ids[id] = true
			}
			var selected []provider.KnowledgeMaterial
			for _, m := range req.Materials {
				if ids[m.ID] {
					selected = append(selected, m)
				}
			}
			req.Materials = selected
			next = "write"
			if req.DiscoveryBatchID != "" {
				next = "select"
				req, err = s.RecallKnowledgeMaterials(ctx, v.ProfileID, v.Provider, req, *chosen)
				if err != nil {
					return err
				}
			}
		}
	case "select":
		req = input.Request
		chosen := provider.SelectKnowledgeTopic(result.Topics, req.History)
		if chosen == nil {
			status = "insufficient"
			reason = result.Reason
			if reason == "" {
				reason = "选材后依据仍不足，保留材料缺口"
			}
		} else {
			req.Topic = chosen
			req.Materials = selectedKnowledgeMaterials(req.Materials, chosen.MaterialIDs)
			topic = jsonString(chosen)
			title, thesis = chosen.Title, chosen.Thesis
			next = "write"
		}
	case "write", "revise":
		title = result.Title
		b, _ := json.Marshal(result.Blocks)
		blocks = string(b)
		req.Blocks = result.Blocks
		next = "review"
		if input.Stage == "revise" {
			next = "review_final"
		}
	case "review", "review_final":
		b, _ := json.Marshal(result.Issues)
		issues = string(b)
		if result.Passed == nil {
			return fmt.Errorf("审校缺少明确结论")
		}
		if *result.Passed {
			status = "ready"
		} else if input.Stage == "review" {
			req.Issues = result.Issues
			next = "revise"
		} else {
			status = "needs_review"
			reason = "一次自动修订后仍未通过审校"
		}
	default:
		return fmt.Errorf("未知自动文章阶段")
	}
	if next != "" {
		status = next
	}
	// Only original selection remains in the immutable library input; successors
	// receive their selected subset plus the exact current draft/review feedback.
	if input.Stage != "discover" && input.Stage != "select" {
		var t provider.KnowledgeTopic
		if err := json.Unmarshal([]byte(topic), &t); err != nil {
			return err
		}
		req = input.Request
		req.Topic = &t
		if input.Stage == "write" || input.Stage == "revise" {
			req.Blocks = result.Blocks
		} else {
			req.Issues = result.Issues
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRunControl(ctx, tx, job.ID); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT result_state FROM processing_jobs WHERE id=?`, job.ID).Scan(&state); err != nil {
		return err
	}
	if state == models.JobResultComplete {
		return nil
	}
	expected := v.WorkingRevision
	if input.ExpectedRevision != nil {
		expected = *input.ExpectedRevision
	}
	actualProvider, actualModel, promptVersion := v.Provider, v.Model, provider.KnowledgeArticlePromptVersion
	if err := tx.QueryRowContext(ctx, `SELECT configured_provider,configured_model,config_version FROM processing_jobs WHERE id=?`, job.ID).Scan(&actualProvider, &actualModel, &promptVersion); err != nil {
		return err
	}
	newRevision := v.WorkingRevision
	if input.Stage == "write" || input.Stage == "revise" {
		assigned, assignErr := assignKnowledgeBlockIDs(ctx, tx, v.ID, expected, result.Blocks, false)
		if assignErr != nil {
			return assignErr
		}
		req.Blocks = assigned
		blocks = jsonString(assigned)
		newRevision, err = appendKnowledgeRevision(ctx, tx, v.ID, expected, title, req, assigned, "ai:"+job.ID, actualProvider, actualModel, promptVersion)
		if err != nil {
			return err
		}
	}
	if (input.Stage == "review" || input.Stage == "review_final") && expected > 0 {
		var fingerprint string
		if err := tx.QueryRowContext(ctx, `SELECT content_hash FROM knowledge_article_revisions WHERE article_id=? AND revision=?`, v.ID, expected).Scan(&fingerprint); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_article_reviews(id,article_id,revision,job_id,content_hash,passed,issues_json,provider,model,prompt_version) VALUES(?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), v.ID, expected, job.ID, fingerprint, *result.Passed, issues, actualProvider, actualModel, promptVersion); err != nil {
			return err
		}
	}
	resultJSON, _ := json.Marshal(result)
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_article_runs(job_id,article_id,parent_revision,stage,result_json) VALUES(?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET result_json=excluded.result_json`, job.ID, v.ID, expected, input.Stage, string(resultJSON)); err != nil {
		return err
	}
	candidateID := v.CandidateID
	if input.Stage == "discover" && req.DiscoveryBatchID != "" {
		candidateID, err = saveKnowledgeCandidates(ctx, tx, req.DiscoveryBatchID, result.Topics, req.History)
		if err != nil {
			return err
		}
		if candidateID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_topic_candidates SET article_id=?,status='selected' WHERE id=?`, v.ID, candidateID); err != nil {
				return err
			}
		}
	}
	passedRevision := v.PassedRevision
	if status == "ready" {
		passedRevision = newRevision
	}
	res, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status=?,stage=?,title=?,thesis=?,topics_json=?,topic_json=?,blocks_json=?,issues_json=?,reason=?,working_revision=?,passed_revision=?,updated_at=datetime('now') WHERE id=? AND stage=? AND working_revision=?`, status, chooseStage(next, input.Stage), title, thesis, topics, topic, blocks, issues, reason, newRevision, passedRevision, v.ID, input.Stage, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		if req.Update != nil {
			if err := updateKnowledgeProposalStage(ctx, tx, req.Update.ProposalID, "parent_changed", newRevision); err != nil {
				return err
			}
		}
		// A late result remains auditable, without selecting its body as working/passed.
		if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, string(resultJSON), job.ID); err != nil {
			return err
		}
		return tx.Commit()
	}

	if req.Update != nil {
		proposalState := "generating"
		if input.Stage == "write" || input.Stage == "revise" || status == "needs_review" {
			proposalState = "needs_review"
		}
		if status == "ready" {
			proposalState = "completed"
		}
		if err := updateKnowledgeProposalStage(ctx, tx, req.Update.ProposalID, proposalState, newRevision); err != nil {
			return err
		}
	}
	if candidateID != "" {
		candidateStatus := "selected"
		if next == "" {
			candidateStatus = status
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET candidate_id=? WHERE id=?`, candidateID, v.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_topic_candidates SET status=?,reason=?,selection_json=CASE WHEN ?='select' THEN ? ELSE selection_json END,topic_json=CASE WHEN ?='select' THEN ? ELSE topic_json END WHERE id=?`, candidateStatus, reason, input.Stage, jsonString(result.Topics), input.Stage, topic, candidateID); err != nil {
			return err
		}
	}
	if next != "" {
		if err := enqueueKnowledgeStage(ctx, tx, v.ID, next, v.Provider, v.Model, req, job.Automated); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_json=?,result_state='complete' WHERE id=?`, string(resultJSON), job.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func chooseStage(next, current string) string {
	if next != "" {
		return next
	}
	return current
}

// FailKnowledgeArticle exposes a failed queued stage without deleting its draft.
func (s *Store) FailKnowledgeArticle(ctx context.Context, id, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET status='failed',reason=?,updated_at=datetime('now') WHERE id=? AND status NOT IN ('ready','needs_review','insufficient')`, reason, id)
	return err
}

// RetryKnowledgeArticle retries the same frozen failed stage only by explicit action.
func (s *Store) RetryKnowledgeArticle(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stage, status string
	if err := tx.QueryRowContext(ctx, `SELECT stage,status FROM knowledge_articles WHERE id=?`, id).Scan(&stage, &status); err != nil {
		return err
	}
	if status != "failed" {
		return fmt.Errorf("只有失败的文章阶段可重试")
	}
	var previousID, snapshot, checkpoint, configuredProvider, configuredModel, version string
	if err := tx.QueryRowContext(ctx, `SELECT id,input_snapshot_json,checkpoint_json,configured_provider,configured_model,config_version FROM processing_jobs WHERE source_type='knowledge_article' AND source_id=? AND intent_id=? AND status='failed' ORDER BY created_at DESC,id DESC LIMIT 1`, id, "knowledge-article:"+id+":"+stage).Scan(&previousID, &snapshot, &checkpoint, &configuredProvider, &configuredModel, &version); err != nil {
		return fmt.Errorf("没有可重试的失败任务: %w", err)
	}
	var prior KnowledgeStageInput
	var cached struct {
		Result *provider.KnowledgeArticleResult `json:"result"`
	}
	if checkpoint != "" && (json.Unmarshal([]byte(snapshot), &prior) != nil || json.Unmarshal([]byte(checkpoint), &cached) != nil || provider.ValidateKnowledgeResult(prior.Request, cached.Result) != nil) {
		checkpoint = ""
	}
	if json.Unmarshal([]byte(snapshot), &prior) == nil && prior.UpdateProposalID != "" {
		res, e := tx.ExecContext(ctx, `UPDATE knowledge_update_proposals SET state='generating',automated=0,updated_at=datetime('now') WHERE id=? AND article_id=? AND state='failed'`, prior.UpdateProposalID, id)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
	}
	// Each explicit retry is a new attempt: preserve old usage/reservation audit.
	if _, err := tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,checkpoint_json,config_version,configured_provider,configured_model) VALUES(?,'knowledge_article',?,'knowledge_article','queued',?,?,?,?,?,?)`, uuid.NewString(), id, "knowledge-article:"+id+":"+stage, snapshot, checkpoint, version, configuredProvider, configuredModel); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_articles SET status=stage,reason='',updated_at=datetime('now') WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// KnowledgeDebounceReady checks a library timestamp against the configured delay.
func KnowledgeDebounceReady(latest string, minutes int, now time.Time) bool {
	if latest == "" || minutes == 0 {
		return true
	}
	t, err := time.Parse("2006-01-02 15:04:05", latest)
	return err == nil && now.UTC().Sub(t) >= time.Duration(minutes)*time.Minute
}
