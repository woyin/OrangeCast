// Highlight（高光片段）相关的类型定义、生成接口与校验逻辑（ADR-0016）。
// Highlight 是 AI 按价值密度选出的连续音频区间，与 KnowledgeCard 并列但独立版本化。
package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// highlightSystemPrompt 高光片段生成系统提示（ADR-0016）。
// 模型从全部 Segment 中选出最有价值的连续区间，只引用 Segment ID。
const highlightSystemPrompt = `你是一个播客高光片段编辑。基于用户提供的播客转录稿（全部带编号片段），选出这集最值得听的 3-6 个连续区间。

每个高光片段必须包含：
- gist：1-2 句话说明这段为什么值得听（你组织的概括，不要逐字摘抄原文）
- citations：构成这个高光区间的片段 ID 列表（必须是输入中存在的 ID，通常 3-15 个连续片段）

选择标准：信息密度高、有洞察、有金句潜力的区间；避免纯寒暄、广告、重复内容。
高光区间按出现顺序排列，区间之间不应重叠。

只输出 JSON：{"highlights":[{"gist":"...","citations":["seg-0001","seg-0002",...]}]}
不要输出任何其他文字或 markdown 代码块。`

// HighlightProvider 从全部 Segment 生成高光片段（ADR-0016）的能力接口。
// 这是独立的 AI 任务（与 KnowledgeCard 分析不同），需要喂入全部 Segment；
// 实现由各 Provider 提供（Groq/OpenAI）。
type HighlightProvider interface {
	GenerateHighlights(segments []Segment) (*HighlightSet, error)
	Name() string
}

// ValidateHighlightSet 校验并清洗高光片段（ADR-0016）。
// 规则：Citation 必须是真实存在的 Segment ID；时间范围由程序从 Citation 算。
// ValidateHighlightSet 高光校验（D01）：在引用存在性之上增加程序化约束——
//   - 连续性：一个高光的引用必须是转录顺序上连续的 Segment 区间；跨空洞
//     （如广告段被跳过）或乱序引用显式拒绝，不允许 min/max 把无关音频带进区间；
//   - 重叠：不同高光的 Segment 区间不得相交；重复 Citation 集合去重；
//   - 零时长：解析出的时间跨度 end<=start 显式拒绝。
//
// 起止时间始终由程序从 Segment 解析，模型不参与估算（ADR-0016/0008）。
func ValidateHighlightSet(hs *HighlightSet, segments []Segment) (*HighlightSet, error) {
	if hs == nil {
		return nil, fmt.Errorf("高光片段为空")
	}
	segs := segmentIndex(segments)
	order := make(map[string]int, len(segments))
	for i, seg := range segments {
		order[seg.ID] = i
	}
	type span struct{ lo, hi int }
	cleaned := &HighlightSet{}
	seen := map[string]bool{}
	var taken []span
	for _, h := range hs.Highlights {
		cites := validCitations(h.Citations, segs)
		if strings.TrimSpace(h.Gist) == "" || len(cites) == 0 {
			continue // 省略无效项（既有行为）
		}
		// 连续性检查：引用的 Segment 下标必须构成连续区间。
		indices := make([]int, 0, len(cites))
		for _, c := range cites {
			indices = append(indices, order[c])
		}
		sort.Ints(indices)
		lo, hi := indices[0], indices[len(indices)-1]
		if len(indices) != hi-lo+1 {
			return nil, fmt.Errorf("高光 %q 的引用跨越不连续区间（seg %d..%d 中有未引用片段）：区间非法",
				strings.TrimSpace(h.Gist), lo, hi)
		}
		// 零时长/缺时间检查。
		if valid, zero := spanTimeRange(cites, segs); !valid || zero {
			return nil, fmt.Errorf("高光 %q 时间区间非法（零时长或缺少时间）", strings.TrimSpace(h.Gist))
		}
		// 完全相同的引用集合先去重（规范化），再检查与其它高光的重叠。
		id := stableHighlightID(cites)
		if seen[id] {
			continue
		}
		for _, t := range taken {
			if lo <= t.hi && t.lo <= hi {
				return nil, fmt.Errorf("高光 %q 与既有区间重叠（片段 %d..%d）", strings.TrimSpace(h.Gist), lo, hi)
			}
		}
		seen[id] = true
		taken = append(taken, span{lo, hi})
		cleaned.Highlights = append(cleaned.Highlights, Highlight{
			ID:        id,
			Gist:      strings.TrimSpace(h.Gist),
			Citations: cites,
		})
	}
	if len(cleaned.Highlights) == 0 {
		return nil, fmt.Errorf("全部高光片段缺少有效 Citation")
	}
	return cleaned, nil
}

// spanTimeRange 解析区间起止：返回 (时间合法, 是否零时长)。
// 每条引用都必须能解析到 Segment 时间；全部解析后要求 start < end。
func spanTimeRange(cites []string, segs map[string]Segment) (valid, zero bool) {
	var start, end float64
	first := true
	for _, c := range cites {
		seg, ok := segs[c]
		if !ok {
			return false, false
		}
		if first {
			start, end = seg.Start, seg.End
			first = false
			continue
		}
		if seg.Start < start {
			start = seg.Start
		}
		if seg.End > end {
			end = seg.End
		}
	}
	if first || end <= start {
		return true, true
	}
	return true, false
}

// stableHighlightID 由 Citation 集合生成稳定 ID（ADR-0019）。
// HighlightSet 刷新（重新生成高光）时，相同 Citation 集合的 Highlight 得到相同 ID，
// 让挂在它上的 Gist 与 Narration 在刷新后仍能正确关联，避免错挂到错误区间。
// 算法：排序去重 Citation → sha256 → 取前 12 个 hex 字符（够防碰撞，够短可读）。
func stableHighlightID(citations []string) string {
	seen := map[string]bool{}
	var ids []string
	for _, c := range citations {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		ids = append(ids, c)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0}) // 分隔符防 "ab"+"cd" == "a"+"bcd"
	}
	return hex.EncodeToString(h.Sum(nil)[:6]) // 12 hex chars
}
