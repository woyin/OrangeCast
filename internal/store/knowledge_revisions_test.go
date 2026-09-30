package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func finishKnowledgeTestStage(t *testing.T, s *Store, passed bool) (*models.ProcessingJob, KnowledgeStageInput) {
	t.Helper()
	job, err := s.ClaimNextJob(t.Context(), "60 seconds")
	if err != nil || job == nil {
		t.Fatalf("claim: %+v %v", job, err)
	}
	execution, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input KnowledgeStageInput
	if err := json.Unmarshal([]byte(execution.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	result := &provider.KnowledgeArticleResult{}
	switch input.Stage {
	case "discover":
		result.Topics = []provider.KnowledgeTopic{{Title: "怎样保留学习依据", Question: "如何可靠理解？", Thesis: "区分来源与解释", Outline: "来源、理解", Score: 90, Sufficient: true, MaterialIDs: []string{input.Request.Materials[0].ID, input.Request.Materials[1].ID}}}
	case "write", "revise":
		result.Title = "怎样保留学习依据"
		result.Blocks = []provider.KnowledgeBlock{{Kind: "synthesis", Text: "保留来源，明确理解的边界。", MaterialIDs: []string{input.Request.Materials[0].ID}}, {Kind: "synthesis", Text: "比较自己的理解与原始表达。", MaterialIDs: []string{input.Request.Materials[1].ID}}}
	case "review", "review_final":
		result.Passed = &passed
		if !passed {
			result.Issues = []string{"第2段需要解释条件"}
		}
	default:
		t.Fatal("unexpected stage", input.Stage)
	}
	if err := s.CommitKnowledgeStage(t.Context(), job, input, result); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	return job, input
}
func readyKnowledgeRevisionFixture(t *testing.T) (*Store, *KnowledgeArticleRecord, provider.KnowledgeArticleRequest, string) {
	t.Helper()
	s, profile, _, noteID := knowledgeStoreFixture(t)
	req, _, err := s.BuildKnowledgeArticleRequest(t.Context(), profile, "pod")
	if err != nil {
		t.Fatal(err)
	}
	req.ReviewModel = "review-separate"
	article, _, err := s.ReserveKnowledgeArticle(t.Context(), profile, "pod", "writer", req, false)
	if err != nil {
		t.Fatal(err)
	}
	finishKnowledgeTestStage(t, s, true)
	finishKnowledgeTestStage(t, s, true)
	job, _ := finishKnowledgeTestStage(t, s, true)
	execution, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || execution.ConfiguredModel != "review-separate" {
		t.Fatalf("review model %+v %v", execution, err)
	}
	article, err = s.GetKnowledgeArticle(t.Context(), article.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, article, req, noteID
}

func TestKnowledgeRevisionEditingReviewFeedbackAndInvalidation(t *testing.T) {
	s, article, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	original, err := s.GetKnowledgeRevision(ctx, article.ID, 1)
	if err != nil || !original.Passed {
		t.Fatalf("original %+v %v", original, err)
	}
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(original.InputJSON), &req)
	_ = json.Unmarshal([]byte(original.BlocksJSON), &blocks)
	blocks[0].Text = "人工修改：解释应该保留来源条件。"
	draft, err := s.SaveKnowledgeDraft(ctx, article.ID, 1, "手动修改", blocks, req)
	if err != nil || draft.Passed || draft.Revision != 2 {
		t.Fatalf("manual %+v %v", draft, err)
	}
	current, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if current.PassedRevision != 1 || current.WorkingRevision != 2 || current.Status != "needs_review" {
		t.Fatalf("old pass lost %+v", current)
	}
	if _, err := s.SaveKnowledgeDraft(ctx, article.ID, 1, "过期编辑", blocks, req); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.QueueKnowledgeRevision(ctx, article.ID, 2, "说明第2段的条件", "review-new", true); err != nil {
		t.Fatal(err)
	}
	job, input := finishKnowledgeTestStage(t, s, false)
	if input.Request.Instructions != "说明第2段的条件" || input.ExpectedRevision == nil || *input.ExpectedRevision != 2 {
		t.Fatalf("lost directed request %+v", input)
	}
	_, input = finishKnowledgeTestStage(t, s, false)
	if input.Stage != "review_final" {
		t.Fatal("unbounded re-review", input.Stage)
	}
	pending, _ := s.ListQueuedOrRunning(ctx)
	for _, j := range pending {
		if j.JobType == models.JobKnowledgeArticle {
			t.Fatal("revision loops")
		}
	}
	current, _ = s.GetKnowledgeArticle(ctx, article.ID)
	if current.Status != "needs_review" || current.PassedRevision != 1 || current.WorkingRevision != 3 {
		t.Fatalf("bounded result %+v", current)
	}
	if err := s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: job.ID + ":cost", AttemptID: job.ID, Operation: "knowledge_article_revise", Provider: "pod", Model: "writer", InputUnits: 20, OutputUnits: 10, CostKnown: true, CostCents: 3}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListKnowledgeExecutions(ctx, article.ID)
	if err != nil || len(runs) != 5 {
		t.Fatal(runs, err)
	}
	known := false
	for _, run := range runs {
		if run.JobID == job.ID && run.Cost == "3.0000" {
			known = true
		}
	}
	if !known {
		t.Fatal("known receipt missing", runs)
	}
	if err := s.RecordKnowledgeFeedback(ctx, article.ID, 1, "shallow", "需要适用条件"); err != nil {
		t.Fatal(err)
	}
	feedback, err := s.ListKnowledgeFeedback(ctx, article.ID)
	if err != nil || len(feedback) != 1 || feedback[0].Revision != 1 {
		t.Fatal(feedback, err)
	}
	prefs, _ := s.GetKnowledgeArticleSettings(ctx)
	if strings.Contains(prefs.Style, "需要适用条件") {
		t.Fatal("feedback silently changed policy")
	}
	usages, err := s.FindUsageByNote(ctx, noteID)
	if err != nil || len(usages) < 3 {
		t.Fatal("missing backlinks", usages, err)
	}
	note, _ := s.GetOwnerNote(ctx, noteID)
	if _, err := s.UpdateOwnerNote(ctx, noteID, "更新理解", "[]", "[]", note.Revision); err != nil {
		t.Fatal(err)
	}
	original, _ = s.GetKnowledgeRevision(ctx, article.ID, 1)
	state, _, err := s.KnowledgeEvidenceState(ctx, current, original)
	if err != nil || state != "outdated" || !original.Passed {
		t.Fatalf("execution/evidence conflated %+v %s %v", original, state, err)
	}
	note, _ = s.GetOwnerNote(ctx, noteID)
	if err := s.DeleteOwnerNote(ctx, noteID, note.Revision); err != nil {
		t.Fatal(err)
	}
	original, _ = s.GetKnowledgeRevision(ctx, article.ID, 1)
	if original.EvidenceStatus != "unavailable" {
		t.Fatal("delete failed to invalidate", original)
	}
	history, err := s.ListKnowledgeRevisions(ctx, article.ID)
	if err != nil || len(history) != 3 || history[2].BlocksJSON == history[1].BlocksJSON {
		t.Fatalf("immutable history %+v %v", history, err)
	}
}

func TestKnowledgeLateReviewCannotSelectNewDraft(t *testing.T) {
	s, article, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	if err := s.QueueKnowledgeRevision(ctx, article.ID, 1, "", "review-next", false); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil {
		t.Fatal(err)
	}
	execution, _ := s.GetJobExecution(ctx, job.ID)
	var input KnowledgeStageInput
	_ = json.Unmarshal([]byte(execution.InputSnapshotJSON), &input)
	old, _ := s.GetKnowledgeRevision(ctx, article.ID, 1)
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(old.InputJSON), &req)
	_ = json.Unmarshal([]byte(old.BlocksJSON), &blocks)
	blocks[0].Text = "后来的人工编辑"
	if _, err := s.SaveKnowledgeDraft(ctx, article.ID, 1, "新工作稿", blocks, req); err != nil {
		t.Fatal(err)
	}
	passed := true
	if err := s.CommitKnowledgeStage(ctx, job, input, &provider.KnowledgeArticleResult{Passed: &passed}); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetKnowledgeArticle(ctx, article.ID)
	if current.WorkingRevision != 2 || current.PassedRevision != 1 || current.Status != "needs_review" {
		t.Fatal("late review overwrote work", current)
	}
	reviews, err := s.ListKnowledgeReviews(ctx, article.ID)
	if err != nil || len(reviews) != 2 || reviews[0].Revision != 1 {
		t.Fatal("late review audit lost", reviews, err)
	}
	draft, _ := s.GetKnowledgeRevision(ctx, article.ID, 2)
	if draft.Passed {
		t.Fatal("old review passed new content")
	}
}

