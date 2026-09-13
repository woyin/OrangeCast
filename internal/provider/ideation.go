// ideation.go 定向构思的材料诊断（C03 / ADR-0024 §1）。
// 输入：Owner 的当前问题/约束 + 前文轮次 + 冻结材料快照（ID+原文+引用）。
// 输出：支持/反驳/补充、缺口与少量可选主张——每项必须引用输入材料 ID；
// 引用了输入之外的材料（虚构）→ ValidateIdeationDiagnosis 显式拒绝。
// 空材料不是错误：输出只含缺口（诚实，不伪造支持）。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// IdeationMaterial 一条冻结材料。
type IdeationMaterial struct {
	ID          string
	SourceTitle string
	Content     string
	Citations   []string
}

// IdeationDiagnosisRequest 诊断输入（全部来自冻结快照）。
type IdeationDiagnosisRequest struct {
	Question    string
	Constraints string
	PriorRounds []string // 前文轮次的用户输入（多轮上下文）
	Materials   []IdeationMaterial
}

// DiagnosisItem 与具体材料关联的一条诊断。
type DiagnosisItem struct {
	MaterialID string `json:"materialId"`
	Text       string `json:"text"`
}

// ProposedClaimItem 可选主张（尚非 OwnerClaim）。
type ProposedClaimItem struct {
	Claim       string   `json:"claim"`
	MaterialIDs []string `json:"materialIds"`
}

// IdeationDiagnosis 诊断输出。
type IdeationDiagnosis struct {
	Supports       []DiagnosisItem     `json:"supports"`
	Contradicts    []DiagnosisItem     `json:"contradicts"`
	Supplements    []DiagnosisItem     `json:"supplements"`
	Gaps           []string            `json:"gaps"`
	ProposedClaims []ProposedClaimItem `json:"proposedClaims"`
}

// ToJSON 供存储层持久化。
func (d *IdeationDiagnosis) ToJSON() (string, error) {
	b, err := json.Marshal(d)
	return string(b), err
}

// IdeationDiagnosisProvider 诊断能力（Groq/OpenAI 实现）。
type IdeationDiagnosisProvider interface {
	DiagnoseIdeation(ctx context.Context, req IdeationDiagnosisRequest) (*IdeationDiagnosis, TaskUsage, error)
	Name() string
}

// ValidateIdeagnosisInput 输入校验。
func ValidateIdeagnosisInput(req IdeationDiagnosisRequest) error {
	if strings.TrimSpace(req.Question) == "" {
		return fmt.Errorf("诊断缺少问题")
	}
	return nil
}

// ValidateIdeationDiagnosis 校验诊断输出没有越界（C03）：
// 每个诊断项与主张引用的材料 ID 必须来自输入快照；虚构引用显式拒绝。
func ValidateIdeationDiagnosis(diag *IdeationDiagnosis, req IdeationDiagnosisRequest) []error {
	ids := map[string]bool{}
	for _, m := range req.Materials {
		ids[m.ID] = true
	}
	var errs []error
	check := func(items []DiagnosisItem, kind string) {
		for _, it := range items {
			if !ids[it.MaterialID] {
				errs = append(errs, fmt.Errorf("%s 引用了输入之外的材料 %q", kind, it.MaterialID))
			}
		}
	}
	check(diag.Supports, "支持")
	check(diag.Contradicts, "反驳")
	check(diag.Supplements, "补充")
	for _, pc := range diag.ProposedClaims {
		for _, id := range pc.MaterialIDs {
			if !ids[id] {
				errs = append(errs, fmt.Errorf("主张引用了输入之外的材料 %q", id))
			}
		}
	}
	return errs
}

// BuildIdeationPrompt 构造诊断用户消息（材料以 ID 标注，要求输出逐项关联 ID）。
func BuildIdeationPrompt(req IdeationDiagnosisRequest) string {
	var sb strings.Builder
	sb.WriteString("Owner 的问题：" + req.Question + "\n")
	if req.Constraints != "" && req.Constraints != "{}" {
		sb.WriteString("约束：" + req.Constraints + "\n")
	}
	for i, r := range req.PriorRounds {
		sb.WriteString(fmt.Sprintf("前文轮次 %d：%s\n", i+1, r))
	}
	sb.WriteString("\n可用材料（诊断只能引用这些 ID）：\n")
	for _, m := range req.Materials {
		sb.WriteString(fmt.Sprintf("[%s] %s：%s\n", m.ID, m.SourceTitle, m.Content))
	}
	sb.WriteString(`
请输出 JSON：{"supports":[{"materialId":"...","text":"..."}],"contradicts":[...],"supplements":[...],
"gaps":["材料未覆盖的问题"],"proposedClaims":[{"claim":"...","materialIds":["..."]}]}
要求：超出材料的问题放入 gaps，不要编造；主张必须引用材料 ID；材料之间相互冲突时同时给出支持与反驳。`)
	return sb.String()
}

const ideationSystemPrompt = `你是严格基于材料的构思助手。只依据给出的材料 ID 与原文进行诊断；
超出材料的问题放入 gaps；引用必须使用输入中的材料 ID，不得编造。只输出 JSON。`

// DiagnoseIdeation Groq 诊断实现（C03）。
func (g *GroqProvider) DiagnoseIdeation(ctx context.Context, req IdeationDiagnosisRequest) (*IdeationDiagnosis, TaskUsage, error) {
	if err := ValidateIdeagnosisInput(req); err != nil {
		return nil, TaskUsage{}, err
	}
	content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
		{"role": "system", "content": ideationSystemPrompt},
		{"role": "user", "content": BuildIdeationPrompt(req)},
	}, "object")
	if err != nil {
		return nil, usage, err
	}
	diag := &IdeationDiagnosis{}
	if err := parseJSONLoose(content, diag); err != nil {
		return nil, usage, fmt.Errorf("解析诊断输出: %w", err)
	}
	return diag, usage, nil
}

// DiagnoseIdeation OpenAI 诊断实现（C03），与 Groq 同一契约。
func (o *OpenAIProvider) DiagnoseIdeation(ctx context.Context, req IdeationDiagnosisRequest) (*IdeationDiagnosis, TaskUsage, error) {
	if err := ValidateIdeagnosisInput(req); err != nil {
		return nil, TaskUsage{}, err
	}
	payload := map[string]any{
		"model":        o.effectiveAnalysisModel(),
		"instructions": ideationSystemPrompt,
		"input":        BuildIdeationPrompt(req),
	}
	data, meta, err := o.chatCompleteWithMeta(ctx, payload, "构思诊断")
	if err != nil {
		return nil, TaskUsage{}, err
	}
	var r struct {
		OutputText string `json:"output_text"`
	}
	_ = json.Unmarshal(data, &r)
	diag := &IdeationDiagnosis{}
	if err := parseJSONLoose(r.OutputText, diag); err != nil {
		return nil, chatUsage(data, meta), fmt.Errorf("解析诊断输出: %w", err)
	}
	return diag, chatUsage(data, meta), nil
}
