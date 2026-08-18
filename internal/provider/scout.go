package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// ScoutPromptVersion identifies the prompt contract used by topic scouting requests.
	ScoutPromptVersion = "scout-v2"
	scoutSystemPrompt  = `你是内容 Scout。只根据已确认 Theme 中提供的 KeyPoint 材料发现可写的公众号选题。不要添加任何外部事实。输出 JSON 对象，字段 proposals；每个 proposal 有 kind（fresh/evergreen/follow_up/deep_read）、title、thesis、audience、rationale、candidateKeyPointIds。`
)

func scoutPrompt(request ScoutRequest) string {
	mode := request.Mode
	if mode == "" {
		mode = ScoutModeCrossEpisode
	}
	count := request.ProposalCount
	if count <= 0 {
		count = 1
	}
	if mode == ScoutModeDeepRead {
		return scoutSystemPrompt + fmt.Sprintf("当前模式是单集深读：所有候选必须只使用当前选定 Episode 的材料，kind 必须为 deep_read；每个候选至少引用一个输入 KeyPoint。必须返回恰好 %d 条互不重复的候选。", count)
	}
	return scoutSystemPrompt + fmt.Sprintf("当前模式是跨 Episode：每个候选必须引用至少两个不同 Episode 的输入 KeyPoint，kind 使用 fresh、evergreen 或 follow_up。必须返回恰好 %d 条互不重复的候选。", count)
}

// Scout produces proposal candidates through Groq chat completion.
func (g *GroqProvider) Scout(ctx context.Context, request ScoutRequest) (*ScoutResult, error) {
	input, err := scoutInput(request)
	if err != nil {
		return nil, err
	}
	content, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{{"role": "system", "content": scoutPrompt(request) + "\n必须只输出一个 JSON 对象。"}, {"role": "user", "content": input}}, "object")
	if err != nil {
		return nil, err
	}
	result := &ScoutResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析 Scout 输出: %w", err)
	}
	result.Usage = usage
	return validateScoutResult(result, request)
}

// Scout produces proposal candidates through OpenAI Responses.
func (o *OpenAIProvider) Scout(ctx context.Context, request ScoutRequest) (*ScoutResult, error) {
	input, err := scoutInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	data, retries, err := o.chatCompleteWithMeta(ctx, map[string]any{"model": model, "instructions": scoutPrompt(request), "input": input, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}, "Scout")
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
	result.Usage = chatUsage(data, retries)
	return validateScoutResult(result, request)
}

func scoutInput(request ScoutRequest) (string, error) {
	if len(request.Themes) == 0 {
		return "", fmt.Errorf("至少需要一个包含素材的确认 Theme")
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
	mode := request.Mode
	if mode == "" {
		mode = ScoutModeCrossEpisode
	}
	if mode != ScoutModeCrossEpisode && mode != ScoutModeDeepRead {
		return nil, fmt.Errorf("Scout 模式无效")
	}
	if request.ProposalCount > 0 && len(result.Proposals) != request.ProposalCount {
		return nil, fmt.Errorf("Scout 必须返回恰好 %d 条提案，实际 %d 条", request.ProposalCount, len(result.Proposals))
	}
	allowed, sourceByKeyPoint := scoutMaterialIndexes(request)
	titles := map[string]bool{}
	for _, proposal := range result.Proposals {
		key := strings.ToLower(strings.TrimSpace(proposal.Title))
		if key == "" || titles[key] || !validScoutKindForMode(proposal.Kind, mode) || len(proposal.CandidateKeyPointIDs) == 0 {
			return nil, fmt.Errorf("Scout 返回无效或重复提案")
		}
		if mode == ScoutModeDeepRead && request.SourceID != "" {
			for _, id := range proposal.CandidateKeyPointIDs {
				if sourceByKeyPoint[id] != request.SourceID {
					return nil, fmt.Errorf("单集深读提案引用了所选 Episode 之外的素材")
				}
			}
		}
		if err := validateScoutProposalMaterials(proposal, allowed, sourceByKeyPoint, mode); err != nil {
			return nil, err
		}
		titles[key] = true
	}
	return result, nil
}

func scoutMaterialIndexes(request ScoutRequest) (map[string]bool, map[string]string) {
	allowed := map[string]bool{}
	sourceByKeyPoint := map[string]string{}
	for _, theme := range request.Themes {
		for _, material := range theme.Materials {
			allowed[material.KeyPointID] = true
			sourceByKeyPoint[material.KeyPointID] = material.SourceID
		}
	}
	return allowed, sourceByKeyPoint
}

func validateScoutProposalMaterials(proposal ScoutProposal, allowed map[string]bool, sourceByKeyPoint map[string]string, mode string) error {
	sources := map[string]bool{}
	for _, id := range proposal.CandidateKeyPointIDs {
		if !allowed[id] {
			return fmt.Errorf("Scout 引用了未授权 KeyPoint %q", id)
		}
		if sourceByKeyPoint[id] != "" {
			sources[sourceByKeyPoint[id]] = true
		}
	}
	minSources := 2
	if mode == ScoutModeDeepRead {
		minSources = 1
	}
	if len(sources) < minSources {
		return fmt.Errorf("Scout 提案必须覆盖至少 %d 个 Source", minSources)
	}
	return nil
}

func validScoutKindForMode(kind, mode string) bool {
	if mode == ScoutModeDeepRead {
		return kind == "deep_read"
	}
	return kind == "fresh" || kind == "evergreen" || kind == "follow_up"
}
