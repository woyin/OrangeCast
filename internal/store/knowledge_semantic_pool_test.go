package store

import (
	"context"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"reflect"
	"sort"
	"testing"
)

func TestKnowledgeSemanticPoolPreservesLexicalProjection(t *testing.T) {
	s, _, doc := embeddingIndexFixture(t)
	for i := 0; i < 120; i++ {
		_, err := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: fmt.Sprintf("条件反例 练习 %03d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []KnowledgeSearchQuery{
		{Text: "练习"}, {Text: "反例", Kind: "notes"}, {Text: "不存在的罕见词"}, {Text: ""},
		{Text: "反例", SourceType: "document", SourceID: doc.ID},
		{Text: "反例", From: "2000-01-01", Until: "2000-01-02"},
		{Text: "反例", Recall: true, SendProvider: "pod", MetadataOnly: true},
		{Text: "反例", Theme: "练习"},
	} {
		q.Page, q.PerPage, q.MetadataOnly = 1, 100, true
		want, err := s.SearchKnowledge(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.searchKnowledgeLexicalPool(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("query %+v: got %+v want %+v", q, got, want)
		}
	}
}

func TestKnowledgeSemanticLazyFallbackPreservesPagination(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	enableEmbeddingDoc(t, s, cfg, doc)
	for _, q := range []KnowledgeSearchQuery{{Text: "反例", Page: 2, PerPage: 1}, {Text: ""}, {Text: "反例", Page: 10001}} {
		want, werr := s.SearchKnowledge(t.Context(), q)
		got, gerr := s.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: q, Semantic: true, EmbeddingConfigID: cfg.ID})
		if (gerr != nil) != (werr != nil) {
			t.Fatalf("errors %v %v", gerr, werr)
		}
		if werr == nil && (!reflect.DeepEqual(got.KnowledgeSearchResult, want) || got.Method != "fts") {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
}

func TestKnowledgeSemanticExactTopMatchesFullSort(t *testing.T) {
	matrix := &knowledgeEmbeddingMatrix{Objects: 407}
	query := []float32{0.6, 0.8}
	best := map[string]semanticDocumentRank{}
	for i := 0; i < 407; i++ {
		// Reverse keys and repeated scores exercise cutoff ties and heap replacement.
		key := fmt.Sprintf("doc:%04d", 406-i)
		matrix.Documents = append(matrix.Documents, semanticDocumentRank{Key: key, Revision: 1})
		for window := 0; window < 2; window++ {
			vector := []float32{0.6, 0.8}
			if (i+window)%3 == 0 {
				vector[0] = -vector[0]
			}
			if (i+window)%5 == 0 {
				vector[1] = -vector[1]
			}
			matrix.Entries = append(matrix.Entries, embeddingMatrixEntry{Key: key, Revision: 1, Window: window, Document: i, Vector: vector})
			var score float64
			for j, v := range vector {
				score += float64(v) * float64(query[j])
			}
			old, exists := best[key]
			if !exists || score > old.Score {
				best[key] = semanticDocumentRank{Key: key, Revision: 1, Score: score}
			}
		}
	}
	want := make([]semanticDocumentRank, 0, len(best))
	for _, r := range best {
		want = append(want, r)
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Score == want[j].Score {
			return want[i].Key < want[j].Key
		}
		return want[i].Score > want[j].Score
	})
	want = want[:knowledgeSemanticCandidateLimit]
	got, err := semanticRanks(t.Context(), matrix, query)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("exact rankings differ: %v", err)
	}
	for _, d := range matrix.Documents {
		if d.Score != 0 {
			t.Fatal("query mutated cached document")
		}
	}
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = semanticRanks(cancelCtx, matrix, query); err == nil {
		t.Fatal("cancellation ignored")
	}
	matrix.Entries[0].Document = -1
	if _, err = semanticRanks(t.Context(), matrix, query); err == nil {
		t.Fatal("invalid document index accepted")
	}
	matrix.Entries[0].Document = 0
	matrix.Entries[0].Vector = []float32{1}
	if _, err = semanticRanks(t.Context(), matrix, query); err == nil {
		t.Fatal("bad dimensions accepted")
	}
}
