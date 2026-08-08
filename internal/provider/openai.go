package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpenAIProvider 兜底实现（第 9 题中间层：groq 主力、openai 可切换）。
// 转录走 /audio/transcriptions（gpt-4o-mini-transcribe），分析/QA 走 /responses（json_schema strict）。
// 注意：OpenAI 的 /responses 与 Groq 的 /chat/completions 契约不对称（第 10 题），此处独立实现。
type OpenAIProvider struct {
	apiKey        string
	baseURL       string // 空则用默认
	analysisModel string // 空则用默认
}

const (
	openaiBaseURL         = "https://api.openai.com/v1"
	openaiTranscribeModel = "gpt-4o-mini-transcribe"
	openaiAnalysisModel   = "gpt-4.1-mini"
)

func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{apiKey: apiKey}
}

func (o *OpenAIProvider) Name() string { return "openai" }

// WithBaseURL 返回指向自定义 base URL 的新实例（测试/兼容 API 用）。
func (o *OpenAIProvider) WithBaseURL(url string) *OpenAIProvider {
	return &OpenAIProvider{apiKey: o.apiKey, baseURL: url, analysisModel: o.analysisModel}
}

// WithModel 返回使用指定分析模型的新实例。
func (o *OpenAIProvider) WithModel(model string) *OpenAIProvider {
	return &OpenAIProvider{apiKey: o.apiKey, baseURL: o.baseURL, analysisModel: model}
}

// doResponses 发送一次 POST /responses，返回原始响应体。
// 集中 6 处重复的 baseURL 解析、鉴权、超时与 HTTP 错误处理；调用方各自 Unmarshal/解析。
// label 用于错误信息标注（哪个任务失败）。
func (o *OpenAIProvider) doResponses(payload map[string]any, label string) ([]byte, error) {
	bURL := o.baseURL
	if bURL == "" {
		bURL = openaiBaseURL
	}
	buf, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, bURL+"/responses", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai %s 请求: %w", label, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai %s 失败 HTTP %d: %s", label, resp.StatusCode, string(data))
	}
	return data, nil
}

func (o *OpenAIProvider) Transcribe(filePath string) (*TranscriptResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("model", openaiTranscribeModel)
	_ = w.WriteField("response_format", "verbose_json")
	part, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	w.Close()

	bURL := o.baseURL
	if bURL == "" {
		bURL = openaiBaseURL
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, bURL+"/audio/transcriptions", body)
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai 转录请求: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai 转录失败 HTTP %d: %s", resp.StatusCode, string(data))
	}
	var raw struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析 openai 转录响应: %w", err)
	}
	res := &TranscriptResult{Language: raw.Language, Text: raw.Text}
	for i, s := range raw.Segments {
		res.Segments = append(res.Segments, Segment{
			ID:    fmt.Sprintf("seg-%04d", i+1),
			Start: s.Start, End: s.End, Text: s.Text,
		})
	}
	return res, nil
}

// Analyze 走 /responses + json_schema strict（OpenAI 保证结构化输出）。
func (o *OpenAIProvider) Analyze(transcript string, segments []Segment) (*KnowledgeCard, error) {
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	aModel := o.analysisModel
	if aModel == "" {
		aModel = openaiAnalysisModel
	}
	payload := map[string]any{
		"model":        aModel,
		"instructions": analysisSystemPrompt,
		"input":        "请基于以下带编号片段的播客转录稿生成结构化知识卡片（citations 引用片段ID）：\n\n" + sb.String(),
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "knowledge_card",
				"strict": true,
				"schema": knowledgeCardSchema,
			},
		},
	}
	data, err := o.doResponses(payload, "分析")
	if err != nil {
		return nil, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	card := &KnowledgeCard{}
	if err := parseJSONLoose(r.OutputText, card); err != nil {
		return nil, fmt.Errorf("解析 openai KnowledgeCard: %w", err)
	}
	return card, nil
}

