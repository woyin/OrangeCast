package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/safehttp"
	"golang.org/x/net/html"
)

const maxDocumentSize = 4 << 20

func fetchWebDocument(ctx context.Context, rawURL string) (string, string, error) {
	if err := safehttp.ValidateURL(rawURL); err != nil {
		return "", "", err
	}
	client := safehttp.NewClient(5, maxDocumentSize, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("抓取网页: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("网页返回 HTTP %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") && !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/plain") {
		return "", "", fmt.Errorf("不支持的内容类型")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentSize+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(body)) > maxDocumentSize {
		return "", "", fmt.Errorf("网页正文超过 %d 字节上限", maxDocumentSize)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/plain") {
		return rawURL, strings.TrimSpace(string(body)), nil
	}
	return readableHTML(string(body))
}

func readableHTML(raw string) (string, string, error) {
	z := html.NewTokenizer(strings.NewReader(raw))
	title := ""
	inTitle := false
	var text []string
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			return "", "", z.Err()
		}
		tok := z.Token()
		if tt == html.StartTagToken {
			if tok.Data == "title" {
				inTitle = true
			}
			if tok.Data == "script" || tok.Data == "style" {
				skip++
			}
			continue
		}
		if tt == html.EndTagToken {
			if tok.Data == "title" {
				inTitle = false
			}
			if (tok.Data == "script" || tok.Data == "style") && skip > 0 {
				skip--
			}
			continue
		}
		if tt == html.TextToken && skip == 0 {
			s := strings.TrimSpace(tok.Data)
			if s != "" {
				if inTitle && title == "" {
					title = s
				}
				text = append(text, s)
			}
		}
	}
	content := strings.TrimSpace(strings.Join(text, "\n\n"))
	if content == "" {
		return "", "", fmt.Errorf("网页没有可保存文本")
	}
	return title, content, nil
}
