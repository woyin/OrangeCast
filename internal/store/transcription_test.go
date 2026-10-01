package store

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestSourceTranscriptionApplicationIsAtomicAndIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	source := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='{"depth":"deep"}' WHERE id=?`, job.ID)
	result := &provider.TranscriptResult{Text: "带位置的节目转录", Segments: []provider.Segment{{ID: "s1", Start: 0, End: 2, Text: "带位置的节目转录"}}}
	s.DB.Exec(`CREATE TRIGGER fail_continuation BEFORE INSERT ON processing_jobs WHEN NEW.job_type='analyze' BEGIN SELECT RAISE(ABORT,'disk full'); END`)
	if err := s.CommitSourceTranscription(ctx, job, "groq", "timestamp-model", result); err == nil {
		t.Fatal("partial commit accepted")
	}
	var count int
	s.DB.QueryRow(`SELECT count(*) FROM artifact_versions WHERE source_id=?`, source).Scan(&count)
	if count != 0 {
		t.Fatal("transcript escaped rollback")
	}
	s.DB.Exec(`DROP TRIGGER fail_continuation`)
	for i := 0; i < 2; i++ {
		if err := s.CommitSourceTranscription(ctx, job, "groq", "timestamp-model", result); err != nil {
			t.Fatal(err)
		}
	}
	s.DB.QueryRow(`SELECT count(*) FROM artifact_versions WHERE source_id=?`, source).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate transcript", count)
	}
	var snapshot string
	s.DB.QueryRow(`SELECT input_snapshot_json FROM processing_jobs WHERE job_type='analyze' AND source_id=?`, source).Scan(&snapshot)
	if snapshot != `{"depth":"deep"}` {
		t.Fatal("depth lost", snapshot)
	}
	if err := s.CommitSourceTranscription(ctx, job, "groq", "model", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err := s.CommitSourceTranscription(ctx, job, "openai", "plain-text-model", &provider.TranscriptResult{Text: "without timestamps"}); err == nil {
		t.Fatal("invented timestamps")
	}
	other := *job
	other.SourceType = "voice_note"
	if err := s.CommitSourceTranscription(ctx, &other, "groq", "model", result); err == nil {
		t.Fatal("personal recording became evidence")
	}
	other = *job
	other.ID = "missing"
	if err := s.CommitSourceTranscription(ctx, &other, "groq", "model", result); err == nil {
		t.Fatal("missing task accepted")
	}
	// Simulate a separate started attempt without a known response.
	s.DB.Exec(`UPDATE processing_jobs SET result_state='unknown' WHERE id=?`, job.ID)
	if err := s.CommitSourceTranscription(ctx, job, "groq", "model", result); err == nil {
		t.Fatal("unknown response applied")
	}
	if ptrValue(nil) != "" {
		t.Fatal("nil setting")
	}
	v := "model"
	if ptrValue(&v) != v {
		t.Fatal("model ignored")
	}
}

func TestSourceASRAudioEstimateFreezesPriceAndSharesBudget(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	source := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='{"depth":"deep"}' WHERE id=?`, job.ID)
	if _, err := s.FreezeSourceASREstimate(ctx, job.ID, "groq", "model", "hash", math.NaN()); err == nil {
		t.Fatal("NaN duration")
	}
	if _, err := s.FreezeSourceASREstimate(ctx, "missing", "groq", "model", "hash", 60); err == nil {
		t.Fatal("missing task")
	}
	s.SetASRAudioPrice(ctx, "groq", "model", 2)
	in, err := s.FreezeSourceASREstimate(ctx, job.ID, "groq", "model", "hash", 1800)
	if err != nil {
		t.Fatal(err)
	}
	s.SetASRAudioPrice(ctx, "groq", "model", 200)
	frozen, err := s.FreezeSourceASREstimate(ctx, job.ID, "groq", "model", "hash", 2)
	if err != nil || frozen.CentsPerMinute != 2 || frozen.DurationSeconds != 1800 {
		t.Fatal(frozen, err)
	}
	if _, err = s.FreezeSourceASREstimate(ctx, job.ID, "groq", "model", "different", 1800); !errors.Is(err, ErrConflict) {
		t.Fatal("changed audio", err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var raw map[string]any
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &raw)
	if raw["depth"] != "deep" {
		t.Fatal("input overwritten")
	}
	if r, e := s.HoldSourceASRBudget(ctx, job.ID, in); e != nil || r != nil {
		t.Fatal("no configured budget", r, e)
	}
	budget := int64(100)
	s.SetOwnerMonthlyBudget(ctx, &budget)
	r, err := s.HoldSourceASRBudget(ctx, job.ID, in)
	if err != nil || r.EstimatedCostCents != 60 {
		t.Fatal(r, err)
	}
	if _, err = s.HoldSourceASRBudget(ctx, job.ID, in); err != nil {
		t.Fatal("duplicate hold", err)
	}
	for _, bad := range []VoiceASRInput{{}, {DurationSeconds: math.Inf(1), EstimateVersion: "voice-audio-minute-v1"}, {DurationSeconds: 60, EstimateVersion: "voice-audio-minute-v1"}, {DurationSeconds: 60, PriceKnown: true, CentsPerMinute: math.MaxFloat64, EstimateVersion: "voice-audio-minute-v1"}} {
		if _, err = s.HoldSourceASRBudget(ctx, "other", bad); err == nil {
			t.Fatal("invalid or unpriced estimate", bad)
		}
	}
}
