// Package markdown 生成确定性、Obsidian 友好的 KnowledgeNote Markdown（Roadmap Phase 5）。
// 信息分层（ADR-0018）：CitedDerivative（摘要/要点/章节区间/金句）挂 Citation，逐字可核验；
// GeneratedDerivative（Gist 等讲解）挂 Reference，明确标注 AI 生成、非原文。两种块视觉可区分。
package markdown

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// GeneratedBlock 一个待下沉的 GeneratedDerivative 块（ADR-0018 R4）。
// 由 Paraphrase 或 Owner 手选的 StudyChat 回答构成；下沉到 KnowledgeNote 时
// 明确标注为 AI 讲解、非原文，并挂 Reference 链接（?ref=），与 Citation 块视觉区分。
type GeneratedBlock struct {
	Kind       string   // "paraphrase" | "studychat"（用于块标题说明）
	Body       string   // AI 讲解文本（非逐字原文）
	References []string // Reference 指向的 Segment ID（时间范围由程序解析）
}

// Input 渲染 KnowledgeNote Markdown 所需的全部输入。
// Card 必须已通过 ValidateCard（含有效 Citation）；Segments 来自当前转录版本。
type Input struct {
	Card            *provider.KnowledgeCard // 已验证（含有效 Citation）
	Segments        []provider.Segment      // 当前转录版本段（解析 Citation/Reference 时间范围）
	SourceType      string
	SourceID        string
	Title           string           // 标题（优先卡片标题，回退 source 标题）
	BaseURL         string           // 实例公开 URL，用于 Citation/Reference 链接
	GeneratedAt     string           // 渲染时间（可注入以便 golden test 确定性）
	GeneratedBlocks []GeneratedBlock // 可选：下沉的 GeneratedDerivative 块（ADR-0018 R4）
}

