package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func studyCoverageV4Fixture(t *testing.T) (*Store, *QuestionStudyTurn, *models.ProcessingJob, QuestionStudyJobInput) {
	t.Helper()
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	client, err := provider.NewSelector("", "").WithPod("fixture", "https://fixture.invalid/v1", "generate").QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	turn, job, _, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "明确问题", uuid.NewString(), nil, client.Config())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var in QuestionStudyJobInput
	if err = json.Unmarshal([]byte(execution.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	return s, turn, job, in
}

func TestQuestionStudyCoverageV4CommandContractAndCheckpointCAS(t *testing.T) {
	s, turn, job, in := studyCoverageV4Fixture(t)
	for _, c := range []struct {
		key      string
		revision int
		config   provider.QuestionStudyConfig
	}{{"bad", 1, in.Config}, {uuid.NewString(), 0, in.Config}, {uuid.NewString(), 1, provider.QuestionStudyConfig{}}, {uuid.NewString(), 1, func() provider.QuestionStudyConfig { c := in.Config; c.Provider = "groq"; return c }()}, {uuid.NewString(), 1, func() provider.QuestionStudyConfig {
		c := in.Config
		c.GenerationModel = strings.Repeat("x", 201)
		return c
	}()}} {
		if _, _, _, err := s.SubmitQuestionStudyTurn(t.Context(), turn.SessionID, c.revision, "draft", c.key, nil, c.config); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(c, err)
		}
	}
	ex, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveQuestionStudyCheckpoint(t.Context(), job.ID, "different-input", "response"); !errors.Is(err, ErrConflict) {
		t.Fatal("cross input checkpoint", err)
	}
	if err = s.SaveQuestionStudyCheckpoint(t.Context(), job.ID, ex.InputSnapshotJSON, "known-response"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkQuestionStudyCallStarted(t.Context(), job.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal("queued crossed remote boundary", err)
	}
	if _, err = s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkQuestionStudyCallStarted(t.Context(), job.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkQuestionStudyCallStarted(t.Context(), job.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal("same remote boundary twice", err)
	}
	if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='failed',checkpoint_json='' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FailQuestionStudyGeneration(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.State != "unknown" {
		t.Fatal(current, err)
	}
	if err = s.SaveQuestionStudyCheckpoint(t.Context(), job.ID, ex.InputSnapshotJSON, "late-response"); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal checkpoint changed", err)
	}
}

func TestQuestionStudyCoverageV4ReceiptPreservesUnknownModelAndDeduplicates(t *testing.T) {
	s, _, job, in := studyCoverageV4Fixture(t)
	for _, response := range []*provider.QuestionStudyResponse{nil, {InputUnits: -1}, {OutputUnits: -1}} {
		if err := s.RecordQuestionStudyReceipt(t.Context(), job.ID, in, response); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(response, err)
		}
	}
	for _, known := range []bool{false, true} {
		response := &provider.QuestionStudyResponse{Model: "actual-other-model", InputUnits: 13, OutputUnits: 7, UsageKnown: known, UnverifiedModel: true}
		origin := uuid.NewString()
		for i := 0; i < 2; i++ {
			if err := s.RecordQuestionStudyReceipt(t.Context(), origin, in, response); err != nil {
				t.Fatal(err)
			}
		}
		usage, err := s.ListRunUsage(t.Context(), origin)
		if err != nil || len(usage) != 1 || usage[0].UnitsKnown != known || usage[0].Model != "actual-other-model" || usage[0].CostKnown {
			t.Fatal(usage, err)
		}
	}
	priced := in
	estimate := *in.Estimate
	estimate.PriceKnown = true
	estimate.InputCentsPerMillion = 1000000
	estimate.OutputCentsPerMillion = 2000000
	priced.Estimate = &estimate
	origin := uuid.NewString()
	if err := s.RecordQuestionStudyReceipt(t.Context(), origin, priced, &provider.QuestionStudyResponse{Model: "generate", InputUnits: 13, OutputUnits: 7, UsageKnown: true}); err != nil {
		t.Fatal(err)
	}
	usage, err := s.ListRunUsage(t.Context(), origin)
	if err != nil || len(usage) != 1 || !usage[0].CostKnown || usage[0].CostCents != 27 {
		t.Fatal("frozen two-sided price not used", usage, err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER coverage_receipt_fail BEFORE INSERT ON usage_records BEGIN SELECT RAISE(FAIL,'receipt unavailable');END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordQuestionStudyReceipt(t.Context(), job.ID, in, &provider.QuestionStudyResponse{Model: "generate", InputUnits: 3, UsageKnown: true}); err == nil {
		t.Fatal("receipt failure hidden")
	}
}

func TestQuestionStudyCoverageV4ExplicitRecoveryIdentityAndRollback(t *testing.T) {
	for _, scenario := range []string{"unknown", "known", "malformed", "wrong_job", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			s, turn, job, in := studyCoverageV4Fixture(t)
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := ""
			if scenario == "known" {
				raw, _ := json.Marshal(QuestionStudyCheckpoint{Version: in.Version, JobID: job.ID, InputHash: fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON))), Response: &provider.QuestionStudyResponse{Model: "generate", Content: "saved", InputUnits: 8, OutputUnits: 4, UsageKnown: true}})
				checkpoint = string(raw)
			}
			if scenario == "malformed" {
				checkpoint = "{broken"
			}
			if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='failed',remote_call_started=1,checkpoint_json=? WHERE id=?`, checkpoint, job.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), job.ID, "bad", 1, true); !errors.Is(err, ErrInvalidEditorialState) {
				t.Fatal(err)
			}
			if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), "absent", uuid.NewString(), 1, true); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), job.ID, uuid.NewString(), 99, true); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			if scenario == "unknown" {
				if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), job.ID, uuid.NewString(), 1, false); !errors.Is(err, ErrConflict) {
					t.Fatal("unknown retry did not require consent", err)
				}
			}
			if scenario == "wrong_job" {
				if _, err = s.DB.Exec(`UPDATE question_study_turns SET generation_job_id=NULL WHERE id=?`, turn.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "rollback" {
				if _, err = s.DB.Exec(`CREATE TRIGGER retry_insert_fail BEFORE INSERT ON question_study_requests BEGIN SELECT RAISE(FAIL,'command receipt unavailable');END`); err != nil {
					t.Fatal(err)
				}
			}
			key := uuid.NewString()
			retry, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, 1, true)
			if scenario == "malformed" || scenario == "wrong_job" || scenario == "rollback" {
				if err == nil {
					t.Fatal("invalid recovery created", retry, created)
				}
				var count int
				if err = s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='question_study'`).Scan(&count); err != nil || count != 1 {
					t.Fatal("partial recovery escaped rollback", count, err)
				}
				return
			}
			if err != nil || !created {
				t.Fatal(retry, created, err)
			}
			replay, created, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, 1, true)
			if err != nil || created || replay.ID != retry.ID {
				t.Fatal(replay, created, err)
			}
			if _, _, err = s.RetryQuestionStudyGeneration(t.Context(), job.ID, key, 1, false); !errors.Is(err, ErrConflict) {
				t.Fatal("command receipt payload changed", err)
			}
			next, err := s.GetJobExecution(t.Context(), retry.ID)
			if err != nil {
				t.Fatal(err)
			}
			var recovered QuestionStudyJobInput
			if err = json.Unmarshal([]byte(next.InputSnapshotJSON), &recovered); err != nil {
				t.Fatal(err)
			}
			if scenario == "known" {
				if recovered.OriginJobID != job.ID || next.CheckpointJSON == "" {
					t.Fatal("known response lost origin", recovered, next)
				}
			} else if recovered.OriginJobID != "" || next.CheckpointJSON != "" {
				t.Fatal("unknown reused response", recovered, next)
			}
		})
	}
}