func TestKnowledgeLegacyStatesUpgrade(t *testing.T) {
	db := openRaw(t, filepath.Join(t.TempDir(), "legacy.db"))
	ctx := t.Context()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version <= 58 {
			if err := applyOne(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Store{DB: db}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"ready", "ready", "needs_review", "failed", "review"} {
		body := `[{"kind":"synthesis","text":"原正文","material_ids":["m"]}]`
		if _, err := db.Exec(`INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,blocks_json,provider,model) VALUES(?,?,?,'{"materials":[]}',?,?,?,'pod','old-model')`, fmt.Sprintf("a%d", i), profile.ID, fmt.Sprintf("h%d", i), state, state, body); err != nil {
			t.Fatal(err)
		}
	}
	applied, err := Migrate(ctx, db)
	if err != nil || len(applied) < 1 {
		t.Fatal(applied, err)
	}
	if applied, err = Migrate(ctx, db); err != nil || len(applied) != 0 {
		t.Fatal("non-idempotent upgrade", applied, err)
	}
	for i, state := range []string{"ready", "ready", "needs_review", "failed", "review"} {
		article, err := s.GetKnowledgeArticle(ctx, fmt.Sprintf("a%d", i))
		if err != nil || article.Status != state || article.WorkingRevision != 1 {
			t.Fatal(article, err)
		}
		v, err := s.GetKnowledgeRevision(ctx, article.ID, 1)
		if err != nil || v.Passed != (state == "ready") || v.Model != "old-model" {
			t.Fatal("invented pass/config", v, err)
		}
	}
}
