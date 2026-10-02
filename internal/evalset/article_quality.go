package evalset

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/provider"

	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArticleQualityRun is a private checkpoint and receipt, not a claim of human quality.
type ArticleQualityRun struct {
	OriginalOutputJSON, Expected                                                                                    string
	ManifestHash, CaseID, CaseFingerprint, Provider, Model, ActualModel, PromptVersion, PriceVersion, Status, Error string
	Input                                                                                                           provider.KnowledgeArticleRequest
	Output                                                                                                          *provider.KnowledgeArticleResult
	Usage                                                                                                           provider.TaskUsage
	UsageKnown                                                                                                      bool
	DurationMillis                                                                                                  int64
	EstimatedCost, ActualCost                                                                                       *float64
	Human                                                                                                           *LearningHumanScore
}

// ArticleQualityRegression preserves unknown usage and missing human judgement.
type ArticleQualityRegression struct {
	HumanAssessed                                          int
	HumanBelowThreshold                                    int
	KnownCosts, UnknownCosts, UnknownEstimates             int
	QualityStatus                                          string
	TechnicalFailures, ReviewFailures                      int
	Runs                                                   []ArticleQualityRun
	KnownUsage, UnknownUsage, HumanMissing, SevereFailures int
	EstimatedCost, KnownActualCost                         float64
	EstimationError                                        *float64
}

// SummarizeArticleQuality compares one frozen case and purpose, counts unknown prices separately,
// and accepts quality only from valid explicit Owner scores without severe errors.
func SummarizeArticleQuality(runs []ArticleQualityRun) (ArticleQualityRegression, error) {
	report := ArticleQualityRegression{Runs: runs, QualityStatus: "pending_human"}
	var corpus string
	var purpose string
	for i, r := range runs {
		raw, _ := json.Marshal(r.Input.WritingPurpose)
		if i == 0 {
			corpus = r.CaseFingerprint
			purpose = string(raw)
		} else if r.CaseFingerprint != corpus || (r.Status == "succeeded" && string(raw) != purpose) {
			return report, fmt.Errorf("different frozen case or writing purpose cannot share quality criteria")
		}
	}
	for _, r := range runs {
		if r.Status != "succeeded" {
			report.TechnicalFailures++
		}
		if r.Input.Stage == "review" && (r.Output == nil || r.Output.Passed == nil || !*r.Output.Passed) {
			report.ReviewFailures++
		}
		if r.UsageKnown {
			report.KnownUsage++
		} else {
			report.UnknownUsage++
		}
		if r.EstimatedCost == nil {
			report.UnknownEstimates++
		}
		if r.ActualCost == nil {
			report.UnknownCosts++
		} else {
			report.KnownCosts++
		}
		if r.EstimatedCost != nil {
			report.EstimatedCost += *r.EstimatedCost
		}
		if r.ActualCost != nil {
			report.KnownActualCost += *r.ActualCost
		}
		if r.Human == nil {
			if r.Input.Stage != "review" {
				report.HumanMissing++
			}
		} else {
			report.HumanAssessed++
			h := r.Human
			if h.Evaluator == "" || h.AnswersQuestion < 1 || h.AnswersQuestion > 5 || h.SourceFidelity < 1 || h.SourceFidelity > 5 || h.Specificity < 1 || h.Specificity > 5 || h.Readability < 1 || h.Readability > 5 || h.Usefulness < 1 || h.Usefulness > 5 {
				return report, fmt.Errorf("invalid explicit human assessment")
			}
			if h.AnswersQuestion+h.SourceFidelity+h.Specificity+h.Readability+h.Usefulness < 20 {
				report.HumanBelowThreshold++
			}
			if h.SevereError {
				report.SevereFailures++
			}
		}
	}
	if report.UnknownUsage == 0 && len(runs) > 0 {
		allCosts := true
		for _, r := range runs {
			if r.ActualCost == nil || r.EstimatedCost == nil {
				allCosts = false
			}
		}
		if allCosts {
			v := report.KnownActualCost - report.EstimatedCost
			report.EstimationError = &v
		}
	}
	if report.SevereFailures > 0 {
		report.QualityStatus = "failed_severe_error"
	} else if report.HumanBelowThreshold > 0 {
		report.QualityStatus = "failed_human_threshold"
	} else if report.TechnicalFailures > 0 || report.ReviewFailures > 0 {
		report.QualityStatus = "failed_stage"
	} else if report.HumanMissing == 0 && report.HumanAssessed > 0 && len(runs) > 0 {
		report.QualityStatus = "accepted_human"
	}
	return report, nil
}
func writeQualityCheckpoint(path string, r ArticleQualityRun) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(path+".tmp", path)
}

