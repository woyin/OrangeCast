package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func embeddingJobInput(t *testing.T, s *Store, id string) (KnowledgeEmbeddingJobInput, string) {
	t.Helper()
	ex, err := s.GetJobExecution(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var in KnowledgeEmbeddingJobInput
	if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	return in, ex.InputSnapshotJSON
}
func embeddingJobRevision(t *testing.T, s *Store, id string) int {
	t.Helper()
	var revision int
	if err := s.DB.QueryRowContext(t.Context(), `SELECT control_revision FROM processing_jobs WHERE id=?`, id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestEmbeddingJobsPreflightIsIndependentAndIdempotent(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	ctx := t.Context()
	key := uuid.NewString()
	if _, _, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, "bad", cfg); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	job, created, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, key, cfg)
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	in, _ := embeddingJobInput(t, s, job.ID)
	if in.Kind != "preflight" || len(in.Inputs()) != 1 || len(in.Windows) != 0 || in.Estimate.PriceKnown {
		t.Fatal(in)
	}
	again, created, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, key, cfg)
	if err != nil || created || again.ID != job.ID {
		t.Fatal(again, created, err)
	}
	changed := cfg
	changed.Model = "different"
	if _, _, err = s.ReserveKnowledgeEmbeddingPreflight(ctx, key, changed); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	bad := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: 2, Vectors: [][]float32{{0, 0}}}
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, bad); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	good := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: 2, Vectors: [][]float32{{1, 0}}}
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, good); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if err != nil || c.Enabled || c.SemanticEnabled {
		t.Fatal(c, err)
	}
	ex, err := s.GetJobExecution(ctx, job.ID)
	if err != nil || ex.ResultState != "complete" {
		t.Fatal(ex, err)
	}
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, good); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestEmbeddingJobsContentAdmissionScopeAndCheckpointCAS(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	if _, _, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	enableEmbeddingDoc(t, s, cfg, doc)
	job, created, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	in, raw := embeddingJobInput(t, s, job.ID)
	if len(in.Inputs()) != len(in.Windows) || len(in.Windows) == 0 {
		t.Fatal(in)
	}
	again, created, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || created || again.ID != job.ID {
		t.Fatal(again, created, err)
	}
	if err = s.SaveKnowledgeEmbeddingCheckpoint(ctx, job.ID, raw+" ", "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	c, _ := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if _, err = s.ChangeKnowledgeEmbeddingScope(ctx, cfg.ID, c.Revision, false, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if ex.RemoteCallStarted || ex.CheckpointJSON != "" {
		t.Fatal("rejected admission recorded a paid call", ex)
	}
	configs, err := s.EnabledKnowledgeEmbeddingConfigs(ctx, cfg)
	if err != nil || len(configs) != 0 {
		t.Fatal(configs, err)
	}
}