func TestQuestionStudyCoverageV4ClosedDatabaseDoesNotSucceed(t *testing.T) {
	s, turn, job, in := studyCoverageV4Fixture(t)
	s.DB.Close()
	if _, _, _, err := s.SubmitQuestionStudyTurn(t.Context(), turn.SessionID, 2, "question", uuid.NewString(), nil, in.Config); err == nil {
		t.Fatal("closed DB accepted command")
	}
	if _, _, err := s.RetryQuestionStudyGeneration(t.Context(), job.ID, uuid.NewString(), 1, true); err == nil {
		t.Fatal("closed DB accepted retry")
	}
	if err := s.SaveQuestionStudyCheckpoint(t.Context(), job.ID, "input", "checkpoint"); err == nil {
		t.Fatal("closed DB checkpoint saved")
	}
	if err := s.MarkQuestionStudyCallStarted(t.Context(), job.ID, in); err == nil {
		t.Fatal("closed DB marked paid call")
	}
	if err := s.FailQuestionStudyGeneration(t.Context(), job.ID); err == nil {
		t.Fatal("closed DB hid failure")
	}
}

func TestQuestionStudyCoverageV4GenerationAdoptionRollsBackSuccessor(t *testing.T) {
	s, turn, job, in := studyCoverageV4Fixture(t)
	m := in.Scope.Materials[0]
	answer := provider.QuestionStudyAnswer{Version: provider.QuestionStudyPromptVersion, State: "answered", SourceClaims: []provider.QuestionStudyClaim{{Text: "来源条件", References: []provider.QuestionStudyReference{{MaterialKey: m.Key, Revision: m.Revision, SegmentIDs: []string{m.Segments[0].SegmentID}}}}}}
	wrong := in
	wrong.Stage = "review"
	if err := s.CommitQuestionStudyGeneration(t.Context(), job.ID, wrong, answer); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.CommitQuestionStudyGeneration(t.Context(), job.ID, in, provider.QuestionStudyAnswer{}); err == nil {
		t.Fatal("invalid answer adopted")
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER check_enqueue_fail BEFORE INSERT ON processing_jobs WHEN new.configured_model='review' BEGIN SELECT RAISE(FAIL,'review job unavailable');END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitQuestionStudyGeneration(t.Context(), job.ID, in, answer); err == nil {
		t.Fatal("partial generation adopted")
	}
	current, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.CheckJobID != "" || current.State != "queued" {
		t.Fatal("successor rollback changed private state", current, err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER check_enqueue_fail`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.CommitQuestionStudyGeneration(t.Context(), job.ID, in, answer); err != nil {
			t.Fatal(err)
		}
	}
	current, err = s.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || current.CheckJobID == "" || current.AcceptedJSON != "" {
		t.Fatal(current, err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM processing_jobs WHERE configured_model='review'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("generation recovery doubled review", count, err)
	}
	history, err := s.QuestionStudyHistory(t.Context(), turn.SessionID)
	if err != nil || len(history) != 0 {
		t.Fatal("unchecked answer published", history, err)
	}
	if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted' WHERE id=?`, turn.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitQuestionStudyGeneration(t.Context(), job.ID, in, answer); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted turn rewritten", err)
	}
}

func TestQuestionStudyCoverageV4FrozenScopeRejectsChangedMaterials(t *testing.T) {
	for _, scenario := range []string{"revision", "body", "kind", "source", "snapshot", "unlinked", "permission"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, in := studyCoverageV4Fixture(t)
			m := &in.Scope.Materials[0]
			switch scenario {
			case "revision":
				m.Revision++
			case "body":
				m.Content = "different frozen text"
			case "kind":
				m.Kind = "keypoint"
			case "source":
				m.SourceType = "invalid"
			case "snapshot":
				m.SnapshotID = "absent-snapshot"
			case "unlinked":
				if _, err := s.DB.Exec(`DELETE FROM learning_question_links WHERE question_id=?`, in.Scope.Question.ID); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if _, err := s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, m.SourceID); err != nil {
					t.Fatal(err)
				}
			}
			if err := checkQuestionStudyScope(t.Context(), s.DB, in.Scope, "pod"); err == nil {
				t.Fatal("changed scope allowed", scenario)
			}
		})
	}
}

