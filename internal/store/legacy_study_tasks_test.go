package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"testing"
)

func legacyFixture(t *testing.T) (*Store, string, provider.QuestionStudyConfig) {
	t.Helper()
	s := newTestStore(t)
	id := seedIntentEpisode(t, s, "legacy-study")
	seedSnapshotTranscript(t, s, models.SourceEpisode, id, "通胀是物价上升")
	return s, id, provider.QuestionStudyConfig{Provider: "groq", ConnectionID: strings.Repeat("a", 64), GenerationModel: "qa", ReviewModel: "qa"}
}
func TestLegacyStudyAdmissionIdentityScopeAndHistory(t *testing.T) {
	s, id, cfg := legacyFixture(t)
	key := uuid.NewString()
	turn, job, created, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "解释通胀", key, cfg)
	if err != nil || !created {
		t.Fatal(turn, job, created, err)
	}
	again, reused, created, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "解释通胀", key, cfg)
	if err != nil || created || again.ID != turn.ID || reused.ID != job.ID {
		t.Fatal(again, reused, created, err)
	}
	_, _, _, err = s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "不同问题", key, cfg)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	_, _, _, err = s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, turn.SessionID, 2, "第二个问题", uuid.NewString(), cfg)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("parallel turn admitted", err)
	}
	messages, _ := s.ListStudyMessages(t.Context(), turn.SessionID, true)
	if len(messages) != 1 {
		t.Fatal(messages)
	}
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	var in LegacyStudyJobInput
	if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil || in.Scope.Question != "解释通胀" || in.SnapshotID == "" || in.ContentVersionID == "" || in.Config.Provider != "groq" {
		t.Fatal(in)
	}
	var questions int
	s.DB.QueryRow(`SELECT count(*) FROM learning_questions`).Scan(&questions)
	if questions != 0 {
		t.Fatal("fabricated learning question", questions)
	}
}
func TestLegacyStudyForeignSessionAndLocalOnlyFailBeforeDispatch(t *testing.T) {
	s, id, cfg := legacyFixture(t)
	other := seedIntentEpisode(t, s, "foreign-study")
	session, _ := s.CreateStudySession(t.Context(), models.SourceEpisode, other, "foreign")
	_, _, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, session.ID, 1, "通胀", uuid.NewString(), cfg)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, id)
	_, _, _, err = s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), cfg)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM legacy_study_turns`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func TestLegacyStudyPurgeDeletesBodyAndStopsTasks(t *testing.T) {
	s, id, cfg := legacyFixture(t)
	turn, job, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	var in LegacyStudyJobInput
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
	if _, err = s.DB.Exec(`UPDATE source_snapshots SET status='purged' WHERE id=?`, in.SnapshotID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetLegacyStudyTurn(t.Context(), turn.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ex, _ = s.GetJobExecution(t.Context(), job.ID)
	if ex.InputSnapshotJSON != "{}" || ex.CheckpointJSON != "" {
		t.Fatal(ex)
	}
}
func TestLegacyStudyConcurrentCommandAndBoundedHistoricContext(t *testing.T) {
	s, id, cfg := legacyFixture(t)
	session, _ := s.CreateStudySession(t.Context(), models.SourceEpisode, id, "legacy")
	for i := 0; i < 20; i++ {
		s.AppendStudyMessage(t.Context(), session.ID, "user", fmt.Sprintf("historic-%02d", i), nil, false)
	}
	s.AppendStudyMessage(t.Context(), session.ID, "assistant", "suppressed-private", nil, true)
	key := uuid.NewString()
	results := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		go func() {
			turn, _, _, err := s.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, session.ID, 1, "通胀", key, cfg)
			if turn != nil {
				ids <- turn.ID
			}
			results <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	close(ids)
	first := ""
	for id := range ids {
		if first != "" && id != first {
			t.Fatal("duplicate logical turns", id, first)
		}
		first = id
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM legacy_study_turns`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	_, job, err := s.LegacyStudyRequest(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(t.Context(), job.ID)
	var in LegacyStudyJobInput
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
	if len(in.Scope.History) != 6 || in.Scope.History[0].Content != "historic-14" || in.Scope.History[5].Content != "historic-19" {
		t.Fatal(in.Scope.History)
	}
	var usage int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records`).Scan(&usage)
	if usage != 0 {
		t.Fatal("historic usage fabricated", usage)
	}
}
