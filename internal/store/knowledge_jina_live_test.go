package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

type jinaEvalFixture struct {
	Corpus []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"corpus"`
	Queries []struct {
		ID       string   `json:"id"`
		Group    string   `json:"group"`
		Query    string   `json:"query"`
		Relevant []string `json:"relevant_ids"`
	} `json:"queries"`
}

// Opt-in only: real provider calls over authored public fixture text in a fresh
// temporary DB. It never opens the personal DB or imports a quality-gate report.
func TestKnowledgeSemanticJinaLive(t *testing.T) {
	if os.Getenv("CWP_JINA_LIVE_EVAL") != "1" {
		t.Skip("explicit paid live evaluation required")
	}
	path := os.Getenv("CWP_JINA_EVAL_OUTPUT")
	if path == "" {
		t.Fatal("explicit report output path required before any provider call")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		t.Fatal("report output directory must already exist")
	}
	fixturePath := os.Getenv("CWP_JINA_EVAL_FIXTURE")
	if fixturePath == "" {
		fixturePath = "testdata/jina-retrieval-fixture.json"
	}
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture jinaEvalFixture
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Corpus) != 60 || len(fixture.Queries) != 40 {
		t.Fatal("unexpected fixture size")
	}
	ids := map[string]bool{}
	bytes := 0
	for _, item := range fixture.Corpus {
		if item.ID == "" || ids[item.ID] || item.Text == "" {
			t.Fatal("invalid corpus")
		}
		ids[item.ID] = true
		bytes += len(item.Text)
	}
	for _, q := range fixture.Queries {
		if q.Query == "" || len(q.Relevant) != 1 || !ids[q.Relevant[0]] || (q.Group != "original" && q.Group != "rewrite" && q.Group != "holdout") {
			t.Fatal("invalid labels")
		}
		bytes += len(q.Query)
	}
	if bytes > 48000 {
		t.Fatal("fixture input exceeds bounded evaluation")
	}
	client, err := provider.NewEmbeddingClient(os.Getenv("LEARNING_EMBEDDING_API_KEY"), os.Getenv("LEARNING_EMBEDDING_BASE_URL"), os.Getenv("LEARNING_EMBEDDING_MODEL"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LEARNING_EMBEDDING_BASE_URL") != "https://api.jina.ai/v1" || client.Config().Model != "jina-embeddings-v5-text-small" {
		t.Fatal("evaluation requires fixed tested Jina route")
	}
	client, err = client.WithProfile(os.Getenv("CWP_JINA_EVAL_PROFILE"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "evaluation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := client.Config()
	if err = s.RegisterKnowledgeEmbeddingConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	source, err := s.CreatePastedDocument(t.Context(), "中文检索效果评测", "本文仅用于隔离评测，不包含个人资料。")
	if err != nil {
		t.Fatal(err)
	}
	objectIDs := map[string]string{}
	for _, item := range fixture.Corpus {
		note, e := s.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: source.ID, Kind: "owner_reflection", Content: item.Text})
		if e != nil {
			t.Fatal(e)
		}
		objectIDs[note.ID] = item.ID
	}
	if _, err = s.ChangeKnowledgeEmbeddingScope(t.Context(), cfg.ID, 1, true, []EmbeddingSource{{"document", source.ID}}); err != nil {
		t.Fatal(err)
	}
	calls, tokens, windows := 0, 0, 0
	for {
		batch, e := s.PrepareKnowledgeEmbeddingBatch(t.Context(), cfg.ID)
		if e != nil {
			t.Fatal(e)
		}
		if len(batch) == 0 {
			break
		}
		inputs := make([]string, len(batch))
		for i, w := range batch {
			inputs[i] = w.Input
		}
		result, e := client.Embed(t.Context(), inputs)
		calls++
		if e != nil {
			t.Fatal(e)
		}
		if !result.UsageKnown {
			t.Fatal("supplier usage missing")
		}
		tokens += result.InputTokens
		n, e := s.AdoptKnowledgeEmbeddings(t.Context(), cfg.ID, batch, result)
		if e != nil || n != len(batch) {
			t.Fatal(n, e)
		}
		windows += n
		if calls > 8 {
			t.Fatal("index batch limit")
		}
	}
	// Keep each actual query response and its exact usage attached to its durable
	// query-cache job. No invented allocation of batch token totals to queries.
	for i, q := range fixture.Queries {
		job, _, e := s.ReserveKnowledgeQueryEmbedding(t.Context(), cfg.ID, q.Query)
		if e != nil {
			t.Fatal(e)
		}
		claimed, e := s.ClaimNextJob(t.Context(), "120 seconds")
		if e != nil || claimed == nil || claimed.ID != job.ID {
			t.Fatal(claimed, e)
		}
		execution, e := s.GetJobExecution(t.Context(), job.ID)
		if e != nil {
			t.Fatal(e)
		}
		var input KnowledgeEmbeddingJobInput
		if e = json.Unmarshal([]byte(execution.InputSnapshotJSON), &input); e != nil {
			t.Fatal(e)
		}
		result, e := client.EmbedQuery(t.Context(), []string{q.Query})
		calls++
		if e != nil {
			t.Fatal(e)
		}
		if !result.UsageKnown {
			t.Fatal("supplier usage missing")
		}
		tokens += result.InputTokens
		if e = s.CommitKnowledgeEmbeddingResponse(t.Context(), job.ID, input, result); e != nil {
			t.Fatal(e)
		}
		if e = s.MarkJobSucceeded(t.Context(), job.ID); e != nil {
			t.Fatal(e)
		}
		if (i+1)%10 == 0 {
			t.Logf("prepared %d/40 queries", i+1)
		}
	}
	identity, err := s.KnowledgeEmbeddingQualityIdentity(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = s.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	metrics := func(hits []KnowledgeSearchHit, want string) map[string]float64 {
		m := map[string]float64{"recall_at_1": 0, "recall_at_5": 0, "recall_at_10": 0, "mrr_at_10": 0}
		for i, h := range hits {
			if objectIDs[h.ObjectID] == want {
				m["recall_at_10"] = 1
				m["mrr_at_10"] = 1 / float64(i+1)
				if i < 5 {
					m["recall_at_5"] = 1
				}
				if i == 0 {
					m["recall_at_1"] = 1
				}
				break
			}
		}
		return m
	}
	var reranker *provider.RerankClient
	if os.Getenv("CWP_JINA_EVAL_RERANK") == "1" {
		reranker, err = provider.NewRerankClient(os.Getenv("LEARNING_EMBEDDING_API_KEY"), "https://api.jina.ai/v1", "jina-reranker-v3.5")
		if err != nil {
			t.Fatal(err)
		}
	}
	rerankCalls, rerankTokens := 0, 0
	results := []map[string]any{}
	groups := map[string]map[string]map[string]float64{}
	counts := map[string]int{}
	coldTimes, warmTimes := []float64{}, []float64{}
	for _, q := range fixture.Queries {
		req := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: q.Query, Kind: "notes", PerPage: 10}, Semantic: true, EmbeddingConfigID: cfg.ID}
		lexical, e := s.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: req.Search})
		if e != nil {
			t.Fatal(e)
		}
		s.embeddingMatrixMu.Lock()
		s.embeddingMatrix = nil
		s.embeddingMatrixMu.Unlock()
		start := time.Now()
		hybrid, e := s.EvaluateKnowledgeRetrieval(t.Context(), req)
		coldTimes = append(coldTimes, float64(time.Since(start).Microseconds())/1000)
		if e != nil || hybrid.Method != "rrf" || hybrid.IndexedCount != 60 {
			t.Fatal(hybrid, e)
		}
		start = time.Now()
		warm, e := s.EvaluateKnowledgeRetrieval(t.Context(), req)
		warmTimes = append(warmTimes, float64(time.Since(start).Microseconds())/1000)
		if e != nil || !reflect.DeepEqual(warm, hybrid) {
			t.Fatal("cold/warm ranking differs", e)
		}
		l, h := metrics(lexical.Hits, q.Relevant[0]), metrics(hybrid.Hits, q.Relevant[0])
		top := []string{}
		for i, hit := range hybrid.Hits {
			if i == 3 {
				break
			}
			top = append(top, objectIDs[hit.ObjectID])
		}
		results = append(results, map[string]any{"id": q.ID, "group": q.Group, "query": q.Query, "relevant_ids": q.Relevant, "lexical": l, "hybrid": h, "hybrid_top3": top})
		if groups[q.Group] == nil {
			groups[q.Group] = map[string]map[string]float64{"lexical": {}, "hybrid": {}}
		}
		for k, v := range l {
			groups[q.Group]["lexical"][k] += v
		}
		for k, v := range h {
			groups[q.Group]["hybrid"][k] += v
		}
		if reranker != nil {
			candidateReq := req
			candidateReq.Search.PerPage = 30
			pool, e := s.EvaluateKnowledgeRetrieval(t.Context(), candidateReq)
			if e != nil {
				t.Fatal(e)
			}
			frozen := KnowledgeRerankInput{Version: "knowledge-rerank-v1", Config: reranker.Config(), Request: req}
			tx, e := s.DB.BeginTx(t.Context(), nil)
			if e != nil {
				t.Fatal(e)
			}
			for _, hit := range pool.Hits {
				var body string
				var revision int
				e = tx.QueryRowContext(t.Context(), `SELECT revision,title||char(10)||char(10)||body FROM knowledge_search_docs WHERE key=?`, hit.Key).Scan(&revision, &body)
				if e != nil {
					t.Fatal(e)
				}
				frozen.Candidates = append(frozen.Candidates, RerankCandidate{Key: hit.Key, Revision: revision, Hash: rerankHash(body)})
			}
			docs, e := rerankDocuments(t.Context(), tx, frozen)
			tx.Rollback()
			if e != nil {
				t.Fatal(e)
			}
			ranked, e := reranker.Rerank(t.Context(), q.Query, docs)
			rerankCalls++
			if e != nil || !ranked.UsageKnown {
				t.Fatal(ranked, e)
			}
			rerankTokens += ranked.InputTokens
			scores := map[string]float64{}
			for i, hit := range pool.Hits {
				scores[hit.Key] = ranked.Scores[i]
			}
			sort.SliceStable(pool.Hits, func(i, j int) bool { return scores[pool.Hits[i].Key] > scores[pool.Hits[j].Key] })
			m := metrics(pool.Hits[:min(10, len(pool.Hits))], q.Relevant[0])
			if groups[q.Group]["rerank"] == nil {
				groups[q.Group]["rerank"] = map[string]float64{}
			}
			for k, v := range m {
				groups[q.Group]["rerank"][k] += v
			}
			results[len(results)-1]["rerank"] = m
		}
		counts[q.Group]++
	}
	if err = s.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("retrieval performed writes", before, after, err)
	}
	afterIdentity, err := s.KnowledgeEmbeddingQualityIdentity(t.Context(), cfg.ID)
	if err != nil || afterIdentity != identity {
		t.Fatal("evaluation identity changed", err)
	}
	for group, adapters := range groups {
		for _, scores := range adapters {
			for k, v := range scores {
				scores[k] = v / float64(counts[group])
			}
		}
	}
	miss, err := s.EvaluateKnowledgeRetrieval(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "未准备过的测试问题", Kind: "notes"}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || miss.Method != "fts" || miss.Degradation == "" {
		t.Fatal("cache miss did not degrade", err)
	}
	normal, err := s.Retrieve(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: fixture.Queries[0].Query}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || normal.Method != "fts" {
		t.Fatal("quality gate bypass", err)
	}
	if err = s.SetSourceProductionPolicy(t.Context(), models.SourceDocument, source.ID, "internal", models.ModelDataLocalOnly); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.EvaluateKnowledgeRetrieval(t.Context(), KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: fixture.Queries[0].Query, Kind: "notes"}, Semantic: true, EmbeddingConfigID: cfg.ID})
	if err != nil || revoked.Method != "fts" {
		t.Fatal("revocation did not degrade", err)
	}
	status, err := s.KnowledgeEmbeddingStatus(t.Context(), cfg.ID)
	if err != nil || status.IndexedWindows != 0 {
		t.Fatal("revocation retained vectors", err)
	}
	p95 := func(v []float64) float64 { sort.Float64s(v); return v[(len(v)*95+99)/100-1] }
	hash := sha256.Sum256(raw)
	report := map[string]any{"schema": 1, "profile": cfg.Profile, "model": cfg.Model, "dimensions": cfg.Dimensions, "fixture_sha256": hex.EncodeToString(hash[:]), "corpus_objects": 60, "indexed_windows": windows, "queries": 40, "remote_calls": calls, "rerank_calls": rerankCalls, "rerank_input_tokens": rerankTokens, "supplier_input_tokens": tokens, "cash_cost": nil, "real_model_embeddings": true, "personal_corpus": false, "human_attested_relevance": false, "quality_gate_passed": false, "identity_stable": true, "measurement_identity": identity, "retrieval_read_only": true, "cache_miss_fallback": true, "ordinary_quality_gate_enforced": true, "source_revocation_invalidates": true, "cold_p95_ms": p95(coldTimes), "warm_p95_ms": p95(warmTimes), "groups": groups, "results": results}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(output, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("live report written; calls=%d tokens=%d groups=%v", calls, tokens, groups)
}

// Gold labels are fixed in the checked-in fixture before the live request; this
// offline check also runs in normal CI without API credentials or paid calls.
func TestKnowledgeJinaEvaluationFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/jina-retrieval-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture jinaEvalFixture
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Corpus) != 60 || len(fixture.Queries) != 40 {
		t.Fatal("unexpected fixture size")
	}
	ids, queryIDs, texts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	groups := map[string]int{}
	labels := map[string]int{}
	size := 0
	for _, doc := range fixture.Corpus {
		if doc.ID == "" || doc.Text == "" || ids[doc.ID] {
			t.Fatal("duplicate or empty corpus entry")
		}
		ids[doc.ID] = true
		size += len(doc.Text)
	}
	for _, q := range fixture.Queries {
		if q.ID == "" || q.Query == "" || queryIDs[q.ID] || texts[q.Query] || (q.Group != "original" && q.Group != "rewrite") || len(q.Relevant) != 1 || !ids[q.Relevant[0]] {
			t.Fatal("invalid query or gold label")
		}
		queryIDs[q.ID] = true
		texts[q.Query] = true
		groups[q.Group]++
		labels[q.Relevant[0]]++
		size += len(q.Query)
	}
	if groups["original"] != 20 || groups["rewrite"] != 20 || len(labels) != 20 || size > 48000 {
		t.Fatal("unbalanced fixture")
	}
	for _, n := range labels {
		if n != 2 {
			t.Fatal("each target must have one original and one rewrite")
		}
	}
}

func TestKnowledgeJinaHoldoutFrozenLabels(t *testing.T) {
	raw, err := os.ReadFile("testdata/jina-retrieval-holdout.json")
	if err != nil {
		t.Fatal(err)
	}
	var f jinaEvalFixture
	if json.Unmarshal(raw, &f) != nil || len(f.Corpus) != 60 || len(f.Queries) != 40 {
		t.Fatal("holdout size")
	}
	seen := map[string]bool{}
	labels := map[string]bool{}
	holdout := 0
	for _, q := range f.Queries {
		if q.Query == "" || seen[q.Query] || len(q.Relevant) != 1 {
			t.Fatal("invalid label")
		}
		seen[q.Query] = true
		if q.Group == "holdout" {
			holdout++
			labels[q.Relevant[0]] = true
		}
	}
	if holdout != 20 || len(labels) != 20 {
		t.Fatal("unbalanced holdout")
	}
	original, err := os.ReadFile("testdata/jina-retrieval-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var old jinaEvalFixture
	json.Unmarshal(original, &old)
	for _, q := range old.Queries {
		for _, n := range f.Queries {
			if n.Group == "holdout" && n.Query == q.Query {
				t.Fatal("reused query")
			}
		}
	}
}