// RunArticleQuality executes exactly one isolated, explicitly selected case. A dispatched checkpoint blocks replay;
// completed receipts resume without calling the provider. Re-authorize reads current permissions before dispatch.
func RunArticleQuality(ctx context.Context, m *QualityManifest, id, model, prompt, price, path string, p provider.KnowledgeArticleProvider, authorize func(context.Context, string, string) error) (ArticleQualityRun, error) {
	var run ArticleQualityRun
	if m == nil || len(m.Cases) == 0 || len(m.Cases) > 100 || m.Provider != p.Name() || model == "" || prompt == "" || price == "" || authorize == nil {
		return run, fmt.Errorf("invalid bounded quality run")
	}
	var selected *QualityCase
	for _, c := range m.Cases {
		if c.ID == id {
			selected = c
		}
	}
	if selected == nil || selected.State != "accepted" {
		return run, fmt.Errorf("case not accepted")
	}
	if err := authorize(ctx, id, p.Name()); err != nil {
		return run, err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &run) != nil || run.ManifestHash != m.Fingerprint || run.CaseID != id || run.Model != model || run.PromptVersion != prompt || run.PriceVersion != price {
			return run, fmt.Errorf("checkpoint identity conflict")
		}
		if run.Status == "succeeded" {
			return run, nil
		}
		if run.Status == "failed" {
			return run, fmt.Errorf("previous attempt failed; no automatic paid retry")
		}
		return run, fmt.Errorf("previous dispatch outcome unknown; no automatic paid retry")
	} else if !os.IsNotExist(err) {
		return run, err
	}
	if err := json.Unmarshal([]byte(selected.InputJSON), &run.Input); err != nil {
		return run, err
	}
	if run.Input.Stage != "review" {
		run.Input.Stage = "write"
	}
	run.Input.PromptVersion = prompt
	run.Expected = selected.Expected
	run.OriginalOutputJSON = selected.BlocksJSON
	suffix := ArticleQualityExpectedInstruction(selected.Expected)
	if suffix != "" && !strings.HasSuffix(run.Input.Instructions, suffix) {
		run.Input.Instructions += suffix
	}
	if run.Input.StageConfigs == nil {
		run.Input.StageConfigs = map[string]provider.KnowledgeStageConfig{}
	}
	cfg := provider.FreezeKnowledgeStageConfigs(map[string]string{run.Input.Stage: model})
	run.Input.StageConfigs[run.Input.Stage] = cfg[run.Input.Stage]
	if run.Input.Stage == "review" {
		run.Input.ReviewModel = model
	}
	run.ManifestHash = m.Fingerprint
	run.CaseID = id
	run.CaseFingerprint = selected.Fingerprint
	run.Provider = p.Name()
	run.Model = model
	run.PromptVersion = prompt
	run.PriceVersion = price
	frozenPrice := run.Input.Estimate
	estimate, estimateErr := provider.EstimateKnowledgeRequest(run.Input, model)
	if estimateErr == nil && estimate != nil && frozenPrice != nil && frozenPrice.InputFingerprint == estimate.InputFingerprint {
		estimate.PriceKnown = frozenPrice.PriceKnown
		estimate.PriceUpdatedAt = frozenPrice.PriceUpdatedAt
		estimate.InputCentsPerMillion = frozenPrice.InputCentsPerMillion
		estimate.OutputCentsPerMillion = frozenPrice.OutputCentsPerMillion
		if cents, known := estimate.CostForUnits(estimate.InputTokens, estimate.OutputTokens); known {
			estimate.CostCents = &cents
		}
	}
	if estimateErr == nil {
		run.Input.Estimate = estimate
		if estimate != nil && estimate.CostCents != nil {
			v := float64(*estimate.CostCents)
			run.EstimatedCost = &v
		}
	}
	run.Status = "dispatched"
	if err := writeQualityCheckpoint(path, run); err != nil {
		return run, err
	}
	start := time.Now()
	output, usage, err := p.KnowledgeArticleStep(ctx, run.Input)
	run.DurationMillis = time.Since(start).Milliseconds()
	if err == nil {
		err = provider.ValidateKnowledgeResult(run.Input, output)
	}
	run.Usage = usage
	run.UsageKnown = usage.InputUnits > 0 || usage.OutputUnits > 0
	run.Output = output
	priceModelMatches := true // Legacy local test providers have no response model metadata.
	if metered, ok := p.(interface {
		QualityReceiptModel() string
		QualityReceiptUsageKnown() bool
	}); ok {
		run.ActualModel = metered.QualityReceiptModel()
		priceModelMatches = run.ActualModel != "" && run.ActualModel == model
		run.UsageKnown = metered.QualityReceiptUsageKnown()
	}
	if permissionErr := authorize(ctx, id, p.Name()); permissionErr != nil {
		run.Output = nil
		run.OriginalOutputJSON = ""
		run.Input = provider.KnowledgeArticleRequest{}
		err = fmt.Errorf("case permissions revoked; source body suppressed")
	}
	if estimateErr == nil && estimate != nil {
		if estimate.CostCents != nil {
			v := float64(*estimate.CostCents)
			run.EstimatedCost = &v
		}
		if run.UsageKnown && priceModelMatches {
			if cents, known := estimate.CostForUnits(usage.InputUnits, usage.OutputUnits); known {
				v := float64(cents)
				run.ActualCost = &v
			}
		}
	}
	if err != nil {
		run.Status = "failed"
		run.Error = "provider operation failed; private output and numerical usage retained"
	} else {
		run.Status = "succeeded"
	}
	if writeErr := writeQualityCheckpoint(path, run); writeErr != nil {
		return run, writeErr
	}
	return run, err
}

// QualityManifest is the portable private frozen manifest accepted by the isolated runner.
type QualityManifest struct {
	Version               int
	Provider, Fingerprint string
	Cases                 []*QualityCase
}

// QualityCase carries the accepted private input, original output and Owner expectation;
// state and fingerprint must match the manifest before any paid dispatch.
type QualityCase struct{ ID, State, InputJSON, BlocksJSON, Expected, Fingerprint string }

// ArticleQualityExpectedInstruction serializes the exact Owner expectation used by both estimation and dispatch.
// It does not grant new source permissions or relax the grounded writing contract.
func ArticleQualityExpectedInstruction(expected string) string {
	if expected == "" {
		return ""
	}
	return "\nOwner明确接纳的私有评测期望：" + expected + "。仍只能使用给定来源，不为满足期望补造事实。"
}
