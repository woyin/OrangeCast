package queue

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func TestKnowledgeRerankWorkerRecovery(t *testing.T) {
	for _, mode := range []string{"success", "invalid-response", "unknown", "checkpoint", "route", "malformed", "stopped", "revoked", "bad-checkpoint", "unavailable", "unpriced", "checkpoint-write", "receipt-write", "error-receipt-write", "unknown-write"} {
		t.Run(mode, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := t.Context()
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "invalid-response" || mode == "error-receipt-write" || mode == "unknown-write" {
					fmt.Fprint(resp, `{"model":"rank","results":[],"usage":{"prompt_tokens":5,"total_tokens":5}}`)
					return
				}
				fmt.Fprint(resp, `{"model":"rank","results":[{"index":0,"relevance_score":0.8}],"usage":{"prompt_tokens":5,"total_tokens":5}}`)
			}))
			defer remote.Close()
			w.selector.WithRerank("secret", remote.URL, "rank")
			p, _ := w.selector.Reranker()
			doc, err := s.CreatePastedDocument(ctx, "主动回忆", "主动回忆测试资料。")
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := s.ReserveKnowledgeRerank(ctx, store.KnowledgeRetrieveQuery{Search: store.KnowledgeSearchQuery{Text: "主动回忆"}}, p.Config())
			if err != nil {
				t.Fatal(err)
			}
			s.DB.Exec(`UPDATE processing_jobs SET status='running' WHERE id=?`, job.ID)
			switch mode {
			case "unknown":
				s.DB.Exec(`UPDATE processing_jobs SET remote_call_started=1 WHERE id=?`, job.ID)
			case "checkpoint":
				if err = w.processJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				s.DB.Exec(`UPDATE processing_jobs SET result_state='',result_json='' WHERE id=?`, job.ID)
				w.selector.WithRerank("", "", "")
			case "unavailable":
				w.selector.WithRerank("", "", "")
			case "unpriced":
				budget := int64(100)
				if err = s.SetOwnerMonthlyBudget(ctx, &budget); err != nil {
					t.Fatal(err)
				}
			case "checkpoint-write":
				_, err = s.DB.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs BEGIN SELECT RAISE(ABORT,'test persistence failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "receipt-write", "error-receipt-write":
				_, err = s.DB.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON usage_records BEGIN SELECT RAISE(ABORT,'test receipt failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "unknown-write":
				_, err = s.DB.Exec(`CREATE TRIGGER fail_unknown BEFORE UPDATE OF result_state ON processing_jobs WHEN NEW.result_state='unknown' BEGIN SELECT RAISE(ABORT,'test unknown persistence failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "route":
				w.selector.WithRerank("secret", remote.URL, "other")
			case "malformed":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='invalid' WHERE id=?`, job.ID)
			case "stopped":
				s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, job.ID)
			case "revoked":
				if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
					t.Fatal(err)
				}
			case "bad-checkpoint":
				s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{}' WHERE id=?`, job.ID)
			}
			err = w.processJob(ctx, job)
			if mode == "success" || mode == "checkpoint" {
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			} else if err == nil {
				t.Fatal("accepted", mode)
			}
			if mode != "success" && mode != "checkpoint" && mode != "invalid-response" && mode != "checkpoint-write" && mode != "receipt-write" && mode != "error-receipt-write" && mode != "unknown-write" && calls != 0 {
				t.Fatal("unauthorized replay", calls)
			}
			if mode == "unknown" || mode == "invalid-response" {
				ex, _ := s.GetJobExecution(ctx, job.ID)
				if ex.ResultState != models.JobResultUnknown {
					t.Fatal(ex)
				}
				if w.processJob(ctx, job) == nil || calls > 1 {
					t.Fatal("unknown replay")
				}
			}
			if mode == "success" {
				var snapshot string
				s.DB.QueryRow(`SELECT input_snapshot_json FROM processing_jobs WHERE id=?`, job.ID).Scan(&snapshot)
				var in store.KnowledgeRerankInput
				json.Unmarshal([]byte(snapshot), &in)
				if strings.Contains(snapshot, "测试资料") {
					t.Fatal("body persisted")
				}
				if in.Estimate.PriceKnown {
					t.Fatal("invented price")
				}
			}
		})
	}
}

func TestKnowledgeRerankMissingExecution(t *testing.T) {
	_, w := newTestWorker(t)
	if w.processJob(t.Context(), &models.ProcessingJob{ID: "missing", JobType: models.JobKnowledgeRerank}) == nil {
		t.Fatal("missing execution")
	}
}
