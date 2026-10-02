package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestKnowledgeRerankDurableCachePermissionsAndPages(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	doc, err := s.CreatePastedDocument(ctx, "测试材料", "主动回忆帮助记忆。\n\n主动回忆保留反例。")
	if err != nil {
		t.Fatal(err)
	}
	p, err := provider.NewRerankClient("secret", "https://api.jina.ai/v1", "jina-reranker-v3.5")
	if err != nil {
		t.Fatal(err)
	}
	req := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "主动回忆", PerPage: 1}, Purpose: RetrieveLocal}
	job, created, err := s.ReserveKnowledgeRerank(ctx, req, p.Config())
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	again, c, err := s.ReserveKnowledgeRerank(ctx, req, p.Config())
	if err != nil || c || again.ID != job.ID {
		t.Fatal("duplicate paid admission", err)
	}
	ex, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ex.InputSnapshotJSON, "帮助记忆") {
		t.Fatal("durable body copy")
	}
	var in KnowledgeRerankInput
	if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	docs, err := s.StartKnowledgeRerank(ctx, job.ID, in)
	if err != nil || len(docs) != 2 {
		t.Fatal(docs, err)
	}
	if _, err = s.StartKnowledgeRerank(ctx, job.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal("boundary replay", err)
	}
	r := &provider.RerankResult{Model: p.Config().Model, Scores: []float64{0.1, 0.9}, InputTokens: 20, UsageKnown: true}
	if err = s.RecordRerankReceipt(ctx, job.ID, in, r); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitKnowledgeRerank(ctx, job.ID, in, r); err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	req.Rerank = &cfg
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	first, err := s.Retrieve(ctx, req)
	if err != nil || !first.RerankApplied || len(first.Hits) != 1 || first.Hits[0].Key != in.Candidates[1].Key {
		t.Fatal(first, err)
	}
	req.Search.Page = 2
	second, err := s.Retrieve(ctx, req)
	if err != nil || !second.RerankApplied || second.Hits[0].Key != in.Candidates[0].Key {
		t.Fatal(second, err)
	}
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("read writes")
	}
	if err = s.SetSourceProductionPolicy(ctx, models.SourceDocument, doc.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.Retrieve(ctx, req)
	if err != nil || withdrawn.RerankApplied {
		t.Fatal("permission stale", withdrawn, err)
	}
	req.Rerank = nil
	if _, _, err = s.ReserveKnowledgeRerank(ctx, req, cfg); err == nil {
		t.Fatal("private source admitted")
	}
}
func TestKnowledgeSearchNaturalQueryAndFeedback(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, err := s.CreatePastedDocument(ctx, "工作记忆", "工作记忆的容量有限。")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"工作记忆", "工作记忆是什么？", "请问如何使用工作记忆？"} {
		result, err := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: q})
		if err != nil || len(result.Hits) != 1 {
			t.Fatal(q, result, err)
		}
	}
	result, _ := s.SearchKnowledge(ctx, KnowledgeSearchQuery{Text: "工作记忆"})
	hit := result.Hits[0]
	f := KnowledgeSearchFeedback{Query: "工作记忆是什么？", Key: hit.Key, Revision: hit.Revision, Label: "relevant", Method: "fts"}
	if err = s.RecordKnowledgeSearchFeedback(ctx, f); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ExportKnowledgeSearchFeedback(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	raw, err := MarshalSearchFeedbackExport(rows)
	if err != nil || !strings.Contains(string(raw), `"quality_gate_passed":false`) {
		t.Fatal(string(raw), err)
	}
	f.Revision++
	if err = s.RecordKnowledgeSearchFeedback(ctx, f); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	f.Label = "trusted"
	if err = s.RecordKnowledgeSearchFeedback(ctx, f); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DELETE FROM knowledge_search_docs WHERE key=?`, hit.Key); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ExportKnowledgeSearchFeedback(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("purged label exported", rows, err)
	}
}
func TestKnowledgeEmbeddingRetrievalParagraphWindows(t *testing.T) {
	a := strings.Repeat("甲", 200)
	b := strings.Repeat("乙", 200)
	c := strings.Repeat("丙", 200)
	old, err := embeddingWindows("key", 1, "标题", a+"\n\n"+b+"\n\n"+c, 1)
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	current, err := embeddingWindowsProfile("key", 1, "标题", a+"\n\n"+b+"\n\n"+c, 1, "jina-retrieval-v1")
	if err != nil || len(current) != 2 {
		t.Fatal(current, err)
	}
	for _, w := range current {
		if len(w.Input) > 1536 {
			t.Fatal("bound", len(w.Input))
		}
	}
	oversized := strings.Repeat("甲", 3000)
	if _, err = embeddingWindowsProfile("key", 1, "标题", oversized, 1, "jina-retrieval-v1"); err == nil {
		t.Fatal("sliced owner expression")
	}
}

func TestKnowledgeRerankResumeAndLane(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, err := s.CreatePastedDocument(ctx, "回忆", "回忆学习资料。")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := provider.NewRerankClient("key", "https://api.jina.ai/v1", "rank")
	req := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "回忆"}}
	job, _, err := s.ReserveKnowledgeRerank(ctx, req, p.Config())
	if err != nil {
		t.Fatal(err)
	}
	var lane string
	s.DB.QueryRow(`SELECT run_lane FROM processing_jobs WHERE id=?`, job.ID).Scan(&lane)
	if lane != "index" {
		t.Fatal(lane)
	}
	s.DB.Exec(`UPDATE processing_jobs SET status='failed' WHERE id=?`, job.ID)
	if err = s.ResumeKnowledgeRerank(ctx, job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeKnowledgeRerank(ctx, job.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale resume", err)
	}
	s.DB.Exec(`UPDATE processing_jobs SET status='failed',remote_call_started=1 WHERE id=?`, job.ID)
	if err = s.ResumeKnowledgeRerank(ctx, job.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown replay", err)
	}
	s.DB.Exec(`UPDATE run_controls SET paused=1 WHERE kind='lane' AND target='index'`)
	req.Search.Text = "回忆资料"
	if _, _, err = s.ReserveKnowledgeRerank(ctx, req, p.Config()); err == nil {
		t.Fatal("paused lane admitted")
	}
}

func TestKnowledgeRetrievalProfileIndexAdoptsSameCompleteWindows(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, _ := provider.NewEmbeddingClient("key", "https://api.jina.ai/v1", "jina-embeddings-v5-text-small", 2)
	p, err := p.WithProfile("jina-retrieval-v1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(ctx, "条件", "测试学习材料。")
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := strings.Repeat("甲", 250), strings.Repeat("乙", 100), strings.Repeat("丙", 250)
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: a + "\n\n" + b + "\n\n" + c})
	if err != nil {
		t.Fatal(err)
	}
	enableEmbeddingDoc(t, s, cfg, doc)
	windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	var parts []EmbeddingWindow
	for _, w := range windows {
		if strings.Contains(w.DocKey, note.ID) {
			parts = append(parts, w)
		}
	}
	if len(parts) != 2 || !strings.Contains(parts[0].Input, b) || !strings.Contains(parts[1].Input, b) || !strings.Contains(parts[1].Input, c) {
		t.Fatal("whole-paragraph overlap", parts)
	}
	job, _, err := s.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	var in KnowledgeEmbeddingJobInput
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
	s.DB.Exec(`UPDATE processing_jobs SET status='running' WHERE id=?`, job.ID)
	if err = s.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); err != nil {
		t.Fatal(err)
	}
	r := fakeEmbeddingResult(cfg, in.Windows)
	if err = s.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, r); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
	if err != nil || len(next) != 0 {
		t.Fatal(next, err)
	}
	read, err := s.GetKnowledgeEmbeddingConfig(ctx, cfg.ID)
	if err != nil || read.Profile != cfg.Profile {
		t.Fatal(read, err)
	}
}
