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

// KnowledgeScope expresses human-selected learning material scope and history cursor.
type KnowledgeScope struct {
	PodcastID, SourceType, SourceID, Theme, From, Until string
	MaterialIDs                                         []string
	ExploreHistory                                      bool
	HistoryCursor                                       int64
}

// KnowledgeDiscoveryBatch is one frozen, idempotent discovery input.
type KnowledgeDiscoveryBatch struct {
	ID, ProfileID, InputHash, InputJSON, ScopeJSON, Provider, Model, PromptVersion, ArticleID, Status, Reason, CreatedAt string
	LastSeq                                                                                                              int64
	Automated                                                                                                            bool
}

// KnowledgeTopicCandidate is a separately idempotent writing direction within a batch.
type KnowledgeTopicCandidate struct {
	ID, BatchID, DirectionHash, TopicJSON, Status, ArticleID, Reason, SelectionJSON, CreatedAt string
	Topic                                                                                      provider.KnowledgeTopic
}

func (s *Store) knowledgeMaterial(ctx context.Context, profile, name, id string) (*provider.KnowledgeMaterial, error) {
	var m provider.KnowledgeMaterial
	kp, err := s.GetKeyPoint(ctx, id)
	if err == nil {
		eligible, e := s.IsKeyPointEligibleForProfile(ctx, profile, id)
		if e != nil {
			return nil, e
		}
		if !eligible || kp.StaleAt != "" || kp.EvidenceStatus == "stale" || kp.ProductionStatus == models.KeyPointDismissed || (kp.QualityStatus != models.KeyPointReady && kp.QualityStatus != models.KeyPointOwnerConfirmed) {
			return nil, nil
		}
		m = provider.KnowledgeMaterial{ID: kp.ID, Kind: "keypoint", SourceType: string(kp.SourceType), SourceID: kp.SourceID, SourceTitle: kp.SourceTitle, Version: kp.CardVersion, Content: kp.Content, Description: kp.Description}
		if err := json.Unmarshal([]byte(kp.CitationsJSON), &m.Citations); err != nil {
			return nil, err
		}
	} else if errors.Is(err, ErrNotFound) {
		note, e := s.GetOwnerNote(ctx, id)
		if errors.Is(e, ErrNotFound) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		m = provider.KnowledgeMaterial{ID: note.ID, Kind: note.Kind, SourceType: note.SourceType, SourceID: note.SourceID, Version: note.Revision, Content: note.Content}
		refs := note.CitationsJSON
		if note.Kind == "owner_reflection" {
			refs = note.ReferencesJSON
		}
		if err := json.Unmarshal([]byte(refs), &m.Citations); err != nil {
			return nil, err
		}
		var a models.NoteAnchor
		if json.Unmarshal([]byte(note.AnchorJSON), &a) == nil {
			m.SnapshotID = a.SnapshotID
			m.Position = a.Position
		}
	} else {
		return nil, err
	}
	usable, err := s.prepareKnowledgeMaterial(ctx, profile, name, &m)
	if err != nil {
		return nil, err
	}
	if !usable {
		return nil, nil
	}
	if err := s.CheckKnowledgeMaterials(ctx, profile, name, []provider.KnowledgeMaterial{m}); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrSnapshotInvalidated) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}
func scopeQuery(scope KnowledgeScope) KnowledgeSearchQuery {
	return KnowledgeSearchQuery{Kind: "materials", PodcastID: scope.PodcastID, SourceType: scope.SourceType, SourceID: scope.SourceID, Theme: scope.Theme, From: scope.From, Until: scope.Until, PerPage: 20}
}
func (s *Store) scopeAllows(ctx context.Context, scope KnowledgeScope, m provider.KnowledgeMaterial) (bool, error) {
	if scope.SourceID != "" && m.SourceID != scope.SourceID {
		return false, nil
	}
	if scope.SourceType != "" && m.SourceType != scope.SourceType {
		return false, nil
	}
	if scope.PodcastID != "" {
		if m.SourceType != "episode" {
			return false, nil
		}
		ep, err := s.GetEpisodeByID(ctx, m.SourceID)
		if err != nil {
			return false, err
		}
		if ep.PodcastID != scope.PodcastID {
			return false, nil
		}
	}
	if scope.From != "" || scope.Until != "" {
		var created string
		err := s.DB.QueryRowContext(ctx, `SELECT created_at FROM knowledge_search_docs WHERE object_id=? AND kind IN ('source_note','owner_reflection','keypoint') LIMIT 1`, m.ID).Scan(&created)
		if err != nil {
			return false, err
		}
		if scope.From != "" && created < scope.From || scope.Until != "" && created >= scope.Until+" 23:59:59" {
			return false, nil
		}
	}
	if scope.Theme != "" && !strings.Contains(strings.ToLower(m.Content+" "+m.Description+" "+m.SourceTitle), strings.ToLower(scope.Theme)) {
		return false, nil
	}
	return true, nil
}

