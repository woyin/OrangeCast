package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type countedTranscriber struct{ calls int }

func (f *countedTranscriber) Name() string { return "fixture-asr" }
func (f *countedTranscriber) Transcribe(string) (*provider.TranscriptResult, error) {
	f.calls++
	return &provider.TranscriptResult{Text: "Owner理解", Model: "actual-asr", Usage: provider.TaskUsage{InputUnits: 12, OutputUnits: 3}, Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 1, Text: "Owner理解"}}}, nil
}

func TestTranscriptionPaidResponseRecoveryAndAtomicApplication(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	source := seedEpisode(t, s)
	seedEvidence(t, s, w, models.SourceEpisode, source)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	f := &countedTranscriber{}
	bundle := &provider.ProviderBundle{Transcription: f}
	_, err = s.DB.Exec(`CREATE TRIGGER reject_analysis BEFORE INSERT ON processing_jobs WHEN NEW.job_type='analyze' BEGIN SELECT RAISE(ABORT,'simulated disk failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.doTranscribe(ctx, job, bundle); err == nil {
		t.Fatal("application should fail")
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var cp transcriptionCheckpoint
	if json.Unmarshal([]byte(ex.CheckpointJSON), &cp) != nil || cp.Model != "actual-asr" || cp.Usage.InputUnits != 12 {
		t.Fatalf("paid response missing: %+v", cp)
	}
	versions, err := s.ListArtifactVersions(ctx, models.SourceEpisode, source, store.KindTranscript)
	if err != nil || len(versions) != 0 {
		t.Fatalf("half committed transcript: %v %v", versions, err)
	}
	s.DB.Exec(`DROP TRIGGER reject_analysis`)
	if err = w.doTranscribe(ctx, job, bundle); err != nil {
		t.Fatal(err)
	}
	if err = w.doTranscribe(ctx, job, bundle); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("replayed charged response: %d", f.calls)
	}
	versions, _ = s.ListArtifactVersions(ctx, models.SourceEpisode, source, store.KindTranscript)
	var jobs, receipts int
	s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='analyze'`).Scan(&jobs)
	s.DB.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":transcription").Scan(&receipts)
	if len(versions) != 1 || jobs != 1 || receipts != 1 {
		t.Fatalf("duplicate application: versions=%d jobs=%d receipts=%d", len(versions), jobs, receipts)
	}
}

func TestTranscriptionStartedWithoutCheckpointNeverReplays(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	source := seedEpisode(t, s)
	seedEvidence(t, s, w, models.SourceEpisode, source)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	s.MarkJobRemoteCallStarted(ctx, job.ID)
	f := &countedTranscriber{}
	err := w.doTranscribe(ctx, job, &provider.ProviderBundle{Transcription: f})
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if err == nil || f.calls != 0 || ex.ResultState != models.JobResultUnknown {
		t.Fatalf("unknown replay: calls=%d ex=%+v err=%v", f.calls, ex, err)
	}
}

func TestTranscriptionCheckpointRejectsAudioChangeAndKeepsFacts(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	source := seedEpisode(t, s)
	seedEvidence(t, s, w, models.SourceEpisode, source)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	f := &countedTranscriber{}
	if err := w.doTranscribe(ctx, job, &provider.ProviderBundle{Transcription: f}); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var cp transcriptionCheckpoint
	json.Unmarshal([]byte(ex.CheckpointJSON), &cp)
	cp.AudioSHA256 = "different"
	raw, _ := json.Marshal(cp)
	s.SaveJobCheckpoint(ctx, job.ID, string(raw))
	err := w.doTranscribe(ctx, job, &provider.ProviderBundle{Transcription: f})
	if err == nil || !strings.Contains(err.Error(), "不一致") || f.calls != 1 {
		t.Fatalf("tampered replay: %v %d", err, f.calls)
	}
}
