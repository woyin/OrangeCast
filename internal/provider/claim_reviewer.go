// claim_reviewer.go 独立语义审校（C11/R20 / ADR-0024 §6 / product-goal.md ClaimReview）。
//
// 与写作角色分离的独立审校角色，检查：
//   - 来源归因：SourceClaim 是否准确归因到来源（不冒充客观事实）；
//   - Owner 授权：OwnerClaim 是否与确认的主张一致；
//   - 综合边界：SynthesisClaim 是否伪装成来源原意；
//   - 事实核验：VerifiedFact 是否附引用；
//   - Brief 外表达：是否有确认主张之外的新主张。
//
// 不直接复制 EvidenceReviewer 状态到 ClaimReview；输出逐条问题（位置+类型）。
// 状态枚举统一为 passed|failed（R20）：passed findings 必须空，failed findings 必须
// 非空；excerpt 必须位于被审校正文内，引用的材料 ID 不得越出授权集合。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ClaimReview 状态枚举（Provider/Store 统一）。
const (
	ClaimReviewPassed = "passed"
	ClaimReviewFailed = "failed"
)

// ClaimReviewFinding 一条审校问题（含位置与严重程度）。
type ClaimReviewFinding struct {
	Excerpt   string `json:"excerpt"`   // 相关正文片段
	IssueKind string `json:"issueKind"` // misattributed | unauthorized_claim | synthesis_as_source | unverified_fact | outside_brief
	Detail    string `json:"detail"`
}

// ClaimReviewResult 独立审校结论。
type ClaimReviewResult struct {
	Status   string               `json:"status"` // passed | failed
	Findings []ClaimReviewFinding `json:"findings"`
}

// ClaimReviewRequest 审校输入：正文 + ClaimMap + 确认主张 + 授权素材 ID +
// 冻结授权材料全文（R20）：模型必须能读到材料原文才能判断来源归因；
// Materials 只能包含 AuthorizedIDs 内的材料，且 ClaimMap 引用的材料必须在其中。
type ClaimReviewRequest struct {
	Markdown       string            `json:"markdown"`
	ClaimMap       []ClaimMapEntry   `json:"claimMap"`
	ConfirmedClaim string            `json:"confirmedClaim"`
	AuthorizedIDs  []string          `json:"authorizedIds"`
	Materials      []ArticleMaterial `json:"materials"`
}

// ClaimReviewProvider 独立审校接口（Groq/OpenAI 实现）。
type ClaimReviewProvider interface {
	ReviewClaims(ctx context.Context, req ClaimReviewRequest) (*ClaimReviewResult, TaskUsage, error)
	Name() string
}

// validClaimReviewIssueKinds 合法的审校问题类型。
var validClaimReviewIssueKinds = map[string]bool{
	"misattributed": true, "unauthorized_claim": true, "synthesis_as_source": true,
	"unverified_fact": true, "outside_brief": true,
}

// ValidateClaimReviewResult 校验审校输出自洽性（R20）：
// status 必须是 passed|failed；passed findings 必须空；failed findings 必须非空、
// issueKind 合法且每条问题都给出位置与说明。
func ValidateClaimReviewResult(result *ClaimReviewResult) error {
	if result == nil {
		return fmt.Errorf("审校结论为空")
	}
	if result.Status != ClaimReviewPassed && result.Status != ClaimReviewFailed {
		return fmt.Errorf("非法审校结论 %q", result.Status)
	}
	if result.Status == ClaimReviewPassed && len(result.Findings) > 0 {
		return fmt.Errorf("passed 审校不得携带问题")
	}
	if result.Status == ClaimReviewFailed && len(result.Findings) == 0 {
		return fmt.Errorf("failed 审校必须列出问题")
	}
	for i, f := range result.Findings {
		if !validClaimReviewIssueKinds[f.IssueKind] {
			return fmt.Errorf("findings[%d] 非法问题类型 %q", i, f.IssueKind)
		}
		if strings.TrimSpace(f.Excerpt) == "" {
			return fmt.Errorf("findings[%d] 缺少正文片段", i)
		}
		if strings.TrimSpace(f.Detail) == "" {
			return fmt.Errorf("findings[%d] 缺少问题说明", i)
		}
	}
	return nil
}

