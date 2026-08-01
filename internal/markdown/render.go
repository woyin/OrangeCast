// Package markdown 生成确定性、Obsidian 友好的 KnowledgeNote Markdown（Roadmap Phase 5）。
// 摘要、要点、章节、金句全部携带可点击的 Citation 链接，指向本实例 EvidenceAudio 的确定时间点。
package markdown

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// Input 渲染所需的全部数据。
type Input struct {
	Card        *provider.KnowledgeCard // 已验证（含有效 Citation）
	Segments    []provider.Segment      // 当前转录版本段（解析 Citation 时间范围）
	SourceType  string
	SourceID    string
	Title       string // 标题（优先卡片标题，回退 source 标题）
	BaseURL     string // 实例公开 URL，用于 Citation 链接
	GeneratedAt string // 渲染时间（可注入以便 golden test 确定性）
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

	// 章节
	if len(in.Card.Chapters) > 0 {
		b.WriteString("## 章节\n\n")
		for _, ch := range in.Card.Chapters {
			start, end, ok := span(ch.Citations, in.Segments)
			timeRange := ""
			if ok {
				timeRange = fmt.Sprintf("（%s – %s）", fmtTime(start), fmtTime(end))
			}
			fmt.Fprintf(&b, "### %s%s\n\n", ch.Title, timeRange)
			if ch.Gist != "" {
				b.WriteString(ch.Gist + "\n\n")
			}
			b.WriteString(citationLinks(in.SourceType, in.SourceID, in.BaseURL, ch.Citations, in.Segments) + "\n\n")
		}
	}

	// 金句
	if len(in.Card.Quotes) > 0 {
		b.WriteString("## 金句\n\n")
		for _, q := range in.Card.Quotes {
			fmt.Fprintf(&b, "> %s\n>\n", q.Text)
			b.WriteString("> " + citationLinks(in.SourceType, in.SourceID, in.BaseURL, q.Citations, in.Segments) + "\n\n")
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

// span 取 citations 覆盖的最小时间范围。
func span(citations []string, segments []provider.Segment) (float64, float64, bool) {
	var start, end float64
	first := true
	for _, c := range citations {
		s, e, ok := provider.ResolveCitationRange(c, segments)
		if !ok {
			continue
		}
		if first || s < start {
			start = s
		}
		if first || e > end {
			end = e
		}
		first = false
	}
	return start, end, !first
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
