package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// QuestionStudyConfig 冻结无凭据的连接身份和生成、检查模型。
type QuestionStudyConfig struct {
	Provider         string `json:"provider"`
	ConnectionID     string `json:"connection_id"`
	GenerationModel  string `json:"generation_model"`
	ReviewModel      string `json:"review_model"`
	IndependentModel bool   `json:"independent_model"`
}

// QuestionStudyResponse 保留内容及数值用量，包括不可采用的响应。
type QuestionStudyResponse struct {
	Content         string `json:"content"`
	Model           string `json:"model"`
	InputUnits      int    `json:"input_units"`
	OutputUnits     int    `json:"output_units"`
	UsageKnown      bool   `json:"usage_known"`
	UnverifiedModel bool   `json:"unverified_model"`
	Failure         string `json:"failure,omitempty"`
}

// QuestionStudyClient 执行单次有界调用，不自动重发未知结果。
type QuestionStudyClient struct {
	config       QuestionStudyConfig
	key, baseURL string
	http         *http.Client
}

// QuestionStudy Uses the existing POD connection, with models resolved by Config's new task
// fallback chain. Construction performs no request and freezes no credentials.
func (sel *Selector) QuestionStudy(generation, review string) (*QuestionStudyClient, error) {
	if generation == "" {
		generation = sel.podModel
	}
	if review == "" {
		review = sel.podModel
	}
	base := strings.TrimRight(strings.TrimSpace(sel.podBaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || sel.podAPIKey == "" || strings.ContainsAny(sel.podAPIKey, "\r\n") {
		return nil, errors.New("question study POD connection unavailable")
	}
	for _, model := range []string{generation, review} {
		if strings.TrimSpace(model) == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n") {
			return nil, errors.New("question study model unavailable")
		}
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("question-study\x00"+base)))
	cfg := QuestionStudyConfig{Provider: "pod", ConnectionID: fingerprint, GenerationModel: generation, ReviewModel: review, IndependentModel: generation != review}
	return &QuestionStudyClient{config: cfg, key: sel.podAPIKey, baseURL: base, http: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Config 返回无凭据的冻结配置身份。
func (c *QuestionStudyClient) Config() QuestionStudyConfig { return c.config }

// Generate 生成待检查回答，不直接发布到历史。
func (c *QuestionStudyClient) Generate(ctx context.Context, scope QuestionStudyScope) (*QuestionStudyResponse, error) {
	messages, err := QuestionStudyMessages(scope)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, c.config.GenerationModel, 4096, messages)
}

// Review 使用冻结回答及材料执行独立检查阶段。
func (c *QuestionStudyClient) Review(ctx context.Context, scope QuestionStudyScope, answer QuestionStudyAnswer) (*QuestionStudyResponse, error) {
	messages, err := QuestionStudyReviewMessages(scope, answer)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, c.config.ReviewModel, 2048, messages)
}
func (c *QuestionStudyClient) call(ctx context.Context, model string, cap int, messages []QuestionStudyMessage) (*QuestionStudyResponse, error) {
	payload, _ := json.Marshal(struct {
		Model               string                 `json:"model"`
		Messages            []QuestionStudyMessage `json:"messages"`
		MaxCompletionTokens int                    `json:"max_completion_tokens"`
	}{model, messages, cap})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("question study request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.key)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("question study transport failed; remote result unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("question study HTTP status %d; response body omitted", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return nil, errors.New("question study response incomplete or exceeds capacity")
	}
	var wire struct {
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
	if json.Unmarshal(raw, &wire) != nil {
		return nil, errors.New("invalid question study response envelope")
	}
	result := &QuestionStudyResponse{Model: model}
	if wire.Usage != nil && wire.Usage.Input != nil && wire.Usage.Output != nil && *wire.Usage.Input >= 0 && *wire.Usage.Output >= 0 {
		result.UsageKnown = true
		result.InputUnits = *wire.Usage.Input
		result.OutputUnits = *wire.Usage.Output
	}
	if wire.Model != model {
		result.Model = "unknown"
		result.UnverifiedModel = true
		result.Failure = "question study response model mismatch"
		return result, nil
	}
	if len(wire.Choices) != 1 || len(wire.Choices[0].Message.Content) > 64*1024 || !json.Valid([]byte(wire.Choices[0].Message.Content)) {
		result.Failure = "question study response content invalid or exceeds capacity"
		return result, nil
	}
	result.Content = wire.Choices[0].Message.Content
	return result, nil
}

// EstimateQuestionStudyReview 按实际序列化检查消息计算有界估计。
func EstimateQuestionStudyReview(scope QuestionStudyScope, answer QuestionStudyAnswer) (*KnowledgeEstimate, error) {
	messages, err := QuestionStudyReviewMessages(scope, answer)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(messages)
	return &KnowledgeEstimate{Method: "question-study-serialized-byte-bound-v1", InputFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), InputTokens: len(raw) + 64, OutputTokens: 2048, Approximate: true}, nil
}
