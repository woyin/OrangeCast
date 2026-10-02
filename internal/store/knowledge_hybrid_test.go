package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/evalset"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// Synthetic unit vectors exercise the ranking adapter only. They do not measure
// model quality, and no Recall@10 improvement is derived from this fixture.
func cacheSyntheticQuery(t *testing.T, s *Store, cfg provider.EmbeddingConfig, query string) {
	t.Helper()
	job, _, err := s.ReserveKnowledgeQueryEmbedding(t.Context(), cfg.ID, query)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status == models.StatusSucceeded {
		return
	}
	claimed, err := s.ClaimNextJob(t.Context(), "60 seconds")
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatal(claimed, err)
	}
	ex, err := s.GetJobExecution(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var in KnowledgeEmbeddingJobInput
	if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	result := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: cfg.Dimensions, Vectors: [][]float32{{1, 0}}, UsageKnown: true, InputTokens: 1}
	if err = s.CommitKnowledgeEmbeddingResponse(t.Context(), job.ID, in, result); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobSucceeded(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeHybridRetrieveRRFAndFilters(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "我的解释仍需反例"})
	if err != nil {
		t.Fatal(err)
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	cacheSyntheticQuery(t, s, cfg, "反例")
	request := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "反例", SourceType: "document", SourceID: doc.ID}, Semantic: true, EmbeddingConfigID: cfg.ID}
	before := 0
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	cold, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || cold.Method != "rrf" || cold.SemanticCount != 3 || cold.IndexedCount != 3 || cold.Total != 3 || cold.Coverage == "" {
		t.Fatalf("%+v %v", cold, err)
	}
	hot, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || !reflect.DeepEqual(cold, hot) {
		t.Fatal("cold/hot mismatch", cold, hot, err)
	}
	after := 0
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before {
		t.Fatal("ranking wrote or queued")
	}
	seen := map[string]bool{}
	both, semantic := 0, 0
	for _, hit := range cold.Hits {
		if seen[hit.Key] || hit.Revision < 1 {
			t.Fatal("duplicate or unversioned hit")
		}
		seen[hit.Key] = true
		if hit.MatchKind == "both" {
			both++
		}
		if hit.MatchKind == "semantic" {
			semantic++
		}
		if !strings.Contains(hit.Reason, "不代表事实支持") && hit.MatchKind != "lexical" {
			t.Fatal("similarity claimed evidence confidence", hit)
		}
	}
	if both != 2 || semantic != 1 {
		t.Fatal("match explanation incorrect", both, semantic)
	}
	request.Search.Kind = "owner_reflection"
	notes, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || notes.Total != 1 || notes.Hits[0].ObjectID != note.ID {
		t.Fatal(notes, err)
	}
	request.Search.Kind = ""
	request.Search.Page = 2
	request.Search.PerPage = 1
	page, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || len(page.Hits) != 1 || page.Total != 3 || page.Hits[0].Key != cold.Hits[1].Key {
		t.Fatal(page, err)
	}
	request.Search.Page = 1
	request.Search.PerPage = 20
	request.Search.Theme = "适用条件"
	themed, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || themed.Total != 1 {
		t.Fatal(themed, err)
	}
	request.Search.Theme = ""
	request.Search.From = "2100-01-01"
	dated, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || dated.Method != "fts" || dated.Total != 0 {
		t.Fatal(dated, err)
	}
}

