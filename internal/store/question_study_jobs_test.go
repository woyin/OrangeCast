package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
)

func TestQuestionStudyGenerationConcurrentAdmissionAndReplay(t *testing.T) {
	s, session, source := questionStudyFixture(t)
	confirmStudySource(t, s, session.QuestionID, source.SourceID, "link")
	cfg, err := provider.NewSelector("", "").WithPod("test-secret", "https://pod.example/v1", "generate").QuestionStudy("generate", "review")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, job, _, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "原问题", key, nil, cfg.Config())
			if err != nil {
				failures <- err
				return
			}
			ids <- job.ID
		}()
	}
	group.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate task")
		}
		id = got
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='question_study'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err = s.DB.Exec(`UPDATE documents SET model_data_policy='local_only' WHERE id=?`, source.SourceID); err != nil {
		t.Fatal(err)
	}
	turn, replay, created, err := s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "原问题", key, nil, cfg.Config())
	if err != nil || created || replay.ID != id || turn.OwnerInput != "原问题" {
		t.Fatal(turn, replay, created, err)
	}
	if _, _, _, err = s.SubmitQuestionStudyTurn(t.Context(), session.ID, 1, "修改问题", key, nil, cfg.Config()); !errors.Is(err, ErrConflict) {
		t.Fatal("changed payload reused command", err)
	}
}
