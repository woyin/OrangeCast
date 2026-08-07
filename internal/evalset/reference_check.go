package evalset

import (
	"github.com/woyin/orangecast/internal/provider"
)

// ReferenceCheckSample 主题锚定校验的评测样本（ADR-0018 R3）。
// ReferenceCheck 是 StudyChat 信任的根：校验器的误杀（合法被判不相关）与漏放（非法被判相关）
// 都进 EvalSet 作为同等公民接受评测。本集合即 R3 grilling 中定义的 6 个场景：
// 3 个放行（合法 GeneratedDerivative）+ 3 个挡住（虚挂/漂移/脱离）。
type ReferenceCheckSample struct {
	ID                string
	Segments          []provider.Segment // 参考片段原文
	Question          string
	Answer            string
	ExpectRelated     bool // 期望校验器判定 related=true（放行）或 false（挡住）
	ExpectDescription string
}

// ReferenceCheckSamples 主题锚定校验样本集（R3 的 6 个场景）。
var ReferenceCheckSamples = []ReferenceCheckSample{
	// —— 放行（合法 GeneratedDerivative：解释/重述、类比例化、结构重组）——
	{
		ID: "rc-pass-01",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 10, Text: "通胀是物价总水平持续上升，货币购买力下降。"},
		},
		Question:          "能不能用买房举例说明通胀？",
		Answer:            "好的。假设你今天买房要 100 万，通胀让物价上涨，明年同样的房子可能要 110 万——这就是货币购买力下降的体现，正如本集提到的通胀定义。",
		ExpectRelated:     true,
		ExpectDescription: "类比例化：用买房例子说明参考片段中的通胀概念，主题扎根。",
	},
	{
		ID: "rc-pass-02",
		Segments: []provider.Segment{
			{ID: "seg-0002", Start: 0, End: 8, Text: "量化宽松是央行购买长期债券以压低长期利率。"},
		},
		Question:          "量化宽松是什么意思？",
		Answer:            "量化宽松是一种货币政策：央行直接在市场上买入长期债券，目的是把长期利率压低，从而刺激投资和消费。简单说就是央行通过买债向市场注入资金。",
		ExpectRelated:     true,
		ExpectDescription: "解释/重述：用别的话重新讲参考片段中的量化宽松定义。",
	},
	{
		ID: "rc-pass-03",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 6, Text: "第一步，设定目标。"},
			{ID: "seg-0002", Start: 6, End: 12, Text: "第二步，拆解任务。"},
			{ID: "seg-0003", Start: 12, End: 18, Text: "第三步，执行并复盘。"},
		},
		Question:          "这一章的逻辑我没懂，能不能换个顺序讲？",
		Answer:            "这三步其实是一个闭环：先做拆解，再执行，最后用复盘反过来调整目标。原片段按 1-2-3 线性讲，但从闭环角度看，目标也会被复盘反过来塑造。",
		ExpectRelated:     true,
		ExpectDescription: "结构重组：把参考片段的线性步骤重组为闭环视角，主题仍是这些步骤。",
	},
	// —— 挡住（虚挂/主题漂移/脱离内容）——
	{
		ID: "rc-block-01",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 10, Text: "通胀是物价总水平持续上升，货币购买力下降。"},
		},
		Question:          "明年利率会到多少？我该不该卖房？",
		Answer:            "我预测明年利率会降到 2%，建议你现在卖房锁定利润。正如本集通胀段所述，购买力在变化。",
		ExpectRelated:     false,
		ExpectDescription: "主题漂移：回答主体是预测与投资建议（参考片段未涉及），仅顺带提了一句原文。",
	},
	{
		ID: "rc-block-02",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 10, Text: "通胀是物价总水平持续上升，货币购买力下降。"},
		},
		Question:          "你觉得这个主播怎么样？",
		Answer:            "这个主播表达很清晰，节奏也好。",
		ExpectRelated:     false,
		ExpectDescription: "脱离内容：评价主播，与参考片段内容无关。",
	},
	{
		ID: "rc-block-03",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 10, Text: "通胀是物价总水平持续上升，货币购买力下降。"},
		},
		Question:          "告诉我一个和通胀无关的冷知识。",
		Answer:            "蜂鸟是唯一能倒着飞的鸟。正如本集通胀段所提到的购买力，蜂鸟的能量消耗也很大。",
		ExpectRelated:     false,
		ExpectDescription: "措辞蹭原文：回答主题是蜂鸟（与通胀无概念联系），仅在措辞上蹭了一句原文。",
	},
}

// ReferenceCheckIssue 一条校验器评测问题。
type ReferenceCheckIssue struct {
	SampleID string
	Got      bool
	Want     bool
	Reason   string
}

// CheckReferenceSamples 用给定校验器跑全部 ReferenceCheck 样本，返回不符项（空 = 全部正确）。
// 这是把 ReferenceCheck 作为 EvalSet 同等公民的自动评测入口（ADR-0018 R3）。
func CheckReferenceSamples(checker provider.ReferenceChecker) []ReferenceCheckIssue {
	var issues []ReferenceCheckIssue
	if checker == nil {
		return []ReferenceCheckIssue{{SampleID: "(nil-checker)", Got: false, Want: false, Reason: "未提供校验器"}}
	}
	for _, smp := range ReferenceCheckSamples {
		res, err := checker.CheckReference(smp.Question, smp.Answer, smp.Segments)
		if err != nil {
			issues = append(issues, ReferenceCheckIssue{SampleID: smp.ID, Got: false, Want: smp.ExpectRelated, Reason: "校验报错: " + err.Error()})
			continue
		}
		if res.Related != smp.ExpectRelated {
			issues = append(issues, ReferenceCheckIssue{SampleID: smp.ID, Got: res.Related, Want: smp.ExpectRelated, Reason: res.Reason})
		}
	}
	return issues
}