// BuildKnowledgeDiscoveryRequest uses changed/recent seeds; historic recall happens per question.
func (s *Store) BuildKnowledgeDiscoveryRequest(ctx context.Context, profile, name string, scope KnowledgeScope, automatic bool) (provider.KnowledgeArticleRequest, int64, string, error) {
	base, latest, err := s.BuildKnowledgeArticleRequest(ctx, profile, name)
	if err != nil {
		return base, 0, "", err
	}
	if _, e := s.SearchKnowledge(ctx, scopeQuery(scope)); e != nil {
		return base, 0, "", e
	}
	base.Materials = nil
	base.ScopeJSON = jsonString(scope)
	if len(scope.MaterialIDs) > 20 || scope.HistoryCursor < 0 {
		return base, 0, "", ErrInvalidEditorialState
	}
	cursor := int64(0)
	if automatic {
		err = s.DB.QueryRowContext(ctx, `SELECT last_seq FROM knowledge_discovery_cursors WHERE profile_id=?`, profile).Scan(&cursor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return base, 0, "", err
		}
	}
	ids := append([]string(nil), scope.MaterialIDs...)
	last := cursor
	if scope.ExploreHistory || automatic {
		start := cursor
		if scope.ExploreHistory {
			start = scope.HistoryCursor
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT seq,material_id FROM knowledge_learning_changes WHERE seq>? ORDER BY seq LIMIT 20`, start)
		if err != nil {
			return base, 0, "", err
		}
		for rows.Next() {
			var seq int64
			var id string
			if err := rows.Scan(&seq, &id); err != nil {
				rows.Close()
				return base, 0, "", err
			}
			ids = append(ids, id)
			last = seq
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return base, 0, "", err
		}
	} else if len(ids) == 0 {
		result, err := s.SearchKnowledge(ctx, scopeQuery(scope))
		if err != nil {
			return base, 0, "", err
		}
		for _, hit := range result.Hits {
			ids = append(ids, hit.ObjectID)
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM knowledge_learning_changes`).Scan(&last); err != nil {
			return base, 0, "", err
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		m, err := s.knowledgeMaterial(ctx, profile, name, id)
		if err != nil {
			return base, 0, "", err
		}
		if m == nil {
			continue
		}
		allowed, err := s.scopeAllows(ctx, scope, *m)
		if err != nil {
			return base, 0, "", err
		}
		if !allowed {
			continue
		}
		m.RetrievalReason = "发现种子：当前范围内新增、修改或选定的学习材料"
		base.Materials = append(base.Materials, *m)
	}
	candidates := base.Materials
	base.Materials = boundKnowledgeMaterials(candidates, 20)
	base.Exclusions = knowledgeExclusions(candidates, base.Materials)
	// A single new seed can be complemented by eligible old materials before discovery.
	if len(base.Materials) == 1 {
		req, err := s.RecallKnowledgeMaterials(ctx, profile, name, base, provider.KnowledgeTopic{Question: base.Materials[0].Content})
		if err != nil {
			return base, 0, "", err
		}
		base.Materials = req.Materials
	}
	return base, last, latest, nil
}
func boundKnowledgeMaterials(materials []provider.KnowledgeMaterial, limit int) []provider.KnowledgeMaterial {
	var out []provider.KnowledgeMaterial
	size := 0
	seen := map[string]bool{}
	sources := map[string]int{}
	add := func(m provider.KnowledgeMaterial) {
		if seen[m.ID] || len(out) >= limit {
			return
		}
		raw, _ := json.Marshal(m)
		if len(raw) > 10000 || size+len(raw) > 40000 {
			return
		}
		seen[m.ID] = true
		sources[m.SourceType+":"+m.SourceID]++
		size += len(raw)
		out = append(out, m)
	}
	// Preserve relevance order while reserving room for other real sources.
	for _, m := range materials {
		if sources[m.SourceType+":"+m.SourceID] < 6 {
			add(m)
		}
	}
	for _, m := range materials {
		add(m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func knowledgeExclusions(candidates, admitted []provider.KnowledgeMaterial) []provider.KnowledgeExclusion {
	kept := map[string]bool{}
	for _, m := range admitted {
		kept[m.ID] = true
	}
	seen := map[string]bool{}
	var out []provider.KnowledgeExclusion
	for _, m := range candidates {
		if kept[m.ID] || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, provider.KnowledgeExclusion{MaterialID: m.ID, Reason: "候选超过20项或完整材料超过40KB/单项10KB；按文字相关性和来源多样性保留，不截断依据"})
	}
	return out
}

// RecallKnowledgeMaterials retrieves up to20 real historical candidates for a question.
func (s *Store) RecallKnowledgeMaterials(ctx context.Context, profile, name string, req provider.KnowledgeArticleRequest, topic provider.KnowledgeTopic) (provider.KnowledgeArticleRequest, error) {
	var scope KnowledgeScope
	if req.ScopeJSON != "" && json.Unmarshal([]byte(req.ScopeJSON), &scope) != nil {
		return req, ErrInvalidEditorialState
	}
	query := scopeQuery(scope)
	query.Recall = true
	query.Text = topic.Question + " " + topic.Thesis
	if len([]rune(query.Text)) > 200 {
		query.Text = string([]rune(query.Text)[:200])
	}
	result, err := s.SearchKnowledge(ctx, query)
	if err != nil {
		return req, err
	}
	material := append([]provider.KnowledgeMaterial(nil), req.Materials...)
	for _, hit := range result.Hits {
		m, err := s.knowledgeMaterial(ctx, profile, name, hit.ObjectID)
		if err != nil {
			return req, err
		}
		if m == nil {
			continue
		}
		m.RetrievalReason = "历史召回：" + hit.Reason
		material = append(material, *m)
	}
	req.Materials = boundKnowledgeMaterials(material, 20)
	req.Exclusions = append(req.Exclusions, knowledgeExclusions(material, req.Materials)...)
	if req.Topic != nil {
		ids := map[string]bool{}
		for _, m := range req.Materials {
			ids[m.ID] = true
		}
		topic := *req.Topic
		var admitted []string
		for _, id := range topic.MaterialIDs {
			if ids[id] {
				admitted = append(admitted, id)
			}
		}
		topic.MaterialIDs = admitted
		req.Topic = &topic
	}
	relevant, err := s.knowledgeDirectionHistory(ctx, profile, topic)
	if err != nil {
		return req, err
	}
	seen := map[string]bool{}
	var history []provider.KnowledgeTopic
	size := 0
	for _, h := range append(relevant, req.History...) {
		key := h.ArticleID + "\x00" + h.Title + "\x00" + h.Question
		if seen[key] {
			continue
		}
		raw := jsonString(h)
		if size+len(raw) > 8000 {
			continue
		}
		seen[key] = true
		size += len(raw)
		history = append(history, h)
	}
	req.History = history
	return req, nil
}

// EnsureKnowledgeDiscoveryBatch deduplicates discovery separately from writing directions.
func (s *Store) EnsureKnowledgeDiscoveryBatch(ctx context.Context, profile, name, model string, req provider.KnowledgeArticleRequest, last int64, automatic bool) (*KnowledgeDiscoveryBatch, error) {
	input := req
	input.History = nil
	input.DiscoveryBatchID = ""
	raw := jsonString(input)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw+"\x00"+name+"\x00"+model)))
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_discovery_batches(id,profile_id,input_hash,input_json,scope_json,provider,model,prompt_version,last_seq,automated)VALUES(?,?,?,?,?,?,?,?,?,?)`, id, profile, hash, jsonString(req), req.ScopeJSON, name, model, req.PromptVersion, last, automatic)
	if err != nil {
		return nil, err
	}
	return s.getKnowledgeBatchByHash(ctx, profile, hash)
}
func (s *Store) getKnowledgeBatchByHash(ctx context.Context, profile, hash string) (*KnowledgeDiscoveryBatch, error) {
	return scanKnowledgeBatch(s.DB.QueryRowContext(ctx, `SELECT id,profile_id,input_hash,input_json,scope_json,provider,model,prompt_version,last_seq,automated,article_id,status,reason,created_at FROM knowledge_discovery_batches WHERE profile_id=? AND input_hash=?`, profile, hash))
}
func scanKnowledgeBatch(row interface{ Scan(...any) error }) (*KnowledgeDiscoveryBatch, error) {
	v := &KnowledgeDiscoveryBatch{}
	err := row.Scan(&v.ID, &v.ProfileID, &v.InputHash, &v.InputJSON, &v.ScopeJSON, &v.Provider, &v.Model, &v.PromptVersion, &v.LastSeq, &v.Automated, &v.ArticleID, &v.Status, &v.Reason, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// GetKnowledgeDiscoveryBatch retrieves its frozen scope and cursor for a writing candidate.
func (s *Store) GetKnowledgeDiscoveryBatch(ctx context.Context, id string) (*KnowledgeDiscoveryBatch, error) {
	return scanKnowledgeBatch(s.DB.QueryRowContext(ctx, `SELECT id,profile_id,input_hash,input_json,scope_json,provider,model,prompt_version,last_seq,automated,article_id,status,reason,created_at FROM knowledge_discovery_batches WHERE id=?`, id))
}

// ListKnowledgeTopicCandidates retains qualified, duplicate and insufficient directions.
func (s *Store) ListKnowledgeTopicCandidates(ctx context.Context) ([]*KnowledgeTopicCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,batch_id,direction_hash,topic_json,status,article_id,reason,selection_json,created_at FROM knowledge_topic_candidates ORDER BY created_at DESC,id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KnowledgeTopicCandidate
	for rows.Next() {
		v := &KnowledgeTopicCandidate{}
		if err := rows.Scan(&v.ID, &v.BatchID, &v.DirectionHash, &v.TopicJSON, &v.Status, &v.ArticleID, &v.Reason, &v.SelectionJSON, &v.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(v.TopicJSON), &v.Topic); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func saveKnowledgeCandidates(ctx context.Context, tx *sql.Tx, batchID string, topics, history []provider.KnowledgeTopic) (string, error) {
	sort.SliceStable(topics, func(i, j int) bool { return topics[i].Score > topics[j].Score })
	chosen := provider.SelectKnowledgeTopic(topics, history)
	seenDirections := append([]provider.KnowledgeTopic(nil), history...)
	chosenID := ""
	for _, topic := range topics {
		status, reason := "waiting", "材料充分，等待成稿准入"
		if !topic.Sufficient || topic.Score < 70 {
			status = "insufficient"
			reason = "材料成熟度不足：" + strings.Join(topic.Missing, "；")
		}
		if status == "waiting" {
			for _, previous := range seenDirections {
				if provider.KnowledgeTopicDuplicate(topic, previous) {
					status = "duplicate"
					reason = "与既有问题/主旨及材料重复"
					break
				}
			}
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(topic.Question)+"\x00"+strings.ToLower(topic.Thesis))))
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO knowledge_topic_candidates(id,batch_id,direction_hash,topic_json,status,reason,selection_json)VALUES(?,?,?,?,?,?,?)`, id, batchID, hash, jsonString(topic), status, reason, jsonString(topic.Selection)); err != nil {
			return "", err
		}
		if status == "waiting" {
			seenDirections = append(seenDirections, topic)
		}
		if status == "waiting" && chosen != nil && chosen.Question == topic.Question && chosen.Thesis == topic.Thesis {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM knowledge_topic_candidates WHERE batch_id=? AND direction_hash=?`, batchID, hash).Scan(&chosenID); err != nil {
				return "", err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_discovery_batches SET status='complete',reason=? WHERE id=?`, map[bool]string{true: "发现结果已冻结；其他方向按各自身份等待成稿", false: "本批未发现充分且不重复的方向"}[chosenID != ""], batchID); err != nil {
		return "", err
	}
	return chosenID, nil
}

