package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Replay actual, public-fixture model scores through the production ordering
// function. This has no network dependency and does not label its own answers.
func TestKnowledgeJinaPaidCandidateReplayNonRegression(t *testing.T) {
	path := "../../docs/superpowers/validation/jina-retrieval-v3/v35-raw.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Queries       int    `json:"queries"`
		FixtureSHA256 string `json:"fixture_sha256"`
		Results       []struct {
			ID, Group, Query string
			Relevant         []string           `json:"relevant_ids"`
			Lexical          map[string]float64 `json:"lexical"`
			Hybrid           map[string]float64 `json:"hybrid"`
			RawRerank        map[string]float64 `json:"rerank"`
			Candidates       []struct {
				ID        string
				Rank      int
				Protected bool `json:"lexical_protected"`
				Score     float64
			} `json:"candidates"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &captured) != nil || captured.Queries != 80 || len(captured.Results) != 80 {
		t.Fatal("invalid captured scores")
	}
	fixture, err := os.ReadFile("testdata/jina-retrieval-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(fixture)) != captured.FixtureSHA256 {
		t.Fatal("capture fixture changed")
	}
	rows := []map[string]any{}
	groups := map[string]map[string]map[string]float64{}
	counts := map[string]int{}
	gains, ties, regressions := 0, 0, 0
	metric := func(hits []KnowledgeSearchHit, want string) map[string]float64 {
		m := map[string]float64{"recall_at_1": 0, "recall_at_5": 0, "recall_at_10": 0, "mrr_at_10": 0}
		for i, h := range hits[:min(10, len(hits))] {
			if h.Key == want {
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
	for _, q := range captured.Results {
		if len(q.Relevant) != 1 || len(q.Candidates) != 30 {
			t.Fatal("candidate snapshot")
		}
		hits := make([]KnowledgeSearchHit, len(q.Candidates))
		scores := map[string]float64{}
		for i, c := range q.Candidates {
			if c.Rank != i+1 || c.ID == "" {
				t.Fatal("candidate rank")
			}
			kind := "semantic"
			if c.Protected {
				kind = "both"
			}
			hits[i] = KnowledgeSearchHit{Key: c.ID, Revision: 1, MatchKind: kind, LexicalProtected: c.Protected}
			scores[c.ID] = c.Score
		}
		applyKnowledgeRerankScores(hits, scores, true)
		got := metric(hits, q.Relevant[0])
		baseline := q.Lexical["mrr_at_10"]
		if got["mrr_at_10"] > baseline {
			gains++
		} else if got["mrr_at_10"] == baseline {
			ties++
		} else {
			regressions++
		}
		for _, key := range []string{"recall_at_1", "recall_at_5", "recall_at_10", "mrr_at_10"} {
			if got[key] < q.Lexical[key] {
				t.Errorf("FTS regression: %s %s %.3f < %.3f", q.ID, key, got[key], q.Lexical[key])
			}
		}
		if got["recall_at_10"] < q.Hybrid["recall_at_10"] {
			t.Errorf("hybrid recall displaced: %s", q.ID)
		}
		if q.ID == "rewrite-01" && got["recall_at_1"] != 1 {
			t.Error("known severe displacement remains")
		}
		top := []string{}
		for _, h := range hits[:10] {
			top = append(top, h.Key)
		}
		rows = append(rows, map[string]any{"id": q.ID, "group": q.Group, "query": q.Query, "relevant_ids": q.Relevant, "fts": q.Lexical, "hybrid": q.Hybrid, "raw_rerank": q.RawRerank, "guarded_rerank": got, "guarded_top10": top})
		if groups[q.Group] == nil {
			groups[q.Group] = map[string]map[string]float64{"fts": {}, "hybrid": {}, "raw_rerank": {}, "guarded_rerank": {}}
		}
		for method, m := range map[string]map[string]float64{"fts": q.Lexical, "hybrid": q.Hybrid, "raw_rerank": q.RawRerank, "guarded_rerank": got} {
			for k, v := range m {
				groups[q.Group][method][k] += v
			}
		}
		counts[q.Group]++
	}
	for group, methods := range groups {
		for _, m := range methods {
			for k, v := range m {
				m[k] = v / float64(counts[group])
			}
		}
	}
	if gains != 60 || ties != 20 || regressions != 0 {
		t.Fatal("expected frozen comparison", gains, ties, regressions)
	}
	report := map[string]any{"queries": 80, "ranking_policy": KnowledgeFusionVersion, "rerank_policy": knowledgeRerankPolicyVersion, "replay_source_sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "fixture_sha256": captured.FixtureSHA256, "remote_calls_in_replay": 0, "strict_mrr_improvements": gains, "ties": ties, "fts_regressions": regressions, "groups": groups, "results": rows, "personal_corpus": false, "human_attested_relevance": false, "quality_gate_passed": false}
	if output := os.Getenv("CWP_JINA_REPLAY_OUTPUT"); output != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(output, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual-score replay: gains=%d ties=%d regressions=%d groups=%v", gains, ties, regressions, groups)
}
