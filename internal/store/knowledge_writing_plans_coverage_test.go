package store

import (
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func TestKnowledgeWritingPlanRejectedEditsPreserveFrozenRevision(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	for _, outline := range []string{"  ", strings.Repeat("字", 12001)} {
		if _, e := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, outline); !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if _, e := s.EditKnowledgeWritingPlanPurpose(ctx, p.ArticleID, p.Revision, p.Hash, "有效提纲", "invented"); e == nil {
		t.Fatal("unknown purpose accepted")
	}
	if _, e := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, "wrong", "有效提纲"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.EditKnowledgeWritingPlan(ctx, "missing", 1, "wrong", "有效提纲"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.ConfirmKnowledgeWritingPlan(ctx, "missing", 1, "wrong"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	current, e := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if e != nil || current.Revision != p.Revision || current.Hash != p.Hash {
		t.Fatal(current, e)
	}
	if _, e = s.DB.Exec(`UPDATE knowledge_articles SET status='needs_review' WHERE id=?`, p.ArticleID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "有效提纲"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestKnowledgeWritingPlanEditInsertFailureRestoresPreviousRevision(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if _, e := s.DB.Exec(`CREATE TRIGGER fail_plan_revision BEFORE INSERT ON knowledge_writing_plans WHEN NEW.revision>1 BEGIN SELECT RAISE(ABORT,'disk write simulation'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "新提纲"); e == nil {
		t.Fatal("durable insert failure ignored")
	}
	current, e := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if e != nil || current.State != "pending" || current.Hash != p.Hash {
		t.Fatal(current, e)
	}
	if _, e = s.DB.Exec(`DROP TRIGGER fail_plan_revision`); e != nil {
		t.Fatal(e)
	}
	next, e := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "新提纲")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "旧界面保存"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE knowledge_writing_plans SET state='superseded' WHERE article_id=? AND revision=?`, next.ArticleID, next.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, next.ArticleID, next.Revision, next.Hash); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestKnowledgeWritingPlanBudgetAndDirectionAdmission(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	budget := int64(1000)
	if e := s.SetOwnerMonthlyBudget(ctx, &budget); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(e, ErrBudgetUnpriced) {
		t.Fatal(e)
	}
	if e := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "writer", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 1000000}); e != nil {
		t.Fatal(e)
	}
	p, e := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, p.Request.Topic.Outline)
	if e != nil {
		t.Fatal(e)
	}
	if p.Request.Estimate.CostCents == nil {
		t.Fatal("missing frozen cost")
	}
	budget = *p.Request.Estimate.CostCents - 1
	if e = s.SetOwnerMonthlyBudget(ctx, &budget); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(e, ErrBudgetExhausted) {
		t.Fatal(e)
	}
	budget = *p.Request.Estimate.CostCents + 1
	if e = s.SetOwnerMonthlyBudget(ctx, &budget); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO run_controls(kind,target,paused) VALUES('direction',?,1)`, "knowledge_article:"+p.ArticleID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(e, ErrRunControlled) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE run_controls SET paused=0 WHERE kind='direction'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); e != nil {
		t.Fatal(e)
	}
	if _, e = s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "确认后旧表单"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

// These tests execute the accepting-transaction recheck itself, covering changes
// between a preliminary policy check and admission without manufacturing paid calls.
func TestKnowledgeWritingPlanTransactionMaterialRevocation(t *testing.T) {
	cases := []struct {
		name, sql string
		args      func(*KnowledgeWritingPlan) []any
	}{
		{"archive", `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].SourceID} }},
		{"approvedwrong", `UPDATE episodes SET model_data_policy='approved_providers_only',approved_providers_json='["another"]' WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].SourceID} }},
		{"malformedallowlist", `UPDATE episodes SET model_data_policy='approved_providers_only',approved_providers_json='broken' WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].SourceID} }},
		{"snapshotpurged", `UPDATE source_snapshots SET status='purged' WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].SnapshotID} }},
		{"transcriptchanged", `UPDATE episodes SET current_transcript_version=current_transcript_version+1 WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].SourceID} }},
		{"keypointdismissed", `UPDATE keypoint_index SET production_status='dismissed' WHERE id=?`, func(p *KnowledgeWritingPlan) []any { return []any{p.Request.Materials[0].ID} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, p := writingPlanFixture(t)
			if p.Request.Materials[0].Kind != "keypoint" {
				p.Request.Materials[0], p.Request.Materials[1] = p.Request.Materials[1], p.Request.Materials[0]
			}
			ctx := t.Context()
			tx, e := s.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = tx.Exec(c.sql, c.args(p)...); e != nil {
				t.Fatal(e)
			}
			if e = checkWritingPlanMaterials(ctx, tx, p.Provider, p.Request.Materials); e == nil {
				t.Fatal("changed dependency admitted")
			}
		})
	}
}

func TestKnowledgeWritingPlanTransactionAllowsExactApprovedProviderAndRejectsChangedNote(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE episodes SET model_data_policy='approved_providers_only',approved_providers_json='[" POD "]' WHERE id=?`, p.Request.Materials[0].SourceID); e != nil {
		t.Fatal(e)
	}
	if e = checkWritingPlanMaterials(ctx, tx, "pod", p.Request.Materials); e != nil {
		t.Fatal(e)
	}
	var note provider.KnowledgeMaterial
	for _, m := range p.Request.Materials {
		if m.Kind != "keypoint" {
			note = m
		}
	}
	if _, e = tx.Exec(`UPDATE owner_notes SET revision=revision+1 WHERE id=?`, note.ID); e != nil {
		t.Fatal(e)
	}
	if e = checkWritingPlanMaterials(ctx, tx, "pod", p.Request.Materials); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestKnowledgeWritingPlanConfirmationReceiptFailureRollsBackJob(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if _, e := s.DB.Exec(`CREATE TRIGGER fail_confirm_receipt BEFORE UPDATE OF state ON knowledge_writing_plans WHEN NEW.state='confirmed' BEGIN SELECT RAISE(ABORT,'receipt unavailable'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); e == nil {
		t.Fatal("receipt failure ignored")
	}
	var count int
	if e := s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE intent_id=?`, "knowledge-article:"+p.ArticleID+":write").Scan(&count); e != nil || count != 0 {
		t.Fatal("orphan admitted job", count, e)
	}
	current, e := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if e != nil || current.State != "pending" {
		t.Fatal(current, e)
	}
	if _, e = s.DB.Exec(`DROP TRIGGER fail_confirm_receipt`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); e != nil {
		t.Fatal(e)
	}
}

func TestKnowledgeWritingPlanSelectionMustFinishBeforePlanAdmission(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	request := p.Request
	request.Topic = nil
	if e = createKnowledgeWritingPlan(ctx, tx, p.ArticleID, request, p.Provider, p.Model, false); !errors.Is(e, ErrConflict) {
		t.Fatalf("unfinished selection admitted: %v", e)
	}
	request = p.Request
	topic := *request.Topic
	topic.Outline = ""
	request.Topic = &topic
	if e = createKnowledgeWritingPlan(ctx, tx, p.ArticleID, request, p.Provider, p.Model, false); !errors.Is(e, ErrConflict) {
		t.Fatalf("empty outline admitted: %v", e)
	}
	if e = createKnowledgeWritingPlan(ctx, tx, "missing-article", p.Request, p.Provider, p.Model, false); e == nil {
		t.Fatal("missing article received plan")
	}
}
