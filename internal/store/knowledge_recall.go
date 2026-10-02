package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func (s *Store) recallKnowledgePool(ctx context.Context, profile, name string, req provider.KnowledgeArticleRequest, topic provider.KnowledgeTopic) (provider.KnowledgeArticleRequest, error) {
	var scope KnowledgeScope
	if req.ScopeJSON != "" && json.Unmarshal([]byte(req.ScopeJSON), &scope) != nil {
		return req, ErrInvalidEditorialState
	}
	scope.Question = req.Question
	q := scopeQuery(scope)
	q.Recall, q.MetadataOnly, q.PerPage = true, true, 200
	q.SendProvider, q.RecallProfileID = name, profile
	q.Text = topic.Question + " " + topic.Thesis
	if len([]rune(q.Text)) > 200 {
		q.Text = string([]rune(q.Text)[:200])
	}
	result, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: q, Purpose: RetrieveExternal})
	if err != nil {
		return req, err
	}
	var pool []provider.KnowledgeRecallCandidate
	seeds := map[string]provider.KnowledgeMaterial{}
	seen := map[string]bool{}
	for _, m := range req.Materials {
		seeds[m.ID] = m
		seen[m.ID] = true
		pool = append(pool, provider.KnowledgeRecallCandidate{MaterialID: m.ID, SourceType: m.SourceType, SourceID: m.SourceID, Title: m.SourceTitle, State: "not_read", Reason: "发现种子", LexicalClue: knowledgeOppositionClue(m.Content)})
	}
	omittedMetadata := false
	for _, hit := range result.Hits {
		if seen[hit.ObjectID] {
			continue
		}
		if len(pool) >= 200 {
			omittedMetadata = true
			continue
		}
		seen[hit.ObjectID] = true
		pool = append(pool, provider.KnowledgeRecallCandidate{MaterialID: hit.ObjectID, SourceType: hit.SourceType, SourceID: hit.SourceID, Title: hit.Title, State: "not_read", Reason: hit.Reason, Rank: hit.Rank, LexicalClue: knowledgeOppositionClue(hit.Title + " " + hit.Snippet)})
	}
	coverage := &provider.KnowledgeRecallCoverage{Method: "lexical-diverse-v1", TotalMatches: result.Total, MetadataCount: len(pool), LimitReached: result.Total > len(result.Hits) || omittedMetadata}
	var admitted []provider.KnowledgeMaterial
	bytes := 0
	contents := map[string]bool{}
	for _, i := range knowledgeDiverseOrder(pool) {
		c := &pool[i]
		if len(admitted) >= 20 || bytes >= 40000 {
			c.Reason = "正文容量上限：尚未读取，不代表材料无用"
			coverage.LimitReached = true
			continue
		}
		allowed, err := s.CanSendSourceToProvider(ctx, models.SourceType(c.SourceType), c.SourceID, name)
		if c.SourceID == "" {
			v, e := s.GetUnderstandingSnapshot(ctx, c.MaterialID)
			if e == nil {
				allowed, err = s.UnderstandingMaySend(ctx, v.ID, v.Version, name)
			} else if e != ErrNotFound {
				return req, e
			}
		}
		if err != nil {
			return req, err
		}
		if !allowed {
			c.State, c.Reason = "skipped", "来源策略禁止外发（程序检查，未读正文）"
			continue
		}
		m, ok := seeds[c.MaterialID]
		if !ok {
			item, err := s.knowledgeMaterial(ctx, profile, name, c.MaterialID)
			if err != nil {
				return req, err
			}
			coverage.ReadCount++
			if item == nil {
				c.State, c.Reason = "skipped", "来源或材料资格失效，未外发"
				continue
			}
			m = *item
		} else {
			coverage.ReadCount++
		}
		if !questionAllowsMaterial(req.Question, m) {
			c.State, c.Reason = "skipped", "不属于冻结的问题材料范围"
			continue
		}
		m.RetrievalReason = "本地词项召回/来源轮转；相关性不表示支持论点"
		if c.LexicalClue != "" {
			m.RetrievalReason += "；" + c.LexicalClue
		}
		if err := s.compactKnowledgeEvidence(ctx, &m, q.Text); err != nil {
			return req, err
		}
		raw, _ := json.Marshal(m)
		if len(raw) > 10000 || bytes+len(raw) > 40000 {
			c.State, c.Reason = "skipped", "已读，但完整笔记/片段超过单项10KB或总40KB容量，未截断"
			coverage.LimitReached = true
			continue
		}
		key := m.SourceType + ":" + m.SourceID + ":" + m.Kind + ":" + strings.TrimSpace(m.Content) + ":" + m.Evidence
		if contents[key] {
			c.State, c.Reason = "skipped", "同来源已有相同内容及冻结依据"
			continue
		}
		contents[key] = true
		bytes += len(raw)
		c.State, c.Reason = "admitted", "采用完整材料/证据窗口，等待模型支持/补充/反方判断"
		admitted = append(admitted, m)
	}
	sort.Slice(admitted, func(i, j int) bool { return admitted[i].ID < admitted[j].ID })
	req.Materials, req.Candidates, req.Coverage = admitted, pool, coverage
	coverage.AdmittedCount = len(admitted)
	if coverage.LimitReached {
		coverage.Reason = "达到检索或正文容量上限；当前集合不足不表示全库没有其它合格材料"
	} else {
		coverage.Reason = "已检查本次文字匹配候选；未检验文字未匹配的其它材料"
	}
	for _, c := range pool {
		if c.State != "admitted" {
			req.Exclusions = append(req.Exclusions, provider.KnowledgeExclusion{MaterialID: c.MaterialID, Reason: c.Reason})
		}
	}
	if req.Topic != nil {
		copyTopic := *req.Topic
		ids := map[string]bool{}
		for _, m := range admitted {
			ids[m.ID] = true
		}
		copyTopic.MaterialIDs = nil
		for _, id := range req.Topic.MaterialIDs {
			if ids[id] {
				copyTopic.MaterialIDs = append(copyTopic.MaterialIDs, id)
			}
		}
		req.Topic = &copyTopic
	}
	// Reuse the established chronological/relevant history union, without a second
	// material retrieval or reading a library of full article bodies.
	history, err := s.knowledgeDirectionHistory(ctx, profile, topic)
	if err != nil {
		return req, err
	}
	seenHistory := map[string]bool{}
	req.History = append(history, req.History...)
	var bounded []provider.KnowledgeTopic
	historyBytes := 0
	for _, h := range req.History {
		key := h.ArticleID + "\x00" + h.Title + "\x00" + h.Question
		if seenHistory[key] {
			continue
		}
		seenHistory[key] = true
		raw, _ := json.Marshal(h)
		if historyBytes+len(raw) > 8000 {
			continue
		}
		historyBytes += len(raw)
		bounded = append(bounded, h)
	}
	req.History = bounded
	return req, nil
}