// StartKnowledgeCandidate admits one frozen direction once, respecting the same limits.
func (s *Store) StartKnowledgeCandidate(ctx context.Context, candidateID string, automatic bool) (*KnowledgeArticleRecord, bool, error) {
	var batchID, articleID, status, topicJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT batch_id,article_id,status,topic_json FROM knowledge_topic_candidates WHERE id=?`, candidateID).Scan(&batchID, &articleID, &status, &topicJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if articleID != "" {
		v, err := s.GetKnowledgeArticle(ctx, articleID)
		return v, false, err
	}
	if status != "waiting" {
		return nil, false, ErrConflict
	}
	batch, err := s.GetKnowledgeDiscoveryBatch(ctx, batchID)
	if err != nil {
		return nil, false, err
	}
	var req provider.KnowledgeArticleRequest
	var topic provider.KnowledgeTopic
	if json.Unmarshal([]byte(batch.InputJSON), &req) != nil || json.Unmarshal([]byte(topicJSON), &topic) != nil {
		return nil, false, ErrInvalidEditorialState
	}
	if err := s.checkKnowledgeDirection(ctx, batch.ProfileID, "", candidateID, topic); err != nil {
		if errors.Is(err, ErrConflict) {
			_, updateErr := s.DB.ExecContext(ctx, `UPDATE knowledge_topic_candidates SET status='duplicate',reason='成稿前发现全库重复方向' WHERE id=? AND article_id=''`, candidateID)
			if updateErr != nil {
				return nil, false, updateErr
			}
		}
		return nil, false, err
	}
	history, err := s.knowledgeDirectionHistory(ctx, batch.ProfileID, topic)
	if err != nil {
		return nil, false, err
	}
	for _, previous := range history {
		if provider.KnowledgeTopicDuplicate(topic, previous) {
			_, err := s.DB.ExecContext(ctx, `UPDATE knowledge_topic_candidates SET status='duplicate',reason='成稿前发现历史新增重复方向' WHERE id=? AND article_id=''`, candidateID)
			if err != nil {
				return nil, false, err
			}
			return nil, false, ErrConflict
		}
	}
	req.History = history
	req.Materials = selectedKnowledgeMaterials(req.Materials, topic.MaterialIDs)
	req.Topic = &topic
	req.DiscoveryBatchID = batch.ID
	if err := s.CheckKnowledgeMaterials(ctx, batch.ProfileID, batch.Provider, req.Materials); err != nil {
		return nil, false, err
	}
	req, err = s.RecallKnowledgeMaterials(ctx, batch.ProfileID, batch.Provider, req, topic)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	// Repeat identity check inside the transaction: simultaneous clicks charge once.
	if err := tx.QueryRowContext(ctx, `SELECT article_id FROM knowledge_topic_candidates WHERE id=?`, candidateID).Scan(&articleID); err != nil {
		return nil, false, err
	}
	if articleID != "" {
		tx.Rollback()
		v, err := s.GetKnowledgeArticle(ctx, articleID)
		return v, false, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles WHERE profile_id=? AND status IN ('discover','select','write','review','revise','review_final')`, batch.ProfileID).Scan(&active); err != nil {
		return nil, false, err
	}
	if active > 0 {
		return nil, false, fmt.Errorf("已有文章在途，候选继续等待")
	}
	if automatic {
		var limit, count int
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT daily_limit,enabled FROM knowledge_article_settings WHERE id=1`).Scan(&limit, &enabled); err != nil {
			return nil, false, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_articles WHERE profile_id=? AND automated=1 AND date(created_at)=date('now')`, batch.ProfileID).Scan(&count); err != nil {
			return nil, false, err
		}
		if !enabled || count >= limit {
			return nil, false, fmt.Errorf("今日自动成稿上限已达到")
		}
	}
	articleID = uuid.NewString()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("candidate:"+candidateID)))
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,title,thesis,topic_json,provider,model,automated,review_model,discovery_batch_id,candidate_id)VALUES(?,?,?,?,'select','select',?,?,?,?,?,?,?,?,?)`, articleID, batch.ProfileID, hash, jsonString(req), topic.Title, topic.Thesis, topicJSON, batch.Provider, batch.Model, automatic, req.ReviewModel, batch.ID, candidateID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_topic_candidates SET article_id=?,status='selected' WHERE id=? AND article_id=''`, articleID, candidateID); err != nil {
		return nil, false, err
	}
	if err := enqueueKnowledgeStage(ctx, tx, articleID, "select", batch.Provider, batch.Model, req, automatic); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	v, err := s.GetKnowledgeArticle(ctx, articleID)
	return v, true, err
}

