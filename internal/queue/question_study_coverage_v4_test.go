package queue

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestQuestionStudyCoverageV4ManualKeypointZeroVersionDispatchAndChanges(t *testing.T) {
	for _, change := range []string{"none", "content", "owner_dismissed"} {
		t.Run(change, func(t *testing.T) {
			s, w := newTestWorker(t)
			calls := &atomic.Int32{}
			remote := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				studyTwoStageReply(t, "accept", nil)(resp, req)
			}))
			defer remote.Close()
			w.selector.WithPod("fixture", remote.URL, "generate")
			client, err := w.selector.QuestionStudy("generate", "review")
			if err != nil {
				t.Fatal(err)
			}
			ep := seedEpisode(t, s)
			seedDigestTranscript(t, s, models.SourceEpisode, ep)
			keypoint, err := s.CreateManualKeyPoint(t.Context(), store.KeyPointRow{SourceType: models.SourceEpisode, SourceID: ep, SourceTitle: "手工重点", Content: "通胀应比较适用条件", CitationsJSON: `["seg-0001"]`, TimeStart: 0, TimeEnd: 10})
			if err != nil {
				t.Fatal(err)
			}
			if keypoint.CardVersion != 0 || keypoint.QualityStatus != models.KeyPointOwnerConfirmed {
				t.Fatal("manual fixture does not preserve actual version", keypoint)
			}
			q, err := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "解释手工重点"})
			if err != nil {
				t.Fatal(err)
			}
			q, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "keypoint", ObjectID: keypoint.ID}})
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			turn, job, _, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "解释这条条件", uuid.NewString(), nil, client.Config())
			if err != nil {
				t.Fatal("legal manual keypoint not admitted", err)
			}
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			var frozen store.QuestionStudyJobInput
			if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &frozen); err != nil {
				t.Fatal(err)
			}
			if len(frozen.Scope.Materials) != 1 || frozen.Scope.Materials[0].Kind != "keypoint" || frozen.Scope.Materials[0].Revision != 0 || len(frozen.Scope.Materials[0].Segments) != 1 || frozen.Scope.Materials[0].SnapshotID == "" {
				t.Fatal("manual revision/evidence fabricated", frozen.Scope)
			}
			if change == "content" {
				// There is no public manual-keypoint edit method. Mutate the authoritative
				// row and its search projection to simulate a stored content edit, without
				// changing frozen revision 0 or substituting an automatic-card fixture.
				if _, err = s.DB.Exec(`UPDATE keypoint_index SET content='编辑后的适用条件' WHERE id=?`, keypoint.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB.Exec(`UPDATE knowledge_search_docs SET body='编辑后的适用条件' WHERE key=?`, frozen.Scope.Materials[0].Key); err != nil {
					t.Fatal(err)
				}
			} else if change == "owner_dismissed" {
				if err = s.SetKeyPointProductionStatus(t.Context(), keypoint.ID, models.KeyPointDismissed); err != nil {
					t.Fatal(err)
				}
			}
			if err = w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			wantCalls := int32(0)
			wantHistory := 0
			if change == "none" {
				if err = w.ProcessOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				wantCalls = 2
				wantHistory = 1
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != wantHistory || calls.Load() != wantCalls {
				t.Fatal(change, history, err, calls.Load())
			}
		})
	}
}

