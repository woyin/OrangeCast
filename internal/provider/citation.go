package provider

import (
	"fmt"
	"strings"
)

// TranscriptPayload 转录版本的持久化载荷（存入 artifact_versions.payload）。
type TranscriptPayload struct {
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

// ErrNoValidCitations 表示卡片内容无法满足证据契约（ADR-0008）：
// 核心项缺失或全部 Citation 无效，必须拒绝保存并重试，不能降级为无证据内容。
type ErrNoValidCitations struct{ Detail string }

func (e *ErrNoValidCitations) Error() string {
	return fmt.Sprintf("证据校验失败：%s", e.Detail)
}

// segmentIndex 建立 Segment.ID → Segment 的查找表。
func segmentIndex(segments []Segment) map[string]Segment {
	m := make(map[string]Segment, len(segments))
	for _, s := range segments {
		m[s.ID] = s
	}
	return m
}

// validCitations 返回 citations 中确实存在的 Segment ID（去重保序）。
func validCitations(citations []string, segs map[string]Segment) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range citations {
		c = strings.TrimSpace(c)
		if _, ok := segs[c]; ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// normalize 规范化空白用于逐字校验（去掉首尾空白，压缩连续空白）。
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// quoteVerbatim 金句逐字校验：规范化后的金句必须是规范化后片段文本的子串。
func quoteVerbatim(quote string, citations []string, segs map[string]Segment) bool {
	q := normalize(quote)
	if q == "" {
		return false
	}
	for _, c := range citations {
		seg, ok := segs[c]
		if !ok {
			continue
		}
		if strings.Contains(normalize(seg.Text), q) {
			return true
		}
	}
	return false
}

// ValidateCard 校验并清洗 KnowledgeCard（ADR-0008 / Roadmap Phase 4）。
//
// 规则：
//   - 每条 Citation 必须是 segments 中真实存在的 Segment.ID（时间范围由程序解析）。
//   - 金句 text 必须逐字（规范化空白后）来自被引用片段；校验失败的金句被省略。
//   - summary/keyPoints/chapters 只保留带有效 Citation 的项；全部无效时报错（拒绝保存）。
//   - title/summary 缺失或全部内容无效时报错，调用方应重试。
//
// 返回清洗后的卡片；错误时返回 ErrNoValidCitations。
func ValidateCard(card *KnowledgeCard, segments []Segment) (*KnowledgeCard, error) {
	if card == nil {
		return nil, &ErrNoValidCitations{Detail: "卡片为空"}
	}
	segs := segmentIndex(segments)
	cleaned := &KnowledgeCard{Title: strings.TrimSpace(card.Title), Tags: card.Tags, SuggestedQuestions: card.SuggestedQuestions}
	summary, err := validateCardSummary(card.Summary, segs)
	if err != nil {
		return nil, err
	}
	cleaned.Summary = summary
	cleaned.KeyPoints = cleanCardKeyPoints(card.KeyPoints, segs)
	if len(cleaned.KeyPoints) == 0 {
		return nil, &ErrNoValidCitations{Detail: "keyPoints 全部缺少有效 Citation"}
	}
	cleaned.Chapters = cleanCardChapters(card.Chapters, segs)
	if len(cleaned.Chapters) == 0 {
		return nil, &ErrNoValidCitations{Detail: "chapters 全部缺少有效 Citation"}
	}
	cleaned.Quotes = cleanCardQuotes(card.Quotes, segs)
	if cleaned.Title == "" {
		return nil, &ErrNoValidCitations{Detail: "title 为空"}
	}
	return cleaned, nil
}

func validateCardSummary(summary CitedText, segs map[string]Segment) (CitedText, error) {
	citations := validCitations(summary.Citations, segs)
	if strings.TrimSpace(summary.Text) == "" || len(citations) == 0 {
		return CitedText{}, &ErrNoValidCitations{Detail: "summary 必须包含有效 Citation"}
	}
	return CitedText{Text: strings.TrimSpace(summary.Text), Citations: citations}, nil
}

func cleanCardKeyPoints(keyPoints []KeyPoint, segs map[string]Segment) []KeyPoint {
	cleaned := make([]KeyPoint, 0, len(keyPoints))
	for _, keyPoint := range keyPoints {
		citations := validCitations(keyPoint.Citations, segs)
		if strings.TrimSpace(keyPoint.Content) == "" || len(citations) == 0 {
			continue
		}
		cleaned = append(cleaned, KeyPoint{Content: strings.TrimSpace(keyPoint.Content), Description: strings.TrimSpace(keyPoint.Description), Citations: citations})
	}
	return cleaned
}

func cleanCardChapters(chapters []Chapter, segs map[string]Segment) []Chapter {
	cleaned := make([]Chapter, 0, len(chapters))
	for _, chapter := range chapters {
		citations := validCitations(chapter.Citations, segs)
		if strings.TrimSpace(chapter.Title) == "" || len(citations) == 0 {
			continue
		}
		cleaned = append(cleaned, Chapter{Title: strings.TrimSpace(chapter.Title), Gist: strings.TrimSpace(chapter.Gist), Citations: citations})
	}
	return cleaned
}

func cleanCardQuotes(quotes []Quote, segs map[string]Segment) []Quote {
	cleaned := make([]Quote, 0, len(quotes))
	for _, quote := range quotes {
		citations := validCitations(quote.Citations, segs)
		if strings.TrimSpace(quote.Text) == "" || len(citations) == 0 || !quoteVerbatim(quote.Text, citations, segs) {
			continue
		}
		cleaned = append(cleaned, Quote{Text: strings.TrimSpace(quote.Text), Citations: citations})
	}
	return cleaned
}

// ResolveCitationRange 把一条 Citation 解析为时间范围（程序计算，ADR-0008）。
func ResolveCitationRange(citation string, segments []Segment) (start, end float64, ok bool) {
	for _, s := range segments {
		if s.ID == citation {
			return s.Start, s.End, true
		}
	}
	return 0, 0, false
}

// ResolveReferenceRange 把一组 Reference（Segment ID 列表）解析为聚合时间范围
// min(start)–max(end)（程序计算，ADR-0018 R1）。
// 与 ResolveCitationRange 对偶：Reference 不声称逐字忠实，但时间范围仍由程序从 Segment 解析，
// 保证所引用的时间点真实存在（AI 不得自行估算）。
func ResolveReferenceRange(references []string, segments []Segment) (start, end float64) {
	first := true
	for _, ref := range references {
		for _, s := range segments {
			if s.ID == ref {
				if first || s.Start < start {
					start = s.Start
				}
				if first || s.End > end {
					end = s.End
				}
				first = false
				break
			}
		}
	}
	return start, end
}

// ResolveCitationSpan 把一组 Citation（Segment ID 列表）解析为聚合时间范围
// min(start)–max(end)（程序计算，ADR-0008）。返回 (start, end, ok)；ok=false 表示无有效引用。
//
// 这是 Citation 多片段 span 的统一实现，供 HTTP 层与 Markdown 渲染共用，
// 消除此前散落在 internal/server 与 internal/markdown 的重复实现。
// 对偶的单片段解析见 ResolveCitationRange；Reference 的多片段解析见 ResolveReferenceRange。
func ResolveCitationSpan(citations []string, segments []Segment) (start, end float64, ok bool) {
	if len(citations) == 0 {
		return 0, 0, false
	}
	first := true
	for _, c := range citations {
		s, e, singleOK := ResolveCitationRange(c, segments)
		if !singleOK {
			continue
		}
		if first || s < start {
			start = s
		}
		if first || e > end {
			end = e
		}
		first = false
	}
	return start, end, !first
}
