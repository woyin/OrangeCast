package queue

import (
	"context"
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"strings"
	"testing"
)

func TestKnowledgeMalformedFrozenConfigNeverCallsModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*provider.KnowledgeArticleRequest)
	}{
		{"missing-estimate", func(r *provider.KnowledgeArticleRequest) { r.Estimate = nil }},
		{"wrong-input", func(r *provider.KnowledgeArticleRequest) { r.Instructions = "changed after admission" }},
		{"wrong-model", func(r *provider.KnowledgeArticleRequest) {
			c := r.StageConfigs["discover"]
			c.Model = "other"
			r.StageConfigs["discover"] = c
		}},
		{"wrong-version", func(r *provider.KnowledgeArticleRequest) { r.PromptVersion = "knowledge-article-v3" }},
		{"bad-output-cap", func(r *provider.KnowledgeArticleRequest) {
			c := r.StageConfigs["discover"]
			c.MaxOutputTokens = 1
			r.StageConfigs["discover"] = c
		}},
		{"unknown-estimation", func(r *provider.KnowledgeArticleRequest) { r.Estimate.Method = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, article, f, _ := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, err := s.ListQueuedOrRunning(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ex, err := s.GetJobExecution(ctx, jobs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			var input store.KnowledgeStageInput
			if err := json.Unmarshal([]byte(ex.InputSnapshotJSON), &input); err != nil {
				t.Fatal(err)
			}
			tc.change(&input.Request)
			if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(mustJSON(t, input)), jobs[0].ID); err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.GetKnowledgeArticle(ctx, article.ID)
			if err != nil || fresh.Status != "failed" || f.calls != 0 {
				t.Fatal(fresh, f.calls, err)
			}
			var n int
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&n); err != nil || n != 0 {
				t.Fatal("invalid snapshot incurred usage", n, err)
			}
		})
	}
}

func TestKnowledgeExcessOutputPreservesPaidCheckpointWithoutSuccessorOrReplay(t *testing.T) {
	s, w, article, f, _ := seedKnowledgeQueue(t, func(s *store.Store) {
		if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "model", InputCentsPerMillion: 1_000_000, OutputCentsPerMillion: 1_000_000}); err != nil {
			t.Fatal(err)
		}
	})
	ctx := t.Context()
	f.outputUnits = 3000
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.GetKnowledgeArticle(ctx, article.ID)
	if err != nil || fresh.Status != "failed" || !strings.Contains(fresh.Reason, "计费输出") {
		t.Fatal(fresh, err)
	}
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var n, in, out, cost int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(input_units),SUM(output_units),SUM(estimated_cost) FROM usage_records`).Scan(&n, &in, &out, &cost); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || n != 1 || in != 100 || out != 3000 || cost != 3100 {
		t.Fatal("overrun lost receipt or replayed", f.calls, n, in, out, cost)
	}
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("overrun admitted next stage", jobs, err)
	}
}

func TestKnowledgeBudgetSnapshotFailuresRemainLocal(t *testing.T) {
	s, w, _, _, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json='broken' WHERE id=?`, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := w.holdJobBudget(ctx, jobs[0]); err == nil {
		t.Fatal("malformed snapshot accepted")
	}
	_ = s.Close()
	if err := w.holdJobBudget(ctx, jobs[0]); err == nil {
		t.Fatal("missing durable input accepted")
	}
}

func TestKnowledgePaidResponseWaitsForReceiptPersistenceWithoutResending(t *testing.T) {
	s, w, article, f, _ := seedKnowledgeQueue(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_paid_receipt BEFORE INSERT ON usage_records WHEN new.operation='knowledge_article_discover' BEGIN SELECT RAISE(FAIL,'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		fresh, err := s.GetKnowledgeArticle(ctx, article.ID)
		if err != nil || fresh.Status != "failed" || f.calls != 1 {
			t.Fatal(fresh, f.calls, err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_paid_receipt`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	var n, in, out int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(input_units),SUM(output_units) FROM usage_records`).Scan(&n, &in, &out); err != nil {
		t.Fatal(err)
	}
	if n != 1 || in != 100 || out != 50 || f.calls != 1 {
		t.Fatal("paid response resent or usage lost", n, in, out, f.calls)
	}
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal("successor not admitted after accounting recovery", jobs, err)
	}
}

func TestKnowledgeLegacyReceiptCannotInventPriceOnReadFailure(t *testing.T) {
	s, w, _, _, _ := seedKnowledgeQueue(t)
	jobs, err := s.ListQueuedOrRunning(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = w.recordKnowledgeUsage(ctx, jobs[0], "discover", knowledgeCheckpoint{Provider: "pod", Model: "legacy", Usage: provider.TaskUsage{InputUnits: 100, OutputUnits: 50}})
	if err == nil {
		t.Fatal("failed price lookup treated as free")
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
