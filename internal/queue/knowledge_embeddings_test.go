package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func embeddingJobFixture(t *testing.T, hook func(http.ResponseWriter, *http.Request, int)) (*store.Store, *Worker, *models.ProcessingJob, provider.EmbeddingConfig, *atomic.Int32) {
	t.Helper()
	s, w := newTestWorker(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		n := int(calls.Add(1))
		if hook != nil {
			hook(resp, req, n)
			return
		}
		embeddingJobReply(t, resp, req, true)
	}))
	t.Cleanup(server.Close)
	w.selector.WithEmbedding("secret-sentinel", server.URL+"/v1", "vector", 2)
	p, err := w.selector.Embedding()
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(t.Context(), "学习原文", "主动回忆保留语境。\n\n条件与反例不可遗漏。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeKnowledgeEmbeddingScope(t.Context(), cfg.ID, 1, true, []store.EmbeddingSource{{SourceType: "document", SourceID: doc.ID}}); err != nil {
		t.Fatal(err)
	}
	job, created, err := s.ReserveKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	return s, w, job, cfg, calls
}
func embeddingJobReply(t *testing.T, w http.ResponseWriter, r *http.Request, usage bool) {
	t.Helper()
	var request struct {
		Input []string `json:"input"`
		Model string   `json:"model"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "vector" || len(request.Input) < 1 || len(request.Input) > 16 {
		t.Error("invalid actual batch")
	}
	data := make([]map[string]any, len(request.Input))
	for i := range data {
		data[i] = map[string]any{"index": i, "embedding": []float64{1, 0}}
	}
	result := map[string]any{"model": "vector", "data": data}
	if usage {
		result["usage"] = map[string]int{"prompt_tokens": 31, "total_tokens": 31}
	}
	json.NewEncoder(w).Encode(result)
}
func embeddingControlRevision(t *testing.T, s *store.Store, id string) int {
	t.Helper()
	var revision int
	if err := s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}
func assertEmbeddingReceipt(t *testing.T, s *store.Store, id string, knownUnits bool) {
	t.Helper()
	usage, err := s.ListRunUsage(t.Context(), id)
	if err != nil || len(usage) != 1 || usage[0].UnitKind != "input_tokens" || usage[0].UnitsKnown != knownUnits || usage[0].OutputUnits != 0 {
		t.Fatalf("%+v %v", usage, err)
	}
}

func TestKnowledgeEmbeddingJobFreezesResponseAndCost(t *testing.T) {
	s, w, job, cfg, calls := embeddingJobFixture(t, nil)
	ctx := t.Context()
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("embedding resolved text bundle")
		return nil, nil
	}
	// The admitted job remains unpriced even if a price is subsequently registered.
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.GetJob(ctx, job.ID)
	if completed.Status != models.StatusSucceeded || calls.Load() != 1 {
		t.Fatal(completed, calls.Load())
	}
	assertEmbeddingReceipt(t, s, job.ID, true)
	usage, _ := s.ListRunUsage(ctx, job.ID)
	if usage[0].CostKnown {
		t.Fatal("current price substituted for frozen unknown price")
	}
	status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || status.IndexedWindows != 2 {
		t.Fatal(status, err)
	}
	if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 1 {
		t.Fatal("complete response replayed", err)
	}
	assertEmbeddingReceipt(t, s, job.ID, true)
}

func TestKnowledgeEmbeddingJobCheckpointRetriesWithoutNewCall(t *testing.T) {
	for _, stage := range []string{"receipt", "adoption"} {
		t.Run(stage, func(t *testing.T) {
			s, w, job, cfg, calls := embeddingJobFixture(t, nil)
			ctx := t.Context()
			table := "usage_records"
			when := "WHEN new.operation='embedding_content'"
			if stage == "adoption" {
				table = "knowledge_embedding_vectors"
				when = ""
			}
			if _, err := s.DB.Exec(fmt.Sprintf(`CREATE TRIGGER embedding_fault BEFORE INSERT ON %s %s BEGIN SELECT RAISE(ABORT,'injected durable fault');END`, table, when)); err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			failed, _ := s.GetJob(ctx, job.ID)
			ex, _ := s.GetJobExecution(ctx, job.ID)
			if failed.Status != models.StatusFailed || ex.CheckpointJSON == "" || calls.Load() != 1 {
				t.Fatal(failed, ex, calls.Load())
			}
			if _, err := s.DB.Exec(`DROP TRIGGER embedding_fault`); err != nil {
				t.Fatal(err)
			}
			key := uuid.NewString()
			revision := embeddingControlRevision(t, s, job.ID)
			recovery, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false)
			if err != nil || !created {
				t.Fatal(recovery, err)
			}
			replay, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false)
			if err != nil || created || replay.ID != recovery.ID {
				t.Fatal("retry lost acknowledgement not idempotent", err)
			}
			w.selector.WithEmbedding("", "", "", 0)
			if err = w.ProcessOne(ctx); err != nil || calls.Load() != 1 {
				t.Fatal("known response used removed endpoint", err)
			}
			completed, _ := s.GetJob(ctx, recovery.ID)
			if completed.Status != models.StatusSucceeded {
				t.Fatal(completed)
			}
			status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
			if err != nil || status.IndexedWindows != 2 {
				t.Fatal(status, err)
			}
			assertEmbeddingReceipt(t, s, job.ID, true)
			usage, _ := s.ListRunUsage(ctx, recovery.ID)
			if len(usage) != 0 {
				t.Fatal("recovery billed again")
			}
		})
	}
}

func TestKnowledgeEmbeddingJobUnknownNeverAutomaticallyCallsAgain(t *testing.T) {
	s, w, job, _, calls := embeddingJobFixture(t, func(resp http.ResponseWriter, req *http.Request, n int) {
		if n == 1 {
			connection, _, err := resp.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		embeddingJobReply(t, resp, req, true)
	})
	ctx := t.Context()
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if !ex.RemoteCallStarted || ex.CheckpointJSON != "" || calls.Load() != 1 {
		t.Fatal(ex, calls.Load())
	}
	if _, err := s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil || calls.Load() != 1 {
		t.Fatal("unknown automatically repeated", err)
	}
	ex, _ = s.GetJobExecution(ctx, job.ID)
	if ex.ResultState != models.JobResultUnknown {
		t.Fatal(ex)
	}
	revision := embeddingControlRevision(t, s, job.ID)
	if _, _, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), revision, false); !errors.Is(err, store.ErrConflict) {
		t.Fatal("unknown retry lacked explicit authorization", err)
	}
	retry, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), revision, true)
	if err != nil || !created {
		t.Fatal(retry, err)
	}
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 2 {
		t.Fatal("explicit unknown retry failed", err)
	}
	recovered, _ := s.GetJob(ctx, retry.ID)
	if recovered.Status != models.StatusSucceeded {
		t.Fatal(recovered)
	}
}

func TestKnowledgeEmbeddingJobStopAndWithdrawalKeepPaidFacts(t *testing.T) {
	for _, mode := range []string{"stop", "withdraw"} {
		t.Run(mode, func(t *testing.T) {
			var s *store.Store
			var job *models.ProcessingJob
			fixtureStore, w, fixtureJob, cfg, calls := embeddingJobFixture(t, func(resp http.ResponseWriter, req *http.Request, _ int) {
				if mode == "stop" {
					revision := embeddingControlRevision(t, s, job.ID)
					if err := s.ChangeRunControl(t.Context(), "job", job.ID, "stop", "停止这次索引", uuid.NewString(), revision, 0); err != nil {
						t.Error(err)
					}
				} else {
					var sourceID string
					if err := s.DB.QueryRow(`SELECT source_id FROM knowledge_embedding_sources LIMIT 1`).Scan(&sourceID); err != nil {
						t.Error(err)
					}
					if err := s.SetSourceProductionPolicy(t.Context(), models.SourceDocument, sourceID, "internal", models.ModelDataLocalOnly); err != nil {
						t.Error(err)
					}
				}
				embeddingJobReply(t, resp, req, true)
			})
			s = fixtureStore
			job = fixtureJob
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			ex, _ := s.GetJobExecution(t.Context(), job.ID)
			if ex.CheckpointJSON == "" || calls.Load() != 1 {
				t.Fatal(ex)
			}
			assertEmbeddingReceipt(t, s, job.ID, true)
			status, err := s.KnowledgeEmbeddingStatus(t.Context(), cfg.ID)
			if err != nil || status.IndexedWindows != 0 {
				t.Fatal("stopped/withdrawn response applied", status, err)
			}
		})
	}
}

func TestKnowledgeEmbeddingJobPriceBudgetAndScheduler(t *testing.T) {
	s, w, job, cfg, calls := embeddingJobFixture(t, nil)
	ctx := t.Context()
	if err := w.ScheduleKnowledgeEmbeddings(ctx); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs); err != nil || jobs != 1 || calls.Load() != 0 {
		t.Fatal("scheduler duplicate or external call", jobs, err)
	}
	budget := int64(100)
	if err := s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil || calls.Load() != 0 {
		t.Fatal("unpriced budget sent", err)
	}
	failed, _ := s.GetJob(ctx, job.ID)
	if failed.Status != models.StatusFailed || !strings.Contains(*failed.LastError, "price") {
		t.Fatal(failed)
	}
	if err := w.ScheduleKnowledgeEmbeddings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("failed automatically regenerated")
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000}); err != nil {
		t.Fatal(err)
	}
	recovery, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), embeddingControlRevision(t, s, job.ID), false)
	if err != nil || !created {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 1 {
		t.Fatal(err)
	}
	receipt, _ := s.ListRunUsage(ctx, recovery.ID)
	if len(receipt) != 1 || !receipt[0].CostKnown || receipt[0].CostCents != 1 {
		t.Fatal(receipt)
	}
	hold, err := s.GetJobBudgetReservation(ctx, recovery.ID)
	if err != nil || hold.Status != models.BudgetSettled || hold.ActualCostCents == nil || *hold.ActualCostCents != 1 {
		t.Fatal(hold, err)
	}
}

func TestKnowledgeEmbeddingJobPreflightMeasuresWithoutEnabling(t *testing.T) {
	s, w := newTestWorker(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) { calls++; embeddingJobReply(t, resp, req, false) }))
	defer server.Close()
	w.selector.WithEmbedding("key", server.URL, "vector", 0)
	client, _ := w.selector.Embedding()
	job, created, err := s.ReserveKnowledgeEmbeddingPreflight(t.Context(), uuid.NewString(), client.Config())
	if err != nil || !created || calls != 0 {
		t.Fatal(err)
	}
	if err = w.ProcessOne(context.Background()); err != nil || calls != 1 {
		t.Fatal(err)
	}
	ex, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || ex.ResultState != models.JobResultComplete {
		t.Fatal(ex, err)
	}
	measured, _ := client.Config().WithDimensions(2)
	cfg, err := s.GetKnowledgeEmbeddingConfig(t.Context(), measured.ID)
	if err != nil || cfg.Enabled || cfg.Dimensions != 2 {
		t.Fatal(cfg, err)
	}
	assertEmbeddingReceipt(t, s, job.ID, false)
	usage, _ := s.ListRunUsage(t.Context(), job.ID)
	if usage[0].CostKnown {
		t.Fatal("missing supplier usage recorded free")
	}
}

func TestKnowledgeEmbeddingJobPurgeDuringResponseCannotRestoreFrozenText(t *testing.T) {
	var s *store.Store
	fixture, w, job, cfg, calls := embeddingJobFixture(t, func(resp http.ResponseWriter, req *http.Request, _ int) {
		var sourceID string
		if err := s.DB.QueryRow(`SELECT source_id FROM knowledge_embedding_sources LIMIT 1`).Scan(&sourceID); err != nil {
			t.Error(err)
		}
		if err := s.DeleteSourceRows(t.Context(), models.SourceDocument, sourceID); err != nil {
			t.Error(err)
		}
		embeddingJobReply(t, resp, req, true)
	})
	s = fixture
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	ex, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || ex.InputSnapshotJSON != "{}" || ex.CheckpointJSON != "" || ex.ResultState != models.JobResultComplete || calls.Load() != 1 {
		t.Fatal("purged response resurrected cache or input", ex, err)
	}
	assertEmbeddingReceipt(t, s, job.ID, true)
	status, err := s.KnowledgeEmbeddingStatus(t.Context(), cfg.ID)
	if err != nil || status.IndexedWindows != 0 || len(status.Sources) != 0 {
		t.Fatal(status, err)
	}
}

func TestKnowledgeEmbeddingJobChangedConnectionBlocksBeforeCall(t *testing.T) {
	s, w, job, _, calls := embeddingJobFixture(t, nil)
	w.selector.WithEmbedding("key", "https://different.example/v1", "vector", 2)
	if err := w.ProcessOne(t.Context()); err != nil || calls.Load() != 0 {
		t.Fatal("substituted frozen route", err)
	}
	ex, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || ex.RemoteCallStarted {
		t.Fatal(ex, err)
	}
}

func TestKnowledgeQueryEmbeddingExplicitTaskAndReadonlyReuse(t *testing.T) {
	s, w, _, cfg, calls := embeddingJobFixture(t, nil)
	ctx := t.Context()
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if _, err := s.KnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用"); !errors.Is(err, store.ErrNotFound) || calls.Load() != 1 {
		t.Fatal("read called embedding", err)
	}
	query, created, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, " 如何应用 ")
	if err != nil || !created || calls.Load() != 1 {
		t.Fatal(query, err)
	}
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 2 {
		t.Fatal(err)
	}
	vector, err := s.KnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用")
	if err != nil || vector[0] != 1 || calls.Load() != 2 {
		t.Fatal(vector, err)
	}
	repeat, created, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用")
	if err != nil || created || repeat.ID != query.ID {
		t.Fatal("query not reused", err)
	}
	assertEmbeddingReceipt(t, s, query.ID, true)
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 2 {
		t.Fatal("cached read became call", err)
	}
}

func TestKnowledgeQueryEmbeddingLateEpochKeepsUsageButNoCache(t *testing.T) {
	var s *store.Store
	var config provider.EmbeddingConfig
	fixture, w, _, cfg, calls := embeddingJobFixture(t, func(resp http.ResponseWriter, req *http.Request, n int) {
		if n == 2 {
			current, err := s.GetKnowledgeEmbeddingConfig(t.Context(), config.ID)
			if err != nil {
				t.Error(err)
			}
			if _, err = s.ChangeKnowledgeEmbeddingScope(t.Context(), config.ID, current.Revision, false, nil); err != nil {
				t.Error(err)
			}
		}
		embeddingJobReply(t, resp, req, true)
	})
	s = fixture
	config = cfg
	ctx := t.Context()
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	query, _, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil || calls.Load() != 2 {
		t.Fatal(err)
	}
	assertEmbeddingReceipt(t, s, query.ID, true)
	ex, err := s.GetJobExecution(ctx, query.ID)
	if err != nil || ex.CheckpointJSON == "" {
		t.Fatal(ex, err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM knowledge_query_embeddings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("late query applied to changed scope", err)
	}
}
