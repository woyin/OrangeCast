package provider

import "testing"

// fakeChatFn 构造一个总是返回固定 JSON 的 chatCompleteFn。
func fakeChatFn(output string) func([]map[string]string, string) (string, int, error) {
	return func(_ []map[string]string, _ string) (string, int, error) {
		return output, 10, nil
	}
}

// TestStudyChat_ScopeTether_NoReferenceNoGeneration (ADR-0018 R3 硬约束一)
// 模型返回空 referenceSegmentIds → 必须不生成，返回 ScopeFeedback。
func TestStudyChat_ScopeTether_NoReferenceNoGeneration(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`{"answer":"","referenceSegmentIds":[]}`)
	candidates := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}}
	res, err := g.StudyChatAnswer("明年利率多少？", nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != nil {
		t.Errorf("无 Reference 不应生成回答，实际得到 %+v", res.Answer)
	}
	if res.ScopeFeedback == "" {
		t.Error("无 Reference 时应返回可见 ScopeFeedback")
	}
}

// TestStudyChat_ScopeTether_NoCandidates (ADR-0018 R3 硬约束一)
// 无候选 Segment → 直接不生成。
func TestStudyChat_ScopeTether_NoCandidates(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`{"answer":"x","referenceSegmentIds":["seg-0001"]}`)
	res, err := g.StudyChatAnswer("anything", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != nil || res.ScopeFeedback == "" {
		t.Errorf("无候选 Segment 应不生成并给反馈，实际 %+v", res)
	}
}

// TestStudyChat_FabricatedReferenceIDRejected
// 模型返回不存在的 Segment ID → 视为无法关联，不生成。
func TestStudyChat_FabricatedReferenceIDRejected(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`{"answer":"讲解","referenceSegmentIds":["seg-9999"]}`)
	candidates := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	res, err := g.StudyChatAnswer("解释通胀", nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != nil {
		t.Errorf("编造的 Reference ID 应被拒绝不生成，实际 %+v", res.Answer)
	}
}

// TestStudyChat_ValidAnswerPassesScopeTether
// 模型返回真实存在的 Reference ID 且非空回答 → 生成（硬约束一通过）。
func TestStudyChat_ValidAnswerPassesScopeTether(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`{"answer":"通胀是物价上升","referenceSegmentIds":["seg-0001"]}`)
	candidates := []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价总水平持续上升"}}
	res, err := g.StudyChatAnswer("解释通胀", nil, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer == nil {
		t.Fatal("有效 Reference 应生成回答")
	}
	if len(res.Answer.ReferenceSegmentIDs) != 1 || res.Answer.ReferenceSegmentIDs[0] != "seg-0001" {
		t.Errorf("Reference ID 应保留真实片段，实际 %+v", res.Answer.ReferenceSegmentIDs)
	}
}

// TestReferenceCheck_RelatedJudgment (ADR-0018 R3 硬约束二)
// 校验器返回 related=true/false 时结果正确传递。
func TestReferenceCheck_RelatedJudgment(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`{"related":true,"reason":"主题扎根"}`)
	res, err := g.CheckReference("q", "a", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Related {
		t.Error("related=true 应被正确解析")
	}

	g2 := NewGroqProvider("test")
	g2.chatCompleteFn = fakeChatFn(`{"related":false,"reason":"主题漂移"}`)
	res2, err := g2.CheckReference("q", "a", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Related {
		t.Error("related=false 应被正确解析")
	}
}

// TestReferenceCheck_ParseFailureConservativeReject
// 校验响应解析失败时保守判为不相关（宁误杀不放行虚挂）。
func TestReferenceCheck_ParseFailureConservativeReject(t *testing.T) {
	g := NewGroqProvider("test")
	g.chatCompleteFn = fakeChatFn(`not valid json`)
	res, err := g.CheckReference("q", "a", []Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Related {
		t.Error("解析失败应保守判为不相关")
	}
}
