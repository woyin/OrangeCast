package evalset

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func TestLearningEvaluationCorpusIdentityAndGaps(t *testing.T) {
	c := LearningEvaluationCorpus{Version: 1, Kind: "self-authored", Episodes: []LearningEvaluationEpisode{{ID: "e", Title: "自建", Source: "self-made", Segments: []provider.Segment{{ID: "s", Start: 0, End: 1, Text: "原文"}}}}, Notes: []LearningEvaluationNote{{ID: "n1", EpisodeID: "e", Kind: "source_note", Content: "来源转述", Segments: []string{"s"}}, {ID: "n2", EpisodeID: "e", Kind: "owner_reflection", Content: "我的理解"}}, Cases: []LearningEvaluationCase{{ID: "c", Question: "怎样辨认表达？", NoteIDs: []string{"n1", "n2"}, Expected: []string{"分别归因"}, Forbidden: []string{"理解当作原文"}}}}
	b, _ := json.Marshal(c)
	loaded, err := LoadLearningEvaluation(strings.NewReader(string(b)))
	if err != nil || loaded.Fingerprint() != c.Fingerprint() || len(loaded.Gaps()) != 4 {
		t.Fatal(err, loaded.Gaps())
	}
	if _, err = LoadLearningEvaluation(strings.NewReader(string(b) + " {}")); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	for _, mutate := range []func(*LearningEvaluationCorpus){
		func(c *LearningEvaluationCorpus) { c.Version = 2 },
		func(c *LearningEvaluationCorpus) { c.Kind = "fake-real" },
		func(c *LearningEvaluationCorpus) { c.Episodes[0].Segments[0].End = 0 },
		func(c *LearningEvaluationCorpus) { c.Episodes[0].ModelDataPolicy = "unknown" },
		func(c *LearningEvaluationCorpus) { c.Notes[0].Segments = []string{"missing"} },
		func(c *LearningEvaluationCorpus) { c.Notes[1].ID = "n1" },
		func(c *LearningEvaluationCorpus) { c.Cases[0].NoteIDs = []string{"n1", "n1"} },
	} {
		var invalid LearningEvaluationCorpus
		_ = json.Unmarshal(b, &invalid)
		mutate(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("invalid corpus accepted")
		}
	}
	if _, err = LoadLearningEvaluation(strings.NewReader(`{"version":1,"kind":"real","api_key":"secret"}`)); err == nil {
		t.Fatal("unknown fields accepted")
	}
	if _, err = LoadLearningEvaluation(strings.NewReader(strings.Repeat(" ", 4*1024*1024+1))); err == nil {
		t.Fatal("unbounded manifest")
	}
	empty := LearningEvaluationCorpus{Version: 1, Kind: "real"}
	if empty.Validate() != nil || len(empty.Gaps()) != 3 {
		t.Fatal("empty template must remain pending")
	}
}

func TestLearningHumanScoreRequiresActualCompleteScoring(t *testing.T) {
	var s *LearningHumanScore
	if ok, err := s.Accepted(); ok || err != nil {
		t.Fatal("nil human score accepted")
	}
	s = &LearningHumanScore{Evaluator: "Owner", AnswersQuestion: 4, SourceFidelity: 4, Specificity: 4, Readability: 4, Usefulness: 4}
	if ok, err := s.Accepted(); !ok || err != nil {
		t.Fatal(ok, err)
	}
	s.SevereError = true
	if ok, _ := s.Accepted(); ok {
		t.Fatal("severe attribution error passed")
	}
	s.SevereError = false
	s.Usefulness = 3
	if ok, _ := s.Accepted(); ok {
		t.Fatal("below threshold passed")
	}
	s.Usefulness = 0
	if _, err := s.Accepted(); err == nil {
		t.Fatal("missing dimension accepted")
	}
	s.Usefulness = 4
	s.Evaluator = ""
	if _, err := s.Accepted(); err == nil {
		t.Fatal("anonymous score accepted")
	}
}
