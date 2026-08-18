package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// ArticleWriterPromptVersion identifies the prompt contract used by WriteArticle requests.
	ArticleWriterPromptVersion = "writer-v1"
	articleWriterSystemPrompt  = `你是严格受证据约束的微信公众号文章 Writer。只可使用请求中给出的 KeyPoint 材料，绝不可补充外部事实、常识、数字、人物背景或未给出的出处。输出 JSON：title、markdown、evidenceMaps。evidenceMaps 的每项包含 kind（quoted/paraphrased/synthesized/rhetorical）、excerpt 与 keyPointIds。所有含事实、转述或综合的表达必须映射到一个或多个 KeyPoint；rhetorical 可以为空数组。直接引语必须来自材料内容并在 markdown 中归因。`
)

// WriteArticle generates an evidence-mapped Markdown article through Groq chat completion.
func (g *GroqProvider) WriteArticle(ctx context.Context, request ArticleWritingRequest) (*ArticleWritingResult, error) {
	input, err := articleWriterInput(request)
	if err != nil {
		return nil, err
	}
	content, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": articleWriterSystemPrompt + "\n必须只输出一个 JSON 对象。"},
		{"role": "user", "content": input},
	}, "object")
	if err != nil {
		return nil, err
	}
	result := &ArticleWritingResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析文章 Writer 输出: %w", err)
	}
	result.Usage = usage
	return validateArticleWritingResult(result, request.Materials)
}

// WriteArticle generates an evidence-mapped Markdown article through OpenAI Responses.
func (o *OpenAIProvider) WriteArticle(ctx context.Context, request ArticleWritingRequest) (*ArticleWritingResult, error) {
	input, err := articleWriterInput(request)
	if err != nil {
		return nil, err
	}
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	payload := map[string]any{
		"model": model, "instructions": articleWriterSystemPrompt, "input": input,
		"text": map[string]any{"format": map[string]any{"type": "json_object"}},
	}
	data, retries, err := o.chatCompleteWithMeta(ctx, payload, "文章写作")
	if err != nil {
		return nil, err
	}
	var response struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("解析 openai Writer 响应: %w", err)
	}
	result := &ArticleWritingResult{}
	if err := parseJSONLoose(response.OutputText, result); err != nil {
		return nil, fmt.Errorf("解析文章 Writer 输出: %w", err)
	}
	result.Usage = chatUsage(data, retries)
	return validateArticleWritingResult(result, request.Materials)
}

func articleWriterInput(request ArticleWritingRequest) (string, error) {
	if len(request.Materials) == 0 || strings.TrimSpace(request.Thesis) == "" || strings.TrimSpace(request.Outline) == "" {
		return "", fmt.Errorf("confirmed brief requires thesis, outline, and selected KeyPoints")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	prefix := "基于以下已确认 Brief 和仅允许的 KeyPoint 材料撰写文章：\n"
	if strings.TrimSpace(request.ExistingMarkdown) != "" {
		if len(request.RevisionFeedback) == 0 {
			return "", fmt.Errorf("revision requires at least one review finding")
		}
		prefix = "基于以下原修订、审校反馈、已确认 Brief 和仅允许的 KeyPoint 材料生成新的完整修订。必须解决反馈，但不得使用未提供材料：\n"
	}
	return prefix + string(encoded), nil
}

func validateArticleWritingResult(result *ArticleWritingResult, materials []ArticleMaterial) (*ArticleWritingResult, error) {
	if result == nil || strings.TrimSpace(result.Markdown) == "" || len(result.EvidenceMaps) == 0 {
		return nil, fmt.Errorf("Writer 必须返回正文和 EvidenceMap")
	}
	allowed := make(map[string]bool, len(materials))
	for _, material := range materials {
		allowed[material.KeyPointID] = true
	}
	for _, mapping := range result.EvidenceMaps {
		if strings.TrimSpace(mapping.Excerpt) == "" {
			return nil, fmt.Errorf("EvidenceMap excerpt 不能为空")
		}
		switch mapping.Kind {
		case "quoted", "paraphrased", "synthesized":
			if len(mapping.KeyPointIDs) == 0 {
				return nil, fmt.Errorf("%s EvidenceMap 必须关联 KeyPoint", mapping.Kind)
			}
		case "rhetorical":
		default:
			return nil, fmt.Errorf("未知 EvidenceMap kind %q", mapping.Kind)
		}
		for _, keyPointID := range mapping.KeyPointIDs {
			if !allowed[keyPointID] {
				return nil, fmt.Errorf("Writer 引用了未授权 KeyPoint %q", keyPointID)
			}
		}
	}
	if strings.TrimSpace(result.Title) == "" {
		result.Title = requestTitle(materials)
	}
	return result, nil
}

func requestTitle(materials []ArticleMaterial) string {
	if len(materials) > 0 {
		return materials[0].Content
	}
	return "未命名文章"
}
