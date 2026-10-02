package queue

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func legacyStudyFixture(t *testing.T, hook func(http.ResponseWriter, *http.Request, int32)) (*store.Store, *Worker, *store.LegacyStudyTurn, *models.ProcessingJob, *atomic.Int32) {
	t.Helper()
	s, w := newTestWorker(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if hook != nil {
			hook(resp, r, n)
			return
		}
		legacyReply(resp, r, n, true)
	}))
	t.Cleanup(server.Close)
	w.selector.ApplySettings("key", server.URL, "", "")
	client, err := w.selector.LegacyStudy(provider.TaskConfig{Provider: "groq", Model: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	id := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, id)
	turn, job, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), client.Config())
	if err != nil {
		t.Fatal(err)
	}
	return s, w, turn, job, calls
}
func legacyReply(resp http.ResponseWriter, r *http.Request, n int32, related bool) {
	var request struct {
		Model string `json:"model"`
	}
	json.NewDecoder(r.Body).Decode(&request)
	content := `{"answer":"通胀说明物价条件","referenceSegmentIds":["seg-0001"]}`
	if n > 1 {
		content = fmt.Sprintf(`{"related":%t,"reason":"本集参考内容"}`, related)
	}
	raw, _ := json.Marshal(map[string]any{"model": request.Model, "choices": []any{map[string]any{"message": map[string]any{"content": content}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
	resp.Write(raw)
}
func TestLegacyStudyTwoStagesPublishOnlyAfterRelatedCheck(t *testing.T) {
	s, w, turn, job, calls := legacyStudyFixture(t, nil)
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, false)
	if len(messages) != 1 {
		t.Fatal("unchecked visible", messages)
	}
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "response_saved" || current.CheckJobID == "" {
		t.Fatal(current)
	}
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "accepted" || current.Answer == nil || calls.Load() != 2 {
		t.Fatal(current, calls.Load())
	}
	messages, _ = s.ListStudyMessages(t.Context(), turn.SessionID, false)
	if len(messages) != 2 {
		t.Fatal(messages)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation LIKE 'legacy_study_%' AND units_known=1 AND input_units=31 AND output_units=12`).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
	s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, job.ID)
	w.ProcessOne(t.Context())
	if calls.Load() != 2 {
		t.Fatal("paid response replay", calls.Load())
	}
}
func TestLegacyStudyRejectedCheckSuppressesBody(t *testing.T) {
	s, w, turn, _, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) { legacyReply(resp, r, n, false) })
	w.ProcessOne(t.Context())
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "insufficient" || current.Answer != nil || calls.Load() != 2 {
		t.Fatal(current)
	}
	messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, false)
	if len(messages) != 1 {
		t.Fatal(messages)
	}
	all, _ := s.ListStudyMessages(t.Context(), turn.SessionID, true)
	if len(all) != 2 || !all[1].Suppressed {
		t.Fatal(all)
	}
}
func TestLegacyStudyKnownReceiptFailureRecoversWithoutConnection(t *testing.T) {
	s, w, turn, job, calls := legacyStudyFixture(t, nil)
	if _, err := s.DB.Exec(`CREATE TRIGGER legacy_receipt_fault BEFORE INSERT ON usage_records WHEN new.operation='legacy_study_generate' BEGIN SELECT RAISE(FAIL,'receipt fault');END`); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(t.Context())
	failed, _ := s.GetJob(t.Context(), job.ID)
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	if failed.Status != models.StatusFailed || ex.CheckpointJSON == "" || calls.Load() != 1 {
		t.Fatal(failed, ex, calls.Load())
	}
	s.DB.Exec(`DROP TRIGGER legacy_receipt_fault`)
	var revision int
	s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
	retry, _, err := s.RetryLegacyStudy(t.Context(), job.ID, uuid.NewString(), revision, false)
	if err != nil {
		t.Fatal(err)
	}
	w.selector.ApplySettings("key", "http://127.0.0.1:1", "", "")
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "response_saved" || calls.Load() != 1 {
		t.Fatal(current, calls.Load(), retry)
	}
	var count int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE receipt_id=?`, job.ID+":legacy_study_generate").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
func TestLegacyStudyUnknownRequiresExplicitNewPaidAttempt(t *testing.T) {
	s, w, _, job, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) { resp.Write([]byte(`{`)) })
	w.ProcessOne(t.Context())
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	if !ex.RemoteCallStarted || ex.CheckpointJSON != "" || calls.Load() != 1 {
		t.Fatal(ex, calls.Load())
	}
	var revision int
	s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
	if _, _, err := s.RetryLegacyStudy(t.Context(), job.ID, uuid.NewString(), revision, false); err == nil {
		t.Fatal("unknown automatically replayed")
	}
	retry, created, err := s.RetryLegacyStudy(t.Context(), job.ID, uuid.NewString(), revision, true)
	if err != nil || !created {
		t.Fatal(retry, created, err)
	}
}

func TestLegacyStudyStopDuringPaidResponseRetainsUsageWithoutBody(t *testing.T) {
	var s *store.Store
	var job *models.ProcessingJob
	fixture, w, turn, queued, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) {
		if _, err := s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, job.ID); err != nil {
			t.Error(err)
		}
		legacyReply(resp, r, n, true)
	})
	s = fixture
	job = queued
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	if current.State != "blocked" || current.Answer != nil || ex.CheckpointJSON != "" || calls.Load() != 1 {
		t.Fatal(current, ex, calls.Load())
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='legacy_study_generate' AND units_known=1 AND input_units=31`).Scan(&n)
	if n != 1 {
		t.Fatal("lost stopped response usage", n)
	}
}
func TestLegacyStudyPurgeDuringPaidResponseRetainsUsageWithoutBody(t *testing.T) {
	var s *store.Store
	var session string
	fixture, w, turn, job, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) {
		if err := s.DeleteStudySession(t.Context(), session); err != nil {
			t.Error(err)
		}
		legacyReply(resp, r, n, true)
	})
	s = fixture
	session = turn.SessionID
	w.ProcessOne(t.Context())
	if _, err := s.GetLegacyStudyTurn(t.Context(), turn.ID); err == nil {
		t.Fatal("purged turn reattached")
	}
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	if ex.InputSnapshotJSON != "{}" || ex.CheckpointJSON != "" || calls.Load() != 1 {
		t.Fatal(ex, calls.Load())
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='legacy_study_generate' AND units_known=1`).Scan(&n)
	if n != 1 {
		t.Fatal("lost purged response usage", n)
	}
}
func TestLegacyStudyBudgetMissingPriceStopsBeforePaidBoundary(t *testing.T) {
	s, w, turn, job, calls := legacyStudyFixture(t, nil)
	budget := int64(100)
	if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(t.Context())
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if calls.Load() != 0 || ex.RemoteCallStarted || current.State != "blocked" {
		t.Fatal(current, ex, calls.Load())
	}
}

