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
	state := readableHTMLState{}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			return "", "", z.Err()
		}
		state.consume(tt, z.Token())
	}
	content := strings.TrimSpace(strings.Join(state.text, "\n\n"))
	if content == "" {
		return "", "", fmt.Errorf("网页没有可保存文本")
	}
	return state.title, content, nil
}

type readableHTMLState struct {
	title   string
	inTitle bool
	skip    int
	text    []string
}

func (s *readableHTMLState) consume(tokenType html.TokenType, token html.Token) {
	switch tokenType {
	case html.StartTagToken:
		if token.Data == "title" {
			s.inTitle = true
		}
		if token.Data == "script" || token.Data == "style" {
			s.skip++
		}
	case html.EndTagToken:
		if token.Data == "title" {
			s.inTitle = false
		}
		if (token.Data == "script" || token.Data == "style") && s.skip > 0 {
			s.skip--
		}
	case html.TextToken:
		if s.skip > 0 {
			return
		}
		text := strings.TrimSpace(token.Data)
		if text == "" {
			return
		}
		if s.inTitle && s.title == "" {
			s.title = text
		}
		s.text = append(s.text, text)
	}
}
