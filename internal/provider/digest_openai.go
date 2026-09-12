package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// EpisodeDigest 三角色 OpenAI 兜底实现（ADR-0023）。
// 走 chatCompleteWithMeta 的 /responses 风格载荷；结构约束以提示词下发（兼容端点无 response_format）。
// 与 Groq 实现对偶：同一套 prompt 契约，输出仍须过 G2 门禁。

// digestCallLabel 统一错误前缀。
const digestCallLabel = "digest"

// openaiDigestCall 发送一次 digest 角色 chat 调用并解析 output_text。
func (o *OpenAIProvider) openaiDigestCall(ctx context.Context, system, user string) (string, TaskUsage, error) {
	model := o.analysisModel
	if model == "" {
		model = openaiAnalysisModel
	}
	payload := map[string]any{
		"model":        model,
		"instructions": system,
		"input":        user,
		"text":         map[string]any{"format": map[string]any{"type": "json_object"}},
	}
	data, _, err := o.chatCompleteWithMeta(ctx, payload, digestCallLabel)
	if err != nil {
		return "", TaskUsage{}, err
	}
	var out struct {
		OutputText string `json:"output_text"`
	}
	if err := parseJSONLoose(string(data), &out); err != nil {
		// 兼容端点可能返回原始 content；尝试再包一层解析
		var raw struct {
			OutputText string `json:"output_text"`
		}
		if err2 := json.Unmarshal(data, &raw); err2 == nil && raw.OutputText != "" {
			out.OutputText = raw.OutputText
		} else {
			return "", TaskUsage{}, fmt.Errorf("openai digest 响应解析失败: %w", err)
		}
	}
	return out.OutputText, TaskUsage{}, nil
}

// ComposeDigest 生成精读文长文版初稿（OpenAI 兜底路径）。
func (o *OpenAIProvider) ComposeDigest(ctx context.Context, req DigestWritingRequest) (*DigestWritingResult, error) {
	if len(req.Segments) == 0 {
		return nil, fmt.Errorf("无本集 Segment，无法生成精读文")
	}
	var sb strings.Builder
	sb.WriteString("本集标题：" + req.SourceTitle + "\n")
	if req.SourceSummary != "" {
		sb.WriteString("本集摘要：" + req.SourceSummary + "\n")
	}
	sb.WriteString("\n片段（[ID] 文本）：\n")
	for _, seg := range req.Segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	if len(req.KeyPoints) > 0 {
		sb.WriteString("\n已验证要点：\n")
		for _, kp := range req.KeyPoints {
			sb.WriteString(fmt.Sprintf("- %s（citations: %s）\n", kp.Content, strings.Join(kp.Citations, ",")))
		}
	}
	if len(req.Notes) > 0 {
		sb.WriteString("\nOwner 笔记（note 块只能逐字选用）：\n")
		for _, n := range req.Notes {
			sb.WriteString(fmt.Sprintf("- note_id=%s [%s]: %s\n", n.NoteID, n.Kind, n.Text))
		}
	}
	factBudget := req.FactGapBudget
	if factBudget <= 0 {
		factBudget = 5
	}
	sys := fmt.Sprintf(digestComposeSystemPrompt, factBudget)
	content, _, err := o.openaiDigestCall(ctx, sys, "生成精读文：\n\n"+sb.String())
	if err != nil {
		return nil, fmt.Errorf("openai 精读文初稿: %w", err)
	}
	var resp struct {
		Title    string             `json:"title"`
		Blocks   []DigestBlockDraft `json:"blocks"`
		FactGaps []string           `json:"fact_gaps"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		return nil, fmt.Errorf("解析精读文初稿失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	if resp.Title == "" || len(resp.Blocks) == 0 {
		return nil, fmt.Errorf("精读文初稿缺少标题或内容块")
	}
	return &DigestWritingResult{Title: resp.Title, Blocks: resp.Blocks, FactGaps: resp.FactGaps}, nil
}

// WeaveDigestFacts 第二阶段：把落源文档补织为引用事实块（OpenAI 兜底路径，只追加 cited_fact）。
func (o *OpenAIProvider) WeaveDigestFacts(ctx context.Context, req DigestWeaveRequest) (*DigestWeaveResult, error) {
	if len(req.Documents) == 0 || len(req.FactGaps) == 0 {
		return &DigestWeaveResult{}, nil
	}
	var sb strings.Builder
	sb.WriteString("待补事实缺口：\n")
	for i, gap := range req.FactGaps {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, gap))
	}
	sb.WriteString("\n检索落源文档：\n")
	for _, doc := range req.Documents {
		sb.WriteString(fmt.Sprintf("document_id=%s 《%s》\n", doc.DocumentID, doc.Title))
		for _, seg := range doc.Segments {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
		}
	}
	var existing strings.Builder
	for _, b := range req.Blocks {
		existing.WriteString(fmt.Sprintf("- (%s) %s\n", b.Type, b.Text))
	}
	content, _, err := o.openaiDigestCall(ctx, digestWeaveSystemPrompt, "已有内容块（不可改动）：\n"+existing.String()+"\n"+sb.String())
	if err != nil {
		return nil, fmt.Errorf("openai 补织事实块: %w", err)
	}
	var resp struct {
		Blocks []DigestBlockDraft `json:"blocks"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		return nil, fmt.Errorf("解析补织事实块失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	return &DigestWeaveResult{Blocks: resp.Blocks}, nil
}

// RewriteDigest 渠道语气改写（OpenAI 兜底路径）。
func (o *OpenAIProvider) RewriteDigest(ctx context.Context, req DigestRewriteRequest) (*DigestRewriteResult, error) {
	if len(req.Blocks) == 0 {
		return nil, fmt.Errorf("无内容块可改写")
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 800
	}
	maxTags := req.MaxHashtags
	if maxTags <= 0 {
		maxTags = 5
	}
	var sb strings.Builder
	for _, b := range req.Blocks {
		sb.WriteString(fmt.Sprintf("(%s) %s\n", b.Type, b.Text))
	}
	sys := fmt.Sprintf(digestRewriteSystemPrompt, maxTags, maxChars)
	content, _, err := o.openaiDigestCall(ctx, sys, "改写为小红书笔记版：\n\n"+sb.String())
	if err != nil {
		return nil, fmt.Errorf("openai 渠道改写: %w", err)
	}
	var resp struct {
		Text string `json:"text"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		return nil, fmt.Errorf("解析渠道改写失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	if strings.TrimSpace(resp.Text) == "" {
		return nil, fmt.Errorf("渠道改写为空")
	}
	return &DigestRewriteResult{Text: resp.Text}, nil
}

// Search 实现 SourceSearchProvider（OpenAI 兜底路径）。
// OpenAI chat 无内置联网搜索；显式报错让调用方走 C1 降级，而不是返回空结果伪装成功。
func (o *OpenAIProvider) Search(ctx context.Context, query string) ([]DigestSearchResult, error) {
	return nil, fmt.Errorf("openai provider 不支持 SourceSearch，请使用 groq（compound）")
}
