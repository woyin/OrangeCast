package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestKnowledgeSearchRetrieveRetainsFiltersAndLocalPolicy(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	doc, err := s.CreatePastedDocument(ctx, "主动回忆学习", "主动回忆能用于表达自我理解。")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "主动回忆时检查自己的理解"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	for _, q := range []KnowledgeSearchQuery{{Text: "主动回忆"}, {Text: "retrieval practice", Kind: "notes"}, {Text: "主动回忆", SourceType: "document", SourceID: doc.ID, Page: 2, PerPage: 1}, {Text: "主动回忆", Kind: "materials", From: "2026-01-01", Until: "2027-01-01"}, {Kind: "notes", IncludeHistory: true, IncludeDrafts: true}} {
		before, err := s.SearchKnowledge(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: q, Purpose: RetrieveLocal, Semantic: true})
		if err != nil || !reflect.DeepEqual(before, got.KnowledgeSearchResult) || got.Method != "fts" || got.Degradation == "" || got.LexicalCount != len(before.Hits) {
			t.Fatal(before, got, err)
		}
	}
	remote, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "主动回忆", Kind: "materials", SendProvider: "pod", IncludeDrafts: true}, Purpose: RetrieveExternal})
	if err != nil || remote.Total != 0 {
		t.Fatal("private source sent", remote, err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	_, err = s.Retrieve(ctx, KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "主动回忆"}, Semantic: true})
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || before != after {
		t.Fatal("retrieval writes or schedules work", before, after, err)
	}
}

func TestKnowledgeRecallRetrieveRejectsAmbiguousPurpose(t *testing.T) {
	s := newTestStore(t)
	for _, req := range []KnowledgeRetrieveQuery{{Purpose: "bad"}, {Purpose: RetrieveExternal}, {Purpose: RetrieveExternal, Search: KnowledgeSearchQuery{SendProvider: "  "}}, {Purpose: RetrieveLocal, Search: KnowledgeSearchQuery{SendProvider: "pod"}}, {Purpose: RetrieveLocal, Search: KnowledgeSearchQuery{RecallProfileID: "profile"}}, {Purpose: RetrieveExternal, Search: KnowledgeSearchQuery{SendProvider: "pod", Kind: "bad"}}} {
		if _, err := s.Retrieve(t.Context(), req); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(req, err)
		}
	}
}
