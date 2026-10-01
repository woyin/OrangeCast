package evalset

import "testing"

func TestPersonalLearningV4Fixtures(t *testing.T) {
	c := LearningV4Corpus()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.Episodes) != 3 || len(c.Notes) != 20 || len(c.Cases) != 2 || len(c.Gaps()) != 1 {
		t.Fatal("self-authored corpus must preserve real acceptance gap", c.Gaps())
	}
	if c.Fingerprint() != LearningV4Corpus().Fingerprint() {
		t.Fatal("non-deterministic corpus")
	}
	segments := map[string]bool{}
	for _, ep := range c.Episodes {
		for _, seg := range ep.Segments {
			segments[seg.ID] = true
		}
	}
	queries, paraphrases, missing := LearningV4RetrievalCases(), 0, 0
	seen := map[string]bool{}
	for _, q := range queries {
		if seen[q.ID] || q.Query == "" {
			t.Fatal("invalid query identity", q)
		}
		seen[q.ID] = true
		if q.Paraphrase {
			paraphrases++
		}
		if len(q.RelevantSegmentIDs) == 0 {
			missing++
		}
		for _, id := range q.RelevantSegmentIDs {
			if !segments[id] {
				t.Fatal("invented relevance", id)
			}
		}
	}
	if len(queries) != 40 || paraphrases < 10 || missing != 5 {
		t.Fatal(len(queries), paraphrases, missing)
	}
	understandings := LearningV4UnderstandingCases()
	if len(understandings) != 2 || understandings[0].Body == "" || len(understandings[0].ReferenceSegmentIDs) != 0 {
		t.Fatal("Owner-only answer must not manufacture a Source")
	}
	for _, id := range understandings[1].ReferenceSegmentIDs {
		if !segments[id] {
			t.Fatal("invented understanding reference", id)
		}
	}
}
