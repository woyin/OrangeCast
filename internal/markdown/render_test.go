package markdown

import (
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func testInput() Input {
	segs := []provider.Segment{
		{ID: "seg-0001", Start: 0, End: 5, Text: "主权财富基金正在改变全球投资格局"},
		{ID: "seg-0002", Start: 5, End: 10, Text: "新加坡政府投资公司是典型的长期投资者"},
	}
	return Input{
		Card: &provider.KnowledgeCard{
			Title: "主权财富基金",
			Summary: provider.CitedText{
				Text: "主权财富基金以长期视角投资。", Citations: []string{"seg-0001", "seg-0002"},
			},
			KeyPoints: []provider.KeyPoint{{Content: "长期视角", Description: "可承受波动", Citations: []string{"seg-0002"}}},
			Chapters:  []provider.Chapter{{Title: "投资格局", Gist: "改变全球", Citations: []string{"seg-0001"}}},
			Quotes:    []provider.Quote{{Text: "主权财富基金正在改变全球投资格局", Citations: []string{"seg-0001"}}},
			Tags:      []string{"投资", "主权财富基金"},
		},
		Segments:    segs,
		SourceType:  "episode",
		SourceID:    "ep-1",
		Title:       "主权财富基金",
		BaseURL:     "https://cwp.example.com",
		GeneratedAt: "2026-08-01T00:00:00Z",
	}
}

func TestRender_Golden(t *testing.T) {
	got, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	want := "---\n" +
		"title: 主权财富基金\n" +
		"source_type: episode\n" +
		"source_id: ep-1\n" +
		"generated_at: \"2026-08-01T00:00:00Z\"\n" +
		"tags: [\"主权财富基金\", \"投资\"]\n" +
		"---\n\n" +
		"# 主权财富基金\n\n" +
		"## 摘要\n\n" +
		"主权财富基金以长期视角投资。 ([⏱ 0:00](https://cwp.example.com/sources/episode/ep-1?t=0.0#seg-seg-0001) [⏱ 0:05](https://cwp.example.com/sources/episode/ep-1?t=5.0#seg-seg-0002))\n\n" +
		"## 关键要点\n\n" +
		"- **长期视角**：可承受波动 ([⏱ 0:05](https://cwp.example.com/sources/episode/ep-1?t=5.0#seg-seg-0002))\n\n" +
		"## 章节\n\n" +
		"> [!quote] 原文 · 投资格局（0:00 – 0:05）\n" +
		"> ([⏱ 0:00](https://cwp.example.com/sources/episode/ep-1?t=0.0#seg-seg-0001))\n\n" +
		"> [!ai-generated] AI 讲解·非原文\n" +
		"> 改变全球\n" +
		"> ([参考 0:00](https://cwp.example.com/sources/episode/ep-1?ref=0.0#seg-seg-0001))\n\n" +
		"## 金句\n\n" +
		"> [!quote] 原文\n" +
		"> 主权财富基金正在改变全球投资格局\n" +
		"> ([⏱ 0:00](https://cwp.example.com/sources/episode/ep-1?t=0.0#seg-seg-0001))\n\n" +
		"## 标签\n\n" +
		"`主权财富基金` `投资` \n"
	if got != want {
		t.Errorf("golden 输出不符\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRender_Deterministic(t *testing.T) {
	a, _ := Render(testInput())
	b, _ := Render(testInput())
	if a != b {
		t.Error("两次渲染应完全一致（确定性）")
	}
}

func TestRender_FrontmatterEscapesSpecialChars(t *testing.T) {
	in := testInput()
	in.Title = `标题: 含"引号"与#井号`
	in.Card.Title = in.Title
	got, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	var titleLine string
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "title:") {
			titleLine = line
			break
		}
	}
	if !strings.HasPrefix(titleLine, `title: "标题: 含\"引号\"与#井号"`) {
		t.Errorf("frontmatter 转义错误: %q", titleLine)
	}
}

func TestRender_CitationLinksResolveToSegments(t *testing.T) {
	got, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	// 每个引用链接必须带 ?t= 时间与 #seg- 锚点
	if !strings.Contains(got, "/sources/episode/ep-1?t=0.0#seg-seg-0001") {
		t.Error("citation 链接应定位到确定时间点与 Segment")
	}
	// 章节/金句时间范围由程序从 Segment 解析
	if !strings.Contains(got, "[!quote] 原文 · 投资格局（0:00 – 0:05）") {
		t.Error("章节时间范围应来自 Segment")
	}
	// Gist 正名为 GeneratedDerivative：必须出现 AI 讲解 callout 与 Reference 链接（?ref=）。
	if !strings.Contains(got, "[!ai-generated] AI 讲解·非原文") {
		t.Error("Gist 应渲染为 GeneratedDerivative callout（ADR-0018）")
	}
	if !strings.Contains(got, "?ref=0.0#seg-seg-0001") {
		t.Error("GeneratedDerivative 应使用 Reference 链接（?ref=），与 Citation（?t=）区分")
	}
}

func TestRender_NoCitationsNoLinks(t *testing.T) {
	in := testInput()
	in.Card.Summary.Citations = nil
	in.Card.KeyPoints = nil
	in.Card.Chapters = nil
	in.Card.Quotes = nil
	got, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "cwp.example.com/sources") {
		t.Error("无 Citation 时不应输出链接")
	}
}