func TestLegacyStudyNoReferenceReturnsScopeFeedbackWithoutCheckCall(t *testing.T) {
	s, w, turn, _, calls := legacyStudyFixture(t, func(resp http.ResponseWriter, r *http.Request, n int32) {
		raw, _ := json.Marshal(map[string]any{"model": "qa", "choices": []any{map[string]any{"message": map[string]any{"content": `{"answer":"","referenceSegmentIds":[]}`}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3}})
		resp.Write(raw)
	})
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "insufficient" || current.Feedback == "" || current.CheckJobID != "" || current.Answer != nil || calls.Load() != 1 {
		t.Fatal(current, calls.Load())
	}
}
func TestLegacyStudyAdoptionTransactionFailureKeepsKnownCheckForZeroCallRetry(t *testing.T) {
	s, w, turn, _, calls := legacyStudyFixture(t, nil)
	w.ProcessOne(t.Context())
	current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if _, err := s.DB.Exec(`CREATE TRIGGER legacy_adopt_fault BEFORE INSERT ON study_messages WHEN new.role='assistant' BEGIN SELECT RAISE(FAIL,'adopt fault');END`); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(t.Context())
	current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "blocked" || current.Answer != nil || calls.Load() != 2 {
		t.Fatal(current, calls.Load())
	}
	messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, false)
	if len(messages) != 1 {
		t.Fatal(messages)
	}
	s.DB.Exec(`DROP TRIGGER legacy_adopt_fault`)
	var revision int
	s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, current.CheckJobID).Scan(&revision)
	_, _, err := s.RetryLegacyStudy(t.Context(), current.CheckJobID, uuid.NewString(), revision, false)
	if err != nil {
		t.Fatal(err)
	}
	w.selector.ApplySettings("key", "http://127.0.0.1:1", "", "")
	w.ProcessOne(t.Context())
	current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "accepted" || current.Answer == nil || calls.Load() != 2 {
		t.Fatal(current, calls.Load())
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation LIKE 'legacy_study_%'`).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
}
