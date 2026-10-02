package queue

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyStudyWorkerRejectsCorruptBoundaryWithoutSupplierCall(t *testing.T) {
	for _, fault := range []string{"input", "estimate", "checkpoint", "remote-unknown", "connection"} {
		t.Run(fault, func(t *testing.T) {
			s, w, turn, job, calls := legacyStudyFixture(t, nil)
			ex, _ := s.GetJobExecution(t.Context(), job.ID)
			switch fault {
			case "input":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='{}' WHERE id=?`, job.ID)
			case "estimate":
				var in store.LegacyStudyJobInput
				json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
				in.Estimate.InputFingerprint = "wrong"
				raw, _ := json.Marshal(in)
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(raw), job.ID)
			case "checkpoint":
				s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{}' WHERE id=?`, job.ID)
			case "remote-unknown":
				s.DB.Exec(`UPDATE processing_jobs SET remote_call_started=1 WHERE id=?`, job.ID)
			case "connection":
				w.selector.ApplySettings("key", "http://127.0.0.1:1", "", "")
			}
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			failed, _ := s.GetJob(t.Context(), job.ID)
			if calls.Load() != 0 || failed.Status != models.StatusFailed || current.Answer != nil {
				t.Fatal(current, failed, calls.Load())
			}
			if fault == "remote-unknown" {
				ex, _ = s.GetJobExecution(t.Context(), job.ID)
				if ex.ResultState != models.JobResultUnknown || current.State != "unknown" {
					t.Fatal(ex, current)
				}
			}
		})
	}
}
func TestLegacyStudyWorkerPreservesUsageForUnusablePaidResponses(t *testing.T) {
	for _, fault := range []string{"model", "content", "review"} {
		t.Run(fault, func(t *testing.T) {
			s, w, turn, job, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) {
				if fault == "review" && n == 1 {
					legacyReply(resp, r, n, true)
					return
				}
				model := "qa"
				content := `{"answer":"a","referenceSegmentIds":["seg-0001"]}`
				if fault == "model" {
					model = "different-model"
				} else if fault == "content" {
					content = "plain text"
				} else {
					content = `{"related":"invalid","reason":"x"}`
				}
				json.NewEncoder(resp).Encode(map[string]any{"model": model, "choices": []any{map[string]any{"message": map[string]any{"content": content}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
			})
			w.ProcessOne(t.Context())
			if fault == "review" {
				w.ProcessOne(t.Context())
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if current.State != "blocked" || current.Answer != nil {
				t.Fatal(current)
			}
			var count int
			s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation LIKE 'legacy_study_%' AND units_known=1`).Scan(&count)
			expected := 1
			if fault == "review" {
				expected = 2
			}
			if count != expected || int(calls.Load()) != expected {
				t.Fatal(count, calls.Load())
			}
			if fault == "model" {
				var model string
				var unpriced bool
				if err := s.DB.QueryRow(`SELECT model,estimated_cost IS NULL FROM usage_records WHERE receipt_id=?`, job.ID+":legacy_study_generate").Scan(&model, &unpriced); err != nil || model != "unknown" || !unpriced {
					t.Fatal(model, unpriced, err)
				}
			}
		})
	}
}
func TestLegacyStudyWorkerCheckpointAndReceiptFailureCannotResendUnknown(t *testing.T) {
	s, w, turn, job, calls := legacyStudyFixture(t, nil)
	if _, err := s.DB.Exec(`CREATE TRIGGER legacy_checkpoint_fault BEFORE UPDATE OF checkpoint_json ON processing_jobs WHEN new.id='` + job.ID + `' BEGIN SELECT RAISE(FAIL,'checkpoint unavailable');END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER legacy_usage_fault BEFORE INSERT ON usage_records WHEN new.operation='legacy_study_generate' BEGIN SELECT RAISE(FAIL,'receipt unavailable');END`); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	failed, _ := s.GetJob(t.Context(), job.ID)
	if current.State != "unknown" || failed.Status != models.StatusFailed || calls.Load() != 1 {
		t.Fatal(current, failed, calls.Load())
	}
	if failed.LastError == nil || !strings.Contains(*failed.LastError, "实际用量未持久化") {
		t.Fatal(failed.LastError)
	}
	s.DB.Exec(`DROP TRIGGER legacy_checkpoint_fault; DROP TRIGGER legacy_usage_fault; UPDATE processing_jobs SET status='queued' WHERE id='` + job.ID + `'`)
	w.ProcessOne(t.Context())
	if calls.Load() != 1 {
		t.Fatal("unknown response automatically resent", calls.Load())
	}
}
func TestLegacyStudyWorkerMissingConnectionExecutionAndUnknownResultWrite(t *testing.T) {
	for _, fault := range []string{"execution", "connection", "unknown-state-write", "answer-parse"} {
		t.Run(fault, func(t *testing.T) {
			s, w, turn, job, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) {
				json.NewEncoder(resp).Encode(map[string]any{"model": "qa", "choices": []any{map[string]any{"message": map[string]any{"content": `{}`}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
			})
			switch fault {
			case "execution":
				missing := *job
				missing.ID = "missing"
				if err := w.doLegacyStudy(t.Context(), &missing); err == nil {
					t.Fatal("missing execution admitted")
				}
				return
			case "connection":
				w.selector = provider.NewSelector("", "")
			case "unknown-state-write":
				s.DB.Exec(`UPDATE processing_jobs SET remote_call_started=1 WHERE id=?`, job.ID)
				if _, err := s.DB.Exec(`CREATE TRIGGER legacy_unknown_fault BEFORE UPDATE OF result_json ON processing_jobs WHEN new.id='` + job.ID + `' BEGIN SELECT RAISE(FAIL,'state unavailable');END`); err != nil {
					t.Fatal(err)
				}
			}
			w.ProcessOne(t.Context())
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			failed, _ := s.GetJob(t.Context(), job.ID)
			expected := int32(0)
			if fault == "answer-parse" {
				expected = 1
			}
			if current.Answer != nil || failed.Status != models.StatusFailed || calls.Load() != expected {
				t.Fatal(current, failed, calls.Load())
			}
		})
	}
}