func knowledgeOppositionClue(text string) string {
	text = strings.ToLower(text)
	for _, term := range []string{"反例", "限制", "不适用", "无效", "相反", "contrary", "however", "limitation"} {
		if strings.Contains(text, term) {
			return "含反方/限制词项，需模型核实（程序线索）"
		}
	}
	return ""
}

// Round-robin across real source identities; preserve rank within each source,
// except explicit limitation vocabulary gets an early reading slot.
func knowledgeDiverseOrder(pool []provider.KnowledgeRecallCandidate) []int {
	var sources []string
	groups := map[string][]int{}
	for i, c := range pool {
		key := c.SourceType + ":" + c.SourceID
		if len(groups[key]) == 0 {
			sources = append(sources, key)
		}
		groups[key] = append(groups[key], i)
	}
	for _, key := range sources {
		sort.SliceStable(groups[key], func(i, j int) bool {
			return pool[groups[key][i]].LexicalClue != "" && pool[groups[key][j]].LexicalClue == ""
		})
	}
	var order []int
	for turn := 0; len(order) < len(pool); turn++ {
		for _, key := range sources {
			if turn < len(groups[key]) {
				order = append(order, groups[key][turn])
			}
		}
	}
	return order
}

// compactKnowledgeEvidence keeps complete real cited segments. It never slices
// a note or source sentence to fit capacity. Unread citation IDs remain explicit.
func (s *Store) compactKnowledgeEvidence(ctx context.Context, m *provider.KnowledgeMaterial, question string) error {
	raw, _ := json.Marshal(m)
	if len(raw) <= 10000 || m.SnapshotID == "" || len(m.Citations) == 0 {
		return nil
	}
	_, audio, docs, err := s.SnapshotContent(ctx, m.SnapshotID)
	if err != nil {
		return err
	}
	segments := map[string]provider.KnowledgeEvidenceSegment{}
	for _, seg := range audio {
		segments[seg.ID] = provider.KnowledgeEvidenceSegment{SegmentID: seg.ID, Text: seg.Text, Position: seg.Start}
	}
	for _, seg := range docs {
		segments[seg.ID] = provider.KnowledgeEvidenceSegment{SegmentID: seg.ID, Text: seg.Text, Position: float64(seg.Position)}
	}
	terms := strings.Fields(knowledgeTokens(question))
	ids := append([]string(nil), m.Citations...)
	score := func(id string) int {
		text := " " + knowledgeTokens(segments[id].Text) + " "
		n := 0
		for _, term := range terms {
			if strings.Contains(text, " "+term+" ") {
				n++
			}
		}
		return n
	}
	sort.SliceStable(ids, func(i, j int) bool { return score(ids[i]) > score(ids[j]) })
	copyMaterial := *m
	copyMaterial.Citations, copyMaterial.EvidenceWindow, copyMaterial.OmittedCitationIDs = nil, nil, nil
	copyMaterial.Evidence = ""
	var selected []string
	for _, id := range ids {
		seg, ok := segments[id]
		if !ok {
			return ErrConflict
		}
		trial := copyMaterial
		trial.Citations = append(append([]string(nil), copyMaterial.Citations...), id)
		trial.EvidenceWindow = append(append([]provider.KnowledgeEvidenceSegment(nil), copyMaterial.EvidenceWindow...), seg)
		trial.Evidence = copyMaterial.Evidence + fmt.Sprintf("[%s] %s\n", id, seg.Text)
		b, _ := json.Marshal(trial)
		if len(b) > 8500 {
			copyMaterial.OmittedCitationIDs = append(copyMaterial.OmittedCitationIDs, id)
			continue
		}
		copyMaterial.Citations, copyMaterial.EvidenceWindow, copyMaterial.Evidence = trial.Citations, trial.EvidenceWindow, trial.Evidence
		selected = append(selected, id)
	}
	if len(selected) > 0 {
		*m = copyMaterial
	}
	return nil
}
