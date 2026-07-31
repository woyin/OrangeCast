package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 默认重试策略（第 7 题最小实现：指数退避，最多 3 次）。
const (
	maxRetries        = 3
	baseBackoff       = 2 * time.Second
	maxBackoff        = 16 * time.Second
	requestTimeout    = 5 * time.Minute // 转录/分析可能较慢
)

// httpClient 共享 HTTP 客户端。
var httpClient = &http.Client{Timeout: requestTimeout}

// doWithRetry 对 request 执行 429/5xx 指数退避重试。
// 429 优先读 Retry-After 头。
func doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req.Clone(ctx))
		if err == nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil // 成功或非可重试错误
		}
		// 读取错误响应体后关闭（每次循环 body 不能复用）
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if attempt == maxRetries {
			break
		}
		// 计算退避：优先 Retry-After，否则指数退避
		backoff := time.Duration(math.Pow(2, float64(attempt))) * baseBackoff
		if resp != nil && resp.Header.Get("Retry-After") != "" {
			if ra, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil {
				backoff = time.Duration(ra) * time.Second
			}
		}
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("重试耗尽")
	}
	return nil, fmt.Errorf("请求失败（已重试 %d 次）: %w", maxRetries, lastErr)
}

// uploadFileAsMultipart 将本地文件以 multipart file 字段上传（Groq 转录要求）。
// 返回响应体。
func uploadFileAsMultipart(ctx context.Context, url, apiKey, fieldName, filePath string, extraFields map[string]string) ([]byte, int, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for k, v := range extraFields {
		if err := writer.WriteField(k, v); err != nil {
			return nil, 0, err
		}
	}
	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err != nil {
		return nil, 0, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, 0, err
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := doWithRetry(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

// postJSON 发送 JSON 请求并返回响应体（用于 chat completions）。
func postJSON(ctx context.Context, url, apiKey string, payload any) ([]byte, int, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := doWithRetry(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

// codeBlockRe 匹配 markdown 代码块包裹（```json ... ``` 或 ``` ... ```）。
var codeBlockRe = regexp.MustCompile("(?s)^```\\w*\\s*\n(.*?)\n```\\s*$")

// parseJSONLoose 容错解析 LLM 输出的 JSON（第 10 题：剥离 markdown 包裹 + 尾部垃圾）。
// Groq 的 json_object 不强制 schema，输出可能带代码块标记或额外字段。
// 这里忽略未知字段（容错），由 struct 字段映射 + 后续业务校验保证所需字段存在。
func parseJSONLoose(raw string, out any) error {
	s := strings.TrimSpace(raw)
	// 1. 剥离 ```json ... ``` 包裹
	if m := codeBlockRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	// 2. 若仍含代码块标记（行内残留），去掉首尾到第一个 { 和最后一个 }
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}
	if idx := strings.LastIndex(s, "}"); idx >= 0 && idx < len(s)-1 {
		s = s[:idx+1]
	}
	dec := json.NewDecoder(strings.NewReader(s))
	return dec.Decode(out) // 默认忽略未知字段
}
