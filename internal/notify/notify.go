// Package notify 在来源处理完成后向外部通知渠道推送一条消息：通用 Webhook 与 Telegram。
// 通知是尽力而为：失败只记录，不影响处理流水线。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Event 是一次“来源处理完成”的通知内容。
type Event struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

// Notifier 按已配置的渠道推送事件。零值（没有任何渠道）是合法的，Enabled 为 false。
type Notifier struct {
	WebhookURL       string
	TelegramToken    string
	TelegramChatID   string
	TelegramEndpoint string       // 空则用官方地址（测试可替换）
	Client           *http.Client // 空则用 15 秒超时的默认客户端
}

const (
	telegramEndpoint = "https://api.telegram.org"
	summaryRunes     = 280
)

// Enabled 报告是否至少配置了一个渠道。
func (n *Notifier) Enabled() bool {
	return n != nil && (n.WebhookURL != "" || (n.TelegramToken != "" && n.TelegramChatID != ""))
}

// Send 向所有已配置的渠道推送；任一渠道失败不影响其余渠道，返回合并后的错误。
func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if !n.Enabled() {
		return nil
	}
	ev.Summary = truncate(strings.TrimSpace(ev.Summary), summaryRunes)
	var errs []string
	if n.WebhookURL != "" {
		body, _ := json.Marshal(map[string]string{"event": "source.processed", "title": ev.Title, "summary": ev.Summary, "url": ev.URL})
		if err := n.post(ctx, n.WebhookURL, body); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	if n.TelegramToken != "" && n.TelegramChatID != "" {
		base := n.TelegramEndpoint
		if base == "" {
			base = telegramEndpoint
		}
		text := "✅ 处理完成：" + ev.Title
		if ev.Summary != "" {
			text += "\n\n" + ev.Summary
		}
		text += "\n\n" + ev.URL
		body, _ := json.Marshal(map[string]any{"chat_id": n.TelegramChatID, "text": text, "disable_web_page_preview": true})
		if err := n.post(ctx, base+"/bot"+n.TelegramToken+"/sendMessage", body); err != nil {
			// 错误文本里会带请求 URL（含 bot token），统一抹掉。
			errs = append(errs, "telegram: "+strings.ReplaceAll(err.Error(), n.TelegramToken, "***"))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (n *Notifier) post(ctx context.Context, url string, body []byte) error {
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