func (s *Store) knowledgeDirectionHistory(ctx context.Context, profile string, topic provider.KnowledgeTopic) ([]provider.KnowledgeTopic, error) {
	// Retrieve relevant older directions before the recent metadata fallback.
	var out []provider.KnowledgeTopic
	seen := map[string]bool{}
	size := 0
	text := topic.Question + " " + topic.Thesis
	if len([]rune(text)) > 200 {
		text = string([]rune(text)[:200])
	}
	if strings.TrimSpace(text) != "" {
		result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Kind: "article", Text: text, Recall: true, IncludeDrafts: true, IncludeHistory: true, PerPage: 20})
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			if seen[hit.ObjectID] {
				continue
			}
			a, err := s.GetKnowledgeArticle(ctx, hit.ObjectID)
			if err != nil {
				return nil, err
			}
			if a.ProfileID != profile {
				continue
			}
			var t provider.KnowledgeTopic
			if json.Unmarshal([]byte(a.TopicJSON), &t) != nil {
				continue
			}
			t.ArticleID = a.ID
			raw := jsonString(t)
			if size+len(raw) > 8000 {
				break
			}
			size += len(raw)
			out = append(out, t)
			seen[a.ID] = true
		}
	}

	// Article and pending-candidate pools have separate chronological limits.
	// UUID order is not time order, and candidates must not lose their pool to articles.
	type directionMetadata struct{ key, articleID, raw, at string }
	var recent []directionMetadata
	for i, query := range []string{
		`SELECT id,topic_json,updated_at FROM knowledge_articles WHERE profile_id=? AND topic_json!='{}' ORDER BY updated_at DESC,id DESC LIMIT 100`,
		`SELECT c.id,c.topic_json,c.created_at FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE b.profile_id=? AND c.status='waiting' ORDER BY c.created_at DESC,c.id DESC LIMIT 100`,
	} {
		rows, err := s.DB.QueryContext(ctx, query, profile)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v directionMetadata
			if err := rows.Scan(&v.key, &v.raw, &v.at); err != nil {
				rows.Close()
				return nil, err
			}
			if i == 0 {
				v.articleID = v.key
			} else {
				v.key = "candidate:" + v.key
			}
			recent = append(recent, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i].at != recent[j].at {
			return recent[i].at > recent[j].at
		}
		return recent[i].key > recent[j].key
	})
	for _, v := range recent {
		var previous provider.KnowledgeTopic
		if json.Unmarshal([]byte(v.raw), &previous) != nil || seen[v.key] {
			continue
		}
		if v.articleID == "" && previous.Question == topic.Question && previous.Thesis == topic.Thesis {
			continue
		}
		previous.ArticleID = v.articleID
		encoded := jsonString(previous)
		if size+len(encoded) > 8000 {
			continue
		}
		seen[v.key] = true
		size += len(encoded)
		out = append(out, previous)
	}
	return out, nil
}

