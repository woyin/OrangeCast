package provider

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"unicode"
)

// KnowledgeStageConfig freezes routing and the bounded output contract.
type KnowledgeStageConfig struct {
	Model           string `json:"model"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

// KnowledgeEstimate is an admission estimate, distinct from actual receipt usage.
// Unverified tokenization is explicitly approximate; unknown price has no amount.
type KnowledgeEstimate struct {
	Method                string `json:"method"`
	InputFingerprint      string `json:"input_fingerprint"`
	InputTokens           int    `json:"input_tokens"`
	OutputTokens          int    `json:"output_tokens"`
	Approximate           bool   `json:"approximate"`
	PriceKnown            bool   `json:"price_known"`
	InputCentsPerMillion  int64  `json:"input_cents_per_million"`
	OutputCentsPerMillion int64  `json:"output_cents_per_million"`
	PriceUpdatedAt        string `json:"price_updated_at,omitempty"`
	CostCents             *int64 `json:"cost_cents,omitempty"`
}

// FreezeKnowledgeStageConfigs copies resolved model choices into the new contract.
func FreezeKnowledgeStageConfigs(models map[string]string) map[string]KnowledgeStageConfig {
	caps := map[string]int{"discover": 2048, "select": 4096, "write": 8192, "revise": 8192, "review": 4096, "review_final": 4096, "weekly_review": 2048}
	out := make(map[string]KnowledgeStageConfig, len(caps))
	for stage, cap := range caps {
		// The measured high-reasoning route reports discovery usage including
		// reasoning above 2048; retain headroom instead of silently clipping topics.
		if stage == "discover" && models[stage] == "google/gemini-3.8-flash-high" {
			cap = 8192
		}
		out[stage] = KnowledgeStageConfig{Model: models[stage], MaxOutputTokens: cap}
	}
	return out
}

// KnowledgeConfigForStage preserves old routing and rejects invalid new overrides.
func KnowledgeConfigForStage(req KnowledgeArticleRequest, fallbackModel string) (KnowledgeStageConfig, error) {
	if (req.Stage == "review" || req.Stage == "review_final") && req.ReviewModel != "" {
		fallbackModel = req.ReviewModel
	}
	if req.PromptVersion != KnowledgeArticlePromptVersion {
		return KnowledgeStageConfig{Model: fallbackModel, MaxOutputTokens: 8192}, nil
	}
	cfg, ok := req.StageConfigs[req.Stage]
	if !ok {
		models := map[string]string{req.Stage: fallbackModel}
		cfg, ok = FreezeKnowledgeStageConfigs(models)[req.Stage]
	}
	if !ok || cfg.Model == "" || cfg.MaxOutputTokens < 256 || cfg.MaxOutputTokens > 16384 {
		return cfg, fmt.Errorf("阶段模型或输出上限无效: %s", req.Stage)
	}
	return cfg, nil
}

// KnowledgeArticleMessages is the exact system/user serialization sent by the
// transport. Local audit/config metadata is omitted only for the new contract.
func KnowledgeArticleMessages(req KnowledgeArticleRequest) (string, string, error) {
	if req.PromptVersion != "" && !KnowledgeArticlePromptSupported(req.PromptVersion) {
		return "", "", fmt.Errorf("未知文章提示版本")
	}
	if req.PromptVersion == KnowledgeArticlePromptVersion {
		req.Estimate = nil
		req.StageConfigs = nil
		if req.Question != nil {
			q := *req.Question
			q.Links = nil
			req.Question = &q
		}
		req.Candidates = nil // local-only metadata includes candidates denied send permission
	}
	input, err := json.Marshal(req)
	return knowledgeArticleInstructions(req), string(input) + jsonObjectOutputInstruction, err
}

// EstimateKnowledgeRequest conservatively counts the actual serialized messages.
// This is a versioned heuristic, not a tokenizer or a claim of measured accuracy.
func EstimateKnowledgeRequest(req KnowledgeArticleRequest, model string) (*KnowledgeEstimate, error) {
	cfg, err := KnowledgeConfigForStage(req, model)
	if err != nil {
		return nil, err
	}
	system, input, err := KnowledgeArticleMessages(req)
	if err != nil {
		return nil, err
	}
	method := "messages-heuristic-v1"
	// This exact route was measured on the frozen v3 self-authored evaluation.
	// Other routes retain the more conservative uncalibrated estimate. An old
	// admitted request always keeps its method even after new calibration exists.
	if cfg.Model == "google/gemini-3.8-flash-high" {
		method = "messages-heuristic-gemini-flash-v1"
	}
	if req.Estimate != nil {
		method = req.Estimate.Method
	}
	if method != "messages-heuristic-v1" && method != "messages-heuristic-gemini-flash-v1" {
		return nil, fmt.Errorf("未知输入估算办法")
	}
	var ascii, han, other int
	for _, r := range system + input {
		if r < 128 {
			ascii++
		} else if unicode.Is(unicode.Han, r) {
			han++
		} else {
			other++
		}
	}
	// Approx. ASCII/3 and Han*2, plus framing and 25% safety margin.
	tokens := ((ascii+2)/3 + 2*han + 3*other + 32)
	tokens = (tokens*5 + 3) / 4
	if method == "messages-heuristic-gemini-flash-v1" {
		tokens = ((ascii+3)/4 + han + 2*other + 32)
		tokens = (tokens*6 + 4) / 5
	}
	sum := sha256.Sum256([]byte(system + "\x00" + input))
	return &KnowledgeEstimate{Method: method, InputFingerprint: fmt.Sprintf("%x", sum), InputTokens: tokens, OutputTokens: cfg.MaxOutputTokens, Approximate: true}, nil
}

// CostForUnits uses the frozen price; zero actual usage is known zero.
func (e *KnowledgeEstimate) CostForUnits(input, output int) (int64, bool) {
	if input == 0 && output == 0 {
		return 0, true
	}
	if e == nil || !e.PriceKnown {
		return 0, false
	}
	n := int64(input)*e.InputCentsPerMillion + int64(output)*e.OutputCentsPerMillion
	return (n + 999999) / 1000000, true
}
