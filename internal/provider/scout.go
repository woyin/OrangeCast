package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

const scoutSystemPrompt = `你是内容 Scout。只根据已确认 Theme 中提供的 KeyPoint 材料发现可写的跨 Episode 公众号选题。不要添加任何外部事实。输出 JSON 对象，字段 proposals；每个 proposal 有 kind（fresh/evergreen/follow_up）、title、thesis、audience、rationale、candidateKeyPointIds。每个候选必须引用输入中的 KeyPoint ID，且至少覆盖两个不同 Source。`

// Scout produces proposal candidates through Groq chat completion.
func (g *GroqProvider) Scout(request ScoutRequest) (*ScoutResult, error) {
	input, err := scoutInput(request)
	if err != nil {
		return nil, err
	}
	content, _, err := g.complete([]map[string]string{{"role": "system", "content": scoutSystemPrompt + "\n必须只输出一个 JSON 对象。"}, {"role": "user", "content": input}}, "object")
	if err != nil {
		return nil, err
	}
	result := &ScoutResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析 Scout 输出: %w", err)
	}
	return validateScoutResult(result, request)
}

// Scout produces proposal candidates through OpenAI Responses.
func (o *OpenAIProvider) Scout(request ScoutRequest) (*ScoutResult, error) {
	input, err := scoutInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	data, err := o.doResponses(map[string]any{"model": model, "instructions": scoutSystemPrompt, "input": input, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}, "Scout")
	if err != nil {
		return nil, err
	}
	var response struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析 openai Scout 响应: %w", err)
	}
	result := &ScoutResult{}
	if err := parseJSONLoose(response.OutputText, result); err != nil {
		return nil, fmt.Errorf("解析 Scout 输出: %w", err)
	}
	return validateScoutResult(result, request)
}

func scoutInput(request ScoutRequest) (string, error) {
	if len(request.Themes) == 0 {
		return "", fmt.Errorf("至少需要一个包含跨 Episode 材料的确认 Theme")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return "基于下列 Theme 与 KeyPoint 材料提出不重复的写作选题：\n" + string(encoded), nil
}

func validateScoutResult(result *ScoutResult, request ScoutRequest) (*ScoutResult, error) {
	if result == nil || len(result.Proposals) == 0 {
		return nil, fmt.Errorf("Scout 必须返回至少一个提案")
	}
	allowed := map[string]bool{}
	for _, theme := range request.Themes {
		for _, material := range theme.Materials {
			allowed[material.KeyPointID] = true
		}
	}
	titles := map[string]bool{}
	sourceByKeyPoint := map[string]string{}
	for _, theme := range request.Themes {
		for _, material := range theme.Materials {
			sourceByKeyPoint[material.KeyPointID] = material.SourceID
		}
	}
	for _, proposal := range result.Proposals {
		key := strings.ToLower(strings.TrimSpace(proposal.Title))
		if key == "" || titles[key] || !validScoutKind(proposal.Kind) || len(proposal.CandidateKeyPointIDs) == 0 {
			return nil, fmt.Errorf("Scout 返回无效或重复提案")
		}
		titles[key] = true
		sources := map[string]bool{}
		for _, id := range proposal.CandidateKeyPointIDs {
			if !allowed[id] {
				return nil, fmt.Errorf("Scout 引用了未授权 KeyPoint %q", id)
			}
			if sourceByKeyPoint[id] != "" {
				sources[sourceByKeyPoint[id]] = true
			}
		}
		if len(sources) < 2 {
			return nil, fmt.Errorf("Scout 提案必须覆盖至少两个 Source")
		}
	}
	return result, nil
}

func validScoutKind(kind string) bool {
	return kind == "fresh" || kind == "evergreen" || kind == "follow_up"
}
