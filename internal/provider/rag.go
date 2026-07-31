package provider

import (
	"sort"
	"strings"
	"unicode"
)

// Chunk 是 transcript 的一个片段聚合，保留时间戳范围，作为 RAG 检索单元与引用来源。
type Chunk struct {
	Text  string
	Start float64
	End   float64
}

// BuildChunks 将逐句 segments 按 perChunk 句聚合成 chunk。
// 每个 chunk 记录拼接文本与首句 start、末句 end，供 LLM 引用与播放器跳转。
func BuildChunks(segments []Segment, perChunk int) []Chunk {
	if len(segments) == 0 {
		return nil
	}
	if perChunk < 1 {
		perChunk = 8
	}
	var chunks []Chunk
	for i := 0; i < len(segments); i += perChunk {
		end := i + perChunk
		if end > len(segments) {
			end = len(segments)
		}
		group := segments[i:end]
		var sb strings.Builder
		for _, s := range group {
			sb.WriteString(strings.TrimSpace(s.Text))
			sb.WriteString(" ")
		}
		chunks = append(chunks, Chunk{
			Text:  strings.TrimSpace(sb.String()),
			Start: group[0].Start,
			End:   group[len(group)-1].End,
		})
	}
	return chunks
}

// stopWords 中英文常见停用词，检索时忽略。
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "of": true,
	"to": true, "in": true, "on": true, "and": true, "or": true, "for": true,
	"what": true, "how": true, "why": true, "when": true, "who": true,
	"的": true, "了": true, "是": true, "在": true, "和": true, "与": true,
	"吗": true, "呢": true, "啊": true, "我": true, "你": true, "他": true,
	"什么": true, "怎么": true, "为什么": true, "哪": true, "哪些": true,
}

// tokenize 把问题切成关键词（小写、去标点、去停用词）。
// 英文按单词；中文连续段生成 bigram（"主权""财富"）+ 单字——
// bigram 让多字词（主权财富基金）命中更精准，单字保留作为兜底召回。
func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	var tokens []string
	var latin strings.Builder
	var cjkRun []rune

	flushLatin := func() {
		if latin.Len() > 0 {
			w := latin.String()
			if !stopWords[w] && len(w) > 1 {
				tokens = append(tokens, w)
			}
			latin.Reset()
		}
	}
	flushCJK := func() {
		// bigram：相邻两字组合，捕捉"主权""财富"等多字词语义
		for i := 0; i+1 < len(cjkRun); i++ {
			bg := string(cjkRun[i : i+2])
			if !stopWords[bg] {
				tokens = append(tokens, bg)
			}
		}
		// 单字兜底（过滤停用词如"的""了"）
		for _, r := range cjkRun {
			ch := string(r)
			if !stopWords[ch] {
				tokens = append(tokens, ch)
			}
		}
		cjkRun = cjkRun[:0]
	}

	for _, r := range s {
		if isCJK(r) {
			flushLatin()
			cjkRun = append(cjkRun, r)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			flushCJK()
			latin.WriteRune(r)
		} else {
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return tokens
}

func isCJK(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF
}

// Retrieve 按问题关键词在 chunks 中的命中数排序，取 topK。
// 无任何命中时，返回最前的 min(topK, len) 个 chunk 作为兜底（保证 LLM 总有上下文）。
// 返回的 chunks 保持按相关性降序；调用方按切片顺序作为 LLM 上下文，索引即 cited 引用。
func Retrieve(chunks []Chunk, question string, topK int) []Chunk {
	if len(chunks) == 0 {
		return nil
	}
	if topK < 1 || topK > len(chunks) {
		topK = len(chunks)
	}
	terms := tokenize(question)
	type scored struct {
		idx    int
		score  int
	}
	scoredList := make([]scored, len(chunks))
	for i, c := range chunks {
		low := strings.ToLower(c.Text)
		score := 0
		for _, t := range terms {
			score += strings.Count(low, t)
		}
		scoredList[i] = scored{idx: i, score: score}
	}
	// 稳定排序：分数降序，同分保持原顺序（稳定保证兜底行为可预测）
	sort.SliceStable(scoredList, func(i, j int) bool {
		return scoredList[i].score > scoredList[j].score
	})

	// 若最高分为 0（无命中），兜底取最前 topK 个
	if scoredList[0].score == 0 {
		out := make([]Chunk, 0, topK)
		for i := 0; i < topK; i++ {
			out = append(out, chunks[i])
		}
		return out
	}

	out := make([]Chunk, 0, topK)
	for i := 0; i < topK; i++ {
		out = append(out, chunks[scoredList[i].idx])
	}
	return out
}