func (o *OpenAIProvider) Answer(question string, segments []Segment) (*QAResult, error) {
	chunks := Retrieve(BuildChunks(segments, 8), question, 5)
	if len(chunks) == 0 {
		return &QAResult{Answer: "暂无转录稿可用于回答。"}, nil
	}
	var ctxSB strings.Builder
	for i, c := range chunks {
		ctxSB.WriteString(fmt.Sprintf("[片段%d | %.0f-%.0fs] %s\n", i, c.Start, c.End, c.Text))
	}
	input := fmt.Sprintf("基于以下播客片段回答，输出 JSON {\"answer\":\"\",\"cited\":[编号]}。\n\n%s\n问题：%s", ctxSB.String(), question)
	payload := map[string]any{
		"model":        openaiAnalysisModel,
		"instructions": "你是播客内容助手。通过 cited 标注引用的片段编号。",
		"input":        input,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "qa_result",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
						"cited":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
					},
					"required": []string{"answer", "cited"},
				},
			},
		},
	}
	data, err := o.doResponses(payload, "QA")
	if err != nil {
		return nil, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	var parsed struct {
		Answer string `json:"answer"`
		Cited  []int  `json:"cited"`
	}
	result := &QAResult{Answer: r.OutputText}
	if err := parseJSONLoose(r.OutputText, &parsed); err == nil {
		result.Answer = parsed.Answer
		seen := map[int]bool{}
		for _, idx := range parsed.Cited {
			if idx >= 0 && idx < len(chunks) && !seen[idx] {
				seen[idx] = true
				result.Sources = append(result.Sources, Source{
					Content: chunks[idx].Text, Start: chunks[idx].Start, End: chunks[idx].End,
				})
			}
		}
	}
	if len(result.Sources) == 0 {
		result.Sources = append(result.Sources, Source{
			Content: chunks[0].Text, Start: chunks[0].Start, End: chunks[0].End,
		})
	}
	return result, nil
}

// GenerateHighlights 生成高光片段（ADR-0016）。
func (o *OpenAIProvider) GenerateHighlights(segments []Segment) (*HighlightSet, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("无 Segment 可供生成高光")
	}
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	payload := map[string]any{
		"model":        openaiAnalysisModel,
		"instructions": highlightSystemPrompt,
		"input":        "请基于以下全部带编号片段的播客转录稿，选出最值得听的高光区间：\n\n" + sb.String(),
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "highlight_set",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"highlights": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"gist":      map[string]any{"type": "string"},
									"citations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								},
								"required": []string{"gist", "citations"},
							},
						},
					},
					"required": []string{"highlights"},
				},
			},
		},
	}
	data, err := o.doResponses(payload, "高光")
	if err != nil {
		return nil, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	hs := &HighlightSet{}
	if err := parseJSONLoose(r.OutputText, hs); err != nil {
		return nil, fmt.Errorf("解析 openai 高光: %w", err)
	}
	return hs, nil
}

// Paraphrase 生成复述讲解（GeneratedDerivative，ADR-0018）。
// 与 Groq.Paraphrase 对偶：输出纯文本讲解，非逐字原文；参考片段由调用方传入。
func (o *OpenAIProvider) Paraphrase(question string, referenceSegments []Segment) (*ParaphraseResult, error) {
	if len(referenceSegments) == 0 {
		return nil, fmt.Errorf("复述讲解至少需要一个参考片段")
	}
	var sb strings.Builder
	for _, seg := range referenceSegments {
		sb.WriteString(fmt.Sprintf("[%s | %.0f-%.0fs] %s\n", seg.ID, seg.Start, seg.End, seg.Text))
	}
	refs := make([]string, 0, len(referenceSegments))
	for _, seg := range referenceSegments {
		refs = append(refs, seg.ID)
	}
	input := fmt.Sprintf("参考片段：\n%s\n\n用户的疑问：%s\n\n请基于参考片段重新讲解，帮用户理解。", sb.String(), question)
	payload := map[string]any{
		"model":        openaiAnalysisModel,
		"instructions": paraphraseSystemPrompt,
		"input":        input,
	}
	data, err := o.doResponses(payload, "复述讲解")
	if err != nil {
		return nil, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("解析 openai 复述讲解响应: %w", err)
	}
	return &ParaphraseResult{Text: strings.TrimSpace(r.OutputText), ReferenceSegmentIDs: refs}, nil
}