// Render 生成 KnowledgeNote Markdown。确定性：字段顺序固定、标签排序、时间格式化固定。
func Render(in Input) (string, error) {
	if in.Card == nil {
		return "", fmt.Errorf("card 不能为空")
	}
	var b strings.Builder

	// frontmatter（Obsidian 友好；特殊字符转义）
	title := in.Title
	if title == "" {
		title = in.Card.Title
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", frontmatterValue(title))
	fmt.Fprintf(&b, "source_type: %s\n", frontmatterValue(in.SourceType))
	fmt.Fprintf(&b, "source_id: %s\n", frontmatterValue(in.SourceID))
	if in.GeneratedAt != "" {
		fmt.Fprintf(&b, "generated_at: %s\n", frontmatterValue(in.GeneratedAt))
	}
	tags := append([]string(nil), in.Card.Tags...)
	sort.Strings(tags)
	if len(tags) > 0 {
		var quoted []string
		for _, tg := range tags {
			quoted = append(quoted, `"`+tg+`"`)
		}
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(quoted, ", "))
	}
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", title)

	// 摘要
	if strings.TrimSpace(in.Card.Summary.Text) != "" {
		b.WriteString("## 摘要\n\n")
		fmt.Fprintf(&b, "%s %s\n\n", in.Card.Summary.Text, citationLinks(in.SourceType, in.SourceID, in.BaseURL, in.Card.Summary.Citations, in.Segments))
	}

	// 关键要点
	if len(in.Card.KeyPoints) > 0 {
		b.WriteString("## 关键要点\n\n")
		for _, kp := range in.Card.KeyPoints {
			line := "- **" + kp.Content + "**"
			if kp.Description != "" {
				line += "：" + kp.Description
			}
			line += " " + citationLinks(in.SourceType, in.SourceID, in.BaseURL, kp.Citations, in.Segments)
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// 章节（ADR-0018 信息分层）
	// 章节区间本身是 CitedDerivative：标题 + 时间范围 + Citation 链接，标为原文。
	// 章节上的 Gist 是 GeneratedDerivative：正名为 AI 讲解，独立成 callout 块置于章节区间之后，
	// 挂 Reference（锚点文字"参考"，URL ?ref=），不再蹭章节的 Citation 链接。
	if len(in.Card.Chapters) > 0 {
		b.WriteString("## 章节\n\n")
		for _, ch := range in.Card.Chapters {
			start, end, ok := provider.ResolveCitationSpan(ch.Citations, in.Segments)
			timeRange := ""
			if ok {
				timeRange = fmt.Sprintf("（%s – %s）", fmtTime(start), fmtTime(end))
			}
			// CitedDerivative：章节区间（原文依据）
			fmt.Fprintf(&b, "> [!quote] 原文 · %s%s\n", ch.Title, timeRange)
			cl := citationLinks(in.SourceType, in.SourceID, in.BaseURL, ch.Citations, in.Segments)
			if cl != "" {
				b.WriteString("> " + cl + "\n")
			}
			b.WriteString("\n")
			// GeneratedDerivative：Gist（AI 讲解·非原文），正名后挂 Reference
			if ch.Gist != "" {
				b.WriteString(generatedCallout(ch.Gist, referenceLinks(in.SourceType, in.SourceID, in.BaseURL, ch.Citations, in.Segments)))
			}
		}
	}

	// 金句（CitedDerivative，逐字可核验）
	if len(in.Card.Quotes) > 0 {
		b.WriteString("## 金句\n\n")
		for _, q := range in.Card.Quotes {
			fmt.Fprintf(&b, "> [!quote] 原文\n> %s\n", q.Text)
			cl := citationLinks(in.SourceType, in.SourceID, in.BaseURL, q.Citations, in.Segments)
			if cl != "" {
				b.WriteString("> " + cl + "\n")
			}
			b.WriteString("\n")
		}
	}

	// 标签（正文展示）
	if len(tags) > 0 {
		b.WriteString("## 标签\n\n")
		for _, tg := range tags {
			fmt.Fprintf(&b, "`%s` ", tg)
		}
		b.WriteString("\n")
	}

	// GeneratedDerivative 下沉块（ADR-0018 R4）
	// Owner 手选的 Paraphrase / StudyChat 回答；每块明确标注 AI 讲解·非原文，
	// 挂 Reference 链接（?ref=），与上方 CitedDerivative 块（?t=）视觉与语义区分。
	// 这是把 CloudWisePod 内部的 Cited/Generated 分级延伸到 PersonalKnowledgeBase 的最后一公里。
	if len(in.GeneratedBlocks) > 0 {
		b.WriteString("## AI 讲解（非原文）\n\n")
		b.WriteString("> 以下内容由 AI 重新组织生成，**参考**自原音频片段，但**不是逐字原文**，不可当作原话核验。\n\n")
		for _, blk := range in.GeneratedBlocks {
			label := "AI 讲解·非原文"
			if blk.Kind != "" {
				label = "AI 讲解·非原文（" + blk.Kind + "）"
			}
			b.WriteString(generatedCalloutBody(blk.Body, label, referenceLinks(in.SourceType, in.SourceID, in.BaseURL, blk.References, in.Segments)))
		}
	}

	return b.String(), nil
}

// citationLinks 生成 Citation 链接列表（程序解析时间范围，ADR-0008）。
func citationLinks(sourceType, sourceID, baseURL string, citations []string, segments []provider.Segment) string {
	var links []string
	for _, c := range citations {
		start, _, ok := provider.ResolveCitationRange(c, segments)
		if !ok {
			continue
		}
		// 链接到实例的 Source 页并定位到确定时间点
		url := fmt.Sprintf("%s/sources/%s/%s?t=%s#seg-%s",
			strings.TrimSuffix(baseURL, "/"), sourceType, sourceID, strconv.FormatFloat(start, 'f', 1, 64), c)
		links = append(links, fmt.Sprintf("[⏱ %s](%s)", fmtTime(start), url))
	}
	if len(links) == 0 {
		return ""
	}
	return "(" + strings.Join(links, " ") + ")"
}

// generatedCalloutBody 生成带自定义标签的 GeneratedDerivative callout 块（下沉用，R4）。
func generatedCalloutBody(body, label, links string) string {
	var b strings.Builder
	b.WriteString("> [!ai-generated] " + label + "\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("> " + line + "\n")
	}
	if links != "" {
		b.WriteString("> " + links + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// referenceLinks 生成 Reference 链接列表（GeneratedDerivative，ADR-0018）。
// 与 citationLinks 对偶：锚点文字用"参考"，URL 用 ?ref= 区分于 Citation 的 ?t=，
// 避免参考关系伪装成可核验的引用。时间范围仍由程序从 Segment 解析。
func referenceLinks(sourceType, sourceID, baseURL string, references []string, segments []provider.Segment) string {
	var links []string
	for _, c := range references {
		start, _, ok := provider.ResolveCitationRange(c, segments)
		if !ok {
			continue
		}
		url := fmt.Sprintf("%s/sources/%s/%s?ref=%s#seg-%s",
			strings.TrimSuffix(baseURL, "/"), sourceType, sourceID, strconv.FormatFloat(start, 'f', 1, 64), c)
		links = append(links, fmt.Sprintf("[参考 %s](%s)", fmtTime(start), url))
	}
	if len(links) == 0 {
		return ""
	}
	return "(" + strings.Join(links, " ") + ")"
}

// generatedCallout 生成一个 GeneratedDerivative callout 块（双层标注，R4）。
// Obsidian 渲染为彩色框；首行文字前缀兜底，保证非 Obsidian 渲染器也能识别类别。
// links 为已生成的参考链接串（可为空）。
func generatedCallout(body, links string) string {
	var b strings.Builder
	b.WriteString("> [!ai-generated] AI 讲解·非原文\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("> " + line + "\n")
	}
	if links != "" {
		b.WriteString("> " + links + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// fmtTime 秒 → m:ss 或 h:mm:ss。
func fmtTime(sec float64) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// frontmatterValue 转义 frontmatter 值：含特殊字符时加引号并转义内部引号/反斜杠。
func frontmatterValue(v string) string {
	if v == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(v, ":#\"'{}[]&*!|>%`\\\n\t") || strings.HasPrefix(v, " ")
	if !needsQuote {
		return v
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v)
	return `"` + escaped + `"`
}
