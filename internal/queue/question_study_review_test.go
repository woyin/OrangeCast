package queue

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
)

func studyTwoStageReply(t *testing.T, verdict string, beforeReview func()) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if beforeReview != nil {
			beforeReview()
		}
		var request struct {
			Model    string                          `json:"model"`
			Messages []provider.QuestionStudyMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var content string
		if request.Model == "generate" {
			var scope provider.QuestionStudyScope
			if err := json.Unmarshal([]byte(request.Messages[1].Content), &scope); err != nil {
				t.Error(err)
				return
			}
			m := scope.Materials[0]
			answer := provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", SourceClaims: []provider.QuestionStudyClaim{{Text: "需要核对条件。", References: []provider.QuestionStudyReference{{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}}}}}}
			raw, _ := json.Marshal(answer)
			content = string(raw)
		} else {
			content = `{"version":"question-study-review-v1","verdict":"` + verdict + `","reason":"核对冻结依据","checks":[{"key":"source:0","relevant":true,"supported":true,"conditions_preserved":true}]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []map[string]any{{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
	}
}

func TestQuestionStudyCommitOnlyAcceptedReviewEntersHistory(t *testing.T) {
	for _, verdict := range []string{"accept", "reject", "insufficient"} {
		t.Run(verdict, func(t *testing.T) {
			s, w, turn, _, calls := studyGenerationFixture(t, studyTwoStageReply(t, verdict, nil))
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
			if err != nil || current.CheckJobID == "" || current.AcceptedJSON != "" {
				t.Fatal(current, err)
			}
			if err = w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			want := 0
			if verdict == "accept" {
				want = 1
			}
			if err != nil || len(history) != want || calls.Load() != 2 {
				t.Fatal(history, err, calls.Load())
			}
			usage, err := s.ListRunUsage(t.Context(), current.CheckJobID)
			if err != nil || len(usage) != 1 {
				t.Fatal(usage, err)
			}
		})
	}
}

func TestQuestionStudyCommitKnownReviewRecoversWithoutNewCall(t *testing.T) {
	s, w, turn, _, calls := studyGenerationFixture(t, studyTwoStageReply(t, "accept", nil))
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER review_receipt_fault BEFORE INSERT ON usage_records WHEN new.operation='question_study_review' BEGIN SELECT RAISE(FAIL,'receipt fault');END`); err != nil {
		t.Fatal(err)
	}
	_ = w.ProcessOne(t.Context())
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	var revision int
	if err = s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, current.CheckJobID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER review_receipt_fault`); err != nil {
		t.Fatal(err)
	}
	retry, created, err := s.RetryQuestionStudyGeneration(t.Context(), current.CheckJobID, uuid.NewString(), revision, false)
	if err != nil || !created {
		t.Fatal(retry, created, err)
	}
	w.selector.WithPod("", "", "")
	if err = w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 1 || calls.Load() != 2 {
		t.Fatal(history, err, calls.Load())
	}
}

func TestQuestionStudyCommitLateStopAndPermissionWithdrawal(t *testing.T) {
	for _, action := range []string{"stop", "permission"} {
		t.Run(action, func(t *testing.T) {
			s, w, turn, _, calls := studyGenerationFixture(t, nil)
			var reviewID string
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			reviewID = current.CheckJobID
			if action == "stop" {
				_, err = s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, reviewID)
			} else {
				_, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = w.ProcessOne(t.Context())
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 || calls.Load() != 1 {
				t.Fatal(history, err, calls.Load())
			}
		})
	}
}

func TestQuestionStudyCommitStopAfterPaidReviewSuppressesAnswer(t *testing.T) {
	var stop func()
	reply := studyTwoStageReply(t, "accept", func() {
		if stop != nil {
			stop()
		}
	})
	s, w, turn, _, calls := studyGenerationFixture(t, reply)
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	stop = func() {
		if _, err := s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, current.CheckJobID); err != nil {
			t.Error(err)
		}
	}
	_ = w.ProcessOne(t.Context())
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 0 || calls.Load() != 2 {
		t.Fatal(history, err, calls.Load())
	}
	usage, err := s.ListRunUsage(t.Context(), current.CheckJobID)
	if err != nil || len(usage) != 1 {
		t.Fatal(usage, err)
	}
}

func TestQuestionStudyCommitBudgetBlocksOnlyReviewStage(t *testing.T) {
	s, w, turn, _, calls := studyGenerationFixture(t, studyTwoStageReply(t, "accept", nil), true)
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(100)
	if err = s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetJob(t.Context(), current.CheckJobID)
	if err != nil || string(job.Status) != "failed" || calls.Load() != 1 {
		t.Fatal(job, err, calls.Load())
	}
	ex, err := s.GetJobExecution(t.Context(), current.CheckJobID)
	if err != nil || ex.RemoteCallStarted || ex.CheckpointJSON != "" {
		t.Fatal(ex, err)
	}
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 0 {
		t.Fatal(history, err)
	}
}
