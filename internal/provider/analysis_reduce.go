// analysis_reduce.go 整集重点归并（K01 / ADR-0024 §5）。
// 保留已有分窗分析；在分窗之上增加有输入上限的归并步骤：候选文本超限时分层归并，
// 不再无上限发送整集。归并只使用候选与必要证据；输出经 ValidateReducedCard 校验——
// 引用不存在的候选、丢掉整窗必要证据时失败可见。Groq 与 OpenAI 两条路径共用本契约。
package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// AnalysisReduceFunc 执行一次模型归并：输入候选卡的文本化描述，输出整集卡与用量。
type AnalysisReduceFunc func(ctx context.Context, candidatesText string) (*KnowledgeCard, TaskUsage, error)

// ReduceOptions 归并选项。
type ReduceOptions struct {
	// MaxInputChars 单次归并输入的字符上限；<=0 表示不限制（仍受单窗预算约束）。
	MaxInputChars int
}

// ReduceKnowledgeCards 整集归并入口（K01）。
// 单窗直接返回（零额外调用）；多窗经模型归并并聚合用量；输入超限自动分层。
func ReduceKnowledgeCards(ctx context.Context, cards []*KnowledgeCard, reducer AnalysisReduceFunc, opts ReduceOptions) (*KnowledgeCard, TaskUsage, error) {
	if len(cards) == 0 {
		return nil, TaskUsage{}, fmt.Errorf("归并输入为空")
	}
	if len(cards) == 1 {
		return cards[0], TaskUsage{}, nil
	}
	if reducer == nil {
		return nil, TaskUsage{}, fmt.Errorf("缺少归并 reducer")
	}
	usage := TaskUsage{}
	reduced, u, err := reduceLayer(ctx, cards, reducer, opts)
	usage.InputUnits += u.InputUnits
	usage.OutputUnits += u.OutputUnits
	usage.RetryCount += u.RetryCount
	if err != nil {
		return nil, usage, err
	}
	return reduced, usage, nil
}

// reduceLayer 归并一层候选；超限时先分层归并再对结果归并。
func reduceLayer(ctx context.Context, cards []*KnowledgeCard, reducer AnalysisReduceFunc, opts ReduceOptions) (*KnowledgeCard, TaskUsage, error) {
	usage := TaskUsage{}
	groups := partitionByLimit(cards, opts.MaxInputChars)
	if len(groups) == 1 {
		reduced, u, err := reducer(ctx, buildCandidatesText(cards))
		usage.InputUnits += u.InputUnits
		usage.OutputUnits += u.OutputUnits
		usage.RetryCount += u.RetryCount
		if err != nil {
			return nil, usage, fmt.Errorf("归并 %d 张候选卡: %w", len(cards), err)
		}
		return reduced, usage, nil
	}
	var reducedCards []*KnowledgeCard
	for i, group := range groups {
		// 单组不超过上限时直接递归；组内仍超限会继续分层。
		reduced, u, err := reduceLayer(ctx, group, reducer, opts)
		usage.InputUnits += u.InputUnits
		usage.OutputUnits += u.OutputUnits
		usage.RetryCount += u.RetryCount
		if err != nil {
			return nil, usage, fmt.Errorf("第 %d 层归并: %w", i+1, err)
		}
		reducedCards = append(reducedCards, reduced)
	}
	// 分层结果再归并一层；若分组结果总量仍超限，靠 partition 稳定收敛（每组至少 2 张）。
	if len(reducedCards) == 1 {
		return reducedCards[0], usage, nil
	}
	reduced, u, err := reducer(ctx, buildCandidatesText(reducedCards))
	usage.InputUnits += u.InputUnits
	usage.OutputUnits += u.OutputUnits
	usage.RetryCount += u.RetryCount
	if err != nil {
		return nil, usage, fmt.Errorf("归并分层结果: %w", err)
	}
	return reduced, usage, nil
}

