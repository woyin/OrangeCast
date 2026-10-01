package evalset

import (
	"fmt"

	"github.com/woyin/orangecast/internal/provider"
)

// LearningV4Corpus supplies deterministic, self-authored material without reading the Owner database.
func LearningV4Corpus() LearningEvaluationCorpus {
	c := LearningEvaluationCorpus{Version: 1, Kind: "self-authored"}
	texts := [][]string{
		{"检索练习是先尝试回忆再查看答案；只重读可能产生熟悉感。", "间隔学习把复习分散到不同日期，集中学习适合当天准备但不能据此推断长期效果。", "我的理解需要与来源表达分别归因，引用只证明来源说过什么。"},
		{"Retrieval practice means recalling before checking the answer; recognition is not recall.", "Spaced practice distributes review over time; this example supplies no universal effect size.", "Working memory has limited capacity; a concept example does not establish a treatment effect."},
		{"考试当天需要梳理全部材料时，集中整理可能更方便；这不是长期保持效果的比较。", "反例：已经知道答案的简单题，回忆流畅不能证明能迁移到陌生情境。", "实践建议是记录仍不确定的条件，再找原文或反例；这里没有实验效果数字。"},
	}
	for i, group := range texts {
		ep := LearningEvaluationEpisode{ID: fmt.Sprintf("v4-episode-%d", i+1), Title: []string{"回忆与间隔", "Learning concepts", "条件与反例"}[i], Source: "self-made", ModelDataPolicy: "external_allowed"}
		for j, text := range group {
			ep.Segments = append(ep.Segments, provider.Segment{ID: fmt.Sprintf("v4-%d-seg-%d", i+1, j+1), Start: float64(j * 12), End: float64((j + 1) * 12), Text: text})
		}
		c.Episodes = append(c.Episodes, ep)
	}
	c.Cases = []LearningEvaluationCase{
		{ID: "v4-question-conditions", Question: "集中与间隔学习在不同目的下是否矛盾？", Expected: []string{"区分当天整理与长期保持", "保留无效果数字的边界"}, Forbidden: []string{"虚构提升百分比", "将不同目的强判为矛盾"}},
		{ID: "v4-question-understanding", Question: "怎样检查我的理解能否迁移？", Expected: []string{"个人理解分别归因", "找反例与陌生情境"}, Forbidden: []string{"流畅即掌握", "个人反思当来源原话"}},
	}
	for i := 0; i < 20; i++ {
		ep := c.Episodes[i%3]
		segment := ep.Segments[(i/3)%3]
		kind, content := "source_note", segment.Text
		if i%4 == 3 {
			kind, content = "owner_reflection", "我的理解："+segment.Text+" 我还需要在陌生问题中检查适用条件。"
		}
		n := LearningEvaluationNote{ID: fmt.Sprintf("v4-note-%02d", i+1), EpisodeID: ep.ID, Kind: kind, Content: content, Segments: []string{segment.ID}}
		c.Notes = append(c.Notes, n)
		c.Cases[i%2].NoteIDs = append(c.Cases[i%2].NoteIDs, n.ID)
	}
	return c
}

// LearningRetrievalCase identifies actual relevant segments; empty relevance denotes an unanswerable query.
type LearningRetrievalCase struct {
	ID, Query          string
	RelevantSegmentIDs []string
	Paraphrase         bool
}

// LearningV4RetrievalCases freezes forty queries, including fifteen paraphrases and five missing-answer cases.
func LearningV4RetrievalCases() []LearningRetrievalCase {
	groups := []struct {
		queries []string
		ids     []string
	}{
		{[]string{"检索练习", "回忆答案", "retrieval practice", "recognition recall", "先回忆再检查", "主动想起刚学过的内容", "闭卷尝试回答之后核对", "熟悉并不等于能想起来", "recognizing versus recalling", "checking after recalling"}, []string{"v4-1-seg-1", "v4-2-seg-1"}},
		{[]string{"间隔学习", "集中学习", "spaced practice", "复习不同日期", "长期效果", "把学习分散安排", "隔几天再看一遍", "考试当天集中梳理", "长期保持与当天准备有何区别", "distributed review over time"}, []string{"v4-1-seg-2", "v4-2-seg-2", "v4-3-seg-1"}},
		{[]string{"反例", "陌生情境", "适用条件", "流畅迁移", "实践建议", "能复述是否代表能运用", "简单问题答对能否举一反三", "如何寻找不成立的例子", "换一种情境检验理解", "不确定的边界怎样核实"}, []string{"v4-3-seg-2", "v4-3-seg-3"}},
		{[]string{"来源归因", "working memory", "个人理解", "limited capacity", "引用证明", "把自己的解释与原话分开", "记忆容量为什么有边界", "出处不能证明客观真相", "个人笔记并非标准答案", "an example is not a treatment effect"}, []string{"v4-1-seg-3", "v4-2-seg-3"}},
	}
	var out []LearningRetrievalCase
	for _, group := range groups {
		for i, query := range group.queries {
			out = append(out, LearningRetrievalCase{ID: fmt.Sprintf("v4-query-%02d", len(out)+1), Query: query, RelevantSegmentIDs: append([]string(nil), group.ids...), Paraphrase: i >= 5})
		}
	}
	// Keep the corpus at forty cases while explicitly testing missing answers.
	for i, query := range []string{"效果提高多少百分比", "随机试验样本数量", "每位学习者的诊断结果", "具体药物剂量", "来源中不存在的宇宙年龄"} {
		out[35+i].Query, out[35+i].RelevantSegmentIDs, out[35+i].Paraphrase = query, nil, false
	}
	return out
}

// LearningUnderstandingCase fixes multi-source and unreferenced Owner expressions for later snapshot tests.
type LearningUnderstandingCase struct {
	ID, Body            string
	ReferenceSegmentIDs []string
}

// LearningV4UnderstandingCases includes a valid Owner answer without a synthetic Source.
func LearningV4UnderstandingCases() []LearningUnderstandingCase {
	return []LearningUnderstandingCase{
		{ID: "owner-only", Body: "我目前认为复述流畅还不能证明会运用；这个想法尚无来源依据。"},
		{ID: "multi-source", Body: "不同目的下集中整理和间隔复习可以并存，我仍需检查反例。", ReferenceSegmentIDs: []string{"v4-1-seg-2", "v4-3-seg-1"}},
	}
}
