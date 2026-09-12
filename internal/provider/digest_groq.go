package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// EpisodeDigest 三角色 Groq 实现（ADR-0023）。
// 全部走 json_object + prompt 约束 + parseJSONLoose 容错解析，输出必须再过 G2 门禁。

// DigestWriterPromptVersion 门禁与 prompt 契约版本（费用审计用）。
const DigestWriterPromptVersion = "digest-writer-v1"

const digestComposeSystemPrompt = `你是单集播客精读文的写作者。基于给定的本集素材生成一篇精读文。

硬性规则：
1. 正文只能由三类内容块构成：
   - paraphrase（转述）：忠实转述本集内容，必须给出 citations（本集片段ID数组）。直接引语用中文双引号包裹，单条引语不超过 40 字。
   - ai_expansion（AI 展开）：用别的话讲解、类比、举例，帮助读者理解。text 必须以"AI 展开："开头。绝对不得包含任何具体数字、百分比、年份、金额——这些一律放进 fact_gaps。
   - note（笔记）：只能从 given notes 中逐字选用（note_id 必须对应给定笔记），不得改写、不得扩充。
2. 你想要补充但素材里没有依据的具体事实（数字/日期/事件/外部机构结论），写入 fact_gaps：每条一句话描述需要什么事实。最多 %d 条。
3. 不得发明 Owner 观点。不得输出其他块类型。
4. 标题 30 字以内，体现本集核心价值。

只输出 JSON：{"title":"...","blocks":[{"type":"paraphrase|ai_expansion|note","text":"...","citations":["seg-..."],"note_id":"..."}],"fact_gaps":["..."]}`

const digestWeaveSystemPrompt = `你在为精读文补织引用事实块。给定已定稿的内容块和若干检索落源的文档（带片段ID），只允许追加 cited_fact 类型的块。

硬性规则：
1. 每个新块 type=cited_fact：陈述一个来自文档的具体事实，citations 引用该文档的片段ID，document_id 指明来源文档。
2. 不得改动、不得复述已有块。不得引入文档之外的任何事实。
3. 无法从文档可靠回答的缺口直接跳过（留空）。
4. 每个块只讲一个事实，直接引语不超过 40 字。

只输出 JSON：{"blocks":[{"type":"cited_fact","text":"...","citations":["doc-seg-..."],"document_id":"..."}]}`

const digestRewriteSystemPrompt = `你是渠道语气改写器。输入是已核验的精读文内容块，输出小红书笔记版。

硬性规则：
1. 只允许重组、缩写、换语气；绝对不得引入输入之外的事实、数字或主张。
2. 输出为纯文本：开头一句钩子，正文短句换行，结尾 %d 个话题 tag（#开头，从建议中选或基于正文主题生成）。
3. 全文不超过 %d 字。
4. 直接引语保留中文双引号。

只输出 JSON：{"text":"..."}`

// ComposeDigest 生成精读文长文版初稿（转述/AI 展开/笔记块 + 事实缺口）。
func (g *GroqProvider) ComposeDigest(ctx context.Context, req DigestWritingRequest) (*DigestWritingResult, error) {
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
		sb.WriteString("\n已验证要点（citations 已含片段ID）：\n")
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
	if req.TargetLengthChars > 0 {
		sys += fmt.Sprintf("\n目标正文长度约 %d 字。", req.TargetLengthChars)
	}
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": sys},
		{"role": "user", "content": "生成精读文：\n\n" + sb.String()},
	}, "object")
	if err != nil {
		return nil, fmt.Errorf("groq 精读文初稿: %w", err)
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
	return &DigestWritingResult{Title: resp.Title, Blocks: resp.Blocks, FactGaps: resp.FactGaps, Usage: usage}, nil
}

// WeaveDigestFacts 第二阶段：把落源文档补织为引用事实块（只追加 cited_fact）。
func (g *GroqProvider) WeaveDigestFacts(ctx context.Context, req DigestWeaveRequest) (*DigestWeaveResult, error) {
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
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": digestWeaveSystemPrompt},
		{"role": "user", "content": "已有内容块（不可改动）：\n" + existing.String() + "\n" + sb.String()},
	}, "object")
	if err != nil {
		return nil, fmt.Errorf("groq 补织事实块: %w", err)
	}
	var resp struct {
		Blocks []DigestBlockDraft `json:"blocks"`
	}
	if err := parseJSONLoose(content, &resp); err != nil {
		return nil, fmt.Errorf("解析补织事实块失败（原始输出: %s）: %w", truncate(content, 200), err)
	}
	return &DigestWeaveResult{Blocks: resp.Blocks, Usage: usage}, nil
}

// RewriteDigest 渠道语气改写（F3：输入只有已过门禁的长文块）。
func (g *GroqProvider) RewriteDigest(ctx context.Context, req DigestRewriteRequest) (*DigestRewriteResult, error) {
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
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": sys},
		{"role": "user", "content": "改写为小红书笔记版：\n\n" + sb.String()},
	}, "object")
	if err != nil {
		return nil, fmt.Errorf("groq 渠道改写: %w", err)
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
	return &DigestRewriteResult{Text: resp.Text, Usage: usage}, nil
}

// Groq compound 搜索模型（SourceSearch，ADR-0023 §3）：结果自带 URL，直接喂 fetchWebDocument。
const groqSearchModel = "groq/compound"

// Search 实现 SourceSearchProvider：调 groq/compound 拿 URL 候选，摘要不外传。
func (g *GroqProvider) Search(ctx context.Context, query string) ([]DigestSearchResult, error) {
	payload := map[string]any{
		"model":    groqSearchModel,
		"messages": []map[string]string{{"role": "user", "content": query}},
	}
	data, code, _, err := postJSONWithMeta(ctx, g.base()+"/chat/completions", g.apiKey, payload)
	if err != nil {
		return nil, fmt.Errorf("groq 搜索请求: %w", err)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("groq 搜索失败 HTTP %d: %s", code, string(data))
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := parseJSONLoose(string(data), &response); err != nil {
		// compound 返回 markdown 文本而非 JSON；从中抽取 URL。
		return extractURLs(response.Choices[0].Message.Content), nil
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("groq 搜索返回空 choices")
	}
	return extractURLs(response.Choices[0].Message.Content), nil
}

// extractURLs 从文本抽取 http(s) URL 去重（搜索摘要永不直接入文）。
func extractURLs(content string) []DigestSearchResult {
	seen := map[string]bool{}
	var out []DigestSearchResult
	for _, field := range strings.Fields(content) {
		field = strings.Trim(field, "()[]<>\"'，。；、")
		if !strings.HasPrefix(field, "http://") && !strings.HasPrefix(field, "https://") {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, DigestSearchResult{URL: field})
		if len(out) >= 5 {
			break
		}
	}
	return out
}
