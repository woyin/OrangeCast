package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// LibraryTools 返回只读的资料库工具：检索、读取来源摘要、列出片段。
// 所有工具只读本地库，不调用任何外部模型，也不写数据。
func LibraryTools(s *store.Store, publicURL string) []Tool {
	base := strings.TrimSuffix(publicURL, "/")
	link := func(sourceType, sourceID string, start float64) string {
		u := fmt.Sprintf("%s/sources/%s/%s", base, sourceType, sourceID)
		if start > 0 {
			u += fmt.Sprintf("?t=%.1f", start)
		}
		return u
	}
	return []Tool{
		{
			Name:        "search_library",
			Description: "在用户的播客与文档资料库里做全文检索，返回命中的原文片段、所属来源与可跳转的链接。",
			Schema: map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "检索词或问题"},
				"limit": map[string]any{"type": "integer", "description": "最多返回条数，默认 8，上限 20"},
			}},
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Query) == "" {
					return "", errors.New("需要参数 query")
				}
				a.Limit = clamp(a.Limit, 8, 20)
				res, err := s.Retrieve(ctx, store.KnowledgeRetrieveQuery{Search: store.KnowledgeSearchQuery{Text: a.Query, PerPage: a.Limit}})
				if err != nil {
					return "", fmt.Errorf("检索失败：%w", err)
				}
				if len(res.Hits) == 0 {
					return "没有找到相关内容。", nil
				}
				var b strings.Builder
				for i, h := range res.Hits {
					fmt.Fprintf(&b, "%d. 《%s》（%s）%s\n   %s\n   %s\n", i+1, h.Title, h.Kind, timeLabel(h.Position, h.SegmentID != ""), h.Snippet, link(h.SourceType, h.SourceID, startOf(h)))
				}
				return b.String(), nil
			},
		},
		{
			Name:        "get_source",
			Description: "读取一个来源（单集、上传或文档）的知识卡片：摘要、关键要点、金句与标签。",
			Schema: map[string]any{"type": "object", "required": []string{"source_type", "source_id"}, "properties": map[string]any{
				"source_type": map[string]any{"type": "string", "enum": []string{"episode", "upload", "document"}},
				"source_id":   map[string]any{"type": "string"},
			}},
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					SourceType string `json:"source_type"`
					SourceID   string `json:"source_id"`
				}
				if err := json.Unmarshal(raw, &a); err != nil || a.SourceID == "" {
					return "", errors.New("需要参数 source_type 与 source_id")
				}
				v, err := s.GetCurrentVersion(ctx, models.SourceType(a.SourceType), a.SourceID, store.KindKnowledgeCard)
				if err != nil {
					return "", errors.New("该来源还没有知识卡片")
				}
				var card provider.KnowledgeCard
				if err := json.Unmarshal([]byte(v.Payload), &card); err != nil {
					return "", errors.New("知识卡片数据损坏")
				}
				return renderCard(&card, link(a.SourceType, a.SourceID, 0)), nil
			},
		},
		{
			Name:        "list_snips",
			Description: "列出用户在收听时保存的片段（Snip），带转录原文、时间范围与来源。",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "最多返回条数，默认 10，上限 50"},
			}},
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Limit int `json:"limit"`
				}
				_ = json.Unmarshal(raw, &a) // 参数可省略
				list, err := s.ListLearningExcerpts(ctx, clamp(a.Limit, 10, 50))
				if err != nil {
					return "", fmt.Errorf("读取片段失败：%w", err)
				}
				if len(list) == 0 {
					return "还没有保存的片段。", nil
				}
				texts := map[string]map[string]string{}
				var b strings.Builder
				for i, e := range list {
					m, ok := texts[e.SnapshotID]
					if !ok {
						m = map[string]string{}
						if _, segs, _, err := s.SnapshotContent(ctx, e.SnapshotID); err == nil {
							for _, sg := range segs {
								m[sg.ID] = sg.Text
							}
						}
						texts[e.SnapshotID] = m
					}
					var parts []string
					for _, id := range e.SegmentIDs {
						if t := strings.TrimSpace(m[id]); t != "" {
							parts = append(parts, t)
						}
					}
					fmt.Fprintf(&b, "%d. 《%s》%s\n   %s\n   %s\n", i+1, e.SourceTitle, timeLabel(e.StartSeconds, true), strings.Join(parts, " "), link(string(e.SourceType), e.SourceID, e.StartSeconds))
				}
				return b.String(), nil
			},
		},
	}
}

func clamp(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

func startOf(h store.KnowledgeSearchHit) float64 {
	if h.SegmentID != "" {
		return h.Position
	}
	return 0
}

func timeLabel(sec float64, show bool) string {
	if !show {
		return ""
	}
	s := int(sec)
	return fmt.Sprintf("@ %d:%02d", s/60, s%60)
}

func renderCard(c *provider.KnowledgeCard, url string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n%s\n\n", c.Title, url)
	if c.Summary.Text != "" {
		fmt.Fprintf(&b, "## 摘要\n%s\n\n", c.Summary.Text)
	}
	if len(c.KeyPoints) > 0 {
		b.WriteString("## 关键要点（AI 归纳）\n")
		for _, k := range c.KeyPoints {
			line := "- " + k.Content
			if k.Description != "" {
				line += "：" + k.Description
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if len(c.Quotes) > 0 {
		b.WriteString("## 金句（逐字原文）\n")
		for _, q := range c.Quotes {
			b.WriteString("> " + q.Text + "\n")
		}
		b.WriteString("\n")
	}
	if len(c.Tags) > 0 {
		b.WriteString("标签：" + strings.Join(c.Tags, "、") + "\n")
	}
	return b.String()
}
