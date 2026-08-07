package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	groqBaseURL         = "https://api.groq.com/openai/v1"
	groqTranscribeModel = "whisper-large-v3"
	// 分析/QA 用 llama-3.3-70b：TPM 12K 比 gpt-oss-120b(8K) 宽裕，能吃下整集 transcript。
	// 它不支持 json_schema strict，但支持 json_object（保证合法 JSON），
	// 配合 prompt 约束字段 + parseJSONLoose 容错解析兜底（第 10 题设计）。
	groqAnalysisModel = "llama-3.3-70b-versatile"
	// 留出 prompt、JSON 输出和中文 token 密度的余量，避免触及 Groq 12K TPM。
	analysisWindowCharBudget  = 12000
	analysisWindowMinInterval = 30 * time.Second
)

// GroqProvider Groq 全套实现（方案 B 主力）。
// 转录走 /audio/transcriptions（multipart file），分析/QA 走 /chat/completions。
type GroqProvider struct {
	apiKey         string
	baseURL        string // 空则用默认 groqBaseURL
	model          string // 空则用默认 groqAnalysisModel
	chatCompleteFn func(messages []map[string]string, jsonMode string) (string, int, error)
	sleepFn        func(time.Duration)
}

func NewGroqProvider(apiKey string) *GroqProvider {
	return &GroqProvider{apiKey: apiKey}
}

func (g *GroqProvider) Name() string { return "groq" }

// WithModel 返回使用指定分析模型的新实例（转录模型不变）。
func (g *GroqProvider) WithModel(model string) *GroqProvider {
	return &GroqProvider{apiKey: g.apiKey, baseURL: g.baseURL, model: model, chatCompleteFn: g.chatCompleteFn, sleepFn: g.sleepFn}
}

// Transcribe 转录：Groq 要求上传文件本体（不支持服务端 fetch URL）。
// filePath 是已临时落盘的音频文件。
func (g *GroqProvider) Transcribe(filePath string) (*TranscriptResult, error) {
	data, code, err := uploadFileAsMultipart(
		context.Background(), groqBaseURL+"/audio/transcriptions", g.apiKey, "file", filePath,
		map[string]string{
			"model":                     groqTranscribeModel,
			"response_format":           "verbose_json",
			"timestamp_granularities[]": "segment",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("groq 转录请求: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("groq 转录失败 HTTP %d: %s", code, string(data))
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
		return nil, fmt.Errorf("解析 groq 转录响应: %w", err)
	}
	res := &TranscriptResult{Language: raw.Language, Text: strings.TrimSpace(raw.Text)}
	for i, s := range raw.Segments {
		// 稳定 Segment ID（ADR-0008）：程序分配，模型只引用 ID，不估算时间戳。
		res.Segments = append(res.Segments, Segment{
			ID:    fmt.Sprintf("seg-%04d", i+1),
			Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text),
		})
	}
	return res, nil
}

// chatComplete 调用 /chat/completions，返回助手的 message content。
// jsonMode: "schema" 用 json_schema strict（仅 gpt-oss 支持）；"object" 用 json_object（所有模型，不强制 schema）；"" 不约束。
func (g *GroqProvider) chatComplete(messages []map[string]string, jsonMode string) (string, int, error) {
	model := g.model
	if model == "" {
		model = groqAnalysisModel
	}
	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": 0.3,
	}
	switch jsonMode {
	case "schema":
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "knowledge_card",
				"schema": knowledgeCardSchema,
			},
		}
	case "object":
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	data, code, err := postJSON(context.Background(), groqBaseURL+"/chat/completions", g.apiKey, payload)
	if err != nil {
		return "", code, err
	}
	if code != 200 {
		return "", code, fmt.Errorf("groq chat 失败 HTTP %d: %s", code, string(data))
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", code, fmt.Errorf("解析 groq chat 响应: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", code, fmt.Errorf("groq 返回空 choices")
	}
	return resp.Choices[0].Message.Content, code, nil
}

