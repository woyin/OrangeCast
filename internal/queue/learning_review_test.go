package queue

import (
	"context"
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"testing"
	"time"
)

type reviewQueueFake struct {
	calls int
	bad   bool
}

func (f *reviewQueueFake) Name() string { return "pod" }
func (f *reviewQueueFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	id := req.Materials[0].ID
	if f.bad {
		id = "unknown"
	}
	return &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "怎样用自己的话解释？", AnswerBasis: "这是个人反思的回顾，不是来源事实。", MaterialIDs: []string{id}}}}, provider.TaskUsage{InputUnits: 20, OutputUnits: 10}, nil
}
func seedReviewQueue(t *testing.T) (*store.Store, *Worker, *store.LearningReviewBatch, *reviewQueueFake) {
	t.Helper()
	s, w := newTestWorker(t)
	ep := seedEpisode(t, s)
	_, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "听后独立解释，再回看笔记。"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.ReserveLearningReview(t.Context(), p.ID, "pod", "model", time.Now(), false)
	if err != nil || b == nil {
		t.Fatal(err)
	}
	f := &reviewQueueFake{}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: f}, nil
	})
	return s, w, b, f
}
func TestLearningReviewPaidRecoveryWithoutReplay(t *testing.T) {
	s, w, b, f := seedReviewQueue(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_review_commit BEFORE INSERT ON learning_review_items BEGIN SELECT RAISE(FAIL,'questions unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	batch, _ := s.GetLearningReviewBatch(ctx, b.ID)
	if batch.Status != "failed" || f.calls != 1 {
		t.Fatal(batch, f.calls)
	}
	execution, _ := s.GetJobExecution(ctx, b.JobID)
	if execution.CheckpointJSON == "" {
		t.Fatal("known response lost")
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_review_commit`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryLearningReview(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	batch, _ = s.GetLearningReviewBatch(ctx, b.ID)
	if batch.Status != "ready" || f.calls != 1 {
		t.Fatal("recovery re-called", f.calls, batch)
	}
	var receipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='weekly_review'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("recovery re-billed", err, receipts)
	}
	if err := s.RetryLearningReview(ctx, b.ID); err == nil {
		t.Fatal("ready replayed")
	}
}
func TestLearningReviewUnknownResultAndContracts(t *testing.T) {
	for _, kind := range []string{"unknown", "result-unknown", "bad-identity", "bad-input", "bad-version", "no-provider", "invented-material", "policy", "budget"} {
		t.Run(kind, func(t *testing.T) {
			s, w, b, f := seedReviewQueue(t)
			ctx := t.Context()
			switch kind {
			case "unknown":
				_ = s.MarkJobRemoteCallStarted(ctx, b.JobID)
			case "result-unknown":
				_ = s.SaveJobResult(ctx, b.JobID, "", models.JobResultUnknown)
			case "bad-identity":
				_, _ = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=json_set(input_snapshot_json,'$.batch_id','wrong') WHERE id=?`, b.JobID)
			case "bad-input":
				_, _ = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json='bad' WHERE id=?`, b.JobID)
			case "bad-version":
				_, _ = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET config_version='wrong' WHERE id=?`, b.JobID)
			case "no-provider":
				w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) { return &provider.ProviderBundle{}, nil })
			case "invented-material":
				f.bad = true
			case "policy":
				var req provider.KnowledgeArticleRequest
				_ = json.Unmarshal([]byte(b.InputJSON), &req)
				_ = s.SetSourceProductionPolicy(ctx, models.SourceEpisode, req.Materials[0].SourceID, "internal", models.ModelDataLocalOnly)
			case "budget":
				limit := int64(1)
				_ = s.SetOwnerMonthlyBudget(ctx, &limit)
				_ = s.SetModelPrice(ctx, models.ModelPrice{Provider: "pod", Model: "model", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 1000000})
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			fresh, _ := s.GetLearningReviewBatch(ctx, b.ID)
			if fresh.Status != "failed" {
				t.Fatal(kind, fresh)
			}
			expected := 0
			if kind == "invented-material" {
				expected = 1
			}
			if f.calls != expected {
				t.Fatal(kind, "unsafe model call", f.calls)
			}
		})
	}
}

