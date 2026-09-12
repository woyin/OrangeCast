package provider

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// G2 程序化门禁（ADR-0023 §4）：封闭集合的全部不变量在代码层强制，非 LLM 审校。
// 每条规则对应一条不变量：
//   1. 引用事实/转述块必须挂有效 Citation（指向给定 Segment 集合内真实存在的 ID）；
//   2. AI 展开块必须带标注文本，且不得含硬事实（数字/日期/外部专名启发式）；
//   3. 直接引语长度对照上限，超限应转述化（调用方截断后复检）；
//   4. Owner 笔记块只许重排、不许改写（内容字符序列必须与笔记原文一致）；
//   5. 无类型身份或空文本的内容不得进入正文。

// hardFactPattern 硬事实启发式：百分比、千分位数字、货币金额、年份区间。
// AI 展开块命中即拒绝——数字是 AI 幻觉最常伪装成事实的形态（ADR-0023 §2 表格第 2 行）。
var hardFactPattern = regexp.MustCompile(`\d+(?:\.\d+)?\s*%|\d{4}\s*[-–—~至到]\s*\d{4}|[¥$€£]\s*\d|\b\d{1,3}(?:,\d{3})+\b`)

// yearPattern 独立年份：避免把"2024 年"这类年份也放进 AI 展开（事实属性强）。
var yearPattern = regexp.MustCompile(`\b(19|20)\d{2}\s*年`)

// containsHardFact 判断文本是否含硬事实信号（数字/金额/年份）。
func containsHardFact(text string) bool {
	return hardFactPattern.MatchString(text) || yearPattern.MatchString(text)
}

