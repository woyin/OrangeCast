package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func questionStudyFixture(t *testing.T) (*Store, *QuestionStudySession, QuestionStudySource) {
	t.Helper()
	s := newTestStore(t)
	question, err := s.CreateLearningQuestion(t.Context(), LearningQuestion{Body: "主动回忆在哪些条件下有用？"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartQuestionStudySession(t.Context(), question.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(t.Context(), "条件资料", "source-secret-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	return s, session, QuestionStudySource{"document", doc.ID}
}
func reserveStudyForTest(t *testing.T, s *Store, session *QuestionStudySession, key string, sources []QuestionStudySource) (*QuestionStudyTurn, bool, error) {
	t.Helper()
	tx, err := s.DB.BeginTx(t.Context(), nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	turn, created, err := reserveQuestionStudyTurn(t.Context(), tx, session.ID, session.Revision, "我的问题", key, `{"source":"source-secret-sentinel"}`, sources)
	if err != nil {
		return nil, false, err
	}
	err = tx.Commit()
	return turn, created, err
}
func TestQuestionStudySessionIdentityCASAndPrivateHistory(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	key := uuid.NewString()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, _, err := reserveStudyForTest(t, s, session, key, []QuestionStudySource{source})
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
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate turn")
		}
		id = got
	}
	updated, err := s.GetQuestionStudySession(t.Context(), session.ID)
	if err != nil || updated.Revision != 2 {
		t.Fatal(updated, err)
	}
	if _, _, err = reserveStudyForTest(t, s, session, uuid.NewString(), []QuestionStudySource{source}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale window admitted", err)
	}
	tx, err := s.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = reserveQuestionStudyTurn(t.Context(), tx, session.ID, 1, "不同问题", key, `{}`, []QuestionStudySource{source})
	tx.Rollback()
	if !errors.Is(err, ErrConflict) {
		t.Fatal("UUID changed payload", err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	history, err := s.QuestionStudyHistory(t.Context(), session.ID)
	if err != nil || len(history) != 0 {
		t.Fatal("unreviewed answer became history", history, err)
	}
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("history mutated")
	}
	if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted',accepted_json='{"body":"已检查回答"}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	history, err = s.QuestionStudyHistory(t.Context(), session.ID)
	if err != nil || len(history) != 1 || history[0].FrozenJSON != "" || history[0].AcceptedJSON == "" {
		t.Fatal(history, err)
	}
}
func TestQuestionStudySessionSourceAndQuestionPurgeRetainsPaidFacts(t *testing.T) {
	for _, purgeQuestion := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "question"}[purgeQuestion], func(t *testing.T) {
			s, session, source := questionStudyFixture(t)
			key := uuid.NewString()
			turn, _, err := reserveStudyForTest(t, s, session, key, []QuestionStudySource{source})
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := s.EnqueueJobIdempotent(t.Context(), JobIntentSpec{SourceType: "question", SourceID: session.QuestionID, JobType: models.JobQuestionStudy, IntentID: turn.ID, InputSnapshotJSON: `{"private":"source-secret-sentinel"}`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`UPDATE question_study_turns SET generation_job_id=?,state='accepted',accepted_json='source-secret-sentinel' WHERE id=?`, job.ID, turn.ID); err != nil {
				t.Fatal(err)
			}
			receipt := models.UsageReceipt{ReceiptID: job.ID + ":generation", AttemptID: job.ID + ":response", Operation: "question_study", Provider: "pod", Model: "model", InputUnits: 31, CostKnown: true, CostCents: 3}
			if err = s.RecordUsageReceipt(t.Context(), receipt); err != nil {
				t.Fatal(err)
			}
			if purgeQuestion {
				_, err = s.DB.Exec(`DELETE FROM learning_questions WHERE id=?`, session.QuestionID)
			} else {
				_, err = s.DB.Exec(`DELETE FROM documents WHERE id=?`, source.SourceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			ex, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil || strings.Contains(ex.InputSnapshotJSON, "source-secret") || ex.CheckpointJSON != "" {
				t.Fatal(ex, err)
			}
			usage, err := s.ListRunUsage(t.Context(), job.ID)
			if err != nil || len(usage) != 1 || usage[0].InputUnits != 31 {
				t.Fatal(usage, err)
			}
			if purgeQuestion {
				if _, err = s.GetQuestionStudySession(t.Context(), session.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal(err)
				}
			} else {
				got, err := s.GetQuestionStudyTurn(t.Context(), turn.ID)
				if err != nil || !got.Purged || got.OwnerInput != "" || got.FrozenJSON != "{}" || got.AcceptedJSON != "" {
					t.Fatal(got, err)
				}
				replay, created, err := reserveStudyForTest(t, s, session, key, []QuestionStudySource{source})
				if err != nil || created || !replay.Purged {
					t.Fatal("purged replay resurrected", replay, created, err)
				}
			}
		})
	}
}
func TestQuestionStudySessionHistoricalUpgradeAndBackup(t *testing.T) {
	old, path := historicalTestStore(t, 69)
	doc, err := old.CreatePastedDocument(t.Context(), "旧资料", "旧历史正文")
	if err != nil {
		t.Fatal(err)
	}
	// Seed the v69 schema using its historic columns; current methods include
	// the v80 revision and must not pretend the old database already has it.
	legacy := &StudySessionRow{ID: uuid.NewString()}
	_, err = old.DB.ExecContext(t.Context(), `INSERT INTO study_sessions(id,source_type,source_id,title) VALUES(?,?,?,?)`, legacy.ID, models.SourceDocument, doc.ID, "旧学习对话")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.AppendStudyMessage(t.Context(), legacy.ID, "assistant", "旧回答", nil, false); err != nil {
		t.Fatal(err)
	}
	question, err := old.CreateLearningQuestion(t.Context(), LearningQuestion{Body: "学习问题"})
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM question_study_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration invented facts", count, err)
	}
	if _, err = s.GetStudySession(t.Context(), legacy.ID); err != nil {
		t.Fatal("legacy session lost", err)
	}
	session, err := s.StartQuestionStudySession(t.Context(), question.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	turn, _, err := reserveStudyForTest(t, s, session, uuid.NewString(), []QuestionStudySource{{"document", doc.ID}})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err = ConsistencyBackup(t.Context(), s.DB, copyPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetQuestionStudyTurn(t.Context(), turn.ID)
	if err != nil || got.PayloadHash != turn.PayloadHash || got.State != "queued" {
		t.Fatal(got, err)
	}
	if err = restored.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("upgrade/restore enqueued work", count, err)
	}
}

func TestQuestionStudySessionCapacityAndSessionRequestIdentity(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	key := uuid.NewString()
	first, err := s.StartQuestionStudySession(t.Context(), session.QuestionID, key)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.StartQuestionStudySession(t.Context(), session.QuestionID, key)
	if err != nil || replay.ID != first.ID {
		t.Fatal(replay, err)
	}
	if _, err = s.StartQuestionStudySession(t.Context(), "different-question", key); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		turn, created, err := reserveStudyForTest(t, s, session, uuid.NewString(), []QuestionStudySource{source})
		if err != nil || !created || turn.Ordinal != i+1 {
			t.Fatal(turn, created, err)
		}
		current, err := s.GetQuestionStudySession(t.Context(), session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = reserveStudyForTest(t, s, current, uuid.NewString(), []QuestionStudySource{source}); !errors.Is(err, ErrConflict) {
			t.Fatal("overlapping pending turn admitted", err)
		}
		if _, err = s.DB.Exec(`UPDATE question_study_turns SET state='accepted',accepted_json='{}' WHERE id=?`, turn.ID); err != nil {
			t.Fatal(err)
		}
		session = current
	}
	if _, _, err = reserveStudyForTest(t, s, session, uuid.NewString(), []QuestionStudySource{source}); !errors.Is(err, ErrConflict) {
		t.Fatal("13th turn admitted", err)
	}
}
