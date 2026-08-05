// Highlight（高光片段）相关的类型定义、生成接口与校验逻辑（ADR-0016）。
// Highlight 是 AI 按价值密度选出的连续音频区间，与 KnowledgeCard 并列但独立版本化。
package provider

import (
	"fmt"
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

// GenerateHighlights 从全部 Segment 生成高光片段（ADR-0016）。
// 这是独立的 AI 任务（与 KnowledgeCard 分析不同），需要喂入全部 Segment。
// 实现由各 Provider 提供（Groq/OpenAI）。
type HighlightProvider interface {
	GenerateHighlights(segments []Segment) (*HighlightSet, error)
	Name() string
}

// ValidateHighlightSet 校验并清洗高光片段（ADR-0016）。
// 规则：Citation 必须是真实存在的 Segment ID；时间范围由程序从 Citation 算。
func ValidateHighlightSet(hs *HighlightSet, segments []Segment) (*HighlightSet, error) {
	if hs == nil {
		return nil, fmt.Errorf("高光片段为空")
	}
	segs := segmentIndex(segments)
	cleaned := &HighlightSet{}
	for _, h := range hs.Highlights {
		cites := validCitations(h.Citations, segs)
		if strings.TrimSpace(h.Gist) == "" || len(cites) == 0 {
			continue // 省略无效项
		}
		cleaned.Highlights = append(cleaned.Highlights, Highlight{
			Gist:      strings.TrimSpace(h.Gist),
			Citations: cites,
		})
	}
	if len(cleaned.Highlights) == 0 {
		return nil, fmt.Errorf("全部高光片段缺少有效 Citation")
	}
	return cleaned, nil
}
