package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func embeddingIndexFixture(t *testing.T) (*Store, provider.EmbeddingConfig, *models.Document) {
	t.Helper()
	s := newTestStore(t)
	p, err := provider.NewEmbeddingClient("secret", "https://vector.example/v1", "vector", 2)
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(t.Context(), "学习资料", "主动回忆保留语境。\n\n适用条件与反例不能省略。")
	if err != nil {
		t.Fatal(err)
	}
	return s, cfg, doc
}
func fakeEmbeddingResult(cfg provider.EmbeddingConfig, windows []EmbeddingWindow) *provider.EmbeddingResult {
	r := &provider.EmbeddingResult{Model: cfg.Model, Dimensions: cfg.Dimensions, UsageKnown: true, InputTokens: 20, Vectors: make([][]float32, len(windows))}
	for i := range windows {
		r.Vectors[i] = []float32{1, 0}
	}
	return r
}
func enableEmbeddingDoc(t *testing.T, s *Store, cfg provider.EmbeddingConfig, doc *models.Document) {
	t.Helper()
	c, err := s.GetKnowledgeEmbeddingConfig(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeKnowledgeEmbeddingScope(t.Context(), cfg.ID, c.Revision, true, []EmbeddingSource{{"document", doc.ID}}); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeEmbeddingIndexAdmissionAndInvalidation(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	if _, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("default must stay off")
	}
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(windows) != 2 {
		t.Fatalf("%+v %v", windows, err)
	}
	for _, w := range windows {
		if w.DocKey == "" || w.ContentHash == "" || w.Revision != doc.Version || len(w.Sources) != 1 || w.Sources[0].SourceID != doc.ID || len(w.Input) > 8192 {
			t.Fatal(w)
		}
	}
	if n, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || status.IndexedWindows != 2 || status.PendingObjects != 0 {
		t.Fatalf("%+v %v", status, err)
	}
	if next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID); err != nil || len(next) != 0 {
		t.Fatal(next, err)
	}
	// Provider restrictions change cache validity synchronously, not on worker pickup.
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	after, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || after.IndexedWindows != 0 || after.DeleteEpoch <= status.DeleteEpoch {
		t.Fatal(after, err)
	}
	if n, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil || n != 0 {
		t.Fatal("withdrawn result applied", n, err)
	}
	if next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID); err != nil || len(next) != 0 {
		t.Fatal("private source outbound", next, err)
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataApprovedProvidersOnly); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSourceApprovedProviders(ctx, models.SourceDocument, doc.ID, []string{"pod"}); err != nil {
		t.Fatal(err)
	}
	if next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID); err != nil || len(next) != 0 {
		t.Fatal("text provider permission reused")
	}
	if err = s.SetSourceApprovedProviders(ctx, models.SourceDocument, doc.ID, []string{cfg.Provider}); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(next) != 2 {
		t.Fatal(next, err)
	}
	if n, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, next, fakeEmbeddingResult(cfg, next)); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceDocument, doc.ID); err != nil {
		t.Fatal(err)
	}
	after, err = s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || after.IndexedWindows != 0 {
		t.Fatal("purge vector remains", after, err)
	}
}

