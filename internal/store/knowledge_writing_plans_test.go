package store

import (
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func writingPlanFixture(t *testing.T) (*Store, *KnowledgeWritingPlan) {
	t.Helper()
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, err := s.BuildKnowledgeArticleRequest(ctx, profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "writer", req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_articles SET status='awaiting_plan',stage='select' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	req.Topic = &provider.KnowledgeTopic{Title: "引用", Question: "如何引用", Thesis: "保留依据", Outline: "来源与理解", MaterialIDs: []string{req.Materials[0].ID, req.Materials[1].ID}, Sufficient: true}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = createKnowledgeWritingPlan(ctx, tx, a.ID, req, "pod", "writer", false); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetKnowledgeWritingPlan(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}
func TestKnowledgeWritingPlanExactRevisionAndIdempotency(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	next, err := s.EditKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash, "新提纲")
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != p.Revision+1 || next.Hash == p.Hash {
		t.Fatal("revision/hash not renewed")
	}
	if _, err = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("old accepted: %v", err)
	}
	done, err := s.ConfirmKnowledgeWritingPlan(ctx, next.ArticleID, next.Revision, next.Hash)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.ConfirmKnowledgeWritingPlan(ctx, next.ArticleID, next.Revision, next.Hash)
	if err != nil || repeat.JobID != done.JobID {
		t.Fatalf("repeat: %v %+v", err, repeat)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE source_id=? AND intent_id=?`, p.ArticleID, "knowledge-article:"+p.ArticleID+":write").Scan(&count); err != nil || count != 1 {
		t.Fatalf("jobs=%d %v", count, err)
	}
	var raw string
	if err = s.DB.QueryRow(`SELECT input_snapshot_json FROM processing_jobs WHERE id=?`, done.JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var input KnowledgeStageInput
	if err = json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input.Request.Topic.Outline != "新提纲" || len(input.Request.Materials) != len(next.Request.Materials) {
		t.Fatal("confirmation changed selection")
	}
}
func TestKnowledgeWritingPlanBlockedPreservesPlan(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`UPDATE run_controls SET paused=1,reason='pause' WHERE kind='lane' AND target='knowledge'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrRunControlled) {
		t.Fatalf("stop: %v", err)
	}
	current, err := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if err != nil || current.State != "pending" || current.JobID != "" {
		t.Fatalf("lost: %+v %v", current, err)
	}
	if _, err = s.DB.Exec(`DELETE FROM run_controls`); err != nil {
		t.Fatal(err)
	}
	if err = s.SetOwnerMonthlyBudget(ctx, new(int64)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrBudgetUnpriced) && !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("budget: %v", err)
	}
	current, err = s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if err != nil || current.State != "pending" {
		t.Fatalf("budget lost: %+v %v", current, err)
	}
}

func TestKnowledgeWritingPlanTamperAndSourceRevocation(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`UPDATE knowledge_writing_plans SET request_json='{}' WHERE article_id=?`, p.ArticleID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKnowledgeWritingPlan(ctx, p.ArticleID); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered request accepted: %v", err)
	}
	s, p = writingPlanFixture(t)
	if _, err := s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, p.Request.Materials[0].SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("revocation ignored: %v", err)
	}
	current, err := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if err != nil || current.State != "pending" {
		t.Fatalf("revoked plan lost: %+v %v", current, err)
	}
}