// partitionByLimit 按候选文本长度分组；每组尽量不超过上限，且至少 2 张保证收敛。
func partitionByLimit(cards []*KnowledgeCard, limit int) [][]*KnowledgeCard {
	if limit <= 0 || len(cards) <= 1 {
		return [][]*KnowledgeCard{cards}
	}
	type sized struct {
		card *KnowledgeCard
		size int
	}
	sizedCards := make([]sized, 0, len(cards))
	for _, c := range cards {
		sizedCards = append(sizedCards, sized{c, len(buildCandidatesText([]*KnowledgeCard{c}))})
	}
	// 大候选单独成组，其余按长度贪心装箱（保持稳定顺序）。
	sort.SliceStable(sizedCards, func(i, j int) bool { return sizedCards[i].size > sizedCards[j].size })
	var groups [][]*KnowledgeCard
	var cur []*KnowledgeCard
	curSize := 0
	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
			curSize = 0
		}
	}
	for _, sc := range sizedCards {
		if sc.size > limit {
			flush()
			groups = append(groups, []*KnowledgeCard{sc.card})
			continue
		}
		if curSize+sc.size > limit && len(cur) >= 2 {
			flush()
		}
		cur = append(cur, sc.card)
		curSize += sc.size
	}
	flush()
	return groups
}

// buildCandidatesText 把候选卡序列化为归并输入（带窗口号与引用，供模型与审计）。
func buildCandidatesText(cards []*KnowledgeCard) string {
	var sb strings.Builder
	for i, c := range cards {
		if c == nil {
			continue
		}
		fmt.Fprintf(&sb, "【窗口 %d】标题：%s\n摘要：%s（引用：%s）\n", i+1, c.Title, c.Summary.Text, strings.Join(c.Summary.Citations, ","))
		for _, kp := range c.KeyPoints {
			fmt.Fprintf(&sb, "观点：%s / %s（引用：%s）\n", kp.Content, kp.Description, strings.Join(kp.Citations, ","))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ValidateReducedCard 校验归并输出没有越界（K01，失败可见）：
//  1. 归并卡的全部引用必须 ⊆ 输入候选的引用并集（引用不存在的候选 → 失败）；
//  2. 每个输入窗口若带有引用证据，归并结果必须至少保留该窗口的一条引用
//     （整窗必要证据被丢弃 → 失败）；
//  3. 金句逐字与引用存在性由既有 ValidateCard 在调用方兜底。
func ValidateReducedCard(reduced *KnowledgeCard, inputs []*KnowledgeCard) []error {
	inputCitations := map[string]bool{}
	windowOfCitation := map[string][]int{}
	for wi, c := range inputs {
		if c == nil {
			continue
		}
		hasCitation := false
		for _, cite := range c.Summary.Citations {
			inputCitations[cite] = true
			windowOfCitation[cite] = append(windowOfCitation[cite], wi)
			hasCitation = true
		}
		for _, kp := range c.KeyPoints {
			for _, cite := range kp.Citations {
				inputCitations[cite] = true
				windowOfCitation[cite] = append(windowOfCitation[cite], wi)
				hasCitation = true
			}
		}
		if hasCitation {
			windowOfCitation[fmt.Sprintf("window:%d", wi)] = []int{wi} // 窗口存在标记
		}
	}
	var errs []error
	reducedCitations := map[string]bool{}
	add := func(cites []string) {
		for _, c := range cites {
			reducedCitations[c] = true
			if !inputCitations[c] {
				errs = append(errs, fmt.Errorf("归并引用了候选中不存在的 %q", c))
			}
		}
	}
	add(reduced.Summary.Citations)
	for _, kp := range reduced.KeyPoints {
		add(kp.Citations)
	}
	// 每个带证据的输入窗口至少保留一条引用。
	coveredWindows := map[int]bool{}
	for c := range reducedCitations {
		for _, wi := range windowOfCitation[c] {
			coveredWindows[wi] = true
		}
	}
	for wi, c := range inputs {
		if c == nil {
			continue
		}
		hasEvidence := len(c.Summary.Citations) > 0
		for _, kp := range c.KeyPoints {
			if len(kp.Citations) > 0 {
				hasEvidence = true
			}
		}
		if hasEvidence && !coveredWindows[wi] {
			errs = append(errs, fmt.Errorf("归并丢失窗口 %d 的全部证据引用", wi+1))
		}
	}
	return errs
}