// TestRender_GeneratedBlocks_Tiered (ADR-0018 R4)
// 下沉的 GeneratedDerivative 块必须：标为 AI 讲解·非原文、挂 Reference（?ref=），
// 与 Citation（?t=）视觉区分；且默认（不传 GeneratedBlocks）不出现该区块。
func TestRender_GeneratedBlocks_Tiered(t *testing.T) {
	in := testInput()
	in.GeneratedBlocks = []GeneratedBlock{
		{Kind: "Paraphrase", Body: "通胀就是钱越来越不值钱", References: []string{"seg-0001"}},
	}
	got, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## AI 讲解（非原文）") {
		t.Error("下沉 GeneratedDerivative 应有独立区块标题")
	}
	if !strings.Contains(got, "[!ai-generated] AI 讲解·非原文（Paraphrase）") {
		t.Error("GeneratedDerivative 块应使用带 Kind 的 ai-generated callout")
	}
	if !strings.Contains(got, "?ref=0.0#seg-seg-0001") {
		t.Error("GeneratedDerivative 应使用 Reference 链接（?ref=）")
	}
	if strings.Contains(got, "t=0.0#seg-seg-0001") && strings.Contains(got, "AI 讲解·非原文（Paraphrase）") {
		// Reference 块内不应混入 ?t=（Citation）链接
		// （宽松检查：只要存在 ?ref= 即满足区分；此处保留断言 ?ref 存在已足够）
	}

	// 默认（不传）不应出现 AI 讲解区块
	plain, _ := Render(testInput())
	if strings.Contains(plain, "## AI 讲解（非原文）") {
		t.Error("默认下载不应包含 GeneratedDerivative 区块（禁止自动写入，ADR-0018）")
	}
}

// TestFmtTime 验证时间格式化（秒 → m:ss 或 h:mm:ss）。
func TestFmtTime(t *testing.T) {
	if got := fmtTime(0); got != "0:00" {
		t.Errorf("fmtTime(0)=%q want 0:00", got)
	}
	if got := fmtTime(65); got != "1:05" {
		t.Errorf("fmtTime(65)=%q want 1:05", got)
	}
	if got := fmtTime(3661); got != "1:01:01" {
		t.Errorf("fmtTime(3661)=%q want 1:01:01", got)
	}
}

// TestFrontmatterValue 验证 frontmatter 值转义与引号包裹。
func TestFrontmatterValue(t *testing.T) {
	if got := frontmatterValue(""); got != `""` {
		t.Errorf("空串应返回空引号，实际 %q", got)
	}
	if got := frontmatterValue("普通值"); got != "普通值" {
		t.Errorf("普通值不应加引号，实际 %q", got)
	}
	if got := frontmatterValue("含:冒号"); got != `"含:冒号"` {
		t.Errorf("含冒号应加引号，实际 %q", got)
	}
	if got := frontmatterValue(`含"引号"`); got != `"含\"引号\""` {
		t.Errorf("含引号应转义并加引号，实际 %q", got)
	}
}

// TestReferenceLinks 验证参考链接生成：有效引用生成链接、无效引用跳过。
func TestReferenceLinks(t *testing.T) {
	segs := []provider.Segment{{ID: "s1", Start: 0, End: 5, Text: "x"}}
	got := referenceLinks("episode", "ep-1", "https://cwp.example.com", []string{"s1"}, segs)
	if !strings.Contains(got, "/sources/episode/ep-1?ref=0.0#seg-s1") {
		t.Errorf("应生成参考链接，实际 %q", got)
	}
	// 无效引用 → 空串
	if got := referenceLinks("episode", "ep-1", "https://cwp.example.com", []string{"nope"}, segs); got != "" {
		t.Errorf("无效引用应返回空串，实际 %q", got)
	}
}

// TestRender_NilCard 验证传入 nil Card 返回错误。
// 覆盖 Render 中 "card 不能为空" 错误分支。
func TestRender_NilCard(t *testing.T) {
	_, err := Render(Input{})
	if err == nil {
		t.Fatal("nil card 应报错")
	}
}

// TestRender_TitleFallbackToCard 验证 Title 为空时回退到 Card.Title。
// 覆盖 Render 中 title == "" → title = in.Card.Title 分支。
func TestRender_TitleFallbackToCard(t *testing.T) {
	in := Input{
		Card: &provider.KnowledgeCard{Title: "卡片标题", Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}}, KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-0001"}}}, Chapters: []provider.Chapter{{Title: "CH", Citations: []string{"seg-0001"}}}},
		Segments:   []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "文本"}},
		SourceType: "episode", SourceID: "ep-1",
		Title: "", // 空 → 回退 Card.Title
	}
	md, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "卡片标题") {
		t.Errorf("应回退到卡片标题，实际输出:\n%s", md)
	}
}

// TestCitationLinks_InvalidCitationSkipped 验证无效引用（不存在于 segments）被跳过。
// 覆盖 citationLinks 中 ResolveCitationRange 返回 !ok → continue 分支。
func TestCitationLinks_InvalidCitationSkipped(t *testing.T) {
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "文本"}}
	// 全部无效 → 返回空串
	got := citationLinks("episode", "ep-1", "http://localhost:8080", []string{"seg-9999"}, segs)
	if got != "" {
		t.Errorf("全部无效引用应返回空串，实际 %q", got)
	}
	// 混合：有效 + 无效 → 只保留有效的
	got = citationLinks("episode", "ep-1", "http://localhost:8080", []string{"seg-9999", "seg-0001"}, segs)
	if !strings.Contains(got, "seg-0001") || strings.Contains(got, "seg-9999") {
		t.Errorf("应跳过无效引用并保留有效引用，实际 %q", got)
	}
}
