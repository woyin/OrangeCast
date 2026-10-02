package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func legacyAdmitted(t *testing.T) (*Store, *LegacyStudyTurn, *models.ProcessingJob, LegacyStudyJobInput) {
	t.Helper()
	s, id, cfg := legacyFixture(t)
	if err := s.SetModelPrice(t.Context(), models.ModelPrice{Provider: "groq", Model: "qa", InputCentsPerMillion: 1000, OutputCentsPerMillion: 2000}); err != nil {
		t.Fatal(err)
	}
	turn, job, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, turn, job, legacyInput(t, s, job.ID)
}
func legacyInput(t *testing.T, s *Store, id string) LegacyStudyJobInput {
	t.Helper()
	execution, err := s.GetJobExecution(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var in LegacyStudyJobInput
	if err = json.Unmarshal([]byte(execution.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	return in
}
func legacyPaidCheckpoint(t *testing.T, s *Store, job *models.ProcessingJob, in LegacyStudyJobInput, known bool) {
	t.Helper()
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkLegacyStudyCallStarted(t.Context(), job.ID, in); err != nil {
		t.Fatal(err)
	}
	execution, _ := s.GetJobExecution(t.Context(), job.ID)
	response := &provider.QuestionStudyResponse{Content: `{"answer":"通胀是物价变化","referenceSegmentIds":["seg-0001"]}`, Model: in.Model(), InputUnits: 31, OutputUnits: 12, UsageKnown: known}
	cp := QuestionStudyCheckpoint{Version: in.Version, JobID: job.ID, InputHash: fmt.Sprintf("%x", sha256.Sum256([]byte(execution.InputSnapshotJSON))), Response: response}
	raw, _ := json.Marshal(cp)
	if err := s.SaveLegacyStudyCheckpoint(t.Context(), job.ID, execution.InputSnapshotJSON, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLegacyStudyReceipt(t.Context(), job.ID, in, response); err != nil {
		t.Fatal(err)
	}
}
func legacyCandidate() *provider.StudyChatResult {
	return &provider.StudyChatResult{Answer: &provider.StudyChatMessage{Role: "assistant", Content: "通胀是物价变化", ReferenceSegmentIDs: []string{"seg-0001"}}}
}
func TestLegacyStudyStoreStageAdoptionAndReceiptSemantics(t *testing.T) {
	for _, related := range []bool{true, false} {
		t.Run(fmt.Sprint(related), func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			legacyPaidCheckpoint(t, s, job, in, true)
			if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, legacyCandidate()); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if err != nil || current.State != "response_saved" || current.Answer != nil {
				t.Fatal(current, err)
			}
			messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, false)
			if len(messages) != 1 {
				t.Fatal("unreviewed body visible", messages)
			}
			reviewJob, err := s.GetJob(t.Context(), current.CheckJobID)
			if err != nil {
				t.Fatal(err)
			}
			review := legacyInput(t, s, reviewJob.ID)
			if review.Operation() != "legacy_study_review" || review.Model() != "qa" || review.Answer == nil {
				t.Fatal(review)
			}
			legacyPaidCheckpoint(t, s, reviewJob, review, false)
			// Unknown units have numeric observations but no inferred zero price.
			var unknown, unpriced int
			s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='legacy_study_review' AND units_known=0 AND estimated_cost IS NULL`).Scan(&unknown)
			s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='legacy_study_generate' AND units_known=1 AND estimated_cost IS NOT NULL`).Scan(&unpriced)
			if unknown != 1 || unpriced != 1 {
				t.Fatal(unknown, unpriced)
			}
			if err = s.CommitLegacyStudyReview(t.Context(), reviewJob.ID, review, provider.ReferenceCheckResult{Related: related, Reason: "scope decision"}); err != nil {
				t.Fatal(err)
			}
			current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
			messages, _ = s.ListStudyMessages(t.Context(), turn.SessionID, false)
			if related && (current.State != "accepted" || current.Answer == nil || len(messages) != 2) {
				t.Fatal(current, messages)
			}
			if !related && (current.State != "insufficient" || current.Answer != nil || len(messages) != 1 || current.Feedback == "") {
				t.Fatal(current, messages)
			}
			if err = s.CommitLegacyStudyReview(t.Context(), reviewJob.ID, review, provider.ReferenceCheckResult{Related: true}); !errors.Is(err, ErrConflict) {
				t.Fatal("accepted turn mutated", err)
			}
			if err = s.FailLegacyStudy(t.Context(), reviewJob.ID); err != nil {
				t.Fatal(err)
			}
			current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if (related && current.State != "accepted") || (!related && current.State != "insufficient") {
				t.Fatal("terminal scope decision replaced", current)
			}
		})
	}
}
func TestLegacyStudyStoreKnownResponseRetryIdentityAndUnknownConsent(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			if known {
				legacyPaidCheckpoint(t, s, job, in, true)
			} else {
				s.MarkJobRunning(t.Context(), job.ID)
				if err := s.MarkLegacyStudyCallStarted(t.Context(), job.ID, in); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.MarkJobFailed(t.Context(), job.ID, "process crashed"); err != nil {
				t.Fatal(err)
			}
			s.FailLegacyStudy(t.Context(), job.ID)
			var revision int
			s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
			if !known {
				if _, _, err := s.RetryLegacyStudy(t.Context(), job.ID, uuid.NewString(), revision, false); !errors.Is(err, ErrConflict) {
					t.Fatal("unknown replay without consent", err)
				}
			}
			key := uuid.NewString()
			retry, created, err := s.RetryLegacyStudy(t.Context(), job.ID, key, revision, !known)
			if err != nil || !created {
				t.Fatal(retry, created, err)
			}
			same, created, err := s.RetryLegacyStudy(t.Context(), job.ID, key, revision, !known)
			if err != nil || created || same.ID != retry.ID {
				t.Fatal(same, created, err)
			}
			if _, _, err = s.RetryLegacyStudy(t.Context(), job.ID, key, revision+1, !known); !errors.Is(err, ErrConflict) {
				t.Fatal("changed command reused", err)
			}
			next := legacyInput(t, s, retry.ID)
			ex, _ := s.GetJobExecution(t.Context(), retry.ID)
			if known && (next.OriginJobID != job.ID || ex.CheckpointJSON == "") {
				t.Fatal(next, ex)
			}
			if !known && (next.OriginJobID != "" || ex.CheckpointJSON != "") {
				t.Fatal(next, ex)
			}
			if known {
				if err = s.CommitLegacyStudyGeneration(t.Context(), retry.ID, next, legacyCandidate()); err != nil {
					t.Fatal(err)
				}
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if current.GenerationJobID != retry.ID {
				t.Fatal(current)
			}
		})
	}
}
func TestLegacyStudyStoreAdmissionFailuresAreAtomic(t *testing.T) {
	faults := []struct{ name, sql string }{
		{"job", "CREATE TRIGGER legacy_fault BEFORE INSERT ON processing_jobs WHEN new.source_type='study_chat' BEGIN SELECT RAISE(FAIL,'storage write failure');END"},
		{"turn", "CREATE TRIGGER legacy_fault BEFORE INSERT ON legacy_study_turns BEGIN SELECT RAISE(FAIL,'storage write failure');END"},
		{"receipt", "CREATE TRIGGER legacy_fault BEFORE INSERT ON legacy_study_requests BEGIN SELECT RAISE(FAIL,'storage write failure');END"},
		{"owner-message", "CREATE TRIGGER legacy_fault BEFORE INSERT ON study_messages BEGIN SELECT RAISE(FAIL,'storage write failure');END"},
		{"revision", "CREATE TRIGGER legacy_fault BEFORE UPDATE OF revision ON study_sessions BEGIN SELECT RAISE(FAIL,'storage write failure');END"},
	}
	for _, fault := range faults {
		t.Run(fault.name, func(t *testing.T) {
			s, id, cfg := legacyFixture(t)
			if _, err := s.DB.Exec(fault.sql); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), cfg); err == nil {
				t.Fatal("failure adopted partial command")
			}
			for _, query := range []string{`SELECT count(*) FROM study_sessions`, `SELECT count(*) FROM legacy_study_turns`, `SELECT count(*) FROM legacy_study_requests`, `SELECT count(*) FROM study_messages`, `SELECT count(*) FROM processing_jobs WHERE source_type='study_chat'`} {
				var n int
				if err := s.DB.QueryRow(query).Scan(&n); err != nil || n != 0 {
					t.Fatal(query, n, err)
				}
			}
		})
	}
}
func TestLegacyStudyStoreRevokedStoppedAndInvalidInputs(t *testing.T) {
	for _, action := range []string{"stop", "revoke", "version", "bad-reference", "wrong-stage", "wrong-task"} {
		t.Run(action, func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			s.MarkJobRunning(t.Context(), job.ID)
			switch action {
			case "stop":
				s.DB.Exec(`UPDATE processing_jobs SET stop_requested=1 WHERE id=?`, job.ID)
			case "revoke":
				s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, in.SourceID)
			case "version":
				seedSnapshotTranscript(t, s, models.SourceEpisode, in.SourceID, "新版内容")
			case "bad-reference":
				candidate := legacyCandidate()
				candidate.Answer.ReferenceSegmentIDs = []string{"foreign"}
				if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, candidate); !errors.Is(err, ErrInvalidEditorialState) {
					t.Fatal(err)
				}
				return
			case "wrong-stage":
				in.Stage = "other"
			case "wrong-task":
				in.TurnID = uuid.NewString()
			}
			if err := s.MarkLegacyStudyCallStarted(t.Context(), job.ID, in); err == nil {
				t.Fatal("invalid boundary admitted")
			}
			if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, legacyCandidate()); err == nil {
				t.Fatal("invalid result adopted")
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if current.Answer != nil {
				t.Fatal(current)
			}
		})
	}
	s, _, job, in := legacyAdmitted(t)
	if err := s.SaveLegacyStudyCheckpoint(t.Context(), job.ID, "wrong input", "{}"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.RecordLegacyStudyReceipt(t.Context(), job.ID, in, nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.RecordLegacyStudyReceipt(t.Context(), job.ID, in, &provider.QuestionStudyResponse{InputUnits: -1}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.CommitLegacyStudyReview(t.Context(), job.ID, in, provider.ReferenceCheckResult{}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, _, err := s.RetryLegacyStudy(t.Context(), job.ID, "bad uuid", 1, false); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, _, err := s.RetryLegacyStudy(t.Context(), "missing", uuid.NewString(), 1, false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.GetLegacyStudyTurn(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := s.LegacyStudyRequest(ctx, uuid.NewString()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, in.SourceID, "", 1, strings.Repeat("a", 8193), uuid.NewString(), in.Config); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}

func TestLegacyStudyStoreReviewFailureRollsBackVisibleAdoption(t *testing.T) {
	for _, fault := range []string{"message", "turn", "session", "result"} {
		t.Run(fault, func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			legacyPaidCheckpoint(t, s, job, in, true)
			if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, legacyCandidate()); err != nil {
				t.Fatal(err)
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			reviewJob, _ := s.GetJob(t.Context(), current.CheckJobID)
			review := legacyInput(t, s, reviewJob.ID)
			legacyPaidCheckpoint(t, s, reviewJob, review, true)
			trigger := map[string]string{
				"message": `CREATE TRIGGER legacy_review_fault BEFORE INSERT ON study_messages WHEN new.role='assistant' BEGIN SELECT RAISE(FAIL,'adopt failure');END`,
				"turn":    `CREATE TRIGGER legacy_review_fault BEFORE UPDATE OF answer_message_id ON legacy_study_turns BEGIN SELECT RAISE(FAIL,'adopt failure');END`,
				"session": `CREATE TRIGGER legacy_review_fault BEFORE UPDATE OF revision ON study_sessions BEGIN SELECT RAISE(FAIL,'adopt failure');END`,
				"result":  `CREATE TRIGGER legacy_review_fault BEFORE UPDATE OF result_state ON processing_jobs WHEN new.id='` + reviewJob.ID + `' BEGIN SELECT RAISE(FAIL,'adopt failure');END`,
			}[fault]
			if _, err := s.DB.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := s.CommitLegacyStudyReview(t.Context(), reviewJob.ID, review, provider.ReferenceCheckResult{Related: true}); err == nil {
				t.Fatal("partial adoption accepted")
			}
			current, _ = s.GetLegacyStudyTurn(t.Context(), turn.ID)
			messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, false)
			if current.State != "checking" || current.Answer != nil || len(messages) != 1 {
				t.Fatal(current, messages)
			}
			s.DB.Exec(`DROP TRIGGER legacy_review_fault`)
			if err := s.CommitLegacyStudyReview(t.Context(), reviewJob.ID, review, provider.ReferenceCheckResult{Related: true}); err != nil {
				t.Fatal("known check could not recover", err)
			}
		})
	}
}
func TestLegacyStudyStoreRetryReviewAndRejectedRecoveryInputs(t *testing.T) {
	for _, mode := range []string{"review-known", "review-unknown", "bad-checkpoint", "bad-input", "stale", "revoked", "superseded"} {
		t.Run(mode, func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			if strings.HasPrefix(mode, "review-") {
				legacyPaidCheckpoint(t, s, job, in, true)
				if err := s.CommitLegacyStudyGeneration(t.Context(), job.ID, in, legacyCandidate()); err != nil {
					t.Fatal(err)
				}
				current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
				job, _ = s.GetJob(t.Context(), current.CheckJobID)
				in = legacyInput(t, s, job.ID)
			}
			if mode == "review-known" {
				legacyPaidCheckpoint(t, s, job, in, true)
			} else {
				s.MarkJobRunning(t.Context(), job.ID)
				s.MarkLegacyStudyCallStarted(t.Context(), job.ID, in)
			}
			s.MarkJobFailed(t.Context(), job.ID, "fault")
			s.FailLegacyStudy(t.Context(), job.ID)
			var revision int
			s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
			switch mode {
			case "bad-checkpoint":
				s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{}' WHERE id=?`, job.ID)
			case "bad-input":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='{}' WHERE id=?`, job.ID)
			case "stale":
				revision++
			case "revoked":
				s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, in.SourceID)
			case "superseded":
				artifact, err := s.GetCurrentVersion(t.Context(), models.SourceEpisode, in.SourceID, KindTranscript)
				if err != nil {
					t.Fatal(err)
				}
				s.DB.Exec(`UPDATE legacy_study_turns SET generation_job_id=? WHERE id=?`, artifact.JobID, turn.ID)
			}
			next, created, err := s.RetryLegacyStudy(t.Context(), job.ID, uuid.NewString(), revision, true)
			if strings.HasPrefix(mode, "review-") {
				if err != nil || !created {
					t.Fatal(next, created, err)
				}
				review := legacyInput(t, s, next.ID)
				if review.Stage != "review" || review.Answer == nil {
					t.Fatal(review)
				}
				current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
				if current.CheckJobID != next.ID {
					t.Fatal(current)
				}
			} else if err == nil || created {
				t.Fatal("invalid recovery admitted", mode, next, created)
			}
		})
	}
}
func TestLegacyStudyStoreRetryWriteFailureRetainsOriginalIdentity(t *testing.T) {
	for _, fault := range []string{"job", "original", "turn", "session", "receipt"} {
		t.Run(fault, func(t *testing.T) {
			s, turn, job, in := legacyAdmitted(t)
			legacyPaidCheckpoint(t, s, job, in, true)
			s.MarkJobFailed(t.Context(), job.ID, "fault")
			s.FailLegacyStudy(t.Context(), job.ID)
			var revision int
			s.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
			statements := map[string]string{
				"job":      `CREATE TRIGGER legacy_retry_fault BEFORE INSERT ON processing_jobs WHEN new.source_type='study_chat' BEGIN SELECT RAISE(FAIL,'retry failure');END`,
				"original": `CREATE TRIGGER legacy_retry_fault BEFORE UPDATE OF control_revision ON processing_jobs BEGIN SELECT RAISE(FAIL,'retry failure');END`,
				"turn":     `CREATE TRIGGER legacy_retry_fault BEFORE UPDATE OF generation_job_id ON legacy_study_turns BEGIN SELECT RAISE(FAIL,'retry failure');END`,
				"session":  `CREATE TRIGGER legacy_retry_fault BEFORE UPDATE OF revision ON study_sessions BEGIN SELECT RAISE(FAIL,'retry failure');END`,
				"receipt":  `CREATE TRIGGER legacy_retry_fault BEFORE INSERT ON legacy_study_requests BEGIN SELECT RAISE(FAIL,'retry failure');END`,
			}
			s.DB.Exec(statements[fault])
			key := uuid.NewString()
			if _, _, err := s.RetryLegacyStudy(t.Context(), job.ID, key, revision, false); err == nil {
				t.Fatal("failed retry adopted")
			}
			current, _ := s.GetLegacyStudyTurn(t.Context(), turn.ID)
			if current.GenerationJobID != job.ID {
				t.Fatal(current)
			}
			if _, _, err := s.LegacyStudyRequest(t.Context(), "retry:"+key); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			s.DB.Exec(`DROP TRIGGER legacy_retry_fault`)
			if _, _, err := s.RetryLegacyStudy(t.Context(), job.ID, key, revision, false); err != nil {
				t.Fatal("atomic rollback changed retry identity", err)
			}
		})
	}
}
