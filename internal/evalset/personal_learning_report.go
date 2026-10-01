package evalset

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// LearningEvaluationCorpus freezes private or self-authored material for comparable runs.
type LearningEvaluationCorpus struct {
	Version  int                         `json:"version"`
	Kind     string                      `json:"kind"` // self-authored | real
	Episodes []LearningEvaluationEpisode `json:"episodes"`
	Notes    []LearningEvaluationNote    `json:"notes"`
	Cases    []LearningEvaluationCase    `json:"cases"`
}

// LearningEvaluationEpisode records the actual local transcript, not a live feed dependency.
type LearningEvaluationEpisode struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Source          string             `json:"source"`
	ModelDataPolicy string             `json:"model_data_policy,omitempty"`
	Segments        []provider.Segment `json:"segments"`
}

// LearningEvaluationNote keeps personal reflection separate from source-faithful prose.
type LearningEvaluationNote struct {
	ID        string   `json:"id"`
	EpisodeID string   `json:"episode_id"`
	Kind      string   `json:"kind"`
	Content   string   `json:"content"`
	Segments  []string `json:"segments,omitempty"`
}

// LearningEvaluationCase fixes a question, material scope and human criteria.
type LearningEvaluationCase struct {
	ID        string   `json:"id"`
	Question  string   `json:"question"`
	NoteIDs   []string `json:"note_ids"`
	Expected  []string `json:"expected"`
	Forbidden []string `json:"forbidden"`
}

// LearningHumanScore is explicitly supplied by a named Owner, never inferred from review.
type LearningHumanScore struct {
	Evaluator       string   `json:"evaluator"`
	AnswersQuestion int      `json:"answers_question"`
	SourceFidelity  int      `json:"source_fidelity"`
	Specificity     int      `json:"specificity"`
	Readability     int      `json:"readability"`
	Usefulness      int      `json:"usefulness"`
	SevereError     bool     `json:"severe_error"`
	Changes         []string `json:"changes"`
}

// LearningEvaluationRun separates structural evidence, endpoint evidence and human judgement.
type LearningEvaluationRun struct {
	SourceFingerprint        string              `json:"source_fingerprint"`
	ConfigurationFingerprint string              `json:"configuration_fingerprint"`
	CaseID                   string              `json:"case_id"`
	CorpusHash               string              `json:"corpus_hash"`
	InputHash                string              `json:"input_hash,omitempty"`
	Baseline                 string              `json:"baseline"`
	Model                    string              `json:"model"`
	ReviewModel              string              `json:"review_model"`
	PromptVersion            string              `json:"prompt_version"`
	Status                   string              `json:"status"`
	ArticleID                string              `json:"article_id,omitempty"`
	StructuralIssues         []string            `json:"structural_issues"`
	Human                    *LearningHumanScore `json:"human,omitempty"`
}

// LoadLearningEvaluation reads a bounded manifest and rejects ambiguous identities or references.
func LoadLearningEvaluation(r io.Reader) (LearningEvaluationCorpus, error) {
	var corpus LearningEvaluationCorpus
	raw, err := io.ReadAll(io.LimitReader(r, 4*1024*1024+1))
	if err != nil {
		return corpus, err
	}
	if len(raw) > 4*1024*1024 {
		return corpus, fmt.Errorf("evaluation manifest exceeds 4MB")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&corpus); err != nil {
		return corpus, err
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return corpus, fmt.Errorf("evaluation manifest must contain one JSON object")
	}
	if err = corpus.Validate(); err != nil {
		return corpus, err
	}
	return corpus, nil
}

// Validate preserves source identity and makes incomplete samples explicit rather than inventing them.
func (c LearningEvaluationCorpus) Validate() error {
	if c.Version != 1 || (c.Kind != "self-authored" && c.Kind != "real") {
		return fmt.Errorf("unsupported evaluation version or kind")
	}
	episodes := map[string]map[string]bool{}
	for _, ep := range c.Episodes {
		if ep.ModelDataPolicy != "" && ep.ModelDataPolicy != "local_only" && ep.ModelDataPolicy != "external_allowed" {
			return fmt.Errorf("invalid evaluation source policy")
		}
		if ep.ID == "" || ep.Title == "" || ep.Source == "" || episodes[ep.ID] != nil || len(ep.Segments) == 0 {
			return fmt.Errorf("invalid or duplicate episode")
		}
		segments := map[string]bool{}
		for _, seg := range ep.Segments {
			if seg.ID == "" || strings.TrimSpace(seg.Text) == "" || seg.Start < 0 || seg.End <= seg.Start || segments[seg.ID] {
				return fmt.Errorf("invalid segment in %s", ep.ID)
			}
			segments[seg.ID] = true
		}
		episodes[ep.ID] = segments
	}
	notes := map[string]bool{}
	for _, n := range c.Notes {
		if n.ID == "" || notes[n.ID] || episodes[n.EpisodeID] == nil || strings.TrimSpace(n.Content) == "" || (n.Kind != "source_note" && n.Kind != "owner_reflection") || (n.Kind == "source_note" && len(n.Segments) == 0) {
			return fmt.Errorf("invalid or duplicate note")
		}
		for _, id := range n.Segments {
			if !episodes[n.EpisodeID][id] {
				return fmt.Errorf("note %s refers outside its episode", n.ID)
			}
		}
		notes[n.ID] = true
	}
	cases := map[string]bool{}
	for _, v := range c.Cases {
		if v.ID == "" || cases[v.ID] || strings.TrimSpace(v.Question) == "" || len(v.NoteIDs) < 2 || len(v.Expected) == 0 || len(v.Forbidden) == 0 {
			return fmt.Errorf("invalid or duplicate evaluation case")
		}
		seen := map[string]bool{}
		for _, id := range v.NoteIDs {
			if !notes[id] || seen[id] {
				return fmt.Errorf("invalid material in case %s", v.ID)
			}
			seen[id] = true
		}
		cases[v.ID] = true
	}
	return nil
}

// Fingerprint identifies the complete material and criteria, independent of formatting.
func (c LearningEvaluationCorpus) Fingerprint() string {
	b, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// Gaps reports real acceptance prerequisites; a self-authored run cannot satisfy them.
func (c LearningEvaluationCorpus) Gaps() []string {
	var out []string
	if c.Kind != "real" {
		out = append(out, "real podcast and Owner-note evaluation pending")
	}
	if len(c.Episodes) < 3 {
		out = append(out, "need at least 3 episodes")
	}
	if len(c.Notes) < 20 {
		out = append(out, "need at least 20 notes")
	}
	if len(c.Cases) < 2 {
		out = append(out, "need at least 2 questions")
	}
	return out
}

// Accepted distinguishes complete human scoring from machine review or an empty template.
func (s *LearningHumanScore) Accepted() (bool, error) {
	if s == nil {
		return false, nil
	}
	if strings.TrimSpace(s.Evaluator) == "" {
		return false, fmt.Errorf("human evaluator is required")
	}
	sum := 0
	for _, v := range []int{s.AnswersQuestion, s.SourceFidelity, s.Specificity, s.Readability, s.Usefulness} {
		if v < 1 || v > 5 {
			return false, fmt.Errorf("human score must be 1–5")
		}
		sum += v
	}
	return sum >= 20 && !s.SevereError, nil
}
