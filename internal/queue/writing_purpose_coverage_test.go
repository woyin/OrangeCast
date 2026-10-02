package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type writingPurposeRevisionFake struct{ base writingPurposeQueueFake }

func (f *writingPurposeRevisionFake) Name() string { return "pod" }
func (f *writingPurposeRevisionFake) KnowledgeArticleStep(ctx context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	if req.Stage == "review" {
		f.base.requests = append(f.base.requests, req)
		no := false
		return &provider.KnowledgeArticleResult{Passed: &no, Issues: []string{"说明材料不足的边界需要更清楚"}}, provider.TaskUsage{InputUnits: 100, OutputUnits: 50}, nil
	}
	if req.Stage == "revise" {
		copyReq := req
		copyReq.Stage = "write"
		result, usage, err := f.base.KnowledgeArticleStep(ctx, copyReq)
		f.base.requests[len(f.base.requests)-1] = req
		return result, usage, err
	}
	return f.base.KnowledgeArticleStep(ctx, req)
}

func TestWritingPurposeRevisionAndFinalReviewKeepExactFrozenRules(t *testing.T) {
	for _, mode := range provider.WritingModes() {
		t.Run(mode.ID, func(t *testing.T) {
			s, w, a, _ := seedWritingPurposeQueue(t, mode.ID, provider.KnowledgeArticlePurposePromptVersion, true)
			f := &writingPurposeRevisionFake{}
			w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{KnowledgeArticle: f}, nil
			})
			for i := 0; i < 6; i++ {
				if err := w.ProcessOne(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := s.GetKnowledgeArticle(t.Context(), a.ID)
			if err != nil || fresh.Status != "ready" || len(f.base.requests) != 6 {
				t.Fatalf("revision journey %+v calls=%d err=%v", fresh, len(f.base.requests), err)
			}
			purpose, _ := provider.FreezeWritingPurpose(mode.ID)
			for i, stage := range []string{"discover", "select", "write", "review", "revise", "review_final"} {
				req := f.base.requests[i]
				if req.Stage != stage || req.WritingPurpose == nil || *req.WritingPurpose != purpose {
					t.Fatal("purpose changed at", stage)
				}
				if req.Estimate == nil {
					t.Fatal("stage admitted without estimate", stage)
				}
			}
			var receipts int
			if err = s.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM usage_records WHERE operation LIKE 'knowledge_article_%'`).Scan(&receipts); err != nil || receipts != 6 {
				t.Fatal("missing stage usage", receipts, err)
			}
		})
	}
}

func TestWritingPurposeMalformedSnapshotAndMissingReceiptStopLocally(t *testing.T) {
	for _, kind := range []string{"snapshot", "missing-purpose", "wrong-checkpoint-provider", "receipt-unavailable"} {
		t.Run(kind, func(t *testing.T) {
			s, w, a, f := seedWritingPurposeQueue(t, "practice", provider.KnowledgeArticlePurposePromptVersion, false)
			jobs, err := s.ListQueuedOrRunning(t.Context())
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			job := jobs[0]
			exec, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "snapshot":
				_, err = s.DB.ExecContext(t.Context(), `UPDATE processing_jobs SET input_snapshot_json='{' WHERE id=?`, job.ID)
			case "missing-purpose":
				var input store.KnowledgeStageInput
				if err = json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
					t.Fatal(err)
				}
				input.Request.WritingPurpose = nil
				raw, _ := json.Marshal(input)
				_, err = s.DB.ExecContext(t.Context(), `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(raw), job.ID)
			case "wrong-checkpoint-provider":
				_, err = s.DB.ExecContext(t.Context(), `UPDATE processing_jobs SET checkpoint_json=? WHERE id=?`, `{"provider":"other","result":{"topics":[]}}`, job.ID)
			case "receipt-unavailable":
				_, err = s.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_purpose_receipt BEFORE INSERT ON usage_records BEGIN SELECT RAISE(FAIL,'receipt unavailable'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.GetKnowledgeArticle(t.Context(), a.ID)
			if err != nil || fresh.Status != "failed" {
				t.Fatal(fresh, err)
			}
			wantCalls := 0
			if kind == "receipt-unavailable" {
				wantCalls = 1
			}
			if len(f.requests) != wantCalls {
				t.Fatal("unsafe remote call", len(f.requests), kind)
			}
			if kind != "receipt-unavailable" {
				return
			}
			exec, err = s.GetJobExecution(t.Context(), job.ID)
			if err != nil || exec.CheckpointJSON == "" {
				t.Fatal("lost billable checkpoint", err)
			}
			if _, err = s.DB.ExecContext(t.Context(), `DROP TRIGGER fail_purpose_receipt`); err != nil {
				t.Fatal(err)
			}
			if err = s.RetryKnowledgeArticle(t.Context(), a.ID); err != nil {
				t.Fatal(err)
			}
			if err = w.ProcessOne(t.Context()); err != nil || len(f.requests) != 1 {
				t.Fatal("receipt recovery recalled", err)
			}
			var receipts int
			if err = s.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&receipts); err != nil || receipts != 1 {
				t.Fatal(fmt.Sprint("receipt recovery", receipts, err))
			}
		})
	}
}
