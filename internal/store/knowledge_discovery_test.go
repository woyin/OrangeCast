package store

import (
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func TestKnowledgeDiscoveryDirectionsReuseAndHistoryRecall(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	old, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "旧理解：保留来源上下文才能避免误读。"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 42; i++ {
		_, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "无关的新笔记：日常整理。"})
		if err != nil {
			t.Fatal(err)
		}
	}
	req, seq, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{MaterialIDs: []string{old.ID}}, false)
	if err != nil || len(req.Materials) < 2 {
		t.Fatal(err, len(req.Materials))
	}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "model", req, seq, false)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "model", req, seq, false)
	if err != nil || same.ID != batch.ID {
		t.Fatal("batch reuse", err)
	}
	req.DiscoveryBatchID = batch.ID
	a, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.ListQueuedOrRunning(ctx)
	j := jobs[0]
	ex, _ := s.GetJobExecution(ctx, j.ID)
	var input KnowledgeStageInput
	_ = json.Unmarshal([]byte(ex.InputSnapshotJSON), &input)
	ids := []string{old.ID}
	for _, m := range req.Materials {
		if m.ID != old.ID {
			ids = append(ids, m.ID)
			break
		}
	}
	topics := []provider.KnowledgeTopic{{Title: "可靠理解", Question: "怎样防止来源误读？", Thesis: "检查上下文", Outline: "检查来源", Score: 95, Sufficient: true, MaterialIDs: ids}, {Title: "学习节奏", Question: "怎样安排每周复盘？", Thesis: "安排复盘时间", Outline: "实践安排", Score: 80, Sufficient: true, MaterialIDs: ids}, {Title: "材料缺口", Question: "效果是多少？", Thesis: "没有量化证据", Score: 40, Missing: []string{"缺少量化数据"}, MaterialIDs: ids}}
	if err = s.CommitKnowledgeStage(ctx, j, input, &provider.KnowledgeArticleResult{Topics: topics}); err != nil {
		t.Fatal(err)
	}
	_ = s.MarkJobSucceeded(ctx, j.ID)
	a, _ = s.GetKnowledgeArticle(ctx, a.ID)
	if a.Stage != "select" || a.CandidateID == "" {
		t.Fatal(a)
	}
	candidates, err := s.ListKnowledgeTopicCandidates(ctx)
	if err != nil || len(candidates) != 3 {
		t.Fatal(err, candidates)
	}
	var waiting string
	for _, c := range candidates {
		if c.Status == "waiting" {
			waiting = c.ID
		}
	}
	if waiting == "" {
		t.Fatal("second direction lost")
	}
	if _, _, err = s.StartKnowledgeCandidate(ctx, waiting, false); err == nil {
		t.Fatal("backpressure bypassed")
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET status='ready' WHERE id=?`, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, fresh, err := s.StartKnowledgeCandidate(ctx, waiting, false)
	if err != nil || !fresh || b.ID == a.ID {
		t.Fatal("independent writing identity", err)
	}
	b2, fresh, err := s.StartKnowledgeCandidate(ctx, waiting, false)
	if err != nil || fresh || b2.ID != b.ID {
		t.Fatal("candidate click replay", err)
	}
	var frozen provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(b.InputJSON), &frozen)
	found := false
	for _, m := range frozen.Materials {
		if m.ID == old.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("old material missing")
	}
	if err = s.DeleteSourceRows(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ListKnowledgeTopicCandidates(ctx)
	if err != nil || len(candidates) != 0 {
		t.Fatal("purged seeds retained", err)
	}
}

func TestKnowledgeDiscoveryHistoryCursorScopeAndInvalidity(t *testing.T) {
	s, profile, ep, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	for i := 0; i < 24; i++ {
		_, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "保留来源，区别个人理解。"})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, seq, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{ExploreHistory: true}, false)
	if err != nil || seq != 20 || len(first.Materials) > 20 {
		t.Fatal(seq, err)
	}
	next, last, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{ExploreHistory: true, HistoryCursor: seq}, false)
	if err != nil || last <= seq || len(next.Materials) == 0 {
		t.Fatal(last, err)
	}
	if _, _, _, err = s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{From: "wrong"}, false); err == nil {
		t.Fatal("invalid date accepted")
	}
	frozen := first.Materials
	if err = s.SetSourceProductionPolicy(ctx, models.SourceEpisode, ep, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckKnowledgeMaterials(ctx, profile, "pod", frozen); err == nil {
		t.Fatal("policy revoked evidence accepted")
	}
	filtered, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{}, false)
	if err != nil || len(filtered.Materials) != 0 {
		t.Fatal(err)
	}
}

func TestKnowledgeCandidateBoundsKeepDifferentSourcesAndWholeEvidence(t *testing.T) {
	var candidates []provider.KnowledgeMaterial
	for i := 0; i < 25; i++ {
		candidates = append(candidates, provider.KnowledgeMaterial{ID: fmt.Sprint(i), SourceID: "dominant", SourceType: "episode", Content: "有效原文"})
	}
	candidates = append(candidates, provider.KnowledgeMaterial{ID: "other", SourceID: "other", SourceType: "episode", Content: "反方条件"}, provider.KnowledgeMaterial{ID: "oversized", SourceID: "other", Content: strings.Repeat("x", 11000)})
	admitted := boundKnowledgeMaterials(candidates, 20)
	if len(admitted) != 20 {
		t.Fatal(len(admitted))
	}
	found := false
	for _, m := range admitted {
		if m.ID == "other" {
			found = true
		}
		if m.ID == "oversized" {
			t.Fatal("evidence truncated or oversized")
		}
	}
	if !found {
		t.Fatal("one source crowded out opposition")
	}
	exclusions := knowledgeExclusions(append(candidates, candidates[24]), admitted)
	if len(exclusions) != 7 {
		t.Fatal(exclusions)
	}
	for _, x := range exclusions {
		if x.Reason == "" {
			t.Fatal("unexplained program bound")
		}
	}
	if len(boundKnowledgeMaterials([]provider.KnowledgeMaterial{{ID: "a", Content: strings.Repeat("x", 9500)}, {ID: "b", Content: strings.Repeat("x", 9500)}, {ID: "c", Content: strings.Repeat("x", 9500)}, {ID: "d", Content: strings.Repeat("x", 9500)}, {ID: "e", Content: strings.Repeat("x", 9500)}}, 20)) != 4 {
		t.Fatal("aggregate payload bound")
	}
}
