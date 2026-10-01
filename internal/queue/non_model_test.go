package queue

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestNonModelJobDoesNotResolveProviderOrHoldBudget(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, err := s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status)VALUES(?,'learning_export','export','learning_export','queued')`, id); err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("export resolved Provider")
		return nil, nil
	}
	w.taskConfigFor = func(*models.ProcessingJob) (provider.TaskConfig, error) {
		t.Fatal("export resolved paid configuration")
		return provider.TaskConfig{}, nil
	}
	called := 0
	w.WithLearningExporter(func(ctx context.Context, j *models.ProcessingJob) error {
		called++
		return s.SaveJobResult(ctx, j.ID, `{"manifest_hash":"local"}`, models.JobResultComplete)
	})
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, id)
	if err != nil || j.Status != models.StatusSucceeded || called != 1 {
		t.Fatal(j, err, called)
	}
	var budget, usage int
	if err = s.DB.QueryRow(`SELECT count(*) FROM budget_reservations WHERE job_id=?`, id).Scan(&budget); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE attempt_id=?`, id).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if budget != 0 || usage != 0 {
		t.Fatal(budget, usage)
	}
	if _, err = s.DB.Exec(`UPDATE processing_jobs SET status='queued' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil || called != 1 {
		t.Fatal("recovery reran assembler", called, err)
	}
}
