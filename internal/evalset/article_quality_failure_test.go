package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

type failureQualityProvider struct {
	calls int
	step  func(provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error)
}

func (p *failureQualityProvider) Name() string { return "isolated" }
func (p *failureQualityProvider) KnowledgeArticleStep(_ context.Context, r provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	p.calls++
	if p.step != nil {
		return p.step(r)
	}
	return &provider.KnowledgeArticleResult{Title: "自建", Blocks: []provider.KnowledgeBlock{{Kind: "synthesis", Text: "条件", MaterialIDs: []string{"m"}}, {Kind: "synthesis", Text: "边界", MaterialIDs: []string{"m"}}}}, provider.TaskUsage{InputUnits: 100, OutputUnits: 10}, nil
}

type meteredFailureQualityProvider struct {
	failureQualityProvider
	known bool
	model string
}

func (p *meteredFailureQualityProvider) QualityReceiptModel() string {
	if p.model != "" {
		return p.model
	}
	return "actual-route"
}
func (p *meteredFailureQualityProvider) QualityReceiptUsageKnown() bool { return p.known }
func failureQualityManifest(t *testing.T) *QualityManifest {
	t.Helper()
	raw, e := json.Marshal(provider.KnowledgeArticleRequest{Topic: &provider.KnowledgeTopic{Title: "自建", Question: "条件", MaterialIDs: []string{"m"}}, Materials: []provider.KnowledgeMaterial{{ID: "m", Kind: "source_note", Content: "私人自建来源"}}})
	if e != nil {
		t.Fatal(e)
	}
	return &QualityManifest{Provider: "isolated", Fingerprint: "manifest", Cases: []*QualityCase{{ID: "case", State: "accepted", InputJSON: string(raw), BlocksJSON: "私人原输出", Expected: "私人期望", Fingerprint: "frozen"}}}
}
func permitQuality(context.Context, string, string) error { return nil }
func invokeFailureQuality(t *testing.T, m *QualityManifest, path string, p provider.KnowledgeArticleProvider) (ArticleQualityRun, error) {
	t.Helper()
	return RunArticleQuality(t.Context(), m, "case", "fake-model", provider.KnowledgeArticlePromptVersion, "price-v1", path, p, permitQuality)
}

