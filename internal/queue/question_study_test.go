package queue

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func studyGenerationFixture(t *testing.T, hook func(http.ResponseWriter, *http.Request), priced ...bool) (*store.Store, *Worker, *store.QuestionStudyTurn, *models.ProcessingJob, *atomic.Int32) {
	t.Helper()
	s, w := newTestWorker(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if hook != nil {
			hook(resp, req)
			return
		}
		studyGenerationReply(t, resp, req)
	}))
	t.Cleanup(server.Close)
	w.selector.WithPod("test-secret", server.URL, "generate")
	client, err := w.selector.QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(t.Context(), "条件来源", "主动回忆需要检查条件。")
	if err != nil {
		t.Fatal(err)
	}
	question, err := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "什么条件下适用？"})
	if err != nil {
		t.Fatal(err)
	}
	question, err = s.ChangeLearningQuestion(t.Context(), question.ID, question.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: doc.ID}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartQuestionStudySession(t.Context(), question.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if len(priced) > 0 && priced[0] {
		if err = s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "generate", InputCentsPerMillion: 1000, OutputCentsPerMillion: 2000}); err != nil {
			t.Fatal(err)
		}
	}
	turn, job, created, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "解释适用条件", uuid.NewString(), nil, client.Config())
	if err != nil || !created {
		t.Fatal(turn, job, created, err)
	}
	return s, w, turn, job, calls
}
func studyGenerationReply(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var request struct {
		Model    string                          `json:"model"`
		Messages []provider.QuestionStudyMessage `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
		t.Error(err)
		return
	}
	var scope provider.QuestionStudyScope
	if err := json.Unmarshal([]byte(request.Messages[1].Content), &scope); err != nil || len(scope.Materials) == 0 {
		t.Error(err)
		return
	}
	m := scope.Materials[0]
	answer := provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", SourceClaims: []provider.QuestionStudyClaim{{Text: "需要核对条件。", References: []provider.QuestionStudyReference{{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}}}}}}
	raw, _ := json.Marshal(answer)
	_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []map[string]any{{"message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
}
func TestQuestionStudyGenerationPersistsPrivateResponseAndReceipt(t *testing.T) {
	s, w, turn, job, calls := studyGenerationFixture(t, nil)
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetJob(t.Context(), job.ID)
	if err != nil || completed.Status != models.StatusSucceeded || calls.Load() != 1 {
		t.Fatal(completed, err, calls.Load())
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.State != "response_saved" || current.AcceptedJSON != "" {
		t.Fatal(current, err)
	}
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 0 {
		t.Fatal("unchecked response visible", history, err)
	}
	execution, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || execution.CheckpointJSON == "" || execution.ResultState != models.JobResultComplete {
		t.Fatal(execution, err)
	}
	usage, err := s.ListRunUsage(t.Context(), job.ID)
	if err != nil || len(usage) != 1 || !usage[0].UnitsKnown || usage[0].InputUnits != 31 || usage[0].OutputUnits != 12 {
		t.Fatal(usage, err)
	}
	if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 1 {
		t.Fatal("durable response resent", err, calls.Load())
	}
}
func TestQuestionStudyGenerationKnownReceiptFailureRecoversWithoutConnection(t *testing.T) {
	s, w, turn, job, calls := studyGenerationFixture(t, nil)
	if _, err := s.DB.Exec(`CREATE TRIGGER study_receipt_fault BEFORE INSERT ON usage_records WHEN new.operation='question_study_generate' BEGIN SELECT RAISE(FAIL,'receipt fault');END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	failed, _ := s.GetJob(t.Context(), job.ID)
	execution, _ := s.GetJobExecution(t.Context(), job.ID)
	if failed.Status != models.StatusFailed || execution.CheckpointJSON == "" || calls.Load() != 1 {
		t.Fatal(failed, execution, calls.Load())
	}
	if _, err := s.DB.Exec(`DROP TRIGGER study_receipt_fault`); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	recovery, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, revision, false)
	if err != nil || !created {
		t.Fatal(recovery, created, err)
	}
	replay, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, revision, false)
	if err != nil || created || replay.ID != recovery.ID {
		t.Fatal(replay, created, err)
	}
	w.selector.WithPod("", "", "")
	if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 1 {
		t.Fatal("known recovery called provider", err, calls.Load())
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.State != "response_saved" {
		t.Fatal(current, err)
	}
	usage, err := s.ListRunUsage(t.Context(), job.ID)
	if err != nil || len(usage) != 1 {
		t.Fatal(usage, err)
	}
	usage, err = s.ListRunUsage(t.Context(), recovery.ID)
	if err != nil || len(usage) != 0 {
		t.Fatal("duplicate recovery fee", usage, err)
	}
}
func TestQuestionStudyGenerationUnknownRequiresExplicitNewAttempt(t *testing.T) {
	s, w, turn, job, calls := studyGenerationFixture(t, func(resp http.ResponseWriter, req *http.Request) { http.Error(resp, "unknown", 503) })
	if err := w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.State != "unknown" || calls.Load() != 1 {
		t.Fatal(current, err, calls.Load())
	}
	if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 1 {
		t.Fatal("unknown result auto retried", err, calls.Load())
	}
	var revision int
	s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
	if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), job.ID, uuid.NewString(), revision, false); err == nil {
		t.Fatal("unknown charge not confirmed")
	}
	key := uuid.NewString()
	retry, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, revision, true)
	if err != nil || !created {
		t.Fatal(retry, created, err)
	}
	replay, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, revision, true)
	if err != nil || created || replay.ID != retry.ID {
		t.Fatal(replay, created, err)
	}
	if err = w.ProcessOne(t.Context()); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}

