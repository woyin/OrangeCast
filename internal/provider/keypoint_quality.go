// keypoint_quality.go 自动重点的可解释质量判定（K02 / ADR-0024 §5）。
// 两层判定：
//  1. 程序检查（不依赖模型）：空内容、引用不存在、同源明显重复、背景标题启发式——
//     直接给出 invalid，原因明确；
//  2. 独立判定步骤：只参考"被引用 Segment 的原文"判断证据是否支持、观点能否独立
//     表达、是否纯背景——生成模型不给自己盖章，判定与生成是分开的角色。
//
// 模型不可用或判定失败时返回错误，由调用方保留 needs_review，不伪装通过。
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// KeypointQualityInput 单个重点的判定输入。
type KeypointQualityInput struct {
	KeyPointID      string
	Content         string
	Description     string
	Citations       []string
	Segments        []Segment // 本集 Segment（用于校验引用存在与提取判定依据原文）
	SameSourceTexts []string  // 同源既有重点文本（明显重复的程序检查）
}

// KeypointQualityVerdict 判定结论。
type KeypointQualityVerdict struct {
	Decision string   `json:"decision"` // ready | needs_review | invalid
	Reasons  []string `json:"reasons"`
}

// FingerprintKeypoint 内容指纹（K02）：内容+描述+引用共同决定判定结果的复用身份。
func FingerprintKeypoint(content, description string, citations []string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{"v1", content, description, strings.Join(citations, ",")}, "\x00")))
	return hex.EncodeToString(h[:])
}

// citedSegmentText 提取被引用 Segment 的原文（判定依据；不让模型凭引用 ID 想象）。
func citedSegmentText(input KeypointQualityInput) string {
	set := map[string]bool{}
	for _, c := range input.Citations {
		set[c] = true
	}
	var parts []string
	for _, seg := range input.Segments {
		if set[seg.ID] {
			parts = append(parts, "["+seg.ID+"] "+seg.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// PrecheckKeypointQuality 程序检查（K02 第一层）。返回 (结论, 是否终审)：
// 终审为 true 时不再调用模型。
func PrecheckKeypointQuality(input KeypointQualityInput) (KeypointQualityVerdict, bool) {
	content := strings.TrimSpace(input.Content)
	var reasons []string
	if content == "" {
		return KeypointQualityVerdict{Decision: models.KPQualityInvalid, Reasons: []string{"内容为空"}}, true
	}
	if len(input.Citations) == 0 {
		return KeypointQualityVerdict{Decision: models.KPQualityInvalid, Reasons: []string{"没有引用任何 Segment"}}, true
	}
	segIDs := map[string]bool{}
	for _, seg := range input.Segments {
		segIDs[seg.ID] = true
	}
	for _, c := range input.Citations {
		if !segIDs[c] {
			reasons = append(reasons, fmt.Sprintf("引用 %s 不存在于本集", c))
		}
	}
	if len(reasons) > 0 {
		return KeypointQualityVerdict{Decision: models.KPQualityInvalid, Reasons: reasons}, true
	}
	// 同源明显重复（与既有重点完全同文）。
	for _, t := range input.SameSourceTexts {
		if strings.TrimSpace(t) == content {
			return KeypointQualityVerdict{Decision: models.KPQualityInvalid, Reasons: []string{"与本集既有重点完全重复"}}, true
		}
	}
	// 背景标题启发式：过短且无实质描述。
	if len([]rune(content)) < 8 && strings.TrimSpace(input.Description) == "" {
		return KeypointQualityVerdict{Decision: models.KPQualityInvalid, Reasons: []string{"疑似背景标题：内容过短且无补充说明"}}, true
	}
	return KeypointQualityVerdict{}, false
}

// keypointQualityUserPrompt 构造独立判定的用户消息：观点 + 被引原文（不含整集全文）。
func keypointQualityUserPrompt(input KeypointQualityInput) string {
	cited := citedSegmentText(input)
	if cited == "" {
		cited = "（引用片段无原文）"
	}
	return fmt.Sprintf(`请判定以下重点的质量。

重点内容：%s
补充说明：%s
引用片段原文：
%s

判定标准：
1. 证据是否支持：引用片段的原文是否支持该观点（支持判 ready）；
2. 观点能否独立表达：脱离上下文仍可理解（含必要限定条件）；
3. 是否纯背景：章节标题、寒暄、广告或无信息量的句子（判 invalid）。
证据不足或无法确定时判 needs_review，不要猜。
只输出 JSON：{"decision":"ready|needs_review|invalid","reasons":["..."]}`,
		input.Content, input.Description, cited)
}

// parseQualityVerdict 宽松解析模型判定输出并校验取值。
func parseQualityVerdict(content string) (*KeypointQualityVerdict, error) {
	v := &KeypointQualityVerdict{}
	if err := parseJSONLoose(content, v); err != nil {
		return nil, fmt.Errorf("解析质量判定输出: %w", err)
	}
	switch v.Decision {
	case models.KPQualityReady, models.KPQualityNeedsReview, models.KPQualityInvalid:
	default:
		return nil, fmt.Errorf("非法判定结论 %q", v.Decision)
	}
	if v.Reasons == nil {
		v.Reasons = []string{}
	}
	return v, nil
}

// AssessKeypointQualityGroq Groq 的独立判定实现（K02）。
func (g *GroqProvider) AssessKeypointQuality(ctx context.Context, input KeypointQualityInput) (*KeypointQualityVerdict, TaskUsage, error) {
	if verdict, final := PrecheckKeypointQuality(input); final {
		return &verdict, TaskUsage{}, nil
	}
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": "你是严格的重点质量审核员。只依据给出的引用片段原文判定，不引入外部知识。只输出 JSON。"},
		{"role": "user", "content": keypointQualityUserPrompt(input)},
	}, "object")
	if err != nil {
		return nil, usage, err
	}
	verdict, err := parseQualityVerdict(content)
	return verdict, usage, err
}

// AssessKeypointQualityOpenAI OpenAI 的独立判定实现（K02），与 Groq 同一契约。
func (o *OpenAIProvider) AssessKeypointQuality(ctx context.Context, input KeypointQualityInput) (*KeypointQualityVerdict, TaskUsage, error) {
	if verdict, final := PrecheckKeypointQuality(input); final {
		return &verdict, TaskUsage{}, nil
	}
	payload := map[string]any{
		"model":        o.effectiveAnalysisModel(),
		"instructions": "你是严格的重点质量审核员。只依据给出的引用片段原文判定，不引入外部知识。输出 JSON：{\"decision\":\"ready|needs_review|invalid\",\"reasons\":[]}",
		"input":        keypointQualityUserPrompt(input),
	}
	data, meta, err := o.chatCompleteWithMeta(ctx, payload, "重点质量判定")
	if err != nil {
		return nil, TaskUsage{}, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	verdict, err := parseQualityVerdict(r.OutputText)
	return verdict, chatUsage(data, meta), err
}
