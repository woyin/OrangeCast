package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/provider"
	"io"
	"net/http"
	"strings"
)

// podClient deliberately makes one HTTP attempt; unknown outcomes are never retried implicitly.
type podClient struct {
	apiKey, baseURL, model, actualModel string
	usageKnown                          bool
}

func (p *podClient) Name() string                   { return "pod" }
func (p *podClient) QualityReceiptModel() string    { return p.actualModel }
func (p *podClient) QualityReceiptUsageKnown() bool { return p.usageKnown }
func (p *podClient) KnowledgeArticleStep(ctx context.Context, input provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	p.actualModel = ""
	p.usageKnown = false
	var usage provider.TaskUsage
	system, user, err := provider.KnowledgeArticleMessages(input)
	if err != nil {
		return nil, usage, err
	}
	cfg, err := provider.KnowledgeConfigForStage(input, p.model)
	if err != nil {
		return nil, usage, err
	}
	raw, _ := json.Marshal(map[string]any{"model": cfg.Model, "max_completion_tokens": cfg.MaxOutputTokens, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, usage, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, usage, fmt.Errorf("remote outcome unknown")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return nil, usage, fmt.Errorf("bounded response unreadable")
	}
	var envelope struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Input  *int `json:"prompt_tokens"`
			Output *int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, usage, fmt.Errorf("response envelope invalid")
	}
	p.actualModel = envelope.Model
	if envelope.Usage != nil {
		if envelope.Usage.Input != nil {
			usage.InputUnits = *envelope.Usage.Input
		}
		if envelope.Usage.Output != nil {
			usage.OutputUnits = *envelope.Usage.Output
		}
		p.usageKnown = envelope.Usage.Input != nil && envelope.Usage.Output != nil && usage.InputUnits >= 0 && usage.OutputUnits >= 0
	}
	if response.StatusCode != 200 {
		return nil, usage, fmt.Errorf("remote HTTP %d", response.StatusCode)
	}
	if len(envelope.Choices) != 1 {
		return nil, usage, fmt.Errorf("one output required")
	}
	text := strings.TrimSpace(envelope.Choices[0].Message.Content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	result := &provider.KnowledgeArticleResult{}
	if json.Unmarshal([]byte(strings.TrimSpace(text)), result) != nil {
		return &provider.KnowledgeArticleResult{Reason: text}, usage, fmt.Errorf("invalid output JSON")
	}
	if envelope.Model != "" && envelope.Model != cfg.Model {
		return result, usage, fmt.Errorf("actual model differs from frozen model")
	}
	return result, usage, nil
}