func TestKnowledgeHybridRetrieveDegradationAndProviderPurpose(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	request := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "反例"}, Semantic: true, EmbeddingConfigID: cfg.ID}
	fallback, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || fallback.Method != "fts" || fallback.Degradation == "" {
		t.Fatal(fallback, err)
	}
	windows, _ := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	cacheSyntheticQuery(t, s, cfg, "反例")
	hot, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || hot.Method != "rrf" {
		t.Fatal(hot, err)
	}
	// Warm matrix must not conceal repaired/corrupt dimensions.
	if _, err = s.DB.Exec(`UPDATE knowledge_embedding_vectors SET dimensions=3`); err != nil {
		t.Fatal(err)
	}
	wrong, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || wrong.Method != "fts" || !strings.Contains(wrong.Degradation, "不合格") {
		t.Fatal(wrong, err)
	}
	if _, err = s.DB.Exec(`UPDATE knowledge_embedding_vectors SET dimensions=2`); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataApprovedProvidersOnly); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSourceApprovedProviders(ctx, models.SourceDocument, doc.ID, []string{cfg.Provider}); err != nil {
		t.Fatal(err)
	}
	windows, _ = s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	cacheSyntheticQuery(t, s, cfg, "反例")
	remote := request
	remote.Purpose = RetrieveExternal
	remote.Search.SendProvider = "pod"
	denied, err := s.EvaluateKnowledgeRetrieval(ctx, remote)
	if err != nil || denied.Total != 0 {
		t.Fatal("embedding authorization leaked text-provider material", denied, err)
	}
	allowed, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || allowed.Method != "rrf" {
		t.Fatal(allowed, err)
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || withdrawn.Method != "fts" || withdrawn.Total == 0 {
		t.Fatal("withdrawal must retain Owner FTS", withdrawn, err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceDocument, doc.ID); err != nil {
		t.Fatal(err)
	}
	purged, err := s.EvaluateKnowledgeRetrieval(ctx, request)
	if err != nil || purged.Total != 0 || purged.Method != "fts" {
		t.Fatal("purged matrix resurrected content", purged, err)
	}
}

func TestKnowledgeHybridRetrieveFortyFrozenQueriesHaveStableAdapterResults(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	client, _ := provider.NewEmbeddingClient("fake", "https://synthetic.example/v1", "synthetic-unit-vectors", 2)
	cfg := client.Config()
	if err := s.RegisterKnowledgeEmbeddingConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	corpus := evalset.LearningV4Corpus()
	sourceMap := map[string]*models.Document{}
	segmentMap := map[string]string{}
	var refs []EmbeddingSource
	for _, episode := range corpus.Episodes {
		var paragraphs []string
		for _, seg := range episode.Segments {
			paragraphs = append(paragraphs, seg.Text)
		}
		doc, err := s.CreatePastedDocument(ctx, episode.Title, strings.Join(paragraphs, "\n\n"))
		if err != nil {
			t.Fatal(err)
		}
		sourceMap[episode.ID] = doc
		documentSegments := DocumentSegments(doc)
		for i, seg := range episode.Segments {
			segmentMap[seg.ID] = documentSegments[i].ID
		}
		refs = append(refs, EmbeddingSource{"document", doc.ID})
	}
	for _, note := range corpus.Notes {
		doc := sourceMap[note.EpisodeID]
		value := models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: note.Kind, Content: note.Content}
		references := jsonString([]string{segmentMap[note.Segments[0]]})
		if note.Kind == "source_note" {
			value.CitationsJSON = references
		} else {
			value.ReferencesJSON = references
		}
		if _, err := s.CreateOwnerNote(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ChangeKnowledgeEmbeddingScope(ctx, cfg.ID, 1, true, refs); err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 5; batch++ {
		windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(windows) == 0 {
			break
		}
		if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range evalset.LearningV4RetrievalCases() {
		t.Run(tc.ID, func(t *testing.T) {
			cacheSyntheticQuery(t, s, cfg, tc.Query)
			request := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: tc.Query}, Semantic: true, EmbeddingConfigID: cfg.ID}
			s.embeddingMatrixMu.Lock()
			s.embeddingMatrix = nil
			s.embeddingMatrixMu.Unlock()
			cold, err := s.EvaluateKnowledgeRetrieval(ctx, request)
			if err != nil || cold.Method != "rrf" || cold.IndexedCount != 29 {
				t.Fatal(cold, err)
			}
			hot, err := s.EvaluateKnowledgeRetrieval(ctx, request)
			if err != nil || !reflect.DeepEqual(cold, hot) {
				t.Fatal("frozen query cold/hot changed", err)
			}
			seen := map[string]bool{}
			for _, hit := range hot.Hits {
				if seen[hit.Key] {
					t.Fatal("duplicate stable identity")
				}
				seen[hit.Key] = true
			}
		})
	}
}

func TestKnowledgeHybridRetrieveRRFUsesRanksAndBounds(t *testing.T) {
	ranked := fuseKnowledgeRanks([]KnowledgeSearchHit{{Key: "a", Revision: 1}, {Key: "b", Revision: 1}, {Key: "a", Revision: 1}}, []semanticDocumentRank{{Key: "b", Revision: 1, Score: .1}, {Key: "c", Revision: 1, Score: 1}, {Key: "a", Revision: 2, Score: 999}})
	if len(ranked) != 3 || ranked[0].Key != "b" || ranked[0].MatchKind != "both" || math.Abs(ranked[0].Score-(1.0/61+1.0/62)) > 1e-12 {
		t.Fatalf("RRF rank fusion %+v", ranked)
	}
	if _, err := decodeEmbeddingVector([]byte{1, 2, 3}, 2); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}

func TestKnowledgeHybridRetrieveCapacityDegradesBeforeMatrixLoad(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, _ := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if _, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	cacheSyntheticQuery(t, s, cfg, "反例")
	// Deliberately corrupt/overfill a scratch cache to exercise the capacity guard.
	// These fabricated rows never represent a semantic-quality measurement.
	if _, err := s.DB.Exec(`WITH RECURSIVE numbers(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM numbers WHERE n<50001) INSERT INTO knowledge_embedding_vectors(config_id,doc_key,window_no,revision,content_hash,dimensions,vector) SELECT ?,?,n,1,'synthetic-capacity-only',2,X'0000803f00000000' FROM numbers`, cfg.ID, windows[0].DocKey); err != nil {
		t.Fatal(err)
	}
	result, err := s.EvaluateKnowledgeRetrieval(ctx, KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "反例"}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || result.Method != "fts" || !strings.Contains(result.Degradation, "50,000") || result.Total == 0 {
		t.Fatal(result, err)
	}
	s.embeddingMatrixMu.Lock()
	matrix := s.embeddingMatrix
	s.embeddingMatrixMu.Unlock()
	if matrix != nil {
		t.Fatal("over-capacity vectors loaded")
	}
}

func TestKnowledgeHybridRetrieveCandidateAndCancellationBounds(t *testing.T) {
	matrix := &knowledgeEmbeddingMatrix{Objects: 205}
	for i := 0; i < 205; i++ {
		matrix.Entries = append(matrix.Entries, embeddingMatrixEntry{Key: fmt.Sprintf("object:%03d", i), Revision: 1, Vector: []float32{1, 0}})
	}
	ranks, err := semanticRanks(t.Context(), matrix, []float32{1, 0})
	if err != nil || len(ranks) != knowledgeSemanticCandidateLimit || ranks[0].Key != "object:000" {
		t.Fatal(len(ranks), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = semanticRanks(ctx, matrix, []float32{1, 0}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	matrix.Entries[0].Vector = []float32{1}
	if _, err = semanticRanks(t.Context(), matrix, []float32{1, 0}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}