func TestQuestionStudyCoverageV4AdmissionFailureNeverLeavesPaidWork(t *testing.T) {
	for _, statement := range []string{
		`CREATE TRIGGER admission_fault BEFORE INSERT ON processing_jobs BEGIN SELECT RAISE(FAIL,'job storage unavailable');END`,
		`CREATE TRIGGER admission_fault BEFORE INSERT ON question_study_requests BEGIN SELECT RAISE(FAIL,'command receipt unavailable');END`,
		`CREATE TRIGGER admission_fault BEFORE UPDATE OF generation_job_id ON question_study_turns BEGIN SELECT RAISE(FAIL,'turn binding unavailable');END`,
	} {
		t.Run(strings.Split(statement, " ON ")[1], func(t *testing.T) {
			s, initial, _, in := studyCoverageV4Fixture(t)
			original, err := s.GetQuestionStudySession(t.Context(), initial.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.StartQuestionStudySession(t.Context(), original.QuestionID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "我的未提交草稿", uuid.NewString(), nil, in.Config); err == nil {
				t.Fatal("failed transaction admitted")
			}
			current, err := s.GetQuestionStudySession(t.Context(), session.ID)
			if err != nil || current.Revision != 1 {
				t.Fatal("failed admission consumed revision", current, err)
			}
			history, err := s.QuestionStudyTurnStatuses(t.Context(), session.ID)
			if err != nil || len(history) != 0 {
				t.Fatal("partial paid turn", history, err)
			}
			var count int
			if err = s.DB.QueryRow(`SELECT count(*) FROM processing_jobs WHERE job_type='question_study'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("partial paid job", count, err)
			}
		})
	}
}
