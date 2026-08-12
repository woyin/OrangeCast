package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestCreatePastedDocumentIsImmutableEvidenceSnapshot(t *testing.T) {
	s := newTestStore(t)
	doc, err := s.CreatePastedDocument(context.Background(), "研究笔记", "原始证据内容")
	if err != nil || doc.ContentSHA256 == "" || doc.OriginKind != "pasted" {
		t.Fatalf("document snapshot should persist: doc=%+v err=%v", doc, err)
	}
	profile, err := s.CreateEditorialProfile(context.Background(), models.EditorialProfile{Name: "文档品牌"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSourceScope(context.Background(), profile.ID, models.SourceDocument, doc.ID); err != nil {
		t.Fatalf("document must be a scopeable source: %v", err)
	}
	if _, err := s.CreatePastedDocument(context.Background(), "", "x"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty document must reject: %v", err)
	}
	segments := DocumentSegments(doc)
	if len(segments) != 1 || segments[0].ID != doc.ID+"-p0001" || segments[0].Text != "原始证据内容" {
		t.Fatalf("segments must be deterministic anchors: %+v", segments)
	}
}
