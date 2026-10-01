package store

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeQueryEmbeddingRequiresAuthorizedVectors(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	if _, _, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用"); err == nil {
		t.Fatal("disabled query admitted")
	}
	enableEmbeddingDoc(t, s, cfg, doc)
	if _, _, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用"); err == nil {
		t.Fatal("unindexed query admitted")
	}
	var jobs int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("query enqueued without usable vectors")
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{" ", strings.Repeat("甲", 201), string([]byte{255})} {
		if _, _, err = s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, q); err == nil {
			t.Fatal("invalid query admitted")
		}
	}
	if _, err = s.KnowledgeQueryEmbedding(ctx, cfg.ID, "如何应用"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestKnowledgeQueryEmbeddingStableIdentityAndCache(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, _ := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if _, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, _, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "  HOW\n to APPLY  ")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		} else if first != id {
			t.Fatal("concurrent query duplicated")
		}
	}
	ex, err := s.GetJobExecution(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	var in KnowledgeEmbeddingJobInput
	if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	if in.Query != "how to apply" || in.Estimate.OutputTokens != 0 || in.Estimate.InputFingerprint == "" {
		t.Fatal(in)
	}
	claimed, e := s.ClaimNextJob(ctx, "60 seconds")
	if e != nil || claimed == nil || claimed.ID != first {
		t.Fatal(claimed, e)
	}
	result := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: 2, Vectors: [][]float32{{1, 0}}, InputTokens: 3, UsageKnown: true}
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, first, in, result); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobSucceeded(ctx, first); err != nil {
		t.Fatal(err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	vector, err := s.KnowledgeQueryEmbedding(ctx, cfg.ID, "HOW to apply")
	if err != nil || len(vector) != 2 || vector[0] != 1 {
		t.Fatal(vector, err)
	}
	job, err := s.FindKnowledgeQueryEmbeddingJob(ctx, cfg.ID, "how to apply")
	if err != nil || job.ID != first {
		t.Fatal(job, err)
	}
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before {
		t.Fatal("cache GET writes")
	}
	replay, created, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "how to apply")
	if err != nil || created || replay.ID != first {
		t.Fatal("cached query recreated", err)
	}
	key := uuid.NewString()
	request, _, err := s.ReserveKnowledgeQueryEmbeddingRequest(ctx, cfg.ID, "how to apply", key)
	if err != nil || request.ID != first {
		t.Fatal(err)
	}
	if _, _, err = s.ReserveKnowledgeQueryEmbeddingRequest(ctx, cfg.ID, "a different query", key); !errors.Is(err, ErrConflict) {
		t.Fatal("lost acknowledgement edited identity allowed", err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_query_embeddings SET expires_at=datetime('now','-1 second')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.KnowledgeQueryEmbedding(ctx, cfg.ID, "how to apply"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired cache returned", err)
	}
	replacement, created, err := s.ReserveKnowledgeQueryEmbedding(ctx, cfg.ID, "how to apply")
	if err != nil || !created || replacement.ID == first {
		t.Fatal("explicit expired cache cannot be renewed", err)
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	if _, err = s.KnowledgeQueryEmbedding(ctx, cfg.ID, "how to apply"); err == nil {
		t.Fatal("withdrawn cache survived")
	}
	var caches int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM knowledge_query_embeddings`).Scan(&caches); err != nil || caches != 0 {
		t.Fatal("epoch did not erase query vector", err)
	}
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, replacement.ID, in); err == nil {
		t.Fatal("old epoch call allowed")
	}
}