func TestKnowledgeWritingPlanConcurrentConfirmationAndRollback(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_plan_enqueue BEFORE INSERT ON knowledge_article_runs WHEN NEW.stage='write' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); err == nil {
		t.Fatal("injected failure ignored")
	}
	current, err := s.GetKnowledgeWritingPlan(ctx, p.ArticleID)
	if err != nil || current.State != "pending" {
		t.Fatal(current, err)
	}
	a, err := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if err != nil || a.Status != "awaiting_plan" {
		t.Fatal("article advanced despite rollback", a, err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_plan_enqueue`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, e := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash)
			if e != nil {
				errs <- e
				return
			}
			results <- done.JobID
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	jobID := ""
	for id := range results {
		if jobID != "" && id != jobID {
			t.Fatal("different confirmation identities")
		}
		jobID = id
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE intent_id=?`, "knowledge-article:"+p.ArticleID+":write").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestKnowledgeWritingPlanPricePurposeAndParentRevision(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "writer", InputCentsPerMillion: 100, OutputCentsPerMillion: 400}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("new price silently accepted: %v", err)
	}
	next, err := s.EditKnowledgeWritingPlanPurpose(ctx, p.ArticleID, p.Revision, p.Hash, "实践步骤", "practice")
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != p.Revision+1 || next.Request.WritingPurpose.Mode != "practice" || !next.Request.Estimate.PriceKnown {
		t.Fatal(next)
	}
	oldMaterials, _ := json.Marshal(p.Request.Materials)
	newMaterials, _ := json.Marshal(next.Request.Materials)
	if string(oldMaterials) != string(newMaterials) {
		t.Fatal("purpose editing changed materials")
	}
	var oldBody string
	if err = s.DB.QueryRow(`SELECT request_json FROM knowledge_writing_plans WHERE article_id=? AND revision=?`, p.ArticleID, p.Revision).Scan(&oldBody); err != nil {
		t.Fatal(err)
	}
	var old provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(oldBody), &old); err != nil {
		t.Fatal(err)
	}
	if old.Topic.Outline != p.Request.Topic.Outline || old.WritingPurpose.Mode != p.Request.WritingPurpose.Mode {
		t.Fatal("old revision rewritten")
	}
	if _, err = s.ConfirmKnowledgeWritingPlan(ctx, next.ArticleID, next.Revision, next.Hash); err != nil {
		t.Fatal(err)
	}
	s, p = writingPlanFixture(t)
	if _, err = s.DB.Exec(`UPDATE knowledge_articles SET working_revision=working_revision+1 WHERE id=?`, p.ArticleID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmKnowledgeWritingPlan(ctx, p.ArticleID, p.Revision, p.Hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed parent accepted: %v", err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE intent_id=?`, "knowledge-article:"+p.ArticleID+":write").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestKnowledgeWritingPlanPurgeRecalledSourceBeforeFirstBodyRetainsNumericUsage(t *testing.T) {
	s, p := writingPlanFixture(t)
	ctx := t.Context()
	pod, err := s.CreatePodcast(ctx, "https://plan-extra.test/feed", "召回附加来源", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "extra", Title: "附加来源", AudioURL: "https://plan-extra.test/audio"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, pod.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatal(episodes, err)
	}
	source := episodes[0].ID
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: source, Kind: "owner_reflection", Content: "PURGE_PLAN_EXTRA_BODY"})
	if err != nil {
		t.Fatal(err)
	}
	article, err := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := s.knowledgeMaterial(ctx, article.ProfileID, "pod", note.ID)
	if err != nil || extra == nil {
		t.Fatal(extra, err)
	}
	req := p.Request
	req.Materials = append(req.Materials, *extra)
	req.Topic.MaterialIDs = append(req.Topic.MaterialIDs, extra.ID)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_writing_plans SET state='superseded' WHERE article_id=?`, p.ArticleID); err != nil {
		t.Fatal(err)
	}
	if err = createKnowledgeWritingPlan(ctx, tx, p.ArticleID, req, p.Provider, p.Model, false); err != nil {
		t.Fatal(err)
	}
	if err = enqueueKnowledgeStage(ctx, tx, p.ArticleID, "write", p.Provider, p.Model, req, false); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_id=? AND intent_id=? ORDER BY rowid DESC LIMIT 1`, p.ArticleID, "knowledge-article:"+p.ArticleID+":write").Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveJobCheckpoint(ctx, jobID, `{"result":{"reason":"PURGE_PLAN_EXTRA_BODY"}}`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: jobID + ":writing-plan-purge", AttemptID: jobID, Operation: "knowledge_article_write", Provider: "pod", Model: "writer", InputUnits: 7, OutputUnits: 3, CostKnown: true, CostCents: 2}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceEpisode, source); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetKnowledgeArticle(ctx, p.ArticleID); !errors.Is(err, ErrNotFound) {
		t.Fatal("article body retained", err)
	}
	if _, err = s.GetKnowledgeWritingPlan(ctx, p.ArticleID); !errors.Is(err, ErrNotFound) {
		t.Fatal("plan body retained", err)
	}
	var jobs, receipts, units int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE source_id=?`, p.ArticleID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("job inputs/checkpoints retained", jobs, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(input_units+output_units) FROM usage_records WHERE receipt_id=?`, jobID+":writing-plan-purge").Scan(&receipts, &units); err != nil || receipts != 1 || units != 10 {
		t.Fatal("numeric usage lost", receipts, units, err)
	}
}
