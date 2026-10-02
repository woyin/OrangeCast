package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LegacyStudyScope freezes the original single-source candidates and bounded history.
// Selection is deterministic: Retrieve(BuildChunks(Segments, 8), Question, 6).
type LegacyStudyScope struct {
	Question string             `json:"question"`
	History  []StudyChatMessage `json:"history"`
	Segments []Segment          `json:"segments"`
}

// LegacyStudyClient executes cancellable single-call legacy QA stages against a frozen
// Groq/OpenAI endpoint and model; transport failures never trigger an automatic retry.
type LegacyStudyClient struct {
	config       QuestionStudyConfig
	key, baseURL string
	http         *http.Client
}

// LegacyStudy resolves the existing QA provider/model defaults without calling a
// supplier. It freezes a credential-free endpoint identity and rejects POD routing.
func (sel *Selector) LegacyStudy(tc TaskConfig) (*LegacyStudyClient, error) {
	name := tc.Provider
	if name == "" {
		name = "groq"
	}
	var key, base string
	switch name {
	case "groq":
		key, base = sel.groqAPIKey, sel.groqBaseURL
		if base == "" {
			base = groqBaseURL
		}
	case "openai":
		key, base = sel.openaiAPIKey, sel.openaiBaseURL
		if base == "" {
			base = openaiBaseURL
		}
	default:
		return nil, errors.New("legacy study requires the QA groq or openai provider")
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	model := EffectiveTaskModel(tc)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || key == "" || strings.ContainsAny(key, "\r\n") || strings.TrimSpace(model) == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n") || tc.Transcription {
		return nil, errors.New("legacy study QA connection unavailable")
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte("legacy-study\x00"+name+"\x00"+base)))
	return &LegacyStudyClient{config: QuestionStudyConfig{Provider: name, ConnectionID: id, GenerationModel: model, ReviewModel: model}, key: key, baseURL: base, http: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Config returns the frozen endpoint identity and stage models, excluding API keys.
func (c *LegacyStudyClient) Config() QuestionStudyConfig { return c.config }

func legacyStudyChunks(scope LegacyStudyScope) ([]Chunk, error) {
	if strings.TrimSpace(scope.Question) == "" || len(scope.Question) > 16*1024 || len(scope.Segments) == 0 || len(scope.Segments) > 100000 {
		return nil, errors.New("legacy study scope invalid")
	}
	ids := map[string]bool{}
	total := 0
	for _, s := range scope.Segments {
		total += len(s.Text)
		if s.ID == "" || ids[s.ID] || math.IsNaN(s.Start) || math.IsNaN(s.End) || math.IsInf(s.Start, 0) || math.IsInf(s.End, 0) || s.Start < 0 || s.End < s.Start || total > 8*1024*1024 {
			return nil, errors.New("legacy study candidates invalid")
		}
		ids[s.ID] = true
	}
	return Retrieve(BuildChunks(scope.Segments, 8), scope.Question, 6), nil
}

// LegacyStudyMessages is shared by estimates, execution and adoption validation.
func LegacyStudyMessages(scope LegacyStudyScope, stage string, answer *StudyChatMessage) ([]QuestionStudyMessage, error) {
	chunks, err := legacyStudyChunks(scope)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	system := studyChatSystemPrompt
	switch stage {
	case "generate", "generation":
		b.WriteString("候选片段：\n")
		for _, c := range chunks {
			fmt.Fprintf(&b, "[%s | %.0f-%.0fs] %s\n", c.SegmentID, c.Start, c.End, c.Text)
		}
		b.WriteString("\n对话历史：\n")
		start := len(scope.History) - 6
		if start < 0 {
			start = 0
		}
		for _, m := range scope.History[start:] {
			if m.Suppressed {
				continue
			}
			if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > 64*1024 {
				return nil, errors.New("legacy study history invalid")
			}
			fmt.Fprintf(&b, "%s：%s\n", m.Role, m.Content)
		}
		fmt.Fprintf(&b, "当前用户问题：%s\n输出 JSON 字段 answer(string), referenceSegmentIds(string[])。", scope.Question)
	case "review":
		if answer == nil || answer.Role != "assistant" || answer.Suppressed {
			return nil, errors.New("legacy study review answer unavailable")
		}
		raw, _ := json.Marshal(struct {
			Answer string   `json:"answer"`
			IDs    []string `json:"referenceSegmentIds"`
		}{answer.Content, answer.ReferenceSegmentIDs})
		if parsed, err := ParseLegacyStudyAnswer(scope, string(raw)); err != nil || parsed.Answer == nil {
			return nil, errors.New("legacy study review has no valid references")
		}
		system = referenceCheckSystemPrompt
		fmt.Fprintf(&b, "用户问题：%s\n\nAI 回答：\n%s\n\n参考片段原文：\n", scope.Question, answer.Content)
		wanted := map[string]bool{}
		for _, id := range answer.ReferenceSegmentIDs {
			wanted[id] = true
		}
		for _, c := range chunks {
			if wanted[c.SegmentID] {
				fmt.Fprintf(&b, "[%s] %s\n", c.SegmentID, c.Text)
			}
		}
		b.WriteString("\n输出 JSON 字段 related(boolean), reason(string)。")
	default:
		return nil, errors.New("legacy study stage invalid")
	}
	if b.Len() > 512*1024 {
		return nil, errors.New("legacy study messages exceed capacity")
	}
	return []QuestionStudyMessage{{Role: "system", Content: system}, {Role: "user", Content: b.String()}}, nil
}

// EstimateLegacyStudy hashes the actual serialized stage messages and estimates a
// conservative token bound; it does not claim supplier usage or a known price.
func EstimateLegacyStudy(scope LegacyStudyScope, stage string, answer *StudyChatMessage) (*KnowledgeEstimate, error) {
	messages, err := LegacyStudyMessages(scope, stage, answer)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(messages)
	cap := 4096
	if stage == "review" {
		cap = 2048
	}
	return &KnowledgeEstimate{Method: "legacy-study-serialized-byte-bound-v1", InputFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), InputTokens: len(raw) + 64, OutputTokens: cap, Approximate: true}, nil
}

