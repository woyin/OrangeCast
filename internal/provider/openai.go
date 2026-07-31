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
	apiKey string
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

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, openaiBaseURL+"/audio/transcriptions", body)
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
	for _, s := range raw.Segments {
		res.Segments = append(res.Segments, Segment{Start: s.Start, End: s.End, Text: s.Text})
	}
	return res, nil
}

// Analyze 走 /responses + json_schema strict（OpenAI 保证结构化输出）。
func (o *OpenAIProvider) Analyze(transcript string) (*KnowledgeCard, error) {
	payload := map[string]any{
		"model": openaiAnalysisModel,
		"instructions": analysisSystemPrompt,
		"input":        "请基于以下播客转录稿生成结构化知识卡片：\n\n" + transcript,
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_schema",
				"name": "knowledge_card",
				"strict": true,
				"schema": knowledgeCardSchema,
			},
		},
	}
	buf, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, openaiBaseURL+"/responses", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai 分析请求: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai 分析失败 HTTP %d: %s", resp.StatusCode, string(data))
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
		"model":       openaiAnalysisModel,
		"instructions": "你是播客内容助手。通过 cited 标注引用的片段编号。",
		"input":       input,
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_schema",
				"name": "qa_result",
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
	buf, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, openaiBaseURL+"/responses", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai QA 失败 HTTP %d", resp.StatusCode)
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
