package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// CuratorPromptVersion identifies the prompt contract used by brief curation requests.
	CuratorPromptVersion = "curator-v1"
	curatorSystemPrompt  = `你是内容 Curator。根据 Owner 接受的提案和候选 KeyPoint，生成可供 Owner 审核的 ArticleBrief。只能选择输入材料；必须明确入选、淘汰和冲突处理，输出 thesis、audience、outline、selectedKeyPointIds、rejectedKeyPointIds、conflictPlan、claimType、unresolvedQuestions、notes、style、targetLength。不得写正文。`
)

func curatorInput(request CuratorRequest) (string, error) {
	if strings.TrimSpace(request.Title) == "" || len(request.Materials) == 0 {
		return "", fmt.Errorf("Curator 需要提案和候选材料")
	}
	b, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return "为以下已接受提案生成待审核 Brief：\n" + string(b), nil
}

// ValidateCuratorResult is the public contract gate used by all Curator callers,
// including fakes and durable-worker recovery paths.
func ValidateCuratorResult(result *CuratorResult, request CuratorRequest) (*CuratorResult, error) {
	if result == nil || strings.TrimSpace(result.Thesis) == "" || strings.TrimSpace(result.Outline) == "" || len(result.SelectedKeyPointIDs) == 0 {
		return nil, fmt.Errorf("Curator 必须返回论点、结构和入选材料")
	}
	allowed := map[string]bool{}
	for _, m := range request.Materials {
		allowed[m.KeyPointID] = true
	}
	selected := map[string]bool{}
	for _, id := range result.SelectedKeyPointIDs {
		if !allowed[id] {
			return nil, fmt.Errorf("Curator 引用了未授权 KeyPoint %q", id)
		}
		if selected[id] {
			return nil, fmt.Errorf("Curator 重复选用 KeyPoint %q", id)
		}
		selected[id] = true
	}
	rejected := map[string]bool{}
	for _, id := range result.RejectedKeyPointIDs {
		if !allowed[id] {
			return nil, fmt.Errorf("Curator 引用了未授权 KeyPoint %q", id)
		}
		if rejected[id] {
			return nil, fmt.Errorf("Curator 重复放弃 KeyPoint %q", id)
		}
		if selected[id] {
			return nil, fmt.Errorf("Curator 同时选用和放弃 KeyPoint %q", id)
		}
		rejected[id] = true
	}
	return result, nil
}

// Curate generates an ArticleBrief draft from an accepted proposal via the Groq chat endpoint.
func (g *GroqProvider) Curate(ctx context.Context, request CuratorRequest) (*CuratorResult, error) {
	input, err := curatorInput(request)
	if err != nil {
		return nil, err
	}
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{{"role": "system", "content": curatorSystemPrompt + "\n必须只输出 JSON。"}, {"role": "user", "content": input}}, "object")
	if err != nil {
		return nil, err
	}
	result := &CuratorResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析 Curator 输出: %w", err)
	}
	result.Usage = usage
	return ValidateCuratorResult(result, request)
}

// Curate generates an ArticleBrief draft from an accepted proposal via the OpenAI chat endpoint.
func (o *OpenAIProvider) Curate(ctx context.Context, request CuratorRequest) (*CuratorResult, error) {
	input, err := curatorInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	data, retries, err := o.chatCompleteWithMeta(ctx, map[string]any{"model": model, "instructions": curatorSystemPrompt, "input": input, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}, "Curator")
	if err != nil {
		return nil, err
	}
	var response struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	result := &CuratorResult{}
	if err := parseJSONLoose(response.OutputText, result); err != nil {
		return nil, fmt.Errorf("解析 Curator 输出: %w", err)
	}
	result.Usage = chatUsage(data, retries)
	return ValidateCuratorResult(result, request)
}

// validateCuratorResult keeps the historical package-local test/helper name while
// all production callers use the exported contract gate.
func validateCuratorResult(result *CuratorResult, request CuratorRequest) (*CuratorResult, error) {
	return ValidateCuratorResult(result, request)
}
