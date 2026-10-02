package queue

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// This fixture intentionally exercises the persisted worker and HTTP provider
// boundary, rather than validating a hand-built answer in isolation.
func TestQuestionRecoveryV4ThreeSourceConflictPreservesConditions(t *testing.T) {
	var captured provider.QuestionStudyScope
	s, w, first, _, calls := studyGenerationFixture(t, func(resp http.ResponseWriter, req *http.Request) {
		var in struct {
			Model    string                          `json:"model"`
			Messages []provider.QuestionStudyMessage `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		var content string
		if in.Model == "generate" {
			if err := json.Unmarshal([]byte(in.Messages[1].Content), &captured); err != nil {
				t.Error(err)
				return
			}
			refs := []provider.QuestionStudyReference{}
			for _, m := range captured.Materials {
				refs = append(refs, provider.QuestionStudyReference{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}})
			}
			answer := provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", Disagreements: []provider.QuestionStudyClaim{{Text: "来源的适用条件不同。", Conditions: "主动回忆需要检查条件；熟练者可独立练习；疲劳时暂停。", References: refs}}}
			raw, _ := json.Marshal(answer)
			content = string(raw)
		} else {
			content = `{"version":"question-study-review-v1","verdict":"accept","reason":"三来源条件完整","checks":[{"key":"disagreement:0","relevant":true,"supported":true,"conditions_preserved":true}]}`
		}
		_ = json.NewEncoder(resp).Encode(map[string]any{"model": in.Model, "choices": []map[string]any{{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 51, "completion_tokens": 23}})
	})
	// Discard the unused initial queued job; the fixture supplies the real client.
	if _, err := s.DB.Exec(`UPDATE processing_jobs SET status='cancelled' WHERE id=?`, first.GenerationJobID); err != nil {
		t.Fatal(err)
	}
	originalSession, err := s.GetQuestionStudySession(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"熟练者可以独立练习。", "疲劳时应暂停练习。"} {
		d, err := s.CreatePastedDocument(t.Context(), body, body)
		if err != nil {
			t.Fatal(err)
		}
		q, err := s.GetLearningQuestion(t.Context(), originalSession.QuestionID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: d.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	client, err := w.selector.QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.StartQuestionStudySession(t.Context(), originalSession.QuestionID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	turn, _, created, err := s.SubmitQuestionStudyTurn(t.Context(), fresh.ID, 1, "比较三种条件", uuid.NewString(), nil, client.Config())
	if err != nil || !created {
		t.Fatal(created, err)
	}
	for i := 0; i < 2; i++ {
		if err = w.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 1 || calls.Load() != 2 || len(captured.Materials) != 3 {
		t.Fatal(history, err, calls.Load(), captured)
	}
	if !strings.Contains(history[0].AcceptedJSON, "疲劳时暂停") {
		t.Fatal("condition lost", history[0])
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{current.GenerationJobID, current.CheckJobID} {
		usage, err := s.ListRunUsage(t.Context(), id)
		if err != nil || len(usage) != 1 || usage[0].InputUnits != 51 || usage[0].OutputUnits != 23 {
			t.Fatal(usage, err)
		}
	}
}

func TestQuestionRecoveryV4TwoWindowsReplayOneFrozenTurn(t *testing.T) {
	s, w, first, _, _ := studyGenerationFixture(t, nil)
	client, err := w.selector.QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	originalSession, err := s.GetQuestionStudySession(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.StartQuestionStudySession(t.Context(), originalSession.QuestionID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, _, _, err := s.SubmitQuestionStudyTurn(t.Context(), fresh.ID, 1, "下一问", key, nil, client.Config())
			if err != nil {
				failures <- err
				return
			}
			ids <- turn.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var id string
	count := 0
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("duplicate turn", id, next)
		}
		id = next
		count++
	}
	if count != 2 {
		t.Fatal("missing replay", count)
	}
	var jobs int
	if err = s.DB.QueryRow(`SELECT count(*) FROM question_study_requests WHERE request_key=?`, key).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal(jobs, err)
	}
	if _, _, _, err = s.SubmitQuestionStudyTurn(t.Context(), fresh.ID, 1, "不同负载", key, nil, client.Config()); err == nil {
		t.Fatal("same UUID accepted different payload")
	}
}

func TestQuestionRecoveryV4AdmissionDoesNotSendUnconfirmedOrPrivateBodies(t *testing.T) {
	for _, action := range []string{"empty", "suggestion", "local_only"} {
		t.Run(action, func(t *testing.T) {
			s, w, _, _, calls := studyGenerationFixture(t, nil)
			q, err := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "不可外发的材料"})
			if err != nil {
				t.Fatal(err)
			}
			if action != "empty" {
				d, err := s.CreatePastedDocument(t.Context(), "秘密", "c09-private-body-sentinel")
				if err != nil {
					t.Fatal(err)
				}
				linkAction := "suggest"
				if action == "local_only" {
					linkAction = "link"
					if _, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, d.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: linkAction, Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: d.ID}}); err != nil {
					t.Fatal(err)
				}
			}
			session, err := s.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			client, err := w.selector.QuestionStudy("generate", "review")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "解释", uuid.NewString(), nil, client.Config()); err == nil {
				t.Fatal("inadmissible scope created paid job")
			}
			if calls.Load() != 0 {
				t.Fatal("admission called provider", calls.Load())
			}
			var jobs int
			if err = s.DB.QueryRow(`SELECT count(*) FROM question_study_turns WHERE session_id=?`, session.ID).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("failed admission persisted turn", jobs, err)
			}
		})
	}
}

func TestQuestionRecoveryV4UnderstandingReferenceWithdrawalBlocksDispatch(t *testing.T) {
	for _, acceptedBeforeWithdrawal := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_dispatch", true: "accepted_history"}[acceptedBeforeWithdrawal], func(t *testing.T) {
			s, w, initial, _, calls := studyGenerationFixture(t, studyTwoStageReply(t, "accept", nil))
			session, err := s.GetQuestionStudySession(t.Context(), initial.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			q, err := s.GetLearningQuestion(t.Context(), session.QuestionID)
			if err != nil {
				t.Fatal(err)
			}
			private, err := s.CreatePastedDocument(t.Context(), "理解引用来源", "理解聚合引用条件")
			if err != nil {
				t.Fatal(err)
			}
			reference, err := s.FreezeSourceSnapshot(t.Context(), models.SourceDocument, private.ID)
			if err != nil {
				t.Fatal(err)
			}
			understanding, err := s.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "我需要确认条件", ModelDataPolicy: "external_allowed", References: []store.UnderstandingReference{{Kind: "evidence", ObjectID: reference.ID, Version: reference.ContentVersion}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ChooseCurrentUnderstanding(t.Context(), q.ID, understanding.ID, q.Revision, 1, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			client, err := w.selector.QuestionStudy("generate", "review")
			if err != nil {
				t.Fatal(err)
			}
			turn, job, _, err := s.SubmitQuestionStudyTurn(t.Context(), fresh.ID, 1, "解释我当前的理解", uuid.NewString(), nil, client.Config())
			if err != nil {
				t.Fatal(err)
			}
			execution, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			var frozen store.QuestionStudyJobInput
			if err = json.Unmarshal([]byte(execution.InputSnapshotJSON), &frozen); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range frozen.Scope.Materials {
				if m.Kind == "understanding" && m.Understanding != nil && m.Understanding.ID == understanding.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("current understanding not in frozen paid scope", frozen.Scope)
			}
			if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='cancelled' WHERE id=?`, initial.GenerationJobID); err != nil {
				t.Fatal(err)
			}
			if acceptedBeforeWithdrawal {
				for i := 0; i < 2; i++ {
					if err = w.ProcessOne(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
				if err != nil || len(history) != 1 || calls.Load() != 2 {
					t.Fatal(history, err, calls.Load())
				}
			}
			if _, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, private.ID); err != nil {
				t.Fatal(err)
			}
			if acceptedBeforeWithdrawal {
				scope, err := s.FreezeQuestionStudyScope(t.Context(), fresh.ID, "继续核对", "pod", nil)
				if err != nil || len(scope.History) != 0 || calls.Load() != 2 {
					t.Fatal("withdrawn understanding leaked historical answer", scope, err, calls.Load())
				}
				return
			}
			if err = w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 || calls.Load() != 0 {
				t.Fatal("withdrawn aggregate reference sent", history, err, calls.Load())
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			if err != nil || len(usage) != 0 {
				t.Fatal(usage, err)
			}

		})
	}
}
