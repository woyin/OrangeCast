package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestKnowledgeEmbeddingFrozenContractRejectsCorruptRecovery(t *testing.T) {
	for _, mode := range []string{"input", "checkpoint identity", "checkpoint vector", "missing independent connection", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			s, w, job, cfg, calls := embeddingJobFixture(t, nil)
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "input":
				_, err = s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='{}' WHERE id=?`, job.ID)
			case "checkpoint identity":
				_, err = s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{}' WHERE id=?`, job.ID)
			case "checkpoint vector":
				var in store.KnowledgeEmbeddingJobInput
				if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
					t.Fatal(err)
				}
				vectors := make([][]float32, len(in.Inputs()))
				for i := range vectors {
					vectors[i] = []float32{0, 0}
				}
				cp := embeddingCheckpoint{Version: ex.ConfigVersion, JobID: job.ID, InputFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON))), Result: &provider.EmbeddingResult{Model: cfg.Model, Dimensions: 2, Vectors: vectors}}
				raw, _ := json.Marshal(cp)
				_, err = s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json=? WHERE id=?`, string(raw), job.ID)
			case "missing independent connection":
				w.selector.WithEmbedding("", "", "", 0)
			case "stopped":
				err = s.ChangeRunControl(t.Context(), "job", job.ID, "stop", "停止索引", uuid.NewString(), embeddingControlRevision(t, s, job.ID), 0)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = w.doKnowledgeEmbedding(t.Context(), job)
			if err == nil || calls.Load() != 0 {
				t.Fatalf("unsafe recovery sent or adopted: err=%v calls=%d", err, calls.Load())
			}
			status, e := s.KnowledgeEmbeddingStatus(t.Context(), cfg.ID)
			if e != nil || status.IndexedWindows != 0 {
				t.Fatal(status, e)
			}
			usage, e := s.ListRunUsage(t.Context(), job.ID)
			if e != nil || len(usage) != 0 {
				t.Fatal("rejected recovery manufactured receipt", usage, e)
			}
		})
	}
}

func TestKnowledgeEmbeddingSchedulerAndMissingExecutionErrors(t *testing.T) {
	_, w, _, _, calls := embeddingJobFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.ScheduleKnowledgeEmbeddings(ctx); err == nil || calls.Load() != 0 {
		t.Fatal("cancelled scheduler lost error", err)
	}
	if err := w.doKnowledgeEmbedding(t.Context(), &models.ProcessingJob{ID: "missing"}); err == nil {
		t.Fatal("missing execution accepted")
	}
	w.selector.WithEmbedding("", "", "", 0)
	if err := w.ScheduleKnowledgeEmbeddings(t.Context()); err != nil || calls.Load() != 0 {
		t.Fatal("unconfigured scheduler sent", err)
	}
}

func TestKnowledgeEmbeddingPaidReceiptFailureExplainsNoRetry(t *testing.T) {
	s, w, job, _, calls := embeddingJobFixture(t, nil)
	if _, err := s.DB.Exec(`CREATE TRIGGER deny_embedding_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs WHEN new.checkpoint_json != '' BEGIN SELECT RAISE(ABORT,'checkpoint unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER deny_embedding_receipt BEFORE INSERT ON usage_records WHEN new.operation='embedding_content' BEGIN SELECT RAISE(ABORT,'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetJob(t.Context(), job.ID)
	if err != nil || failed.Status != models.StatusFailed || failed.LastError == nil || !strings.Contains(*failed.LastError, "不能自动重发") || calls.Load() != 1 {
		t.Fatal(failed, err)
	}
	if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 1 {
		t.Fatal("failed persistence rebilled", err)
	}
}

func TestKnowledgeEmbeddingAdmissionPersistenceFailuresDoNotCall(t *testing.T) {
	for _, stage := range []string{"call marker", "unknown state", "invalid response receipt"} {
		t.Run(stage, func(t *testing.T) {
			var hook func(http.ResponseWriter, *http.Request, int)
			if stage == "invalid response receipt" {
				hook = func(resp http.ResponseWriter, req *http.Request, _ int) {
					fmt.Fprint(resp, `{"model":"vector","data":[],"usage":{"prompt_tokens":31,"total_tokens":31}}`)
				}
			}
			s, w, job, _, calls := embeddingJobFixture(t, hook)
			statement := `CREATE TRIGGER deny_embedding_admission BEFORE UPDATE OF remote_call_started ON processing_jobs WHEN new.remote_call_started=1 BEGIN SELECT RAISE(ABORT,'marker unavailable'); END`
			if stage == "unknown state" {
				if _, err := s.DB.Exec(`UPDATE processing_jobs SET remote_call_started=1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				statement = `CREATE TRIGGER deny_embedding_admission BEFORE UPDATE OF result_state ON processing_jobs BEGIN SELECT RAISE(ABORT,'unknown unavailable'); END`
			} else if stage == "invalid response receipt" {
				statement = `CREATE TRIGGER deny_embedding_admission BEFORE INSERT ON usage_records WHEN new.operation='embedding_content' BEGIN SELECT RAISE(ABORT,'receipt unavailable'); END`
			}
			if _, err := s.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			failed, err := s.GetJob(t.Context(), job.ID)
			if err != nil || failed.Status != models.StatusFailed || failed.LastError == nil {
				t.Fatal(failed, err)
			}
			want := int32(0)
			if stage == "invalid response receipt" {
				want = 1
				if !strings.Contains(*failed.LastError, "不能自动重发") {
					t.Fatal(*failed.LastError)
				}
			}
			if calls.Load() != want {
				t.Fatalf("unexpected paid calls: %d", calls.Load())
			}
		})
	}
}