func TestQuestionStudyGenerationLateStopAndPurgeKeepPaidFacts(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "purge"}[purge], func(t *testing.T) {
			var s *store.Store
			var jobID string
			s, w, turn, job, calls := studyGenerationFixture(t, func(resp http.ResponseWriter, req *http.Request) {
				if purge {
					execution, err := s.GetJobExecution(t.Context(), jobID)
					if err != nil {
						t.Error(err)
						return
					}
					var in store.QuestionStudyJobInput
					if err = json.Unmarshal([]byte(execution.InputSnapshotJSON), &in); err != nil {
						t.Error(err)
						return
					}
					source := in.Scope.Materials[0]
					if err = s.DeleteSourceRows(t.Context(), models.SourceType(source.SourceType), source.SourceID); err != nil {
						t.Error(err)
						return
					}
				} else {
					var revision int
					if err := s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, jobID).Scan(&revision); err != nil {
						t.Error(err)
						return
					}
					if err := s.ChangeRunControl(t.Context(), "job", jobID, "stop", "停止迟到回答", uuid.NewString(), revision, 0); err != nil {
						t.Error(err)
						return
					}
				}
				studyGenerationReply(t, resp, req)
			})
			jobID = job.ID
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
			if err != nil || current.State != "blocked" || current.AcceptedJSON != "" || calls.Load() != 1 {
				t.Fatal(current, err, calls.Load())
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			if err != nil || len(usage) != 1 || usage[0].InputUnits != 31 || !usage[0].UnitsKnown {
				t.Fatal(usage, err)
			}
			execution, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if purge && (!current.Purged || current.FrozenJSON != "{}" || execution.InputSnapshotJSON != "{}" || execution.CheckpointJSON != "") {
				t.Fatal("purged response repopulated text", current, execution)
			}
			history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
			if err != nil || len(history) != 0 {
				t.Fatal(history, err)
			}
		})
	}
}

