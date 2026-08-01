// Package evalset 提供代表性子样本集（中英文）与自动质量检查（ADR-0008 / Roadmap Phase 4）。
//
// 目的：
//   - 模型或 Prompt 变更时，用同一组样本跑自动校验，得到可比较的评测结果。
//   - 自动检查覆盖：schema（字段完整）、Citation 存在性、Segment 时间边界、金句逐字匹配。
//   - 人工有用性评分记录在 docs/evalset.md（由人工按样本编号填写）。
package evalset

import (
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/provider"
)

// Sample 一个评测样本：真实感转录段 + 对应的 KnowledgeCard。
type Sample struct {
	ID       string // 编号，对应 docs/evalset.md 评分表
	Language string // zh | en
	Segments []provider.Segment
	Card     *provider.KnowledgeCard
}

// Samples 代表性子样本集（5–10 个，覆盖中英文、口语、数字、引语等场景）。
var Samples = []Sample{
	{
		ID: "zh-01", Language: "zh",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 4.2, Text: "主权财富基金正在改变全球投资格局"},
			{ID: "seg-0002", Start: 4.2, End: 9.8, Text: "新加坡政府投资公司是典型的长期投资者"},
			{ID: "seg-0003", Start: 9.8, End: 15.0, Text: "长期视角意味着可以承受市场短期波动"},
		},
		Card: &provider.KnowledgeCard{
			Title:   "主权财富基金的长期投资逻辑",
			Summary: provider.CitedText{Text: "主权财富基金以长期视角投资，新加坡政府投资公司为代表。", Citations: []string{"seg-0001", "seg-0002"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "长期视角", Description: "可承受短期波动", Citations: []string{"seg-0003"}},
			},
			Chapters: []provider.Chapter{{Title: "主权财富基金", Gist: "改变全球投资格局", Citations: []string{"seg-0001"}}},
			Quotes:   []provider.Quote{{Text: "主权财富基金正在改变全球投资格局", Citations: []string{"seg-0001"}}},
			Tags:     []string{"投资", "主权财富基金"},
		},
	},
	{
		ID: "zh-02", Language: "zh",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 3.0, Text: "今天聊聊睡眠对记忆巩固的作用"},
			{ID: "seg-0002", Start: 3.0, End: 7.5, Text: "研究发现慢波睡眠阶段对情景记忆尤其重要"},
			{ID: "seg-0003", Start: 7.5, End: 12.0, Text: "睡眠不足时海马体的活动会显著下降"},
		},
		Card: &provider.KnowledgeCard{
			Title:   "睡眠与记忆巩固",
			Summary: provider.CitedText{Text: "慢波睡眠对情景记忆巩固至关重要，睡眠不足会削弱海马体功能。", Citations: []string{"seg-0002", "seg-0003"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "慢波睡眠", Description: "对情景记忆尤其重要", Citations: []string{"seg-0002"}},
				{Content: "睡眠不足", Description: "海马体活动下降", Citations: []string{"seg-0003"}},
			},
			Chapters: []provider.Chapter{{Title: "睡眠与记忆", Gist: "慢波睡眠关键作用", Citations: []string{"seg-0002"}}},
			Quotes:   []provider.Quote{{Text: "慢波睡眠阶段对情景记忆尤其重要", Citations: []string{"seg-0002"}}},
			Tags:     []string{"睡眠", "记忆"},
		},
	},
	{
		ID: "zh-03", Language: "zh",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 5.0, Text: "播客创作者需要建立稳定的更新节奏"},
			{ID: "seg-0002", Start: 5.0, End: 9.0, Text: "每周一次的输出比追求完美更可持续"},
			{ID: "seg-0003", Start: 9.0, End: 13.0, Text: "听众的忠诚度来自长期的一致性"},
		},
		Card: &provider.KnowledgeCard{
			Title:   "播客创作的持续之道",
			Summary: provider.CitedText{Text: "稳定更新节奏比完美更重要，听众忠诚来自一致性。", Citations: []string{"seg-0001", "seg-0003"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "稳定节奏", Description: "每周一次可持续", Citations: []string{"seg-0002"}},
			},
			Chapters: []provider.Chapter{{Title: "创作节奏", Gist: "一致性建立忠诚", Citations: []string{"seg-0003"}}},
			Quotes:   []provider.Quote{{Text: "每周一次的输出比追求完美更可持续", Citations: []string{"seg-0002"}}},
			Tags:     []string{"播客", "创作"},
		},
	},
	{
		ID: "en-01", Language: "en",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 3.5, Text: "The future of AI depends on data governance."},
			{ID: "seg-0002", Start: 3.5, End: 8.0, Text: "Open data sets help smaller teams compete."},
			{ID: "seg-0003", Start: 8.0, End: 12.5, Text: "Regulation should focus on transparency."},
		},
		Card: &provider.KnowledgeCard{
			Title:   "AI and Data Governance",
			Summary: provider.CitedText{Text: "AI's future relies on governance, open data, and transparent regulation.", Citations: []string{"seg-0001", "seg-0003"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "Open data", Description: "Lets small teams compete", Citations: []string{"seg-0002"}},
			},
			Chapters: []provider.Chapter{{Title: "Governance", Gist: "Transparency focus", Citations: []string{"seg-0003"}}},
			Quotes:   []provider.Quote{{Text: "The future of AI depends on data governance.", Citations: []string{"seg-0001"}}},
			Tags:     []string{"AI", "governance"},
		},
	},
	{
		ID: "en-02", Language: "en",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 4.0, Text: "Compound interest is the eighth wonder of the world."},
			{ID: "seg-0002", Start: 4.0, End: 8.5, Text: "Start investing early to let time do the work."},
			{ID: "seg-0003", Start: 8.5, End: 13.0, Text: "Consistency beats intensity in wealth building."},
		},
		Card: &provider.KnowledgeCard{
			Title:   "The Power of Compound Interest",
			Summary: provider.CitedText{Text: "Starting early and staying consistent lets compounding work.", Citations: []string{"seg-0002", "seg-0003"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "Start early", Description: "Let time do the work", Citations: []string{"seg-0002"}},
			},
			Chapters: []provider.Chapter{{Title: "Compounding", Gist: "Consistency beats intensity", Citations: []string{"seg-0003"}}},
			Quotes:   []provider.Quote{{Text: "Compound interest is the eighth wonder of the world.", Citations: []string{"seg-0001"}}},
			Tags:     []string{"investing", "compounding"},
		},
	},
	{
		ID: "en-03", Language: "en",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 5.0, Text: "Remote work changes how teams build trust."},
			{ID: "seg-0002", Start: 5.0, End: 10.0, Text: "Over-communication is better than under-communication."},
			{ID: "seg-0003", Start: 10.0, End: 14.0, Text: "Written decisions create a shared record."},
		},
		Card: &provider.KnowledgeCard{
			Title:   "Trust in Remote Teams",
			Summary: provider.CitedText{Text: "Remote teams build trust through over-communication and written records.", Citations: []string{"seg-0002", "seg-0003"}},
			KeyPoints: []provider.KeyPoint{
				{Content: "Over-communication", Description: "Better than under", Citations: []string{"seg-0002"}},
			},
			Chapters: []provider.Chapter{{Title: "Trust", Gist: "Shared written record", Citations: []string{"seg-0003"}}},
			Quotes:   []provider.Quote{{Text: "Over-communication is better than under-communication.", Citations: []string{"seg-0002"}}},
			Tags:     []string{"remote work", "trust"},
		},
	},
}

