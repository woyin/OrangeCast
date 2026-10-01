package queue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/store"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type controlledKnowledgeFake struct {
	base  provider.KnowledgeArticleProvider
	stop  func()
	calls int
}

func (f *controlledKnowledgeFake) Name() string { return "pod" }
func (f *controlledKnowledgeFake) KnowledgeArticleStep(ctx context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	result, usage, err := f.base.KnowledgeArticleStep(ctx, req)
	f.stop()
	return result, usage, err
}
func TestRunStopDuringPaidResponsePreservesCheckpointAndDoesNotApply(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "pause"}[pause], func(t *testing.T) {
			s, w, article, base, _ := seedKnowledgeQueue(t)
			ctx := t.Context()
			jobs, _ := s.ListQueuedOrRunning(ctx)
			id := jobs[0].ID
			fake := &controlledKnowledgeFake{base: base, stop: func() {
				kind, target, action, rev := "job", id, "stop", 2
				if pause {
					kind, target, action, rev = "direction", "knowledge_article:"+article.ID, "pause", 1
				}
				if err := s.ChangeRunControl(ctx, kind, target, action, "响应到达前停止", uuid.NewString(), rev, 0); err != nil {
					t.Fatal(err)
				}
			}}
			w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
			})
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			ex, _ := s.GetJobExecution(ctx, id)
			j, _ := s.GetJob(ctx, id)
			v, _ := s.GetKnowledgeArticle(ctx, article.ID)
			if ex.CheckpointJSON == "" || !ex.RemoteCallStarted || j.Status != "failed" || v.WorkingRevision != 0 || fake.calls != 1 {
				t.Fatal(ex, j, v, fake.calls)
			}
			var n int
			s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE attempt_id=?`, id).Scan(&n)
			if n != 1 {
				t.Fatal(n)
			}
			if err := s.ResetRunningOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if fake.calls != 1 {
				t.Fatal("replayed paid response")
			}
			if pause {
				if err := s.ChangeRunControl(ctx, "direction", "knowledge_article:"+article.ID, "resume", "核对后明确恢复", uuid.NewString(), 2, 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RetryKnowledgeArticle(ctx, article.ID); err != nil {
				t.Fatal(err)
			}
			w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{KnowledgeArticle: base}, nil
			})
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			if base.calls != 1 {
				t.Fatal("known checkpoint retried remote")
			}
		})
	}
}
func TestRunStopWeeklyResponseDoesNotCreateQuestions(t *testing.T) {
	s, w, b, base := seedReviewQueue(t)
	ctx := t.Context()
	f := &controlledKnowledgeFake{base: base, stop: func() {
		if err := s.ChangeRunControl(ctx, "job", b.JobID, "stop", "不采用这次生成", uuid.NewString(), 2, 0); err != nil {
			t.Fatal(err)
		}
	}}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: f}, nil
	})
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	items, _ := s.ListLearningReviewItems(ctx, b.ID)
	ex, _ := s.GetJobExecution(ctx, b.JobID)
	if len(items) != 0 || ex.CheckpointJSON == "" {
		t.Fatal(items, ex)
	}
	// A stopped known response remains an explicit recovery, rather than free automatic replay.
	if err := s.RetryLearningReview(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: base}, nil
	})
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if base.calls != 1 {
		t.Fatal(base.calls)
	}
}

func TestRunStopVoiceResponseDoesNotApplySuggestion(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	var e error
	d, e = s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
	if e != nil {
		t.Fatal(e)
	}
	job, _ := s.GetJob(ctx, d.JobID)
	if ok, err := s.MarkJobRunning(ctx, job.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Saving the supplier response and its receipt remains possible after stop.
	if err := s.ChangeRunControl(ctx, "job", job.ID, "stop", "保留文字，不采用迟到转写", uuid.NewString(), 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobCheckpoint(ctx, job.ID, `{"paid_response":"kept"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: job.ID + ":transcription", AttemptID: job.ID + ":response", Operation: "transcription"}); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var in store.VoiceASRInput
	if err := json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	if err := w.commitVoiceResponse(ctx, job.ID, in, "迟到转写"); !errors.Is(err, store.ErrRunControlled) {
		t.Fatal(err)
	}
	after, _ := s.GetVoiceNoteDraft(ctx, d.ID)
	if after.ASRText != "" || after.Text != d.Text {
		t.Fatal(after)
	}
}
