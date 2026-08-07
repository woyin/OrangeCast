package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestRelationKind_CitationBackfill (ADR-0018 R1)
// 存量/现有承载 Segment 关系的表写入后，relation_kind 必须为 'citation'
// （keypoint_index / annotations / pins / collection_items 均为证据型）。
func TestRelationKind_CitationBackfill(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "ep", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	srcType, srcID := models.SourceEpisode, eps[0].ID

	// KeyPoint
	card := &provider.KnowledgeCard{
		Title:   "T",
		Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}},
		KeyPoints: []provider.KeyPoint{
			{Content: "kp", Citations: []string{"seg-0001"}},
		},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}}
	if err := s.IndexKeyPoints(ctx, srcType, srcID, "ep", 1, card, segs); err != nil {
		t.Fatal(err)
	}
	kps, _, err := s.ListKeyPoints(ctx, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kps) != 1 || kps[0].RelationKind != models.RelationCitation {
		t.Fatalf("keypoint_index.relation_kind 应为 citation，实际 %+v", kps)
	}

	// Annotation
	if _, err := s.UpsertAnnotation(ctx, srcType, srcID, `["seg-0001"]`, 0, 5, "note"); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAnnotation(ctx, srcType, srcID, `["seg-0001"]`)
	if err != nil {
		t.Fatal(err)
	}
	if a.RelationKind != models.RelationCitation {
		t.Errorf("annotations.relation_kind 应为 citation，实际 %s", a.RelationKind)
	}

	// Pin
	if _, err := s.TogglePin(ctx, srcType, srcID, `["seg-0001"]`, 0, 5, "ep"); err != nil {
		t.Fatal(err)
	}
	pins, err := s.ListPins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].RelationKind != models.RelationCitation {
		t.Fatalf("pins.relation_kind 应为 citation，实际 %+v", pins)
	}

	// Collection item
	col, err := s.CreateCollection(ctx, "C", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToCollection(ctx, col.ID, srcType, srcID, `["seg-0001"]`, 0, 5, "ep", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListCollectionItems(ctx, col.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RelationKind != models.RelationCitation {
		t.Fatalf("collection_items.relation_kind 应为 citation，实际 %+v", items)
	}
}
