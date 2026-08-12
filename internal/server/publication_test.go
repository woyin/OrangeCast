package server

import (
	"strings"
	"testing"
)

func TestWechatRichTextEscapesRevisionHTMLAndRendersMarkdown(t *testing.T) {
	rich := wechatRichText("# 标题\n\n一段 <script>alert(1)</script>\n- 第一项\n- 第二项")
	for _, want := range []string{"<h1>标题</h1>", "&lt;script&gt;alert(1)&lt;/script&gt;", "<ul><li>第一项</li><li>第二项</li></ul>"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich text should contain %q: %s", want, rich)
		}
	}
	if strings.Contains(rich, "<script>") {
		t.Fatalf("revision HTML must not be trusted: %s", rich)
	}
}

func TestWechatRichTextCoversHeadingsAndEmptySourceList(t *testing.T) {
	rich := wechatRichText("## 二级\n### 三级\n\n正文")
	for _, want := range []string{"<h2>二级</h2>", "<h3>三级</h3>", "<p>正文</p>"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich text should contain %q: %s", want, rich)
		}
	}
	if got := markdownSourceList(nil); got != "- 本文未使用外部来源。\n" {
		t.Fatalf("empty source list should be explicit: %q", got)
	}
}