func TestLearningReviewMissingIdentityStopsBeforeRemote(t *testing.T) {
	s, w, b, f := seedReviewQueue(t)
	ctx := t.Context()
	jobs, e := s.ListQueuedOrRunning(ctx)
	if e != nil {
		t.Fatal(e)
	}
	job := jobs[0]
	bundle := &provider.ProviderBundle{KnowledgeArticle: f}
	if _, e = s.DB.ExecContext(ctx, `DELETE FROM learning_review_batches WHERE id=?`, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = w.doWeeklyReview(ctx, job, bundle); e == nil || f.calls != 0 {
		t.Fatal("missing batch sent", e)
	}
	_ = s.DB.Close()
	if e = w.doWeeklyReview(ctx, job, bundle); e == nil {
		t.Fatal("DB failure sent")
	}
}
func TestKnowledgeV3WriteRechecksDirections(t *testing.T) {
	s, w, a, f, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, _ := s.ListQueuedOrRunning(ctx)
	job := jobs[0]
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var input store.KnowledgeStageInput
	_ = json.Unmarshal([]byte(ex.InputSnapshotJSON), &input)
	input.Stage = "write"
	input.Request.Stage = "write"
	input.Request.DiscoveryBatchID = "batch"
	input.Request.Topic = &provider.KnowledgeTopic{Title: "独立解释", Question: "怎样留下理解？", Thesis: "先表达再检查", MaterialIDs: []string{input.Request.Materials[0].ID, input.Request.Materials[1].ID}}
	raw, _ := json.Marshal(input)
	_, _ = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(raw), job.ID)
	_, _ = s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET stage='write',status='write',topic_json=? WHERE id=?`, string(mustJSON(t, input.Request.Topic)), a.ID)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatal("valid direction blocked")
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestKnowledgeV3DuplicateStopsBeforePaidWrite(t *testing.T) {
	s, w, a, f, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, _ := s.ListQueuedOrRunning(ctx)
	job := jobs[0]
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var input store.KnowledgeStageInput
	_ = json.Unmarshal([]byte(ex.InputSnapshotJSON), &input)
	input.Stage = "write"
	input.Request.Stage = "write"
	input.Request.DiscoveryBatchID = "batch"
	input.Request.Topic = &provider.KnowledgeTopic{Title: "独立解释", Question: "怎样留下理解？", Thesis: "先表达再检查", MaterialIDs: []string{input.Request.Materials[0].ID, input.Request.Materials[1].ID}}
	raw, _ := json.Marshal(input)
	topic := string(mustJSON(t, input.Request.Topic))
	_, _ = s.DB.ExecContext(ctx, `UPDATE knowledge_article_runs SET stage='write' WHERE job_id=?`, job.ID)
	_, _ = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(raw), job.ID)
	_, _ = s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET stage='write',status='write',topic_json=? WHERE id=?`, topic, a.ID)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO knowledge_articles(id,profile_id,input_hash,input_json,status,stage,topic_json,provider,model) VALUES('new-history',?,'new-history',?,'ready','review',?,'pod','model')`, a.ProfileID, a.InputJSON, topic); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("new duplicate direction called remote")
	}
	fresh, err := s.GetKnowledgeArticle(ctx, a.ID)
	if err != nil || fresh.Status != "failed" {
		t.Fatal(fresh, err)
	}
}
func TestGroundedArticleMissingIdentityAndDatabase(t *testing.T) {
	s, w, a, f, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, _ := s.ListQueuedOrRunning(ctx)
	job := jobs[0]
	bundle := &provider.ProviderBundle{KnowledgeArticle: f}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM knowledge_articles WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.doKnowledgeArticle(ctx, job, bundle); err == nil || f.calls != 0 {
		t.Fatal("missing article sent")
	}
	_ = s.DB.Close()
	if err := w.doKnowledgeArticle(ctx, job, bundle); err == nil {
		t.Fatal("unavailable DB accepted")
	}
}

func TestGroundedCheckpointPolicyFailureStillKeepsPaidUsage(t *testing.T) {
	s, w, b, f := seedReviewQueue(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_receipt BEFORE INSERT ON usage_records BEGIN SELECT RAISE(FAIL,'temporary receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	ex, err := s.GetJobExecution(ctx, b.JobID)
	if err != nil || ex.CheckpointJSON == "" || f.calls != 1 {
		t.Fatal(err, f.calls)
	}
	if _, err = s.DB.ExecContext(ctx, `DROP TRIGGER fail_receipt`); err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(b.InputJSON), &req)
	if err = s.SetSourceProductionPolicy(ctx, models.SourceEpisode, req.Materials[0].SourceID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListRecentCompleted(ctx, 100)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err, jobs)
	}
	if err = w.doWeeklyReview(ctx, jobs[0], &provider.ProviderBundle{KnowledgeArticle: f}); err == nil {
		t.Fatal("revoked policy accepted")
	}
	var receipts int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='weekly_review'`).Scan(&receipts); err != nil || receipts != 1 || f.calls != 1 {
		t.Fatal("known charge lost or replayed", err, receipts, f.calls)
	}
}

func TestKnowledgeSupersededStageDoesNotPayOrReplaceDraft(t *testing.T) {
	s, w, a, f, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE knowledge_articles SET stage='write',status='write' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.doKnowledgeArticle(ctx, jobs[0], &provider.ProviderBundle{KnowledgeArticle: f}); err != nil || f.calls != 0 {
		t.Fatal(err, f.calls)
	}
	ex, err := s.GetJobExecution(ctx, jobs[0].ID)
	if err != nil || ex.ResultJSON != `{"superseded":true}` || ex.RemoteCallStarted {
		t.Fatal(err, ex)
	}
}
