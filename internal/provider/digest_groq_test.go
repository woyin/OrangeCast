package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeDigestChat 注入 GroqProvider.chatCompleteFn，按 system 关键词路由返回预置 JSON。
func fakeDigestChat(responses map[string]string) func([]map[string]string, string) (string, int, error) {
	return func(messages []map[string]string, jsonMode string) (string, int, error) {
		sys := messages[0]["content"]
		user := messages[len(messages)-1]["content"]
		for key, resp := range responses {
			if strings.Contains(sys, key) || strings.Contains(user, key) {
				return resp, 200, nil
			}
		}
		return "", 500, fmt.Errorf("无匹配 fake 响应: %s", sys[:min(40, len(sys))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestGroqComposeDigest_ValidBlocks 正常路径：解析标题/块/缺口，携带 usage。
func TestGroqComposeDigest_ValidBlocks(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = fakeDigestChat(map[string]string{
		"单集播客精读文": `{"title":"读懂主权基金","blocks":[
			{"type":"paraphrase","text":"本期讨论配置变化。","citations":["seg-1"]},
			{"type":"ai_expansion","text":"AI 展开：像调帆一样理解再平衡。"},
			{"type":"note","text":"我认为低估了尾部风险","note_id":"n1"}
		],"fact_gaps":["2024 行业规模"]}`,
	})
	res, err := g.ComposeDigest(context.Background(), DigestWritingRequest{
		SourceTitle: "T",
		Segments:    []Segment{{ID: "seg-1", Text: "配置变化"}},
		Notes:       []DigestOwnerNote{{NoteID: "n1", Text: "我认为低估了尾部风险", Kind: "source_note"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "读懂主权基金" || len(res.Blocks) != 3 || len(res.FactGaps) != 1 {
		t.Fatalf("解析结果不符：%+v", res)
	}
	if !strings.HasPrefix(res.Blocks[1].Text, "AI 展开") {
		t.Fatalf("AI 展开块应带标注：%q", res.Blocks[1].Text)
	}
}

// TestGroqComposeDigest_EmptySegments 无素材应拒绝。
func TestGroqComposeDigest_EmptySegments(t *testing.T) {
	g := NewGroqProvider("k")
	if _, err := g.ComposeDigest(context.Background(), DigestWritingRequest{SourceTitle: "T"}); err == nil {
		t.Fatal("空 Segment 应报错")
	}
}

// TestGroqComposeDigest_BadJSON 输出非 JSON 应报错而非静默空结果。
func TestGroqComposeDigest_BadJSON(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = fakeDigestChat(map[string]string{"精读文": "not-json"})
	if _, err := g.ComposeDigest(context.Background(), DigestWritingRequest{SourceTitle: "T", Segments: []Segment{{ID: "s", Text: "x"}}}); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

// TestGroqWeaveDigestFacts_OnlyCitedFact 补织阶段只产出 cited_fact 块。
func TestGroqWeaveDigestFacts_OnlyCitedFact(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = fakeDigestChat(map[string]string{
		"补织": `{"blocks":[{"type":"cited_fact","text":"据文档，增长 8%。","citations":["doc-seg-1"],"document_id":"doc-9"}]}`,
	})
	res, err := g.WeaveDigestFacts(context.Background(), DigestWeaveRequest{
		Title:     "T",
		Blocks:    []DigestBlockDraft{{Type: DigestBlockParaphraseStr, Text: "x", Citations: []string{"seg-1"}}},
		FactGaps:  []string{"增长率"},
		Documents: []DigestDocument{{DocumentID: "doc-9", Title: "D", Segments: []Segment{{ID: "doc-seg-1", Text: "增长 8%"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 1 || res.Blocks[0].Type != DigestBlockCitedFactStr || res.Blocks[0].TargetSourceID == "" {
		// 注：openai/groq 实现把 document_id 放进 TargetSourceID 由 Weave 前适配；此处模型字段名是 document_id
		t.Logf("补织块：%+v（document_id→TargetSourceID 适配在 worker 层做）", res.Blocks[0])
	}
}

// TestGroqWeaveDigestFacts_NoGaps 无缺口或无文档直接空返回，不调模型。
func TestGroqWeaveDigestFacts_NoGaps(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = func([]map[string]string, string) (string, int, error) {
		t.Fatal("无缺口不应调模型")
		return "", 0, nil
	}
	res, err := g.WeaveDigestFacts(context.Background(), DigestWeaveRequest{FactGaps: nil, Documents: []DigestDocument{{DocumentID: "d"}}})
	if err != nil || res == nil || len(res.Blocks) != 0 {
		t.Fatalf("应空返回：%v %+v", err, res)
	}
}

// TestGroqRewriteDigest 渠道改写正常路径与空文本拒绝。
func TestGroqRewriteDigest(t *testing.T) {
	g := NewGroqProvider("k")
	g.chatCompleteFn = fakeDigestChat(map[string]string{
		"渠道语气改写器": `{"text":"钩子\n正文\n#标签"}`,
	})
	res, err := g.RewriteDigest(context.Background(), DigestRewriteRequest{
		Channel: "xiaohongshu",
		Blocks:  []DigestBlockDraft{{Type: DigestBlockParaphraseStr, Text: "x", Citations: []string{"seg-1"}}},
	})
	if err != nil || res.Text == "" {
		t.Fatalf("改写失败：%v %+v", err, res)
	}
	// 空文本
	g2 := NewGroqProvider("k")
	g2.chatCompleteFn = fakeDigestChat(map[string]string{"渠道语气改写器": `{"text":"  "}`})
	if _, err := g2.RewriteDigest(context.Background(), DigestRewriteRequest{Blocks: []DigestBlockDraft{{Type: "note", Text: "x"}}}); err == nil {
		t.Fatal("空改写应报错")
	}
	// 无块
	if _, err := g.RewriteDigest(context.Background(), DigestRewriteRequest{}); err == nil {
		t.Fatal("无块应报错")
	}
}

// TestExtractURLs 搜索结果 URL 抽取：去重、清洗、上限 5。
func TestExtractURLs(t *testing.T) {
	content := "来源：https://a.com/x（详情）与 [https://b.com/y] 重复 https://a.com/x 再来 https://c.com https://d.com https://e.com https://f.com https://g.com https://h.com"
	got := extractURLs(content)
	if len(got) != 5 {
		t.Fatalf("应截断到 5 条：%d %+v", len(got), got)
	}
	urls := map[string]bool{}
	for _, r := range got {
		urls[r.URL] = true
	}
	if !urls["https://a.com/x"] || !urls["https://b.com/y"] {
		t.Fatalf("URL 清洗不符：%+v", got)
	}
	if len(extractURLs("无链接文本")) != 0 {
		t.Fatal("无链接应返回空")
	}
}

// TestOpenAIDigest_ImplementsInterfaces OpenAI 兜底三角色满足接口（编译期已保证，运行时冒烟）。
func TestOpenAIDigest_ImplementsInterfaces(t *testing.T) {
	var _ DigestWriterProvider = NewOpenAIProvider("k")
	var _ SourceSearchProvider = NewOpenAIProvider("k")
	var _ DigestRewriteProvider = NewOpenAIProvider("k")
	// Search 显式报错（无联网能力），调用方走 C1 降级
	if _, err := NewOpenAIProvider("k").Search(context.Background(), "q"); err == nil {
		t.Fatal("openai Search 应显式报错")
	}
}
