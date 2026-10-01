package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLearningRunLanesAdmissionAndClaims(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	for _, v := range []struct{ source, job, lane string }{{"question_study", "question_study", "study"}, {"study_chat", "question_study", "study"}, {"knowledge_index", "knowledge_embedding", "index"}, {"learning_export", "learning_export", "export"}} {
		id := uuid.NewString()
		_, err := s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status)VALUES(?,?,?,?, 'queued')`, id, v.source, id, v.job)
		if err != nil {
			t.Fatal(err)
		}
		runs, _, err := s.ListRuns(ctx, "queued", v.lane, 0)
		if err != nil || len(runs) == 0 {
			t.Fatal(runs, err)
		}
		c, err := s.GetRunControl(ctx, "lane", v.lane)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ChangeRunControl(ctx, "lane", v.lane, "pause", "暂不处理", uuid.NewString(), c.Revision, 0); err != nil {
			t.Fatal(err)
		}
		if err = s.CheckRunControl(ctx, id); !errors.Is(err, ErrRunControlled) {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec(`INSERT INTO processing_jobs(id,source_type,source_id,job_type,status)VALUES(?,?,?,?, 'queued')`, uuid.NewString(), v.source, "new", v.job); err == nil {
			t.Fatal("paused admission")
		}
		if job, err := s.ClaimNextJob(ctx, "+1 minute"); err != nil || job != nil {
			t.Fatal("paused claim", job, err)
		}
		if err = s.ChangeRunControl(ctx, "lane", v.lane, "resume", "继续", uuid.NewString(), c.Revision+1, 0); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimNextJob(ctx, "+1 minute")
		if err != nil || job.ID != id {
			t.Fatal(job, err)
		}
		if err = s.MarkJobSucceeded(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}
