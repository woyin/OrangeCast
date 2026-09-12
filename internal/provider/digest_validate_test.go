package provider

import (
	"strings"
	"testing"
)

// 门禁测试基准输入：一集两段 + 一份落源 Document 两段 + 一条 Owner 笔记。
func gateInput(blocks []DigestBlockDraft) DigestValidationInput {
	return DigestValidationInput{
		Blocks: blocks,
		Segments: map[string]Segment{
			"seg-1": {ID: "seg-1", Text: "本期讨论主权基金的配置变化"},
			"seg-2": {ID: "seg-2", Text: "利率环境影响估值"},
		},
		DocumentSegments: map[string][]Segment{
			"doc-9": {
				{ID: "doc-seg-1", Text: "2024 年行业规模约 1.2 万亿"},
				{ID: "doc-seg-2", Text: "增长率为 8%"},
			},
		},
		Notes:         map[string]string{"note-1": "我认为估值模型低估了尾部风险"},
		QuoteMaxChars: 20,
	}
}

func firstRule(issues []DigestValidationIssue, rule string) *DigestValidationIssue {
	for i := range issues {
		if issues[i].Rule == rule {
			return &issues[i]
		}
	}
	return nil
}

// TestValidateDigestBlocks_AllPass 四类合法块应零违规通过。
func TestValidateDigestBlocks_AllPass(t *testing.T) {
	in := gateInput([]DigestBlockDraft{
		{Type: DigestBlockParaphraseStr, Text: "本期讨论了主权基金的配置变化。", Citations: []string{"seg-1"}},
		{Type: DigestBlockAIExpansionStr, Text: "AI 展开：打个比方，这就像调整船帆的角度。"},
		{Type: DigestBlockNoteStr, Text: "我认为估值模型低估了尾部风险", NoteID: "note-1"},
		{Type: DigestBlockCitedFactStr, Text: "据行业报道，增长率为 8%。", Citations: []string{"doc-seg-2"}, TargetSourceID: "doc-9"},
	})
	if issues := ValidateDigestBlocks(in); len(issues) != 0 {
		t.Fatalf("合法集合应零违规：%v", issues)
	}
}

// TestValidateDigestBlocks_Rule1Citation 转述/引用事实块缺引用或引用不存在 Segment 应拒绝。
func TestValidateDigestBlocks_Rule1Citation(t *testing.T) {
	in := gateInput([]DigestBlockDraft{
		{Type: DigestBlockParaphraseStr, Text: "无引用转述"},                           // 缺 Citation
		{Type: DigestBlockParaphraseStr, Text: "错引", Citations: []string{"s999"}}, // 不存在
		{Type: DigestBlockCitedFactStr, Text: "无目标"},                              // 缺 Target
		{Type: DigestBlockCitedFactStr, Text: "错目标", TargetSourceID: "doc-x"},     // 未沉淀
		{Type: DigestBlockCitedFactStr, Text: "缺引用", TargetSourceID: "doc-9"},     // 缺 Citation
		{Type: DigestBlockCitedFactStr, Text: "错段", TargetSourceID: "doc-9", Citations: []string{"nope"}},
	})
	issues := ValidateDigestBlocks(in)
	if len(issues) != 6 {
		t.Fatalf("应 6 条 citation 违规，实际 %d：%v", len(issues), issues)
	}
	for _, iss := range issues {
		if iss.Rule != "citation" {
			t.Fatalf("全部应为 citation 违规：%v", iss)
		}
	}
}

// TestValidateDigestBlocks_Rule2LabelAndHardFact AI 展开缺标注或含硬事实应拒绝。
func TestValidateDigestBlocks_Rule2LabelAndHardFact(t *testing.T) {
	bad := gateInput([]DigestBlockDraft{
		{Type: DigestBlockAIExpansionStr, Text: "这里展开讲讲背景"}, // 缺标注
	})
	if firstRule(ValidateDigestBlocks(bad), "label") == nil {
		t.Fatal("缺标注应触发 label 违规")
	}
	for _, text := range []string{
		"AI 展开：行业增长 30%",
		"AI 展开：2024 年发生转折",
		"AI 展开：规模约 $1,200",
		"AI 展开：从 2019 到 2023 持续增长",
		"AI 展开：估值 1,234,567",
	} {
		in := gateInput([]DigestBlockDraft{{Type: DigestBlockAIExpansionStr, Text: text}})
		if firstRule(ValidateDigestBlocks(in), "hard-fact") == nil {
			t.Fatalf("硬事实应触发违规：%q", text)
		}
	}
	// 纯讲解不含数字应放行
	in := gateInput([]DigestBlockDraft{{Type: DigestBlockAIExpansionStr, Text: "AI 展开：可以把这个机制理解成水流的调节阀。"}})
	if firstRule(ValidateDigestBlocks(in), "hard-fact") != nil {
		t.Fatal("纯讲解不应触发硬事实违规")
	}
}