func TestQuestionStudyCoverageV4InvalidFrozenIdentityCannotSpend(t *testing.T) {
	for _, change := range []string{"version", "stage", "estimate", "checkpoint", "review_answer"} {
		t.Run(change, func(t *testing.T) {
			s, w, turn, job, calls := studyGenerationFixture(t, nil)
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			var in store.QuestionStudyJobInput
			if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "version":
				in.Version = "other"
			case "stage":
				in.Stage = "other"
			case "estimate":
				in.Estimate.InputFingerprint = "changed"
			case "review_answer":
				in.Stage = "review"
				in.Answer = nil
			case "checkpoint":
				if _, err = s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{corrupt' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if change != "checkpoint" {
				raw, _ := json.Marshal(in)
				if _, err = s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json=?,configured_model=? WHERE id=?`, string(raw), in.Model(), job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = w.doQuestionStudy(t.Context(), job); err == nil {
				t.Fatal("invalid frozen identity accepted", change)
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 || calls.Load() != 0 {
				t.Fatal(history, err, calls.Load())
			}
		})
	}
}

func TestQuestionStudyCoverageV4CheckpointWriteFailureKeepsPaidBoundary(t *testing.T) {
	for _, receiptFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt_saved", true: "both_unavailable"}[receiptFails], func(t *testing.T) {
			s, w, turn, job, calls := studyGenerationFixture(t, nil)
			if _, err := s.DB.Exec(`CREATE TRIGGER study_checkpoint_fault BEFORE UPDATE OF checkpoint_json ON processing_jobs WHEN new.checkpoint_json!='' BEGIN SELECT RAISE(FAIL,'checkpoint unavailable');END`); err != nil {
				t.Fatal(err)
			}
			if receiptFails {
				if _, err := s.DB.Exec(`CREATE TRIGGER study_usage_fault BEFORE INSERT ON usage_records WHEN new.operation='question_study_generate' BEGIN SELECT RAISE(FAIL,'receipt unavailable');END`); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil || !ex.RemoteCallStarted || ex.CheckpointJSON != "" {
				t.Fatal(ex, err)
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 || calls.Load() != 1 {
				t.Fatal(history, err, calls.Load())
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			want := 1
			if receiptFails {
				want = 0
			}
			if err != nil || len(usage) != want {
				t.Fatal(usage, err)
			}
			if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 1 {
				t.Fatal("lost response resent implicitly", err, calls.Load())
			}
		})
	}
}

func TestQuestionStudyCoverageV4MissingConnectionAndUnknownNeverResend(t *testing.T) {
	for _, scenario := range []string{"connection", "unknown", "unknown_storage", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			s, w, _, job, calls := studyGenerationFixture(t, nil)
			switch scenario {
			case "connection":
				w.selector.WithPod("", "", "")
			case "unknown", "unknown_storage":
				if _, err := s.DB.Exec(`UPDATE processing_jobs SET remote_call_started=1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				if scenario == "unknown_storage" {
					if _, err := s.DB.Exec(`CREATE TRIGGER unknown_state_fault BEFORE UPDATE OF result_state ON processing_jobs WHEN new.result_state='unknown' BEGIN SELECT RAISE(FAIL,'unknown marker unavailable');END`); err != nil {
						t.Fatal(err)
					}
				}
			case "closed":
				s.DB.Close()
			}
			if err := w.doQuestionStudy(t.Context(), job); err == nil {
				t.Fatal("missing durable boundary accepted", scenario)
			}
			if calls.Load() != 0 {
				t.Fatal("unknown response replayed", calls.Load())
			}
		})
	}
}

func TestQuestionStudyCoverageV4RejectedKnownResponsesKeepReceipt(t *testing.T) {
	for _, stage := range []string{"generate", "review"} {
		t.Run(stage, func(t *testing.T) {
			s, w, turn, job, calls := studyGenerationFixture(t, studyTwoStageReply(t, "accept", nil))
			if stage == "review" {
				if err := w.ProcessOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
				if err != nil {
					t.Fatal(err)
				}
				job, err = s.GetJob(t.Context(), current.CheckJobID)
				if err != nil {
					t.Fatal(err)
				}
			}
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			var in store.QuestionStudyJobInput
			if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
				t.Fatal(err)
			}
			response := &provider.QuestionStudyResponse{Model: in.Model(), Content: "{corrupt", InputUnits: 9, OutputUnits: 4, UsageKnown: true}
			if stage == "generate" {
				response.UnverifiedModel = true
				response.Model = "different-actual-model"
			}
			raw, _ := json.Marshal(store.QuestionStudyCheckpoint{Version: in.Version, JobID: job.ID, InputHash: fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON))), Response: response})
			if _, err = s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json=? WHERE id=?`, string(raw), job.ID); err != nil {
				t.Fatal(err)
			}
			receiptOrigin := job.ID
			if stage == "review" {
				if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='failed' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				recovered, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, uuid.NewString(), 1, false)
				if err != nil || !created {
					t.Fatal(recovered, created, err)
				}
				job = recovered
			}
			before := calls.Load()
			if err = w.doQuestionStudy(t.Context(), job); err == nil {
				t.Fatal("invalid known response adopted")
			}
			usage, err := s.ListRunUsage(t.Context(), receiptOrigin)
			if err != nil || len(usage) != 1 || usage[0].InputUnits != 9 || calls.Load() != before {
				t.Fatal("known paid facts lost/recalled", usage, err, calls.Load())
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 {
				t.Fatal(history, err)
			}
		})
	}
}