// StudyChatAnswer 学习对话一轮（GeneratedDerivative，ADR-0018 R3）。
// OpenAI /responses 接口不接收完整 chat history，故把历史折叠进 input 文本。
func (o *OpenAIProvider) StudyChatAnswer(question string, history []StudyChatMessage, candidates []Segment) (*StudyChatResult, error) {
	if len(candidates) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	retrieved := Retrieve(BuildChunks(candidates, 8), question, 6)
	if len(retrieved) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	var ctxSB strings.Builder
	for _, c := range retrieved {
		ctxSB.WriteString(fmt.Sprintf("[%s | %.0f-%.0fs] %s\n", c.SegmentID, c.Start, c.End, c.Text))
	}
	var histSB strings.Builder
	start := 0
	if len(history) > 6 {
		start = len(history) - 6
	}
	for _, m := range history[start:] {
		role := "用户"
		if m.Role == "assistant" {
			role = "助手"
		}
		histSB.WriteString(fmt.Sprintf("%s：%s\n", role, m.Content))
	}
	input := fmt.Sprintf("候选片段：\n%s\n对话历史：\n%s当前用户问题：%s", ctxSB.String(), histSB.String(), question)
	payload := map[string]any{
		"model":        openaiAnalysisModel,
		"instructions": studyChatSystemPrompt,
		"input":        input,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "studychat_result",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer":              map[string]any{"type": "string"},
						"referenceSegmentIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"answer", "referenceSegmentIds"},
				},
			},
		},
	}
	data, err := o.doResponses(payload, "学习对话")
	if err != nil {
		return nil, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("解析 openai 学习对话响应: %w", err)
	}
	var parsed struct {
		Answer              string   `json:"answer"`
		ReferenceSegmentIDs []string `json:"referenceSegmentIds"`
	}
	if err := parseJSONLoose(r.OutputText, &parsed); err != nil {
		return nil, fmt.Errorf("解析 StudyChat 输出失败: %w", err)
	}
	if len(parsed.ReferenceSegmentIDs) == 0 || strings.TrimSpace(parsed.Answer) == "" {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	validIDs := validReferenceIDs(parsed.ReferenceSegmentIDs, retrieved)
	if len(validIDs) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	return &StudyChatResult{
		Answer: &StudyChatMessage{
			Role:                "assistant",
			Content:             strings.TrimSpace(parsed.Answer),
			ReferenceSegmentIDs: validIDs,
		},
	}, nil
}

// CheckReference 主题锚定校验（ADR-0018 R3 硬约束二）。
func (o *OpenAIProvider) CheckReference(question, answer string, referenceSegments []Segment) (ReferenceCheckResult, error) {
	if len(referenceSegments) == 0 {
		return ReferenceCheckResult{Related: false, Reason: "无参考片段"}, nil
	}
	var sb strings.Builder
	for _, seg := range referenceSegments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	input := fmt.Sprintf("用户问题：%s\n\nAI 回答：\n%s\n\n参考片段原文：\n%s", question, answer, sb.String())
	payload := map[string]any{
		"model":        openaiAnalysisModel,
		"instructions": referenceCheckSystemPrompt,
		"input":        input,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "reference_check_result",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"related": map[string]any{"type": "boolean"},
						"reason":  map[string]any{"type": "string"},
					},
					"required": []string{"related", "reason"},
				},
			},
		},
	}
	data, err := o.doResponses(payload, "校验")
	if err != nil {
		return ReferenceCheckResult{}, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return ReferenceCheckResult{Related: false, Reason: "校验解析失败，保守拒绝"}, nil
	}
	var parsed struct {
		Related bool   `json:"related"`
		Reason  string `json:"reason"`
	}
	if err := parseJSONLoose(r.OutputText, &parsed); err != nil {
		return ReferenceCheckResult{Related: false, Reason: "校验解析失败，保守拒绝"}, nil
	}
	return ReferenceCheckResult{Related: parsed.Related, Reason: parsed.Reason}, nil
}
