package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

const styleEditorSystemPrompt = `你是独立 StyleEditor。只依据 EditorialProfile 的目标读者、语气、风格说明和目标篇幅检查文章的标题、结构、节奏、重复、篇幅与禁用表达。不要判断事实或证据。输出 JSON {"status":"passed"或"advisory","issues":["..."]}。有建议时必须 advisory；风格审校永远不能解除或替代证据门禁。`

// ReviewStyle asks Groq for non-blocking style findings.
func (g *GroqProvider) ReviewStyle(request StyleReviewRequest) (*StyleReviewResult, error) {
	input, err := styleReviewInput(request)
	if err != nil {
		return nil, err
	}
	content, _, err := g.complete([]map[string]string{{"role": "system", "content": styleEditorSystemPrompt + "\n必须只输出一个 JSON 对象。"}, {"role": "user", "content": input}}, "object")
	if err != nil {
		return nil, err
	}
	result := &StyleReviewResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析 StyleEditor 输出: %w", err)
	}
	return validateStyleReviewResult(result)
}

// ReviewStyle asks OpenAI for non-blocking style findings.
func (o *OpenAIProvider) ReviewStyle(request StyleReviewRequest) (*StyleReviewResult, error) {
	input, err := styleReviewInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	data, err := o.doResponses(map[string]any{"model": model, "instructions": styleEditorSystemPrompt, "input": input, "text": map[string]any{"format": map[string]any{"type": "json_object"}}}, "StyleEditor")
	if err != nil {
		return nil, err
	}
	var response struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析 openai StyleEditor 响应: %w", err)
	}
	result := &StyleReviewResult{}
	if err := parseJSONLoose(response.OutputText, result); err != nil {
		return nil, fmt.Errorf("解析 StyleEditor 输出: %w", err)
	}
	return validateStyleReviewResult(result)
}

func styleReviewInput(request StyleReviewRequest) (string, error) {
	if strings.TrimSpace(request.Title) == "" || strings.TrimSpace(request.Markdown) == "" {
		return "", fmt.Errorf("风格审校需要标题和正文")
	}
	b, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return "检查以下文章是否符合编辑画像：\n" + string(b), nil
}

func validateStyleReviewResult(result *StyleReviewResult) (*StyleReviewResult, error) {
	if result == nil || (result.Status != "passed" && result.Status != "advisory") {
		return nil, fmt.Errorf("StyleEditor 必须返回 passed 或 advisory")
	}
	if result.Status == "passed" && len(result.Issues) > 0 {
		return nil, fmt.Errorf("通过的风格审校不能包含建议")
	}
	if result.Status == "advisory" && len(result.Issues) == 0 {
		return nil, fmt.Errorf("advisory 风格审校必须说明建议")
	}
	return result, nil
}