// Issue 一条自动校验发现的问题。
type Issue struct {
	SampleID string
	Kind     string // schema | citation | time | verbatim
	Detail   string
}

// Check 对全部样本执行自动校验，返回问题列表（空 = 全部通过）。
// 覆盖 Roadmap Phase 4 自动检查：schema、引用存在性、时间边界、逐字引用。
func Check() []Issue {
	var issues []Issue
	for _, smp := range Samples {
		// schema：ValidateCard 要求核心字段 + 有效 Citation（同时覆盖 citation 存在性）
		_, err := provider.ValidateCard(smp.Card, smp.Segments)
		if err != nil {
			issues = append(issues, Issue{SampleID: smp.ID, Kind: "schema", Detail: err.Error()})
		}
		// 时间边界：每个 Segment start>=0 且 start<end；Citation 时间范围合法
		for _, seg := range smp.Segments {
			if seg.Start < 0 || seg.End <= seg.Start {
				issues = append(issues, Issue{SampleID: smp.ID, Kind: "time", Detail: fmt.Sprintf("segment %s 时间非法: %.2f-%.2f", seg.ID, seg.Start, seg.End)})
			}
		}
		for _, c := range allCitations(smp.Card) {
			if _, _, ok := provider.ResolveCitationRange(c, smp.Segments); !ok {
				issues = append(issues, Issue{SampleID: smp.ID, Kind: "citation", Detail: fmt.Sprintf("citation %q 不存在", c)})
			}
		}
		// verbatim：金句逐字（由 ValidateCard 已覆盖，这里单独记录失败详情）
		for _, q := range smp.Card.Quotes {
			if !quoteInSegments(q.Text, q.Citations, smp.Segments) {
				issues = append(issues, Issue{SampleID: smp.ID, Kind: "verbatim", Detail: fmt.Sprintf("quote %q 非逐字", q.Text)})
			}
		}
	}
	return issues
}

func allCitations(c *provider.KnowledgeCard) []string {
	var out []string
	out = append(out, c.Summary.Citations...)
	for _, kp := range c.KeyPoints {
		out = append(out, kp.Citations...)
	}
	for _, ch := range c.Chapters {
		out = append(out, ch.Citations...)
	}
	for _, q := range c.Quotes {
		out = append(out, q.Citations...)
	}
	return out
}

func quoteInSegments(quote string, citations []string, segments []provider.Segment) bool {
	q := normalize(quote)
	for _, c := range citations {
		for _, s := range segments {
			if s.ID == c && strings.Contains(normalize(s.Text), q) {
				return true
			}
		}
	}
	return false
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
