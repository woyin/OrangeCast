package store

import (
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"path/filepath"
	"testing"
)

func TestKnowledgeWeightedSearchUpgradePreservesV3JobsAndNotes(t *testing.T) {
	ctx := t.Context()
	db := openRaw(t, filepath.Join(t.TempDir(), "v62.db"))
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version <= 62 {
			if err := applyOne(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Store{DB: db}
	doc, err := s.CreatePastedDocument(ctx, "archivallearning 旧来源", "archivallearning 必须保留完整笔记。")
	if err != nil {
		t.Fatal(err)
	}
	var materials []provider.KnowledgeMaterial
	for _, text := range []string{"archivallearning 旧个人理解一", "archivallearning 旧个人理解二"} {
		n, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: text})
		if err != nil {
			t.Fatal(err)
		}
		materials = append(materials, provider.KnowledgeMaterial{ID: n.ID, Kind: n.Kind, SourceType: n.SourceType, SourceID: n.SourceID, Content: n.Content, Version: n.Revision})
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := provider.KnowledgeArticleRequest{PromptVersion: "knowledge-article-v3", Stage: "discover", ReviewModel: "old-review", Materials: materials}
	article, _, err := s.ReserveKnowledgeArticle(ctx, profile.ID, "pod", "old-writer", req, false)
	if err != nil {
		t.Fatal(err)
	}
	finishKnowledgeTestStage(t, s, true)
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	before, err := s.GetJobExecution(ctx, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.ConfigVersion != "knowledge-article-v3" {
		t.Fatal(before.ConfigVersion)
	}
	applied, err := Migrate(ctx, db)
	if err != nil || len(applied) == 0 || applied[0] != 63 {
		t.Fatal(applied, err)
	}
	after, err := s.GetJobExecution(ctx, jobs[0].ID)
	if err != nil || before.InputSnapshotJSON != after.InputSnapshotJSON || before.ConfiguredModel != after.ConfiguredModel {
		t.Fatal("migration rewrote frozen job", err)
	}
	result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "archivallearning", Kind: "notes"})
	if err != nil || result.Total != 2 {
		t.Fatal(result.Total, err)
	}
	for _, hit := range result.Hits {
		if hit.Revision != 1 {
			t.Fatal("note revision changed", hit)
		}
	}
	finishKnowledgeTestStage(t, s, true)
	finishKnowledgeTestStage(t, s, true)
	fresh, err := s.GetKnowledgeArticle(ctx, article.ID)
	if err != nil || fresh.Status != "ready" {
		t.Fatal(fresh, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO knowledge_search_fts(knowledge_search_fts)VALUES('integrity-check')`); err != nil {
		t.Fatal(err)
	}
}