func TestKnowledgeEmbeddingIndexVersionScopeAndWindows(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "我的完整表达", ReferencesJSON: jsonString([]string{DocumentSegments(doc)[0].ID})})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateOwnerNote(ctx, note.ID, "新理解与边界", note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	n, err := s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, before, fakeEmbeddingResult(cfg, before))
	if err != nil || n != len(before)-1 {
		t.Fatal("obsolete note adopted", n, err)
	}
	next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(next) != 1 || !strings.Contains(next[0].Input, "新理解") {
		t.Fatal(next, err)
	}
	c, _ := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if _, err = s.ChangeKnowledgeEmbeddingScope(ctx, cfg.ID, c.Revision, false, []EmbeddingSource{{"document", doc.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, next, fakeEmbeddingResult(cfg, next)); !errors.Is(err, ErrConflict) {
		t.Fatal("disabled applied", err)
	}
	if _, err = s.ChangeKnowledgeEmbeddingScope(ctx, cfg.ID, c.Revision, true, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("scope CAS", err)
	}
	long := strings.Repeat("甲", 2000) + "\n\n" + strings.Repeat("乙", 2000)
	split, err := embeddingWindows("note:x", 2, "标题", long, 3)
	if err != nil || len(split) != 2 || !strings.Contains(split[0].Input, strings.Repeat("甲", 2000)) || !strings.Contains(split[1].Input, strings.Repeat("乙", 2000)) {
		t.Fatal("complete expression lost", err)
	}
	if _, err = embeddingWindows("note:x", 2, "", strings.Repeat("a", 8193), 3); err == nil {
		t.Fatal("oversize paragraph truncated")
	}
	cfg3client, _ := provider.NewEmbeddingClient("secret", "https://vector.example/v1", "vector", 3)
	cfg3 := cfg3client.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(ctx, cfg3); err != nil {
		t.Fatal(err)
	}
	if cfg3.ID == cfg.ID {
		t.Fatal("dimensions mixed")
	}
	if other, err := s.KnowledgeEmbeddingStatus(ctx, cfg3.ID); err != nil || other.IndexedWindows != 0 || other.Config.Enabled {
		t.Fatal("new config inherited vectors or opt-in")
	}
}

func TestKnowledgeEmbeddingIndexUpgradeAndCacheLoss(t *testing.T) {
	old, path := historicalTestStore(t, 69)
	doc, err := old.CreatePastedDocument(t.Context(), "历史来源", "原文可继续全文查找。")
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM knowledge_embedding_configs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration enabled cache", err)
	}
	lexical, err := s.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "原文", SourceID: doc.ID}, Semantic: true})
	if err != nil || lexical.Total == 0 || lexical.Method != "fts" {
		t.Fatal(lexical, err)
	}
	restoredPath := filepath.Join(t.TempDir(), "copy.db")
	if err = ConsistencyBackup(t.Context(), s.DB, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	result, err := restored.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "原文", SourceID: doc.ID}, Semantic: true})
	if err != nil || result.Total != lexical.Total || result.Method != "fts" {
		t.Fatal("cache-less backup broke FTS", result, err)
	}
	if err = restored.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE job_type='knowledge_embedding'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("restore started paid rebuild", err)
	}
}

func TestKnowledgeEmbeddingIndexPassedArticlesAndOversizeNotes(t *testing.T) {
	s, article, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	p, _ := provider.NewEmbeddingClient("secret", "https://vector.example/v1", "vector", 2)
	cfg := p.Config()
	if err := s.RegisterKnowledgeEmbeddingConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT source_type,source_id FROM knowledge_article_material_refs WHERE article_id=? AND revision=? ORDER BY source_type,source_id`, article.ID, article.PassedRevision)
	if err != nil {
		t.Fatal(err)
	}
	var refs []EmbeddingSource
	for rows.Next() {
		var ref EmbeddingSource
		if err = rows.Scan(&ref.SourceType, &ref.SourceID); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if len(refs) == 0 {
		t.Fatal("missing fixture provenance")
	}
	if _, err = s.ChangeKnowledgeEmbeddingScope(ctx, cfg.ID, 1, true, refs); err != nil {
		t.Fatal(err)
	}
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range windows {
		if strings.HasPrefix(w.DocKey, "article:"+article.ID+":") {
			found = true
			if len(w.Sources) != len(refs) {
				t.Fatal("article source omitted")
			}
		}
	}
	if !found {
		t.Fatal("passed article not admitted")
	}
	if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, fakeEmbeddingResult(cfg, windows)); err != nil {
		t.Fatal(err)
	}
	note, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateOwnerNote(ctx, noteID, strings.Repeat("a", 9000), note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range next {
		if w.DocKey == "note:"+noteID {
			t.Fatal("oversize Owner expression truncated")
		}
	}
	status, err := s.KnowledgeEmbeddingStatus(ctx, cfg.ID)
	if err != nil || status.SkippedObjects != 1 {
		t.Fatal(status, err)
	}
	// A passed article must qualify every source, not only its first material.
	if err = s.SetSourceProductionPolicy(ctx, models.SourceType(refs[0].SourceType), refs[0].SourceID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	next, err = s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range next {
		if strings.HasPrefix(w.DocKey, "article:"+article.ID+":") {
			t.Fatal("partial article permission leak")
		}
	}
	var articleVectors int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_embedding_vectors WHERE doc_key LIKE ?`, "article:"+article.ID+":%").Scan(&articleVectors); err != nil || articleVectors != 0 {
		t.Fatal("article cache survived permission withdrawal", err)
	}
}
