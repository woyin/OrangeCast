package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeReducer 记录调用并按脚本输出归并结果。
type fakeReducer struct {
	calls []string
	out   *KnowledgeCard
}

func (f *fakeReducer) reduce(ctx context.Context, candidatesText string) (*KnowledgeCard, TaskUsage, error) {
	f.calls = append(f.calls, candidatesText)
	return f.out, TaskUsage{InputUnits: 100, OutputUnits: 20}, nil
}

func windowCard(title, text, cite string) *KnowledgeCard {
	return &KnowledgeCard{
		Title:     title,
		Summary:   CitedText{Text: text, Citations: []string{cite}},
		KeyPoints: []KeyPoint{{Content: title + " 观点", Description: "d", Citations: []string{cite}}},
	}
}

// TestReduceKnowledgeCards_SingleWindowNoCall 单窗直接返回，不调用模型。
func TestReduceKnowledgeCards_SingleWindowNoCall(t *testing.T) {
	fr := &fakeReducer{}
	card := windowCard("T", "S", "seg-0001")
	got, usage, err := ReduceKnowledgeCards(context.Background(), []*KnowledgeCard{card}, fr.reduce, ReduceOptions{MaxInputChars: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got != card || usage != (TaskUsage{}) || len(fr.calls) != 0 {
		t.Fatalf("单窗不应调用归并: %+v %+v", usage, fr.calls)
	}
}

// TestReduce_MergesDuplicatesKeepsConflicts 语义重复不翻倍、相反观点保留且带各自引用。
func TestReduce_MergesDuplicatesKeepsConflicts(t *testing.T) {
	inputs := []*KnowledgeCard{
		windowCard("咖啡与心血管", "每天一到两杯与更低风险相关", "seg-0001"),
		windowCard("咖啡因升压", "咖啡因短期升高血压", "seg-0002"),
		windowCard("咖啡与心血管·重复", "适量咖啡与更低风险相关", "seg-0001"), // 与窗口1同义重复
	}
	out := &KnowledgeCard{
		Title:   "咖啡的双面证据",
		Summary: CitedText{Text: "归并摘要", Citations: []string{"seg-0001", "seg-0002"}},
		KeyPoints: []KeyPoint{
			{Content: "低剂量与更低风险相关（关联非因果）", Citations: []string{"seg-0001"}},
			{Content: "咖啡因短期升压，确诊人群应限制", Citations: []string{"seg-0002"}},
		},
	}
	fr := &fakeReducer{out: out}
	got, usage, err := ReduceKnowledgeCards(context.Background(), inputs, fr.reduce, ReduceOptions{MaxInputChars: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.KeyPoints) != 2 {
		t.Fatalf("相反观点应分别保留: %+v", got.KeyPoints)
	}
	if usage.InputUnits != 100 {
		t.Fatalf("归并用量应可统计: %+v", usage)
	}
	if errs := ValidateReducedCard(got, inputs); len(errs) != 0 {
		t.Fatalf("合法归并且引用完整: %v", errs)
	}
}

// TestReduce_LayeredWhenInputExceedsLimit 输入超限自动分层：多次调用且收敛到单卡。
func TestReduce_LayeredWhenInputExceedsLimit(t *testing.T) {
	inputs := []*KnowledgeCard{
		windowCard("W1", strings.Repeat("x", 300), "seg-0001"),
		windowCard("W2", strings.Repeat("y", 300), "seg-0002"),
		windowCard("W3", strings.Repeat("z", 300), "seg-0003"),
		windowCard("W4", strings.Repeat("w", 300), "seg-0004"),
	}
	var levels []int
	fn := func(ctx context.Context, text string) (*KnowledgeCard, TaskUsage, error) {
		levels = append(levels, strings.Count(text, "【窗口"))
		return windowCard("归并层", text[:50], "seg-0001"), TaskUsage{InputUnits: 10}, nil
	}
	// 每张候选约 300+ 字符，上限 500 → 分层。
	got, usage, err := ReduceKnowledgeCards(context.Background(), inputs, fn, ReduceOptions{MaxInputChars: 500})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || usage.InputUnits != 10*len(levels) {
		t.Fatalf("分层用量应聚合: %+v levels=%v", usage, levels)
	}
	if len(levels) < 3 {
		t.Fatalf("超限应分层归并（>=3 次调用）: %v", levels)
	}
}

// TestValidateReducedCard_RejectsUnknownCitation 归并引用不存在的候选 → 失败可见。
func TestValidateReducedCard_RejectsUnknownCitation(t *testing.T) {
	inputs := []*KnowledgeCard{windowCard("T", "S", "seg-0001")}
	reduced := &KnowledgeCard{Summary: CitedText{Text: "s", Citations: []string{"seg-9999"}}}
	errs := ValidateReducedCard(reduced, inputs)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "seg-9999") {
		t.Fatalf("应拒绝不存在的引用: %v", errs)
	}
}

// TestValidateReducedCard_DetectsDroppedWindow 整窗证据被丢弃 → 失败可见。
func TestValidateReducedCard_DetectsDroppedWindow(t *testing.T) {
	inputs := []*KnowledgeCard{
		windowCard("W1", "a", "seg-0001"),
		windowCard("W2", "b", "seg-0002"), // 末窗关键观点
	}
	reduced := windowCard("只保留第一窗", "a", "seg-0001")
	errs := ValidateReducedCard(reduced, inputs)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "丢失窗口") {
		t.Fatalf("应检出整窗证据丢失: %v", errs)
	}
}

// TestGroqAnalyze_UsesReduceContract Groq 多窗路径执行归并契约：输出取归并结果、
// 用量含归并调用、非法归并被拒绝。
func TestGroqAnalyze_UsesReduceContract(t *testing.T) {
	g := NewGroqProvider("test")
	g.sleepFn = func(time.Duration) {} // 测试不做真实窗口间隔等待
	g.chatCompleteFn = func(messages []map[string]string, _ string) (string, int, error) {
		// 分窗分析返回合法引用；归并调用返回引用候选之外的 seg-9999，验证校验失败可见。
		if len(messages) > 0 && strings.Contains(messages[0]["content"], analysisReduceSystemPrompt[:20]) {
			return `{"title":"Bad","summary":{"text":"s","citations":["seg-9999"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`, 200, nil
		}
		if len(messages) > 1 && strings.Contains(messages[1]["content"], "seg-0001") {
			return `{"title":"W1","summary":{"text":"s1","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`, 200, nil
		}
		return `{"title":"W2","summary":{"text":"s2","citations":["seg-0002"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`, 200, nil
	}
	_, err := g.Analyze("", []Segment{
		{ID: "seg-0001", Text: strings.Repeat("a", 13000)},
		{ID: "seg-0002", Text: strings.Repeat("b", 13000)},
	})
	if err == nil || !strings.Contains(err.Error(), "整集归并") {
		t.Fatalf("非法归并应失败可见: %v", err)
	}
}

// TestReduceKnowledgeCards_EmptyInput 空输入与缺 reducer 报错。
func TestReduceKnowledgeCards_EmptyInput(t *testing.T) {
	if _, _, err := ReduceKnowledgeCards(context.Background(), nil, nil, ReduceOptions{}); err == nil {
		t.Fatal("空输入应报错")
	}
	card := windowCard("t", "s", "seg-0001")
	if _, _, err := ReduceKnowledgeCards(context.Background(), []*KnowledgeCard{card, card}, nil, ReduceOptions{}); err == nil {
		t.Fatal("缺 reducer 应报错")
	}
	_ = errors.Is
}
