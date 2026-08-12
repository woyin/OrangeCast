package server

import (
	"strings"
	"testing"
)

func TestReadableHTMLExtractsTitleAndExcludesScript(t *testing.T) {
	title, content, err := readableHTML(`<html><head><title>研究页面</title><script>secret()</script></head><body><h1>正文标题</h1><p>可信正文</p></body></html>`)
	if err != nil || title != "研究页面" || content == "" || strings.Contains(content, "secret") {
		t.Fatalf("unexpected extracted page: title=%q content=%q err=%v", title, content, err)
	}
}
