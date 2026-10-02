package store

import (
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"reflect"
	"strings"
	"testing"
)

func TestKnowledgeLexicalPrefixNeverDisplaced(t *testing.T) {
	for count := 0; count <= 100; count++ {
		lexical := []KnowledgeSearchHit{}
		semantic := []semanticDocumentRank{}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("lex-%03d", i)
			lexical = append(lexical, KnowledgeSearchHit{Key: key, Revision: 1})
			semantic = append(semantic, semanticDocumentRank{Key: key, Revision: 1, Score: float64(i)})
		}
		// Reverse the vector votes and add semantic-only material ahead of them.
		for i, j := 0, len(semantic)-1; i < j; i, j = i+1, j-1 {
			semantic[i], semantic[j] = semantic[j], semantic[i]
		}
		semantic = append([]semanticDocumentRank{{Key: "semantic", Revision: 1, Score: 999}}, semantic...)
		ranked := protectKnowledgeLexicalRanks(fuseKnowledgeRanks(lexical, semantic), lexical)
		if len(ranked) != count+1 {
			t.Fatal(count, ranked)
		}
		for i, h := range lexical {
			if ranked[i].Key != h.Key {
				t.Fatal("FTS regression", count, i, ranked[i])
			}
		}
		if ranked[count].Key != "semantic" {
			t.Fatal("lost additional recall")
		}
	}
}
func TestKnowledgeRerankProtectsFTSOrder(t *testing.T) {
	hits := []KnowledgeSearchHit{{Key: "lex-a", MatchKind: "both", LexicalProtected: true}, {Key: "lex-b", MatchKind: "lexical", LexicalProtected: true}, {Key: "sem-a", MatchKind: "semantic"}, {Key: "sem-b", MatchKind: "semantic"}}
	scores := map[string]float64{"lex-a": -10, "lex-b": -9, "sem-a": 1, "sem-b": 2}
	applyKnowledgeRerankScores(hits, scores, true)
	got := []string{}
	for _, h := range hits {
		got = append(got, h.Key)
	}
	if !reflect.DeepEqual(got, []string{"lex-a", "lex-b", "sem-b", "sem-a"}) {
		t.Fatal(got)
	}
	applyKnowledgeRerankScores(hits, scores, false)
	if hits[0].Key != "sem-b" {
		t.Fatal("explicit standalone rerank changed")
	}
}
func TestKnowledgeLexicalProtectionRevisionAndDuplicates(t *testing.T) {
	ranks := []fusedKnowledgeRank{{Key: "stale", Revision: 2}, {Key: "valid", Revision: 1}, {Key: "semantic", Revision: 1}}
	lexical := []KnowledgeSearchHit{{Key: "missing", Revision: 1}, {Key: "stale", Revision: 1}, {Key: "valid", Revision: 1}, {Key: "valid", Revision: 1}}
	got := protectKnowledgeLexicalRanks(ranks, lexical)
	if len(got) != 3 || got[0].Key != "valid" || got[1].Key != "stale" {
		t.Fatal(got)
	}
}

func TestKnowledgeRerankPreservesInitialTopTenAndRejectsExtremeDisagreement(t *testing.T) {
	for n := 1; n <= 30; n++ {
		hits := make([]KnowledgeSearchHit, n)
		scores := map[string]float64{}
		for i := range hits {
			hits[i] = KnowledgeSearchHit{Key: fmt.Sprintf("doc-%02d", i), MatchKind: "semantic"}
			scores[hits[i].Key] = float64(i)
		}
		applyKnowledgeRerankScores(hits, scores, true)
		seen := map[string]bool{}
		for _, h := range hits[:min(10, n)] {
			seen[h.Key] = true
		}
		for i := 0; i < min(10, n); i++ {
			if !seen[fmt.Sprintf("doc-%02d", i)] {
				t.Fatal("top-ten displaced", n, hits)
			}
		}
		if n > 10 && hits[0].Key != "doc-00" {
			t.Fatal("extreme disagreement changed base leader", n, hits)
		}
	}
}

func TestKnowledgeSemanticFTSPrefixAndCachedRerankEndToEnd(t *testing.T) {
	s, cfg, doc := embeddingIndexFixture(t)
	ctx := t.Context()
	enableEmbeddingDoc(t, s, cfg, doc)
	for i := 0; i < 20; i++ {
		if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: fmt.Sprintf("记忆条件第%d条。", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: fmt.Sprintf("其他资料第%d条。", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for {
		windows, err := s.PrepareKnowledgeEmbeddingBatch(ctx, cfg.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(windows) == 0 {
			break
		}
		r := fakeEmbeddingResult(cfg, windows)
		for i, w := range windows {
			if strings.Contains(w.Input, "记忆条件") {
				r.Vectors[i] = []float32{0, 1}
			}
		}
		if _, err = s.AdoptKnowledgeEmbeddings(ctx, cfg.ID, windows, r); err != nil {
			t.Fatal(err)
		}
	}
	cacheSyntheticQuery(t, s, cfg, "记忆条件")
	req := KnowledgeRetrieveQuery{Search: KnowledgeSearchQuery{Text: "记忆条件", Kind: "notes", PerPage: 7}, Semantic: true, EmbeddingConfigID: cfg.ID}
	for page := 1; page <= 3; page++ {
		req.Search.Page = page
		fts, err := s.SearchKnowledge(ctx, req.Search)
		if err != nil {
			t.Fatal(err)
		}
		hybrid, err := s.EvaluateKnowledgeRetrieval(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for i, h := range fts.Hits {
			if hybrid.Hits[i].Key != h.Key || !hybrid.Hits[i].LexicalProtected {
				t.Fatal("FTS prefix lost", page, i)
			}
		}
	}
	req.Search.Page = 1
	p, _ := provider.NewRerankClient("key", "https://rank.example/v1", "rank")
	in, base, err := s.prepareRerank(ctx, req, p.Config(), true)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{SourceType: "knowledge_index", SourceID: p.Config().ID, JobType: models.JobKnowledgeRerank, IntentID: in.Fingerprint, ConfiguredProvider: p.Config().Provider, ConfiguredModel: p.Config().Model})
	if err != nil {
		t.Fatal(err)
	}
	scores := make([]float64, len(base.Hits))
	for i := range scores {
		scores[i] = float64(i)
	}
	raw, _ := json.Marshal(scores)
	if _, err = s.DB.Exec(`INSERT INTO knowledge_rerank_cache(fingerprint,job_id,scores_json)VALUES(?,?,?)`, in.Fingerprint, job.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	rc := p.Config()
	req.Rerank = &rc
	before := 0
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	got, err := s.EvaluateKnowledgeRetrieval(ctx, req)
	if err != nil || !got.RerankApplied {
		t.Fatal(got, err)
	}
	fts, err := s.SearchKnowledge(ctx, req.Search)
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range fts.Hits {
		if got.Hits[i].Key != h.Key {
			t.Fatal("paid ranking displaced FTS")
		}
	}
	after := 0
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before {
		t.Fatal("cached read writes")
	}
	old := qualityGateFixture(t, s, cfg.ID)
	old.Fusion = "rrf-k60-lexical-semantic-v1"
	if validateEmbeddingQualityReport(old) == nil {
		t.Fatal("old policy accepted")
	}
}