func selectedKnowledgeMaterials(materials []provider.KnowledgeMaterial, ids []string) []provider.KnowledgeMaterial {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var out []provider.KnowledgeMaterial
	for _, m := range materials {
		if wanted[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// NextAutomaticKnowledgeCandidate retrieves a previously discovered waiting direction.
func (s *Store) NextAutomaticKnowledgeCandidate(ctx context.Context, profile string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT c.id FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE b.profile_id=? AND b.automated=1 AND c.status='waiting' AND c.article_id='' ORDER BY c.created_at,c.id LIMIT 1`, profile).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// CheckKnowledgeDirection rechecks current history before a paid write, excluding itself.
func (s *Store) CheckKnowledgeDirection(ctx context.Context, profile, articleID string, topic provider.KnowledgeTopic) error {
	return s.checkKnowledgeDirection(ctx, profile, articleID, "", topic)
}

func (s *Store) checkKnowledgeDirection(ctx context.Context, profile, articleID, candidateID string, topic provider.KnowledgeTopic) error {
	// Paid admission checks all current direction metadata locally. A bounded
	// model history and paginated UI must never make an old duplicate invisible.
	rows, err := s.DB.QueryContext(ctx, `SELECT id,topic_json FROM knowledge_articles WHERE profile_id=? AND id!=? AND topic_json!='{}' UNION ALL SELECT '',c.topic_json FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE b.profile_id=? AND c.status='waiting' AND c.id!=?`, profile, articleID, profile, candidateID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		var previous provider.KnowledgeTopic
		if json.Unmarshal([]byte(raw), &previous) != nil {
			continue
		}
		previous.ArticleID = id
		if provider.KnowledgeTopicDuplicate(topic, previous) {
			return fmt.Errorf("成稿前发现重复问题，请选择有真实增量的方向: %w", ErrConflict)
		}
	}
	return rows.Err()
}
