// claim_reviewer.go 独立语义审校（C11 / ADR-0024 §6 / product-goal.md ClaimReview）。
//
// 与写作角色分离的独立审校角色，检查：
//   - 来源归因：SourceClaim 是否准确归因到来源（不冒充客观事实）；
//   - Owner 授权：OwnerClaim 是否与确认的主张一致；
//   - 综合边界：SynthesisClaim 是否伪装成来源原意；
//   - 事实核验：VerifiedFact 是否附引用；
//   - Brief 外表达：是否有确认主张之外的新主张。
//
// 不直接复制 EvidenceReviewer 状态到 ClaimReview；输出逐条问题（位置+类型）。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ClaimReviewFinding 一条审校问题（含位置与严重程度）。
type ClaimReviewFinding struct {
	Excerpt   string `json:"excerpt"`   // 相关正文片段
	IssueKind string `json:"issueKind"` // misattributed | unauthorized_claim | synthesis_as_source | unverified_fact | outside_brief
	Detail    string `json:"detail"`
}

// ClaimReviewResult 独立审校结论。
type ClaimReviewResult struct {
	Status   string               `json:"status"` // pass | fail
	Findings []ClaimReviewFinding `json:"findings"`
}

// ClaimReviewRequest 审校输入：正文 + ClaimMap + 确认主张 + 授权素材 ID。
type ClaimReviewRequest struct {
	Markdown       string          `json:"markdown"`
	ClaimMap       []ClaimMapEntry `json:"claimMap"`
	ConfirmedClaim string          `json:"confirmedClaim"`
	AuthorizedIDs  []string        `json:"authorizedIds"`
}

// ClaimReviewProvider 独立审校接口（Groq/OpenAI 实现）。
type ClaimReviewProvider interface {
	ReviewClaims(ctx context.Context, req ClaimReviewRequest) (*ClaimReviewResult, TaskUsage, error)
	Name() string
}

// ValidateClaimReviewResult 校验审校输出（C11）：status 必须合法。
func ValidateClaimReviewResult(result *ClaimReviewResult) error {
	if result.Status != "pass" && result.Status != "fail" {
		return fmt.Errorf("非法审校结论 %q", result.Status)
	}
	return nil
}

const claimReviewSystemPrompt = `你是独立的主张审校员。检查文章中每个实质性段落的身份是否正确：
- source_claim：是否准确归因到来源（不得写成客观事实）；
- owner_claim：是否与确认主张一致（不得引入新主张）；
- synthesis_claim：是否明确标注为综合（不得伪装成来源原意）；
- verified_fact：是否附引用（未经核验的数字/预测/争议判断必须有来源归因）。
发现问题则输出 status=fail 并列出每条问题的位置与类型。只输出 JSON。`

// BuildClaimReviewPrompt 构造审校用户消息。
func BuildClaimReviewPrompt(req ClaimReviewRequest) string {
	var sb strings.Builder
	sb.WriteString("确认主张：" + req.ConfirmedClaim + "\n\n")
	sb.WriteString("ClaimMap（文章的主张身份映射）：\n")
	for _, cm := range req.ClaimMap {
		sb.WriteString(fmt.Sprintf("片段：%s\n类型：%s\n材料：%s\n\n", cm.Excerpt, cm.ClaimKind, strings.Join(cm.MaterialIDs, ",")))
	}
	sb.WriteString("\n全文：\n" + req.Markdown + "\n")
	return sb.String()
}

// parseClaimReviewResult 解析审校 JSON 输出。
func parseClaimReviewResult(content string) (*ClaimReviewResult, error) {
	result := &ClaimReviewResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, fmt.Errorf("解析审校输出: %w", err)
	}
	if err := ValidateClaimReviewResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ReviewClaims Groq 独立审校。
func (g *GroqProvider) ReviewClaims(ctx context.Context, req ClaimReviewRequest) (*ClaimReviewResult, TaskUsage, error) {
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": claimReviewSystemPrompt + "\n必须只输出一个 JSON 对象。"},
		{"role": "user", "content": BuildClaimReviewPrompt(req)},
	}, "object")
	if err != nil {
		return nil, usage, err
	}
	result, err := parseClaimReviewResult(content)
	return result, usage, err
}

// ReviewClaims OpenAI 独立审校。
func (o *OpenAIProvider) ReviewClaims(ctx context.Context, req ClaimReviewRequest) (*ClaimReviewResult, TaskUsage, error) {
	payload := map[string]any{
		"model":        o.effectiveAnalysisModel(),
		"instructions": claimReviewSystemPrompt,
		"input":        BuildClaimReviewPrompt(req),
	}
	data, meta, err := o.chatCompleteWithMeta(ctx, payload, "主张审校")
	if err != nil {
		return nil, TaskUsage{}, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	result, err := parseClaimReviewResult(r.OutputText)
	return result, chatUsage(data, meta), err
}
