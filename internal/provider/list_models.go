package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ListModels 调用 OpenAI 兼容端点的 GET {base}/models，返回排序去重后的模型 ID。
// baseURL 为空时按 provider 名（groq/openai）取默认地址。
func ListModels(ctx context.Context, providerName, baseURL, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("未配置 API key")
	}
	if baseURL == "" {
		switch providerName {
		case "groq":
			baseURL = groqBaseURL
		case "openai":
			baseURL = openaiBaseURL
		default:
			return nil, fmt.Errorf("未知 provider: %s", providerName)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 /models 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/models 返回 HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析 /models 响应: %w", err)
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" && !seen[m.ID] {
			seen[m.ID] = true
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Credentials 返回 provider 当前生效的 key 与 baseURL（供设置页拉取模型列表）。
func (sel *Selector) Credentials(providerName string) (key, baseURL string) {
	switch providerName {
	case "groq":
		return sel.groqAPIKey, sel.groqBaseURL
	case "openai":
		return sel.openaiAPIKey, sel.openaiBaseURL
	}
	return "", ""
}
