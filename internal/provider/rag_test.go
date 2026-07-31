package provider

import (
	"strings"
	"testing"
)

func TestBuildChunks_Empty(t *testing.T) {
	if got := BuildChunks(nil, 8); got != nil {
		t.Error("空 segments 应返回 nil")
	}
}

func TestBuildChunks_Grouping(t *testing.T) {
	segs := []Segment{
		{Start: 0, End: 5, Text: "a"},
		{Start: 5, End: 10, Text: "b"},
		{Start: 10, End: 15, Text: "c"},
		{Start: 15, End: 20, Text: "d"},
		{Start: 20, End: 25, Text: "e"},
	}
	// perChunk=2 → 3 个 chunk（2+2+1，不能整除）
	chunks := BuildChunks(segs, 2)
	if len(chunks) != 3 {
		t.Fatalf("应有 3 个 chunk，实际 %d", len(chunks))
	}
	if chunks[0].Text != "a b" {
		t.Errorf("chunk0 文本=%q want 'a b'", chunks[0].Text)
	}
	if chunks[0].Start != 0 || chunks[0].End != 10 {
		t.Errorf("chunk0 时间范围错误: %.0f-%.0f", chunks[0].Start, chunks[0].End)
	}
	// 最后一个 chunk（只剩 1 句）
	if chunks[2].Text != "e" || chunks[2].Start != 20 || chunks[2].End != 25 {
		t.Errorf("chunk2（尾部）错误: text=%q %.0f-%.0f", chunks[2].Text, chunks[2].Start, chunks[2].End)
	}
}

func TestBuildChunks_PerChunkClamped(t *testing.T) {
	segs := []Segment{{Text: "x"}}
	// perChunk=0 应被钳制为默认值，不崩溃
	chunks := BuildChunks(segs, 0)
	if len(chunks) != 1 {
		t.Fatalf("应 1 个 chunk，实际 %d", len(chunks))
	}
}

func TestRetrieve_KeywordRanking(t *testing.T) {
	chunks := []Chunk{
		{Text: "介绍挪威的石油财富与主权基金", Start: 0, End: 10},
		{Text: "日本三文鱼的市场营销策略", Start: 10, End: 20},
		{Text: "挪威的社会信任与政府干预经济", Start: 20, End: 30},
	}
	// 问题含"挪威"，应把含挪威的 chunk 排前面
	got := Retrieve(chunks, "挪威的经济", 2)
	if len(got) != 2 {
		t.Fatalf("应返回 top2，实际 %d", len(got))
	}
	for _, c := range got {
		if !strings.Contains(c.Text, "挪威") {
			t.Errorf("检索结果应含关键词'挪威'，实际 %q", c.Text)
		}
	}
}

func TestRetrieve_TopKClamp(t *testing.T) {
	chunks := []Chunk{{Text: "a"}, {Text: "b"}, {Text: "c"}}
	got := Retrieve(chunks, "x", 10) // topK > len
	if len(got) != 3 {
		t.Errorf("topK 超过长度应返回全部，实际 %d", len(got))
	}
}

func TestRetrieve_NoHit_FallbackFront(t *testing.T) {
	// 无任何关键词命中时，应兜底返回最前的 chunk（保证 LLM 有上下文）
	chunks := []Chunk{
		{Text: "first segment here", Start: 0, End: 5},
		{Text: "second segment", Start: 5, End: 10},
	}
	got := Retrieve(chunks, "完全不相关的查询词", 1)
	if len(got) != 1 {
		t.Fatalf("应返回 1 个兜底，实际 %d", len(got))
	}
	if got[0].Start != 0 {
		t.Error("无命中时应兜底返回最前 chunk")
	}
}

func TestRetrieve_Empty(t *testing.T) {
	if got := Retrieve(nil, "q", 5); got != nil {
		t.Error("空 chunks 应返回 nil")
	}
}

func TestTokenize_StopWordsFiltered(t *testing.T) {
	toks := tokenize("What is the Norwegian oil fund")
	for _, tk := range toks {
		if stopWords[tk] {
			t.Errorf("停用词 %q 不应出现在 token 中", tk)
		}
	}
	if len(toks) == 0 {
		t.Error("应有有效 token")
	}
}

func TestTokenize_CJK_Bigrams(t *testing.T) {
	toks := tokenize("主权财富基金")
	joined := strings.Join(toks, ",")
	// bigram 应出现：主权、权财、财富、富基、基金
	for _, bg := range []string{"主权", "财富", "基金"} {
		if !strings.Contains(joined, bg) {
			t.Errorf("中文 bigram %q 应出现在 token 中，实际 %s", bg, joined)
		}
	}
	// "的"等停用词应被过滤（单字层）
	if strings.Contains(joined, "的") {
		t.Error("中文停用词'的'应被过滤")
	}
}

func TestRetrieve_MultiCharChineseTerm(t *testing.T) {
	// 验证 bigram 让多字中文查询命中更精准（回归"主权财富基金"检索问题）
	chunks := []Chunk{
		{Text: "节目的开头介绍和欢迎语，无实质内容", Start: 0, End: 10},
		{Text: "挪威的主权财富基金规模达到两万亿美元", Start: 10, End: 20},
		{Text: "日本三文鱼的营销故事", Start: 20, End: 30},
	}
	got := Retrieve(chunks, "主权财富基金有多少", 1)
	if len(got) != 1 {
		t.Fatalf("应返回 top1，实际 %d", len(got))
	}
	if !strings.Contains(got[0].Text, "主权财富基金") {
		t.Errorf("bigram 应让含'主权财富基金'的 chunk 排第一，实际命中 %q", got[0].Text)
	}
}