func TestEmbeddingJobsKnownRecoveryPreservesPaidIdentity(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 1000000}); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	in, raw := embeddingJobInput(t, s, job.ID)
	result := fakeEmbeddingResult(cfg, in.Windows)
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordEmbeddingReceipt(ctx, job.ID, in, result); err != nil {
		t.Fatal(err)
	}
	cp := embeddingStoredCheckpoint{Version: in.Version, JobID: job.ID, InputFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), Result: result}
	encoded, _ := json.Marshal(cp)
	if err = s.SaveKnowledgeEmbeddingCheckpoint(ctx, job.ID, raw, string(encoded)); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobFailed(ctx, job.ID, "response saved; adoption interrupted"); err != nil {
		t.Fatal(err)
	}
	revision := embeddingJobRevision(t, s, job.ID)
	key := uuid.NewString()
	if _, _, err = s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision+1, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	retry, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false)
	if err != nil || !created {
		t.Fatal(retry, created, err)
	}
	recovered, recoveredRaw := embeddingJobInput(t, s, retry.ID)
	ex, _ := s.GetJobExecution(ctx, retry.ID)
	var recoveredCP embeddingStoredCheckpoint
	if err = json.Unmarshal([]byte(ex.CheckpointJSON), &recoveredCP); err != nil {
		t.Fatal(err)
	}
	if recovered.OriginJobID != job.ID || recovered.UnknownRetry || recoveredCP.JobID != retry.ID || recoveredCP.InputFingerprint != fmt.Sprintf("%x", sha256.Sum256([]byte(recoveredRaw))) {
		t.Fatal(recovered, recoveredCP)
	}
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, retry.ID, recovered, recoveredCP.Result); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordEmbeddingReceipt(ctx, recovered.OriginJobID, recovered, recoveredCP.Result); err != nil {
		t.Fatal(err)
	}
	var count int
	var cost float64
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(estimated_cost) FROM usage_records WHERE operation='embedding_content'`).Scan(&count, &cost); err != nil || count != 1 || cost != 20 {
		t.Fatal(count, cost, err)
	}
	replay, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false)
	if err != nil || created || replay.ID != retry.ID {
		t.Fatal(replay, created, err)
	}
	if _, _, err = s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), revision, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || status.IndexedWindows != len(in.Windows) {
		t.Fatal(status, err)
	}
	if next, created, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID); err != nil || created || next != nil {
		t.Fatal(next, created, err)
	}
}

func TestEmbeddingJobsUnknownRecoveryRequiresExplicitConsentAndFreshPrice(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	ctx := t.Context()
	job, _, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, uuid.NewString(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := embeddingJobInput(t, s, job.ID)
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobFailed(ctx, job.ID, "remote outcome unknown"); err != nil {
		t.Fatal(err)
	}
	revision := embeddingJobRevision(t, s, job.ID)
	key := uuid.NewString()
	if _, _, err = s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.SetModelPrice(ctx, models.ModelPrice{Provider: cfg.Provider, Model: cfg.Model, InputCentsPerMillion: 2000000}); err != nil {
		t.Fatal(err)
	}
	retry, created, err := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, true)
	if err != nil || !created {
		t.Fatal(retry, created, err)
	}
	fresh, _ := embeddingJobInput(t, s, retry.ID)
	ex, _ := s.GetJobExecution(ctx, retry.ID)
	if !fresh.UnknownRetry || fresh.OriginJobID != "" || !fresh.Estimate.PriceKnown || fresh.Estimate.InputCentsPerMillion != 2000000 || ex.CheckpointJSON != "" || ex.RemoteCallStarted {
		t.Fatal(fresh, ex)
	}
	if _, _, err = s.RetryKnowledgeEmbeddingJob(ctx, job.ID, key, revision, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	result := &provider.EmbeddingResult{Model: cfg.Model, InputTokens: 30, UsageKnown: false}
	if err = s.RecordEmbeddingReceipt(ctx, retry.ID, fresh, result); err != nil {
		t.Fatal(err)
	}
	var cost sql.NullFloat64
	var known bool
	if err = s.DB.QueryRowContext(ctx, `SELECT estimated_cost,units_known FROM usage_records WHERE receipt_id=?`, retry.ID+":embedding_preflight").Scan(&cost, &known); err != nil || cost.Valid || known {
		t.Fatal(cost, known, err)
	}
}

func TestEmbeddingJobsRejectCorruptedPaidCheckpoint(t *testing.T) {
	for _, mutation := range []string{"json", "job", "version", "fingerprint", "vector"} {
		t.Run(mutation, func(t *testing.T) {
			s, cfg, _ := embeddingIndexFixture(t)
			ctx := t.Context()
			job, _, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, uuid.NewString(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			in, raw := embeddingJobInput(t, s, job.ID)
			cp := embeddingStoredCheckpoint{Version: in.Version, JobID: job.ID, InputFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), Result: &provider.EmbeddingResult{Model: cfg.Model, Dimensions: cfg.Dimensions, Vectors: [][]float32{{1, 0}}}}
			switch mutation {
			case "job":
				cp.JobID = uuid.NewString()
			case "version":
				cp.Version = "old"
			case "fingerprint":
				cp.InputFingerprint = "other"
			case "vector":
				cp.Result.Vectors = [][]float32{{0, 0}}
			}
			b, _ := json.Marshal(cp)
			if mutation == "json" {
				b = []byte("{")
			}
			if err = s.SaveKnowledgeEmbeddingCheckpoint(ctx, job.ID, raw, string(b)); err != nil {
				t.Fatal(err)
			}
			if err = s.MarkJobFailed(ctx, job.ID, "invalid supplier checkpoint"); err != nil {
				t.Fatal(err)
			}
			revision := embeddingJobRevision(t, s, job.ID)
			if _, _, err = s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), revision, true); !errors.Is(err, ErrInvalidEditorialState) {
				t.Fatal(err)
			}
			if embeddingJobRevision(t, s, job.ID) != revision {
				t.Fatal("invalid paid checkpoint consumed retry CAS")
			}
			var count int
			if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE source_type='knowledge_index'`).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestEmbeddingJobsClosedStoreReportsFailure(t *testing.T) {
	s, cfg, _ := embeddingIndexFixture(t)
	ctx := t.Context()
	job, _, err := s.ReserveKnowledgeEmbeddingPreflight(ctx, uuid.NewString(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	in, raw := embeddingJobInput(t, s, job.ID)
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	result := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: cfg.Dimensions, Vectors: [][]float32{{1, 0}}}
	checks := []struct {
		name string
		call func() error
	}{
		{"reservation", func() error { _, _, e := s.ReserveKnowledgeEmbeddingPreflight(ctx, uuid.NewString(), cfg); return e }},
		{"batch", func() error { _, _, e := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID); return e }},
		{"dispatch", func() error { return s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in) }},
		{"adoption", func() error { return s.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, result) }},
		{"receipt", func() error { return s.RecordEmbeddingReceipt(ctx, job.ID, in, result) }},
		{"enabled configs", func() error { _, e := s.EnabledKnowledgeEmbeddingConfigs(ctx, cfg); return e }},
		{"retry", func() error {
			_, _, e := s.RetryKnowledgeEmbeddingJob(ctx, job.ID, uuid.NewString(), 1, true)
			return e
		}},
		{"checkpoint", func() error { return s.SaveKnowledgeEmbeddingCheckpoint(ctx, job.ID, raw, "checkpoint") }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if e := check.call(); e == nil {
				t.Fatal("closed database falsely accepted operation")
			}
		})
	}
}

func TestEmbeddingJobsIndexAuthorizationDoesNotRequireSemanticEnablement(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	enableEmbeddingDoc(t, s, cfg, doc)
	configs, err := s.EnabledKnowledgeEmbeddingConfigs(t.Context(), cfg)
	if err != nil || len(configs) != 1 || !configs[0].Enabled || configs[0].SemanticEnabled {
		t.Fatal(configs, err)
	}
	route := cfg
	route.Model = "different"
	if configs, err = s.EnabledKnowledgeEmbeddingConfigs(t.Context(), route); err != nil || len(configs) != 0 {
		t.Fatal(configs, err)
	}
	route = cfg
	route.Dimensions = 3
	if configs, err = s.EnabledKnowledgeEmbeddingConfigs(t.Context(), route); err != nil || len(configs) != 0 {
		t.Fatal(configs, err)
	}
}