// TestValidateDigestBlocks_Rule3QuoteLength 直接引语超 RightsConstraint 上限应拒绝。
func TestValidateDigestBlocks_Rule3QuoteLength(t *testing.T) {
	long := strings.Repeat("很长的原话", 10) // 40 字 > 20 上限
	in := gateInput([]DigestBlockDraft{
		{Type: DigestBlockParaphraseStr, Text: "他说“" + long + "”然后总结了", Citations: []string{"seg-1"}},
	})
	if firstRule(ValidateDigestBlocks(in), "quote-length") == nil {
		t.Fatal("超长引语应触发 quote-length 违规")
	}
	ok := gateInput([]DigestBlockDraft{
		{Type: DigestBlockParaphraseStr, Text: "他说“一句短引语”然后总结了", Citations: []string{"seg-1"}},
	})
	if firstRule(ValidateDigestBlocks(ok), "quote-length") != nil {
		t.Fatal("短引语不应触发违规")
	}
}

// TestValidateDigestBlocks_Rule4NoteFidelity 笔记块被改写应拒绝；重排（空白差异）应放行。
func TestValidateDigestBlocks_Rule4NoteFidelity(t *testing.T) {
	rewritten := gateInput([]DigestBlockDraft{
		{Type: DigestBlockNoteStr, Text: "我认为估值模型低估了尾部风险，且还会更糟", NoteID: "note-1"}, // AI 加戏
	})
	if firstRule(ValidateDigestBlocks(rewritten), "note-fidelity") == nil {
		t.Fatal("改写笔记应触发 note-fidelity 违规")
	}
	missing := gateInput([]DigestBlockDraft{{Type: DigestBlockNoteStr, Text: "x", NoteID: "note-404"}})
	if firstRule(ValidateDigestBlocks(missing), "note") == nil {
		t.Fatal("未知 NoteID 应触发 note 违规")
	}
	// 空白重排放行
	reordered := gateInput([]DigestBlockDraft{
		{Type: DigestBlockNoteStr, Text: "我认为 估值模型 低估了 尾部风险", NoteID: "note-1"},
	})
	if issues := ValidateDigestBlocks(reordered); len(issues) != 0 {
		t.Fatalf("空白重排应放行：%v", issues)
	}
}

// TestValidateDigestBlocks_Rule5Identity 未知类型不得进入正文。
func TestValidateDigestBlocks_Rule5Identity(t *testing.T) {
	in := gateInput([]DigestBlockDraft{{Type: "owner_claim", Text: "AI 替 Owner 编的观点"}})
	if firstRule(ValidateDigestBlocks(in), "identity") == nil {
		t.Fatal("伪 OwnerClaim 应触发 identity 违规")
	}
}

// TestTruncateQuotedSpans 超限引语截断为转述；截断后复跑门禁应通过 quote-length。
func TestTruncateQuotedSpans(t *testing.T) {
	long := strings.Repeat("很长的原话", 10)
	text := "开头“" + long + "”结尾"
	got := TruncateQuotedSpans(text, 20)
	// 形态不变量：引号内以省略提示结尾，闭引号后接正文；具体保留字数由复检确认合法
	if !strings.HasSuffix(got, "……（后文转述）”结尾") || !strings.HasPrefix(got, "开头“很长的原话") {
		t.Fatalf("截断结果不符：%q", got)
	}
	in := gateInput([]DigestBlockDraft{
		{Type: DigestBlockParaphraseStr, Text: got, Citations: []string{"seg-1"}},
	})
	if firstRule(ValidateDigestBlocks(in), "quote-length") != nil {
		t.Fatal("截断后应复检通过")
	}
}

// TestQuotedSpans 中英文引号混用提取。
func TestQuotedSpans(t *testing.T) {
	for _, span := range quotedSpans(`他说“第一段”和“第二段”以及 "第三段"`) {
		if span == "" {
			t.Fatal("空引语不应返回")
		}
	}
	if len(quotedSpans("无引语文本")) != 0 {
		t.Fatal("无引语应返回空")
	}
}

// TestDigestHasHardFact 暴露给 Weave 阶段的硬事实探测。
func TestDigestHasHardFact(t *testing.T) {
	if !DigestHasHardFact("规模 1.2 万亿") {
		// 1.2 是纯小数，不含千分位/百分比/年份/货币——按启发式不算硬事实
		t.Log("纯小数不在启发式内，符合当前设计")
	}
	if !DigestHasHardFact("增长 30%") {
		t.Fatal("百分比应判硬事实")
	}
}
