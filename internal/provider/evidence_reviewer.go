package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// EvidenceReviewerPromptVersion identifies the prompt contract used by evidence review requests.
	EvidenceReviewerPromptVersion = "evidence-reviewer-v1"
	evidenceReviewerSystemPrompt  = `你是独立 EvidenceReviewer。只根据请求中给出的 Revision、EvidenceMap 与 KeyPoint 原始材料检查：每个转述/综合是否被材料支持、直接引语是否可追溯、是否有错误归因。不能使用外部知识。输出 JSON {"status":"passed"或"failed","issues":["..."]}。存在任何硬证据问题必须 failed。`
)

// ReviewEvidence asks Groq for an independent evidence decision.
func (g *GroqProvider) ReviewEvidence(ctx context.Context, request EvidenceReviewRequest) (*EvidenceReviewResult, error) {
	input, err := evidenceReviewInput(request)
	if err != nil {
		return nil, err
	}
	content, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{{"role": "system", "content": evidenceReviewerSystemPrompt + "\n必须只输出一个 JSON 对象。"}, {"role": "user", "content": input}}, "object")
	if err != nil {
		return nil, err
	}
	result := &EvidenceReviewResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析 EvidenceReviewer 输出: %w", err)
	}
	result.Usage = usage
	return validateEvidenceReviewResult(result)
}

// ReviewEvidence asks OpenAI for an independent evidence decision.
func (o *OpenAIProvider) ReviewEvidence(ctx context.Context, request EvidenceReviewRequest) (*EvidenceReviewResult, error) {
	input, err := evidenceReviewInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	data, retries, err := o.chatCompleteWithMeta(ctx, map[string]any{"model": model, "instructions": evidenceReviewerSystemPrompt, "input": input, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}, "EvidenceReviewer")
	if err != nil {
		return nil, err
	}
	var response struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析 openai EvidenceReviewer 响应: %w", err)
	}
	result := &EvidenceReviewResult{}
	if err := parseJSONLoose(response.OutputText, result); err != nil {
		return nil, fmt.Errorf("解析 EvidenceReviewer 输出: %w", err)
	}
	result.Usage = chatUsage(data, retries)
	return validateEvidenceReviewResult(result)
}

func evidenceReviewInput(request EvidenceReviewRequest) (string, error) {
	if strings.TrimSpace(request.Markdown) == "" || len(request.Items) == 0 {
		return "", fmt.Errorf("审校需要正文和 EvidenceMap")
	}
	b, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return "独立审校以下证据关系：\n" + string(b), nil
}

func validateEvidenceReviewResult(result *EvidenceReviewResult) (*EvidenceReviewResult, error) {
	if result == nil || (result.Status != "passed" && result.Status != "failed") {
		return nil, fmt.Errorf("EvidenceReviewer 必须返回 passed 或 failed")
	}
	if result.Status == "passed" && len(result.Issues) > 0 {
		return nil, fmt.Errorf("通过的证据审校不能包含硬问题")
	}
	return result, nil
}