// ValidateClaimReviewAgainstInput 对照冻结输入校验审校输出（R20）：
// 每条问题的 excerpt 必须位于被审校正文中；问题不得引用授权集合之外的材料。
func ValidateClaimReviewAgainstInput(result *ClaimReviewResult, req *ClaimReviewRequest) error {
	if err := ValidateClaimReviewResult(result); err != nil {
		return err
	}
	for i, f := range result.Findings {
		if !strings.Contains(req.Markdown, f.Excerpt) {
			return fmt.Errorf("findings[%d] excerpt 不在被审校正文中", i)
		}
	}
	authorized := map[string]bool{}
	for _, id := range req.AuthorizedIDs {
		authorized[id] = true
	}
	known := map[string]bool{}
	for _, m := range req.Materials {
		if !authorized[m.KeyPointID] {
			return fmt.Errorf("冻结材料集合夹带授权之外的材料 %q", m.KeyPointID)
		}
		known[m.KeyPointID] = true
	}
	for i, cm := range req.ClaimMap {
		if cm.Excerpt != "" && !strings.Contains(req.Markdown, cm.Excerpt) {
			return fmt.Errorf("ClaimMap[%d] excerpt 不在被审校正文中", i)
		}
		for _, id := range cm.MaterialIDs {
			if !authorized[id] {
				return fmt.Errorf("ClaimMap[%d] 引用授权之外的材料 %q", i, id)
			}
			if !known[id] {
				return fmt.Errorf("ClaimMap[%d] 引用的材料 %q 不在冻结材料中", i, id)
			}
		}
	}
	return nil
}

const claimReviewSystemPrompt = `你是独立的主张审校员。检查文章中每个实质性段落的身份是否正确：
- source_claim：是否准确归因到来源（不得写成客观事实）；
- owner_claim：是否与确认主张一致（不得引入新主张）；
- synthesis_claim：是否明确标注为综合（不得伪装成来源原意）；
- verified_fact：是否附引用（未经核验的数字/预测/争议判断必须有来源归因）。
每个材料的原始内容与引用附在下方，归因判断必须以材料原文为准，不得使用外部知识。
发现问题则输出 status="failed" 并列出每条问题的位置（excerpt 必须逐字摘自正文）与类型（issueKind：misattributed/unauthorized_claim/synthesis_as_source/unverified_fact/outside_brief）；完全无问题时输出 status="passed" 且 findings 为空数组。只输出 JSON {"status":"passed"或"failed","findings":[{"excerpt":"...","issueKind":"...","detail":"..."}]}。`

// BuildClaimReviewPrompt 构造审校用户消息：确认主张 + ClaimMap + 每份授权材料的
// 原文与引用 + 全文（R20：模型需要材料原文才能判断归因）。
func BuildClaimReviewPrompt(req ClaimReviewRequest) string {
	var sb strings.Builder
	sb.WriteString("确认主张：" + req.ConfirmedClaim + "\n\n")
	sb.WriteString("ClaimMap（文章的主张身份映射）：\n")
	for _, cm := range req.ClaimMap {
		sb.WriteString(fmt.Sprintf("片段：%s\n类型：%s\n材料：%s\n\n", cm.Excerpt, cm.ClaimKind, strings.Join(cm.MaterialIDs, ",")))
	}
	sb.WriteString("\n授权材料原文：\n")
	for _, m := range req.Materials {
		sb.WriteString(fmt.Sprintf("材料 %s｜来源：%s｜类型：%s\n内容：%s\n引用：%s\n\n",
			m.KeyPointID, m.SourceTitle, m.SourceType, m.Content, strings.Join(m.Citations, ",")))
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
		{"role": "system", "content": claimReviewSystemPrompt},
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