func (g *GroqProvider) complete(messages []map[string]string, jsonMode string) (string, int, error) {
	if g.chatCompleteFn != nil {
		return g.chatCompleteFn(messages, jsonMode)
	}
	return g.chatComplete(messages, jsonMode)
}

// Analyze 生成 KnowledgeCard（Evidence-first）。用 json_object + prompt 约束 + 容错解析，
// 再由调用方（CitationValidator）强校验：Citation 必须引用真实 Segment.ID，金句必须逐字匹配。
func (g *GroqProvider) Analyze(transcript string, segments []Segment) (*KnowledgeCard, error) {
	_ = transcript // Segment 才是可引用的最小证据单位。
	windows := splitAnalysisWindows(segments, analysisWindowCharBudget)
	if len(windows) == 0 {
		return nil, fmt.Errorf("无法分析空转录稿")
	}
	cards := make([]*KnowledgeCard, 0, len(windows))
	for i, window := range windows {
		if i > 0 {
			g.waitBetweenAnalysisWindows()
		}
		card, err := g.analyzeWindow(window)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return mergeKnowledgeCards(cards), nil
}

func (g *GroqProvider) waitBetweenAnalysisWindows() {
	if g.sleepFn != nil {
		g.sleepFn(analysisWindowMinInterval)
		return
	}
	time.Sleep(analysisWindowMinInterval)
}

func (g *GroqProvider) analyzeWindow(segments []Segment) (*KnowledgeCard, error) {
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	content, _, err := g.complete([]map[string]string{
		{"role": "system", "content": analysisSystemPrompt + "\n\n必须只输出一个 JSON 对象，不要输出任何其他文字或 markdown 代码块。"},
		{"role": "user", "content": "请基于以下带编号片段的播客转录稿生成结构化知识卡片（citations 引用片段ID）：\n\n" + sb.String()},
	}, "object")
	if err != nil {
		return nil, err
	}
	card := &KnowledgeCard{}
	if err := parseJSONLoose(content, card); err != nil {
		return nil, fmt.Errorf("解析 KnowledgeCard 失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	return card, nil
}

// splitAnalysisWindows 保持 Segment 完整，避免 Citation 横跨被截断的文本。
func splitAnalysisWindows(segments []Segment, charBudget int) [][]Segment {
	if charBudget <= 0 {
		return nil
	}
	var windows [][]Segment
	var current []Segment
	used := 0
	for _, seg := range segments {
		cost := len(seg.ID) + len(seg.Text) + 4
		if len(current) > 0 && used+cost > charBudget {
			windows = append(windows, current)
			current, used = nil, 0
		}
		current = append(current, seg)
		used += cost
	}
	if len(current) > 0 {
		windows = append(windows, current)
	}
	return windows
}

// mergeKnowledgeCards 只做确定性拼接；内容与 Citation 均来自已分析的窗口。
func mergeKnowledgeCards(cards []*KnowledgeCard) *KnowledgeCard {
	merged := &KnowledgeCard{}
	var summaries []string
	var citations []string
	seenTags := map[string]bool{}
	for _, card := range cards {
		if card == nil {
			continue
		}
		if merged.Title == "" && strings.TrimSpace(card.Title) != "" {
			merged.Title = card.Title
		}
		if strings.TrimSpace(card.Summary.Text) != "" {
			summaries = append(summaries, card.Summary.Text)
			citations = appendUnique(citations, card.Summary.Citations...)
		}
		merged.KeyPoints = append(merged.KeyPoints, card.KeyPoints...)
		merged.Chapters = append(merged.Chapters, card.Chapters...)
		merged.Quotes = append(merged.Quotes, card.Quotes...)
		merged.SuggestedQuestions = append(merged.SuggestedQuestions, card.SuggestedQuestions...)
		for _, tag := range card.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" && !seenTags[tag] {
				seenTags[tag] = true
				merged.Tags = append(merged.Tags, tag)
			}
		}
	}
	merged.Summary = CitedText{Text: strings.Join(summaries, "\n\n"), Citations: citations}
	return merged
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}

// Answer 单期问答（RAG：检索相关片段 → 喂 LLM → 返回答案 + 引用时间戳）。
// 只把 top-5 相关 chunk 喂给 LLM，避免整集 transcript 撞 Groq TPM 限制，同时提供可跳转的引用。
func (g *GroqProvider) Answer(question string, segments []Segment) (*QAResult, error) {
	chunks := Retrieve(BuildChunks(segments, 8), question, 5)
	if len(chunks) == 0 {
		return &QAResult{Answer: "暂无转录稿可用于回答。"}, nil
	}

	// 构造带编号的片段上下文，便于 LLM 返回 cited 索引
	var ctxSB strings.Builder
	for i, c := range chunks {
		ctxSB.WriteString(fmt.Sprintf("[片段%d | %.0f-%.0fs] %s\n", i, c.Start, c.End, c.Text))
	}
	prompt := fmt.Sprintf("基于以下播客片段回答问题。只输出 JSON：{\"answer\":\"回答\",\"cited\":[引用的片段编号数组]}。若片段无相关信息，answer 说明并 cited 为空。\n\n%s\n问题：%s",
		ctxSB.String(), question)

	content, _, err := g.complete([]map[string]string{
		{"role": "system", "content": "你是播客内容助手。基于给定的带编号片段回答，必须通过 cited 标注引用了哪些片段编号。"},
		{"role": "user", "content": prompt},
	}, "object")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Answer string `json:"answer"`
		Cited  []int  `json:"cited"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		// 解析失败时退化为直接展示原文，不阻塞回答
		return &QAResult{Answer: content}, nil
	}
	// 只保留模型实际引用的片段（Phase 7：无引用不附加兜底）
	result := &QAResult{Answer: resp.Answer, Sources: MapCitedToSources(chunks, resp.Cited)}
	return result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GenerateHighlights 生成高光片段（ADR-0016）。
// 喂入全部 Segment（不分窗口，因为高光判断需要整集视野）。
func (g *GroqProvider) GenerateHighlights(segments []Segment) (*HighlightSet, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("无 Segment 可供生成高光")
	}
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	content, _, err := g.complete([]map[string]string{
		{"role": "system", "content": highlightSystemPrompt + "\n\n必须只输出一个 JSON 对象，不要输出任何其他文字或 markdown 代码块。"},
		{"role": "user", "content": "请基于以下全部带编号片段的播客转录稿，选出最值得听的高光区间：\n\n" + sb.String()},
	}, "object")
	if err != nil {
		return nil, err
	}
	hs := &HighlightSet{}
	if err := parseJSONLoose(content, hs); err != nil {
		return nil, fmt.Errorf("解析高光片段失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	return hs, nil
}

// Paraphrase 生成复述讲解（GeneratedDerivative，ADR-0018）。
// 输出是 AI 重新组织的讲解，非逐字原文；referenceSegmentIDs 由调用方传入的参考片段决定，
// 模型不参与选择（时间点真实性的保证来自调用方）。
func (g *GroqProvider) Paraphrase(question string, referenceSegments []Segment) (*ParaphraseResult, error) {
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
	prompt := fmt.Sprintf("参考片段：\n%s\n\n用户的疑问：%s\n\n请基于参考片段重新讲解，帮用户理解。",
		sb.String(), question)
	content, _, err := g.complete([]map[string]string{
		{"role": "system", "content": paraphraseSystemPrompt},
		{"role": "user", "content": prompt},
	}, "")
	if err != nil {
		return nil, err
	}
	return &ParaphraseResult{Text: strings.TrimSpace(content), ReferenceSegmentIDs: refs}, nil
}

// StudyChatAnswer 学习对话一轮（GeneratedDerivative，ADR-0018 R3）。
// 模型自选参考 Segment 并生成讲解；硬约束一（无 Reference 不生成）由此函数的调用方依据返回结果执行。
func (g *GroqProvider) StudyChatAnswer(question string, history []StudyChatMessage, candidates []Segment) (*StudyChatResult, error) {
	if len(candidates) == 0 {
		// 硬约束一：无候选 Segment 可关联 → 不生成，返回可见反馈。
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	// RAG 检索 top-N 候选片段作为可参考集（问题驱动）。
	retrieved := Retrieve(BuildChunks(candidates, 8), question, 6)
	if len(retrieved) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	var ctxSB strings.Builder
	for _, c := range retrieved {
		ctxSB.WriteString(fmt.Sprintf("[%s | %.0f-%.0fs] %s\n", c.SegmentID, c.Start, c.End, c.Text))
	}
	// 构造历史消息（最多最近 6 轮）。
	msgs := []map[string]string{
		{"role": "system", "content": studyChatSystemPrompt + "\n\n必须只输出一个 JSON 对象，不要输出任何其他文字或 markdown 代码块。"},
	}
	start := 0
	if len(history) > 6 {
		start = len(history) - 6
	}
	for _, m := range history[start:] {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		msgs = append(msgs, map[string]string{"role": role, "content": m.Content})
	}
	msgs = append(msgs, map[string]string{
		"role": "user", "content": fmt.Sprintf("候选片段：\n%s\n用户问题：%s", ctxSB.String(), question),
	})

	content, _, err := g.complete(msgs, "object")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Answer              string   `json:"answer"`
		ReferenceSegmentIDs []string `json:"referenceSegmentIds"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		return nil, fmt.Errorf("解析 StudyChat 输出失败: %w", err)
	}
	// 硬约束一：模型未关联任何参考 Segment → 不生成，可见反馈。
	if len(resp.ReferenceSegmentIDs) == 0 || strings.TrimSpace(resp.Answer) == "" {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	// 校验所选参考 Segment 真实存在于候选集（防模型编造 ID）。
	validIDs := validReferenceIDs(resp.ReferenceSegmentIDs, retrieved)
	if len(validIDs) == 0 {
		return &StudyChatResult{ScopeFeedback: "这已超出本集内容范围——我找不到任何相关片段来回答这个问题。"}, nil
	}
	return &StudyChatResult{
		Answer: &StudyChatMessage{
			Role:                "assistant",
			Content:             strings.TrimSpace(resp.Answer),
			ReferenceSegmentIDs: validIDs,
		},
	}, nil
}

// CheckReference 主题锚定校验（ADR-0018 R3 硬约束二）。
// 独立判定步骤：只判相关、不参与生成。与生成模型同实例但独立 prompt——
// 成本约束下的妥协（默认零成本），标注为可替换点（虚挂率上升时切换校验模型）。
func (g *GroqProvider) CheckReference(question, answer string, referenceSegments []Segment) (ReferenceCheckResult, error) {
	if len(referenceSegments) == 0 {
		return ReferenceCheckResult{Related: false, Reason: "无参考片段"}, nil
	}
	var sb strings.Builder
	for _, seg := range referenceSegments {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", seg.ID, seg.Text))
	}
	prompt := fmt.Sprintf("用户问题：%s\n\nAI 回答：\n%s\n\n参考片段原文：\n%s", question, answer, sb.String())
	content, _, err := g.complete([]map[string]string{
		{"role": "system", "content": referenceCheckSystemPrompt + "\n\n必须只输出一个 JSON 对象。"},
		{"role": "user", "content": prompt},
	}, "object")
	if err != nil {
		return ReferenceCheckResult{}, err
	}
	var resp struct {
		Related bool   `json:"related"`
		Reason  string `json:"reason"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		// 解析失败时保守判为不相关（宁误杀不放行虚挂）。
		return ReferenceCheckResult{Related: false, Reason: "校验解析失败，保守拒绝"}, nil
	}
	return ReferenceCheckResult{Related: resp.Related, Reason: resp.Reason}, nil
}

// validReferenceIDs 过滤模型选的参考 ID，只保留真实存在于检索结果中的。
func validReferenceIDs(ids []string, retrieved []Chunk) []string {
	exist := map[string]bool{}
	for _, c := range retrieved {
		exist[c.SegmentID] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if exist[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
