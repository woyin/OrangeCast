package store

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"testing"
)

func TestArticleFeedbackCaseLifecycle(t *testing.T) {
	s, a, req, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	r, err := s.GetKnowledgeRevision(ctx, a.ID, a.WorkingRevision)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []provider.KnowledgeBlock
	json.Unmarshal([]byte(r.BlocksJSON), &blocks)
	if _, err = s.RecordArticleQualityFeedback(ctx, a.ID, r.Revision, 0, r.ContentHash, "wrong", "shallow", "条件"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	id, err := s.RecordArticleQualityFeedback(ctx, a.ID, r.Revision, 0, r.ContentHash, qualityHash(blocks[0].Text), "shallow", "条件")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	c, err := s.AcceptArticleQualityCase(ctx, id, key, 0, "补充适用条件")
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.AcceptArticleQualityCase(ctx, id, key, 0, "补充适用条件")
	if err != nil || same.ID != c.ID {
		t.Fatalf("replay %v %v", same, err)
	}
	if _, err = s.AcceptArticleQualityCase(ctx, id, key, 0, "改变"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	m, err := s.BuildArticleQualityManifest(ctx, []string{c.ID}, "pod")
	if err != nil || m.Fingerprint == "" {
		t.Fatalf("manifest %v %v", m, err)
	}
	if err = s.ClassifyArticleQualityCase(ctx, c.ID, c.Version, "capacity_omission", `{}`); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err = s.ClassifyArticleQualityCase(ctx, c.ID, c.Version, "unknown", `{}`); err != nil {
		t.Fatal(err)
	}
	if err = s.ClassifyArticleQualityCase(ctx, c.ID, c.Version, "unknown", `{}`); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	c2, err := s.AcceptArticleQualityCase(ctx, id, uuid.NewString(), 1, "保留来源边界")
	if err != nil || c2.Version != 2 {
		t.Fatalf("revise %v %v", c2, err)
	}
	old, _ := s.GetArticleQualityCase(ctx, c.ID)
	if old.Expected != "补充适用条件" {
		t.Fatal("changed frozen")
	}
	source := req.Materials[0]
	_, err = s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BuildArticleQualityManifest(ctx, []string{c.ID}, "pod"); err == nil {
		t.Fatal("permission bypass")
	}
	if err = s.RetireArticleQualityCase(ctx, c2.ID, c2.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BuildArticleQualityManifest(ctx, []string{c2.ID}, "pod"); err == nil {
		t.Fatal("retired export")
	}
	_, err = s.DB.Exec(`UPDATE knowledge_article_revisions SET evidence_status='unavailable' WHERE article_id=?`, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, _ = s.GetArticleQualityCase(ctx, c.ID)
	if old.InputJSON != "" || old.BlocksJSON != "" || old.State != "unavailable" {
		t.Fatal("purge retention")
	}
}
func TestArticleFeedbackCaseUsefulWholeArticle(t *testing.T) {
	s, a, _, _ := readyKnowledgeRevisionFixture(t)
	r, _ := s.GetKnowledgeRevision(t.Context(), a.ID, a.WorkingRevision)
	if _, err := s.RecordArticleQualityFeedback(t.Context(), a.ID, r.Revision, -1, r.ContentHash, "", "useful", ""); err != nil {
		t.Fatal(err)
	}
}

func TestArticleFeedbackCaseSourcePurgeRetainsOwnerRecord(t *testing.T) {
	s, a, req, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	r, _ := s.GetKnowledgeRevision(ctx, a.ID, a.WorkingRevision)
	id, err := s.RecordArticleQualityFeedback(ctx, a.ID, r.Revision, -1, r.ContentHash, "", "useful", "我的反馈")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AcceptArticleQualityCase(ctx, id, uuid.NewString(), 0, "保留边界")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceType(req.Materials[0].SourceType), req.Materials[0].SourceID); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetArticleQualityCase(ctx, c.ID)
	if err != nil || old.State != "unavailable" || old.InputJSON != "" || old.BlocksJSON != "" || old.Expected != "保留边界" {
		t.Fatalf("purge %+v %v", old, err)
	}
	observations, err := s.ListArticleQualityFeedback(ctx)
	if err != nil || len(observations) != 1 || observations[0].Comment != "我的反馈" {
		t.Fatal(observations, err)
	}
}

func TestArticleFeedbackCaseFrozenFacts(t *testing.T) {
	s, a, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	r, _ := s.GetKnowledgeRevision(ctx, a.ID, a.WorkingRevision)
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(r.InputJSON), &req)
	req.Coverage = &provider.KnowledgeRecallCoverage{Method: "fts", TotalMatches: 80, MetadataCount: 20, ReadCount: 8, AdmittedCount: 4, LimitReached: true}
	req.Candidates = []provider.KnowledgeRecallCandidate{{MaterialID: "not-read", State: "not_read"}}
	raw, _ := json.Marshal(req)
	s.DB.Exec(`UPDATE knowledge_article_revisions SET input_json=? WHERE article_id=? AND revision=?`, string(raw), a.ID, r.Revision)
	id, err := s.RecordArticleQualityFeedback(ctx, a.ID, r.Revision, -1, r.ContentHash, "", "useful", "清楚")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AcceptArticleQualityCase(ctx, id, uuid.NewString(), 0, "保留条件")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := s.ArticleQualityCaseFacts(ctx, c.ID)
	if err != nil || facts.Coverage == nil || facts.Coverage.ReadCount != 8 || len(facts.CapacityOmissions) != 1 || len(facts.Candidates) != 2 || facts.Candidates[1] != "capacity_omission" {
		t.Fatalf("facts %+v %v", facts, err)
	}
	key := uuid.NewString()
	if err = s.ClassifyArticleQualityCaseCommand(ctx, c.ID, c.Version, "capacity_omission", `{"source":"frozen-coverage","limit_reached":true}`, key); err != nil {
		t.Fatal(err)
	}
	if err = s.ClassifyArticleQualityCaseCommand(ctx, c.ID, c.Version, "capacity_omission", `{"source":"frozen-coverage","limit_reached":true}`, key); err != nil {
		t.Fatal("replay", err)
	}
	if err = s.ClassifyArticleQualityCaseCommand(ctx, c.ID, c.Version, "unknown", `{}`, key); !errors.Is(err, ErrConflict) {
		t.Fatal("identity changed", err)
	}
	key = uuid.NewString()
	if err = s.RetireArticleQualityCaseCommand(ctx, c.ID, c.Version, key); err != nil {
		t.Fatal(err)
	}
	if err = s.RetireArticleQualityCaseCommand(ctx, c.ID, c.Version, key); err != nil {
		t.Fatal("retire replay", err)
	}
}
