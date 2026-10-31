// Package notesync 把知识卡片同步到外部笔记工具：Readwise（API）与 Obsidian（写入本地 vault 目录）。
package notesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/woyin/orangecast/internal/provider"
)

const (
	readwiseEndpoint = "https://readwise.io/api/v2/highlights/"
	batchSize        = 100
	maxTextRunes     = 8000 // Readwise 单条上限 8191
)

// Highlight 是一条要推送到 Readwise 的高光。
type Highlight struct {
	Text         string `json:"text"`
	Title        string `json:"title"`
	Author       string `json:"author,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	SourceType   string `json:"source_type"`
	Category     string `json:"category"`
	Note         string `json:"note,omitempty"`
	Location     int    `json:"location,omitempty"`
	LocationType string `json:"location_type,omitempty"`
}

// Highlights 把知识卡片转成 Readwise 高光。
// 金句是逐字原文，直接作为高光；关键要点是 AI 归纳，附上“AI 要点”备注，避免被当成原文。
// 每条高光的位置与链接都由 Citation 经程序解析得到，解析不出时间的条目仍保留但不带位置。
func Highlights(card *provider.KnowledgeCard, segments []provider.Segment, title, author, baseURL, sourceType, sourceID string) []Highlight {
	var out []Highlight
	link := func(citations []string) (string, int, bool) {
		for _, c := range citations {
			if start, _, ok := provider.ResolveCitationRange(c, segments); ok {
				u := fmt.Sprintf("%s/sources/%s/%s?t=%s#seg-%s", strings.TrimSuffix(baseURL, "/"), sourceType, sourceID, strconv.FormatFloat(start, 'f', 1, 64), c)
				return u, int(start), true
			}
		}
		return fmt.Sprintf("%s/sources/%s/%s", strings.TrimSuffix(baseURL, "/"), sourceType, sourceID), 0, false
	}
	add := func(text, note string, citations []string) {
		text = truncate(strings.TrimSpace(text))
		if text == "" {
			return
		}
		u, loc, ok := link(citations)
		h := Highlight{Text: text, Title: title, Author: author, SourceURL: u, SourceType: "orangecast", Category: "podcasts", Note: note}
		if ok {
			h.Location, h.LocationType = loc, "time_offset"
		}
		out = append(out, h)
	}
	for _, q := range card.Quotes {
		add(q.Text, "", q.Citations)
	}
	for _, k := range card.KeyPoints {
		text := k.Content
		if k.Description != "" {
			text += "：" + k.Description
		}
		add(text, "AI 要点（非原文）", k.Citations)
	}
	return out
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxTextRunes {
		return s
	}
	return string([]rune(s)[:maxTextRunes])
}

// Readwise 通过 Readwise API v2 推送高光。
type Readwise struct {
	Token    string
	Endpoint string       // 空则用官方地址（测试可替换）
	Client   *http.Client // 空则用 30 秒超时的默认客户端
}

// Push 分批推送，返回已提交的条数。Readwise 按标题+作者+文本去重，重复推送是安全的。
func (r *Readwise) Push(ctx context.Context, hs []Highlight) (int, error) {
	if strings.TrimSpace(r.Token) == "" {
		return 0, errors.New("未配置 READWISE_TOKEN")
	}
	endpoint, client := r.Endpoint, r.Client
	if endpoint == "" {
		endpoint = readwiseEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	sent := 0
	for start := 0; start < len(hs); start += batchSize {
		end := min(start+batchSize, len(hs))
		body, _ := json.Marshal(map[string]any{"highlights": hs[start:end]})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		req.Header.Set("Authorization", "Token "+r.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return sent, fmt.Errorf("请求 Readwise 失败：%w", err)
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return sent, fmt.Errorf("Readwise 返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(msg)))
		}
		sent += end - start
	}
	return sent, nil
}

// WriteObsidian 把 Markdown 写入 vault 目录下的 <title>.md，返回最终路径。
// 文件名会清理路径分隔符等字符，写入位置始终在 dir 之内；同名文件被覆盖（同一来源重复导出即更新）。
func WriteObsidian(dir, title, markdown string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("未配置 OBSIDIAN_EXPORT_DIR")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := safeName(title) + ".md"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(markdown), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// safeName 去掉文件系统与 Obsidian 链接中不安全的字符，并限制长度。
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '#', '^', '[', ']', 0:
			b.WriteRune('-')
		default:
			if r >= ' ' {
				b.WriteRune(r)
			}
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	if r := []rune(out); len(r) > 120 {
		out = string(r[:120])
	}
	if out == "" {
		return "untitled"
	}
	return out
}