func TestQuestionStudyGenerationHistoryOnlySourcePurgeClearsNewTask(t *testing.T) {
	s, w := newTestWorker(t)
	a, err := s.CreatePastedDocument(t.Context(), "当前来源", "当前适用条件。")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreatePastedDocument(t.Context(), "历史来源", "历史私有条件。")
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "比较条件"})
	if err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		calls++
		if err := s.DeleteSourceRows(t.Context(), models.SourceDocument, b.ID); err != nil {
			t.Error(err)
			return
		}
		studyGenerationReply(t, resp, req)
	}))
	defer server.Close()
	w.selector.WithPod("test-secret", server.URL, "generate")
	client, err := w.selector.QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartQuestionStudySession(t.Context(), q.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	old, oldJob, _, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "历史问题", uuid.NewString(), nil, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	// A self-authored accepted-history fixture, not a supplier quality judgment.
	claimed, err := s.ClaimNextJob(t.Context(), "60 seconds")
	if err != nil || claimed == nil || claimed.ID != oldJob.ID {
		t.Fatal(claimed, err)
	}
	if err = s.MarkJobSucceeded(t.Context(), oldJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted',accepted_json='{"body":"history-private-sentinel"}' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "unlink", Link: provider.LearningQuestionLink{Kind: "source", ObjectID: "document:" + b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(t.Context(), q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "document", SourceID: a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.GetQuestionStudySession(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	turn, job, _, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, session.Revision, "当前问题", uuid.NewString(), nil, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	var dependencies int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM question_study_sources WHERE turn_id=?`, turn.ID).Scan(&dependencies); err != nil || dependencies != 2 {
		t.Fatal("history lineage omitted", dependencies, err)
	}
	if err = w.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || !current.Purged || current.FrozenJSON != "{}" || current.AcceptedJSON != "" || calls != 1 {
		t.Fatal(current, err, calls)
	}
	execution, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil || execution.InputSnapshotJSON != "{}" || execution.CheckpointJSON != "" {
		t.Fatal("history-only purge leaked frozen data", execution, err)
	}
	usage, err := s.ListRunUsage(t.Context(), job.ID)
	if err != nil || len(usage) != 1 || usage[0].InputUnits != 31 {
		t.Fatal(usage, err)
	}
}

func TestQuestionStudyGenerationBudgetAndRouteBlockBeforeCall(t *testing.T) {
	for _, changeRoute := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpriced-budget", true: "route-changed"}[changeRoute], func(t *testing.T) {
			s, w, _, job, calls := studyGenerationFixture(t, nil)
			if changeRoute {
				w.selector.WithPod("test-secret", "https://changed.invalid/v1", "generate")
			} else {
				budget := int64(100)
				if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			failed, err := s.GetJob(t.Context(), job.ID)
			if err != nil || failed.Status != models.StatusFailed || calls.Load() != 0 {
				t.Fatal(failed, err, calls.Load())
			}
			execution, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil || execution.RemoteCallStarted || execution.CheckpointJSON != "" {
				t.Fatal(execution, err)
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			if err != nil || len(usage) != 0 {
				t.Fatal(usage, err)
			}
		})
	}
}

func TestQuestionStudyGenerationUsesFrozenTwoSidedPriceAndBudget(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "frozen-price", true: "priced-zero-budget"}[blocked], func(t *testing.T) {
			s, w, _, job, calls := studyGenerationFixture(t, nil, true)
			if blocked {
				budget := int64(0)
				if err := s.SetOwnerMonthlyBudget(t.Context(), &budget); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "pod", Model: "generate", InputCentsPerMillion: 1000000, OutputCentsPerMillion: 1000000}); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if blocked {
				if calls.Load() != 0 || len(usage) != 0 {
					t.Fatal("budget block called supplier", calls.Load(), usage)
				}
			} else if calls.Load() != 1 || len(usage) != 1 || !usage[0].CostKnown || usage[0].CostCents != 1 {
				t.Fatal("current price substituted for frozen tariff", calls.Load(), usage)
			}
		})
	}
}