func TestQualityRunRefusesInvalidAndUnreadableInputs(t *testing.T) {
	for _, kind := range []string{"nil", "empty", "overbound", "provider", "missing", "unaccepted", "bad-input", "directory", "bad-checkpoint", "identity"} {
		t.Run(kind, func(t *testing.T) {
			m := failureQualityManifest(t)
			p := &failureQualityProvider{}
			path := filepath.Join(t.TempDir(), "receipt.json")
			switch kind {
			case "nil":
				m = nil
			case "empty":
				m.Cases = nil
			case "overbound":
				for len(m.Cases) < 101 {
					m.Cases = append(m.Cases, m.Cases[0])
				}
			case "provider":
				m.Provider = "wrong"
			case "missing":
				m.Cases[0].ID = "other"
			case "unaccepted":
				m.Cases[0].State = "proposed"
			case "bad-input":
				m.Cases[0].InputJSON = "{"
			case "directory":
				if e := os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			case "bad-checkpoint":
				if e := os.WriteFile(path, []byte("{"), 0600); e != nil {
					t.Fatal(e)
				}
			case "identity":
				if e := writeQualityCheckpoint(path, ArticleQualityRun{ManifestHash: "other"}); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 0 {
				t.Fatalf("invalid input dispatched: calls=%d err=%v", p.calls, e)
			}
		})
	}
	m := failureQualityManifest(t)
	p := &failureQualityProvider{}
	path := filepath.Join(t.TempDir(), "r.json")
	for _, args := range [][3]string{{"", provider.KnowledgeArticlePromptVersion, "price"}, {"model", "", "price"}, {"model", provider.KnowledgeArticlePromptVersion, ""}} {
		if _, e := RunArticleQuality(t.Context(), m, "case", args[0], args[1], args[2], path, p, permitQuality); e == nil {
			t.Fatal("missing identity accepted")
		}
	}
	if _, e := RunArticleQuality(t.Context(), m, "case", "model", provider.KnowledgeArticlePromptVersion, "price", path, p, nil); e == nil {
		t.Fatal("nil authorizer accepted")
	}
}
func TestQualityFailedAndUnknownReceiptsNeverResend(t *testing.T) {
	for _, status := range []string{"failed", "dispatched", "unknown"} {
		t.Run(status, func(t *testing.T) {
			m := failureQualityManifest(t)
			p := &failureQualityProvider{}
			path := filepath.Join(t.TempDir(), "receipt.json")
			r := ArticleQualityRun{ManifestHash: m.Fingerprint, CaseID: "case", Model: "fake-model", PromptVersion: provider.KnowledgeArticlePromptVersion, PriceVersion: "price-v1", Status: status}
			if e := writeQualityCheckpoint(path, r); e != nil {
				t.Fatal(e)
			}
			if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 0 {
				t.Fatal("paid replay", e, p.calls)
			}
		})
	}
}
func TestQualityProviderFailureRetainsUsageAndDispatchIdentity(t *testing.T) {
	m := failureQualityManifest(t)
	p := &failureQualityProvider{step: func(r provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
		if !strings.Contains(r.Instructions, "私人期望") || r.StageConfigs["write"].Model != "fake-model" {
			t.Fatal("frozen input lost")
		}
		return nil, provider.TaskUsage{InputUnits: 321, OutputUnits: 7}, errors.New("provider private failure")
	}}
	path := filepath.Join(t.TempDir(), "r.json")
	r, e := invokeFailureQuality(t, m, path, p)
	if e == nil || r.Status != "failed" || !r.UsageKnown || r.Usage.InputUnits != 321 || r.ActualCost != nil || r.EstimatedCost != nil {
		t.Fatalf("failure receipt %+v %v", r, e)
	}
	if strings.Contains(r.Error, "provider private failure") {
		t.Fatal("provider error leaked")
	}
	if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 1 {
		t.Fatal("failure retried", e)
	}
}
func TestQualityMeteredReceiptAndRevocation(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "known-zero"}[known], func(t *testing.T) {
			m := failureQualityManifest(t)
			p := &meteredFailureQualityProvider{known: known, model: "fake-model"}
			p.step = func(provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
				return nil, provider.TaskUsage{}, errors.New("failed")
			}
			r, _ := invokeFailureQuality(t, m, filepath.Join(t.TempDir(), "r"), p)
			if r.ActualModel != "fake-model" || r.UsageKnown != known {
				t.Fatal("metered metadata lost", r)
			}
			if known && (r.ActualCost == nil || *r.ActualCost != 0) {
				t.Fatal("known zero matching model must be preserved")
			}
		})
	}
	m := failureQualityManifest(t)
	p := &failureQualityProvider{}
	n := 0
	path := filepath.Join(t.TempDir(), "r")
	r, e := RunArticleQuality(t.Context(), m, "case", "fake-model", provider.KnowledgeArticlePromptVersion, "price-v1", path, p, func(context.Context, string, string) error {
		n++
		if n == 2 {
			return errors.New("revoked")
		}
		return nil
	})
	if e == nil || r.Status != "failed" || r.Output != nil || len(r.Input.Materials) != 0 || r.OriginalOutputJSON != "" || r.Usage.InputUnits != 100 {
		t.Fatalf("revoked receipt %+v %v", r, e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "私人自建来源") || strings.Contains(string(raw), "私人原输出") {
		t.Fatal("source leaked after revoke")
	}
}
func TestQualityCheckpointIOFailuresRemainNonReplayable(t *testing.T) {
	for _, kind := range []string{"marshal", "parent-file", "temp-directory", "rename-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "receipt")
			r := ArticleQualityRun{}
			switch kind {
			case "marshal":
				v := math.Inf(1)
				r.ActualCost = &v
			case "parent-file":
				if e := os.WriteFile(filepath.Join(dir, "parent"), []byte("x"), 0600); e != nil {
					t.Fatal(e)
				}
				path = filepath.Join(dir, "parent", "receipt")
			case "temp-directory":
				if e := os.Mkdir(path+".tmp", 0700); e != nil {
					t.Fatal(e)
				}
			case "rename-directory":
				if e := os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e := writeQualityCheckpoint(path, r); e == nil {
				t.Fatal("IO failure accepted")
			}
		})
	}
	m := failureQualityManifest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "r")
	if e := os.Mkdir(path+".tmp", 0700); e != nil {
		t.Fatal(e)
	}
	p := &failureQualityProvider{}
	if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 0 {
		t.Fatal("dispatch after unwritten intent")
	}
	path = filepath.Join(t.TempDir(), "receipt")
	p = &failureQualityProvider{step: func(provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
		if e := os.Mkdir(path+".tmp", 0700); e != nil {
			t.Fatal(e)
		}
		return nil, provider.TaskUsage{InputUnits: 5}, errors.New("remote failed")
	}}
	if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 1 {
		t.Fatal("final checkpoint failure missing")
	}
	if _, e := invokeFailureQuality(t, m, path, p); e == nil || p.calls != 1 {
		t.Fatal("lost receipt replayed")
	}
}
func TestQualitySummaryHumanAndReviewGates(t *testing.T) {
	score := func(n int) *LearningHumanScore {
		return &LearningHumanScore{Evaluator: "Owner", AnswersQuestion: n, SourceFidelity: n, Specificity: n, Readability: n, Usefulness: n}
	}
	passed := true
	failed := false
	for _, tc := range []struct {
		name    string
		runs    []ArticleQualityRun
		status  string
		wantErr bool
	}{{"accepted", []ArticleQualityRun{{Status: "succeeded", Human: score(4)}}, "accepted_human", false}, {"below", []ArticleQualityRun{{Status: "succeeded", Human: score(3)}}, "failed_human_threshold", false}, {"failed", []ArticleQualityRun{{Status: "failed", Human: score(5)}}, "failed_stage", false}, {"review-no-output", []ArticleQualityRun{{Status: "succeeded", Input: provider.KnowledgeArticleRequest{Stage: "review"}}}, "failed_stage", false}, {"review-false", []ArticleQualityRun{{Status: "succeeded", Input: provider.KnowledgeArticleRequest{Stage: "review"}, Output: &provider.KnowledgeArticleResult{Passed: &failed}}}, "failed_stage", false}, {"review-passed", []ArticleQualityRun{{Status: "succeeded", Input: provider.KnowledgeArticleRequest{Stage: "review"}, Output: &provider.KnowledgeArticleResult{Passed: &passed}}}, "pending_human", false}, {"frozen-mismatch", []ArticleQualityRun{{CaseFingerprint: "a"}, {CaseFingerprint: "b"}}, "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := SummarizeArticleQuality(tc.runs)
			if (e != nil) != tc.wantErr || (!tc.wantErr && r.QualityStatus != tc.status) {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
	h := score(5)
	h.Evaluator = ""
	if _, e := SummarizeArticleQuality([]ArticleQualityRun{{Human: h}}); e == nil {
		t.Fatal("anonymous assessment accepted")
	}
	r, e := SummarizeArticleQuality(nil)
	if e != nil || r.QualityStatus != "pending_human" || r.EstimationError != nil {
		t.Fatal("empty report invented acceptance")
	}
}

func TestQualityReviewRouteAndInvalidResult(t *testing.T) {
	m := failureQualityManifest(t)
	var input provider.KnowledgeArticleRequest
	if e := json.Unmarshal([]byte(m.Cases[0].InputJSON), &input); e != nil {
		t.Fatal(e)
	}
	input.Stage = "review"
	input.StageConfigs = map[string]provider.KnowledgeStageConfig{"write": {Model: "historic-write"}}
	raw, _ := json.Marshal(input)
	m.Cases[0].InputJSON = string(raw)
	p := &failureQualityProvider{step: func(r provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
		if r.Stage != "review" || r.ReviewModel != "fake-model" || r.StageConfigs["write"].Model != "historic-write" {
			t.Fatalf("stage freeze changed %+v", r)
		}
		return &provider.KnowledgeArticleResult{}, provider.TaskUsage{OutputUnits: 17}, nil
	}}
	r, e := invokeFailureQuality(t, m, filepath.Join(t.TempDir(), "r"), p)
	if e == nil || r.Status != "failed" || r.Output == nil || r.Usage.OutputUnits != 17 {
		t.Fatalf("invalid result treated as success %+v %v", r, e)
	}
}
func TestQualityUnsupportedPromptPreservesUnknownEstimate(t *testing.T) {
	m := failureQualityManifest(t)
	p := &failureQualityProvider{}
	r, e := RunArticleQuality(t.Context(), m, "case", "fake-model", "unsupported", "price-v1", filepath.Join(t.TempDir(), "r"), p, permitQuality)
	if e != nil || p.calls != 1 || r.EstimatedCost != nil || r.Input.Estimate != nil {
		t.Fatalf("estimate failure fabricated known values %+v %v", r, e)
	}
}
func TestQualitySummaryRejectsMixedPurpose(t *testing.T) {
	a := ArticleQualityRun{Status: "succeeded", CaseFingerprint: "same", Input: provider.KnowledgeArticleRequest{WritingPurpose: &provider.FrozenWritingPurpose{Mode: "synthesis"}}}
	b := a
	b.Input.WritingPurpose = &provider.FrozenWritingPurpose{Mode: "practice"}
	if _, e := SummarizeArticleQuality([]ArticleQualityRun{a, b}); e == nil {
		t.Fatal("mixed writing modes accepted")
	}
}

// Rates below are an explicit self-authored price fixture, not a claim about any real model.
func TestQualityFrozenPriceReceipt(t *testing.T) {
	for _, mode := range []string{"matching", "changed-body", "unknown-rate", "known-zero", "model-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			m := failureQualityManifest(t)
			var input provider.KnowledgeArticleRequest
			if e := json.Unmarshal([]byte(m.Cases[0].InputJSON), &input); e != nil {
				t.Fatal(e)
			}
			input.Stage = "write"
			input.PromptVersion = provider.KnowledgeArticlePromptVersion
			input.Instructions += ArticleQualityExpectedInstruction(m.Cases[0].Expected)
			input.StageConfigs = provider.FreezeKnowledgeStageConfigs(map[string]string{"write": "fake-model"})
			estimate, e := provider.EstimateKnowledgeRequest(input, "fake-model")
			if e != nil {
				t.Fatal(e)
			}
			estimate.PriceKnown = mode != "unknown-rate"
			estimate.PriceUpdatedAt = "self-authored-price-fixture-v1"
			estimate.InputCentsPerMillion = 1000000
			estimate.OutputCentsPerMillion = 2000000
			input.Estimate = estimate
			if mode == "changed-body" {
				input.Materials[0].Content += " changed after freeze"
			}
			raw, e := json.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
			m.Cases[0].InputJSON = string(raw)
			p := &meteredFailureQualityProvider{known: true, model: "fake-model"}
			if mode == "model-mismatch" {
				p.model = "actual-route"
			}
			p.step = func(r provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
				if strings.Count(r.Instructions, ArticleQualityExpectedInstruction(m.Cases[0].Expected)) != 1 {
					t.Fatal("Owner suffix duplicated")
				}
				usage := provider.TaskUsage{InputUnits: 100, OutputUnits: 10}
				if mode == "known-zero" {
					usage = provider.TaskUsage{}
				}
				return nil, usage, errors.New("private provider failure")
			}
			path := filepath.Join(t.TempDir(), "receipt")
			r, e := invokeFailureQuality(t, m, path, p)
			if e == nil || p.calls != 1 || !r.UsageKnown {
				t.Fatalf("expected retained failed receipt %+v %v", r, e)
			}
			switch mode {
			case "matching", "known-zero":
				wantEstimated := float64(estimate.InputTokens + 2*estimate.OutputTokens)
				if r.EstimatedCost == nil || *r.EstimatedCost != wantEstimated {
					t.Fatalf("frozen estimated cents %+v want %v", r.EstimatedCost, wantEstimated)
				}
				wantActual := 120.0
				if mode == "known-zero" {
					wantActual = 0
				}
				if r.ActualCost == nil || *r.ActualCost != wantActual || r.Input.Estimate.PriceUpdatedAt != estimate.PriceUpdatedAt {
					t.Fatalf("frozen price receipt %+v", r)
				}
				saved, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				var receipt ArticleQualityRun
				if e = json.Unmarshal(saved, &receipt); e != nil || receipt.ActualCost == nil || *receipt.ActualCost != wantActual {
					t.Fatalf("numerical fee not persisted %+v %v", receipt, e)
				}
			case "model-mismatch":
				if r.ActualCost != nil || r.EstimatedCost == nil || !r.UsageKnown {
					t.Fatal("mismatch lost numeric receipt or charged configured rate", r)
				}
			default:
				if r.EstimatedCost != nil || r.ActualCost != nil || r.Input.Estimate.PriceKnown {
					t.Fatal("stale or unknown price was applied", r)
				}
			}
		})
	}
}
