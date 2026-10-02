package store

import (
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeEmbeddingDocumentIndexPreservesInvalidationAcrossConfigs(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	client, err := provider.NewEmbeddingClient("secret", "https://other.example/v1", "other", 2)
	if err != nil {
		t.Fatal(err)
	}
	other := client.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(ctx, other); err != nil {
		t.Fatal(err)
	}
	for _, c := range []provider.EmbeddingConfig{cfg, other} {
		enableEmbeddingDoc(t, s, c, doc)
		ws, e := s.PrepareKnowledgeEmbeddingBatch(ctx, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		if n, e := s.AdoptKnowledgeEmbeddings(ctx, c.ID, ws, fakeEmbeddingResult(c, ws)); e != nil || n != 2 {
			t.Fatal(n, e)
		}
	}
	var plan string
	var node, parent, unused int
	if err = s.DB.QueryRowContext(ctx, `EXPLAIN QUERY PLAN DELETE FROM knowledge_embedding_vectors WHERE doc_key=?`, "document:"+doc.ID+":0").Scan(&node, &parent, &unused, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "SEARCH knowledge_embedding_vectors") || !strings.Contains(plan, "idx_embedding_vectors_document") {
		t.Fatal("document invalidation scans corpus:", plan)
	}
	var key string
	if err = s.DB.QueryRowContext(ctx, `SELECT doc_key FROM knowledge_embedding_vectors WHERE config_id=? LIMIT 1`, cfg.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	// A current indexed document's projection changes synchronously. Every
	// configuration must lose that key, while its other document window survives.
	if _, err = s.DB.ExecContext(ctx, `UPDATE knowledge_search_docs SET title=title||' 修订' WHERE key=?`, key); err != nil {
		t.Fatal(err)
	}
	for _, c := range []provider.EmbeddingConfig{cfg, other} {
		var count int
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=? AND doc_key=?`, c.ID, key).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE config_id=?`, c.ID).Scan(&count); err != nil || count != 1 {
			t.Fatal("unrelated window lost", count, err)
		}
		var action string
		if err = s.DB.QueryRowContext(ctx, `SELECT action FROM knowledge_embedding_events WHERE config_id=? AND doc_key=?`, c.ID, key).Scan(&action); err != nil || action != "upsert" {
			t.Fatal(action, err)
		}
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors`).Scan(&count); err != nil || count != 0 {
		t.Fatal("withdrawal left another config's vector", count, err)
	}
	for _, c := range []provider.EmbeddingConfig{cfg, other} {
		ws, e := s.PrepareKnowledgeEmbeddingBatch(ctx, c.ID)
		if e != nil || len(ws) != 0 {
			t.Fatal("withdrawn source payable", len(ws), e)
		}
	}
}