// ParseLegacyStudyAnswer accepts only references from the deterministic frozen
// selection. Empty answers/references produce scope feedback; malformed or foreign
// references return an error and cannot become visible history.
func ParseLegacyStudyAnswer(scope LegacyStudyScope, content string) (*StudyChatResult, error) {
	chunks, err := legacyStudyChunks(scope)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Answer *string  `json:"answer"`
		IDs    []string `json:"referenceSegmentIds"`
	}
	if err := strictQuestionStudyJSON(content, &wire); err != nil {
		return nil, err
	}
	if wire.Answer == nil {
		return nil, errors.New("legacy study answer field missing")
	}
	if strings.TrimSpace(*wire.Answer) == "" || len(wire.IDs) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	valid := map[string]bool{}
	for _, c := range chunks {
		valid[c.SegmentID] = true
	}
	seen := map[string]bool{}
	for _, id := range wire.IDs {
		if !valid[id] || seen[id] {
			return nil, errors.New("legacy study reference outside frozen selection")
		}
		seen[id] = true
	}
	return &StudyChatResult{Answer: &StudyChatMessage{Role: "assistant", Content: strings.TrimSpace(*wire.Answer), ReferenceSegmentIDs: wire.IDs}}, nil
}

// ParseLegacyStudyReview requires a JSON boolean related decision and reason;
// missing or malformed fields fail closed instead of implying an accepted answer.
func ParseLegacyStudyReview(content string) (ReferenceCheckResult, error) {
	var wire struct {
		Related *bool   `json:"related"`
		Reason  *string `json:"reason"`
	}
	if err := strictQuestionStudyJSON(content, &wire); err != nil {
		return ReferenceCheckResult{}, err
	}
	if wire.Related == nil || wire.Reason == nil {
		return ReferenceCheckResult{}, errors.New("legacy study review fields missing")
	}
	return ReferenceCheckResult{Related: *wire.Related, Reason: *wire.Reason}, nil
}

// Generate validates frozen materials and performs one cancellable QA generation.
// Its response includes known numeric usage or an explicit unknown-usage flag.
func (c *LegacyStudyClient) Generate(ctx context.Context, scope LegacyStudyScope) (*QuestionStudyResponse, error) {
	m, e := LegacyStudyMessages(scope, "generate", nil)
	if e != nil {
		return nil, e
	}
	return c.call(ctx, 4096, m)
}

// Review makes a separate cancellable call using the same frozen QA model to
// check the candidate answer against its references; it never publishes history.
func (c *LegacyStudyClient) Review(ctx context.Context, scope LegacyStudyScope, answer StudyChatMessage) (*QuestionStudyResponse, error) {
	m, e := LegacyStudyMessages(scope, "review", &answer)
	if e != nil {
		return nil, e
	}
	return c.call(ctx, 2048, m)
}
func (c *LegacyStudyClient) call(ctx context.Context, cap int, messages []QuestionStudyMessage) (*QuestionStudyResponse, error) {
	path := "/chat/completions"
	payload := map[string]any{"model": c.config.GenerationModel, "messages": messages, "max_completion_tokens": cap}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("legacy study request construction failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("legacy study transport failed; remote result unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("legacy study HTTP status %d; response body omitted", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return nil, errors.New("legacy study response incomplete or exceeds capacity")
	}
	var wire struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Prompt     *int `json:"prompt_tokens"`
			Completion *int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return nil, errors.New("invalid legacy study response envelope")
	}
	result := &QuestionStudyResponse{Model: c.config.GenerationModel}
	if wire.Usage != nil {
		input, output := wire.Usage.Prompt, wire.Usage.Completion
		if input != nil && output != nil && *input >= 0 && *output >= 0 {
			result.UsageKnown = true
			result.InputUnits = *input
			result.OutputUnits = *output
		}
	}
	if wire.Model != c.config.GenerationModel {
		result.Model = "unknown"
		result.UnverifiedModel = true
		result.Failure = "legacy study response model mismatch"
		return result, nil
	}
	content := ""
	if len(wire.Choices) == 1 {
		content = wire.Choices[0].Message.Content
	}
	if len(content) > 64*1024 || !json.Valid([]byte(content)) {
		result.Failure = "legacy study response content invalid or exceeds capacity"
		return result, nil
	}
	result.Content = content
	return result, nil
}