// stripAllSpace 去除全部空白（含全角空格），只比内容字符序列。
// 笔记块"只许重排"的判定用它：加空格、换行、Tab 排版后内容仍逐字一致。
func stripAllSpace(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DigestValidationInput 门禁的全部输入：待检块 + 可引用的 Segment 与笔记语料。
type DigestValidationInput struct {
	Blocks           []DigestBlockDraft
	Segments         map[string]Segment   // 本集 Segment（转述块引用目标）
	DocumentSegments map[string][]Segment // 落源 DocumentID → 其 Segment（引用事实块目标）
	Notes            map[string]string    // NoteID → 原文（笔记块比对基准）
	QuoteMaxChars    int                  // 直接引语长度上限（RightsConstraint 投影）
}

// DigestValidationIssue 一条门禁违规。
type DigestValidationIssue struct {
	BlockIndex int
	Rule       string
	Detail     string
}

func (i DigestValidationIssue) String() string {
	return fmt.Sprintf("块 %d 违反 %s：%s", i.BlockIndex, i.Rule, i.Detail)
}

// aiExpansionLabel AI 展开块的强制标注前缀（与领域语言"AI 展开"一致）。
const aiExpansionLabel = "AI 展开"

// ValidateDigestBlocks 对封闭集合执行 G2 门禁；任何违规即失败（生成时拒绝，不静默降级）。
// 返回 nil 表示全部通过。
func ValidateDigestBlocks(in DigestValidationInput) []DigestValidationIssue {
	var issues []DigestValidationIssue
	for i, b := range in.Blocks {
		switch b.Type {
		case DigestBlockParaphraseStr:
			if len(b.Citations) == 0 {
				issues = append(issues, DigestValidationIssue{i, "citation", "转述块必须挂 Citation"})
				continue
			}
			for _, c := range b.Citations {
				if _, ok := in.Segments[c]; !ok {
					issues = append(issues, DigestValidationIssue{i, "citation", fmt.Sprintf("转述块引用了不存在的本集 Segment %q", c)})
				}
			}
		case DigestBlockAIExpansionStr:
			if !strings.Contains(b.Text, aiExpansionLabel) {
				issues = append(issues, DigestValidationIssue{i, "label", "AI 展开块必须带标注文本"})
			}
			if containsHardFact(b.Text) {
				issues = append(issues, DigestValidationIssue{i, "hard-fact", "AI 展开块不得含具体数字/日期/金额（应转 FactGap 或引用事实）"})
			}
		case DigestBlockNoteStr:
			original, ok := in.Notes[b.NoteID]
			if !ok {
				issues = append(issues, DigestValidationIssue{i, "note", fmt.Sprintf("笔记块引用了不存在的 NoteID %q", b.NoteID)})
				continue
			}
			// 重排 = 内容字符序列一致（空白差异属排版，非改写）
			if stripAllSpace(b.Text) != stripAllSpace(original) {
				issues = append(issues, DigestValidationIssue{i, "note-fidelity", "笔记块只许重排，不许改写 Owner 原文"})
			}
		case DigestBlockCitedFactStr:
			if b.TargetSourceID == "" {
				issues = append(issues, DigestValidationIssue{i, "citation", "引用事实块必须指向落源 Document"})
				continue
			}
			segs, ok := in.DocumentSegments[b.TargetSourceID]
			if !ok {
				issues = append(issues, DigestValidationIssue{i, "citation", fmt.Sprintf("引用事实块指向未沉淀的目标 %q", b.TargetSourceID)})
				continue
			}
			if len(b.Citations) == 0 {
				issues = append(issues, DigestValidationIssue{i, "citation", "引用事实块必须挂 Citation"})
				continue
			}
			segMap := make(map[string]Segment, len(segs))
			for _, s := range segs {
				segMap[s.ID] = s
			}
			for _, c := range b.Citations {
				if _, ok := segMap[c]; !ok {
					issues = append(issues, DigestValidationIssue{i, "citation", fmt.Sprintf("引用事实块引用了目标 Document 不存在的 Segment %q", c)})
				}
			}
		default:
			issues = append(issues, DigestValidationIssue{i, "identity", fmt.Sprintf("未知块类型 %q 不得进入正文", b.Type)})
		}
		// 规则 3：直接引语（转述/引用事实块内引号包裹的原文摘录）长度上限。
		if in.QuoteMaxChars > 0 && (b.Type == DigestBlockParaphraseStr || b.Type == DigestBlockCitedFactStr) {
			for _, q := range quotedSpans(b.Text) {
				if len([]rune(q)) > in.QuoteMaxChars {
					issues = append(issues, DigestValidationIssue{i, "quote-length", fmt.Sprintf("直接引语超过 %d 字上限（%d 字），应截断为转述", in.QuoteMaxChars, len([]rune(q)))})
				}
			}
		}
	}
	return issues
}

// quotedSpans 提取文本中被中文双引号包裹的直接引语。
func quotedSpans(text string) []string {
	var out []string
	var buf strings.Builder
	inQuote := false
	for _, r := range text {
		switch r {
		case '“', '『':
			if inQuote && buf.Len() > 0 {
				out = append(out, buf.String())
			}
			buf.Reset()
			inQuote = true
		case '”', '』':
			if inQuote && buf.Len() > 0 {
				out = append(out, buf.String())
			}
			buf.Reset()
			inQuote = false
		default:
			if inQuote {
				buf.WriteRune(r)
			}
		}
	}
	return out
}

// DigestHasHardFact 暴露硬事实启发式给 WeaveDigestFacts 的调用方：
// 模型返回的"已补事实"若仍含无引用硬事实，应转 FactGap 而非入文。
func DigestHasHardFact(text string) bool { return containsHardFact(text) }

// TruncateQuotedSpans 把超长直接引语截断为转述：保留前 maxChars 字并加省略标记。
// RightsConstraint 的"超限自动截断为转述"不变量由它落实（截断后应复跑门禁）。
// 只处理中文双引号（quotedSpans 的提取范围），未闭合引号原样保留。
func TruncateQuotedSpans(text string, maxChars int) string {
	if maxChars <= 0 {
		return text
	}
	var b strings.Builder
	var buf strings.Builder
	inQuote := false
	for _, r := range text {
		switch r {
		case '“', '『':
			if inQuote && buf.Len() > 0 {
				writeTruncated(&b, buf.String(), maxChars)
			}
			buf.Reset()
			inQuote = true
			b.WriteRune('“')
		case '”', '』':
			if inQuote {
				if buf.Len() > 0 {
					writeTruncated(&b, buf.String(), maxChars)
				}
				b.WriteRune('”')
			} else {
				b.WriteRune(r)
			}
			buf.Reset()
			inQuote = false
		default:
			if inQuote {
				buf.WriteRune(r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// writeTruncated 只输出引语内容本身：超限时保留前 maxChars 字并追加省略提示；
// 开闭引号由 TruncateQuotedSpans 统一处理。省略提示在引号内，因此内容截到
// maxChars-len(ellipsis) 保证复跑门禁时引语总长不超过上限。
func writeTruncated(b *strings.Builder, span string, maxChars int) {
	const ellipsis = "……（后文转述）"
	runes := []rune(span)
	if len(runes) <= maxChars {
		b.WriteString(span)
		return
	}
	keep := maxChars - len([]rune(ellipsis))
	if keep < 0 {
		keep = 0
	}
	b.WriteString(string(runes[:keep]) + ellipsis)
}

// ValidateDigestRewrite 渠道格式门禁（F3 后置校验）：字数与话题 tag 数约束。
// 事实不变量已由"输入只有已过门禁的长文块"保证；这里只卡渠道格式。
func ValidateDigestRewrite(text string, maxChars, maxHashtags int) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("改写文本为空")
	}
	if n := len([]rune(text)); n > maxChars {
		return fmt.Errorf("改写 %d 字超过渠道上限 %d", n, maxChars)
	}
	tags := 0
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(field, "#") && len(field) > 1 {
			tags++
		}
	}
	if tags > maxHashtags {
		return fmt.Errorf("话题 tag %d 个超过上限 %d", tags, maxHashtags)
	}
	return nil
}
