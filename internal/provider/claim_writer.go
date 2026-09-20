// claim_writer.go Writer v2 契约（C09 / ADR-0024 §6）。
//
// 新版请求携带确认的 OwnerClaim、来源主张、允许的综合与已核验事实；
// 输出正文 + ClaimMap（每段正文标明主张类型与材料身份）。
// 保留旧 Writer v1 接口不变（兼容旧 Brief 与旧 Writer 输出）。
// 程序校验：ClaimMap 的每个主张引用的材料必须来自 Brief 的授权材料集；
// 正文片段必须非空；主张类型必须合法。独立语义审校留给 C11。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ClaimWriterPromptVersion is the durable contract version for R19 writer jobs.
const ClaimWriterPromptVersion = "claim-writer-v2"

// ClaimKind 主张类型（C09 / product-goal.md ClaimMap）。
const (
	ClaimSource    = "source_claim"    // 来源表达过什么
	ClaimOwner     = "owner_claim"     // Owner 愿意承担什么
	ClaimSynthesis = "synthesis_claim" // 基于多项素材的综合推论
	ClaimVerified  = "verified_fact"   // 已单独核验的事实
)

// ClaimMapEntry 正文片段的主张映射。
type ClaimMapEntry struct {
	Excerpt      string   `json:"excerpt"`      // 正文片段（逐字引用正文）
	ClaimKind    string   `json:"claimKind"`    // source_claim | owner_claim | synthesis_claim | verified_fact
	MaterialIDs  []string `json:"materialIds"`  // 关联的 KeyPoint/Note ID
	SourceTitle  string   `json:"sourceTitle"`  // SourceClaim 时必填
	CitationRefs []string `json:"citationRefs"` // 关联 Citation
}

// ClaimAwareWritingRequest Writer v2 请求。
type ClaimAwareWritingRequest struct {
	Title             string              `json:"title"`
	Audience          string              `json:"audience"`
	Outline           string              `json:"outline"`
	Style             string              `json:"style"`
	SourceAttribution string              `json:"sourceAttribution"`
	ConfirmedClaim    string              `json:"confirmedClaim"`          // Owner 主张
	TargetLength      *int                `json:"targetLength,omitempty"`  // 目标篇幅（字；可空）
	Materials         []ArticleMaterial   `json:"materials"`               // 授权素材
	OwnerNotes        []ClaimOwnerNote    `json:"ownerNotes,omitempty"`    // 个人笔记快照
	VerifiedFacts     []ClaimVerifiedFact `json:"verifiedFacts,omitempty"` // 已核验事实及引用
}

// ClaimOwnerNote 个人笔记快照。
type ClaimOwnerNote struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// ClaimVerifiedFact 已核验事实及引用。
type ClaimVerifiedFact struct {
	Text      string   `json:"text"`
	Citations []string `json:"citations"`
}

// ClaimAwareWritingResult Writer v2 输出。
type ClaimAwareWritingResult struct {
	Title    string          `json:"title"`
	Markdown string          `json:"markdown"`
	ClaimMap []ClaimMapEntry `json:"claimMap"`
	Usage    TaskUsage       `json:"-"`
}

// ClaimAwareWriter Writer v2 接口（C09）。
type ClaimAwareWriter interface {
	WriteArticleWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error)
	Name() string
}

// claimSystemPrompt v2 系统提示词。
const claimSystemPrompt = `你是基于证据的写作者。根据给定的材料与确认主张生成一篇文章，并为每个实质性段落标注主张类型和材料 ID。

规则：
- SourceClaim：引用来源观点，必须注明来源标题与材料 ID
- OwnerClaim：写 Owner 的判断，不得伪装成来源观点
- SynthesisClaim：基于多项素材的综合推论，明确标注为综合
- VerifiedFact：单独核验的事实，附引用
- 不得引入材料之外的新主张或新事实

只输出 JSON：{"title":"...","markdown":"...","claimMap":[{"excerpt":"正文片段","claimKind":"...","materialIds":["..."],"sourceTitle":"..."}]}`

// BuildClaimPrompt 构造 v2 用户消息。
func BuildClaimPrompt(req ClaimAwareWritingRequest) string {
	var sb strings.Builder
	sb.WriteString("确认主张：" + req.ConfirmedClaim + "\n")
	sb.WriteString("目标读者：" + req.Audience + "\n")
	sb.WriteString("结构：\n" + req.Outline + "\n")
	if req.Style != "" {
		sb.WriteString("风格：" + req.Style + "\n")
	}
	if req.TargetLength != nil {
		sb.WriteString(fmt.Sprintf("目标篇幅：%d 字\n", *req.TargetLength))
	}
	sb.WriteString("来源标注：" + req.SourceAttribution + "\n")
	sb.WriteString("\n可用材料（引用只能用这些 KeyPoint ID）：\n")
	for _, m := range req.Materials {
		sb.WriteString(fmt.Sprintf("[%s] %s：%s\n", m.KeyPointID, m.SourceTitle, m.Content))
	}
	for _, n := range req.OwnerNotes {
		sb.WriteString(fmt.Sprintf("个人笔记 [%s]：%s\n", n.ID, n.Content))
	}
	for _, vf := range req.VerifiedFacts {
		sb.WriteString(fmt.Sprintf("已核验事实：%s（引用：%s）\n", vf.Text, strings.Join(vf.Citations, ",")))
	}
	return sb.String()
}

// ValidateClaimMap 程序校验 ClaimMap（C09）：
//   - 每条条目的 Excerpt 必须在正文中逐字出现（正文片段存在）；
//   - ClaimKind 必须合法；
//   - SourceClaim 必须有 MaterialIDs；
//   - 所有引用的 MaterialID 必须来自请求授权素材（材料成员身份）。
//   - 独立语义审校（C11）不在此处。
func ValidateClaimMap(result *ClaimAwareWritingResult, req ClaimAwareWritingRequest) []error {
	authorized := map[string]bool{}
	for _, m := range req.Materials {
		authorized[m.KeyPointID] = true
	}
	validKinds := map[string]bool{
		ClaimSource: true, ClaimOwner: true, ClaimSynthesis: true, ClaimVerified: true,
	}
	var errs []error
	for i, cm := range result.ClaimMap {
		if !validKinds[cm.ClaimKind] {
			errs = append(errs, fmt.Errorf("claimMap[%d] 非法主张类型 %q", i, cm.ClaimKind))
		}
		if cm.Excerpt == "" || !strings.Contains(result.Markdown, cm.Excerpt) {
			errs = append(errs, fmt.Errorf("claimMap[%d] 正文片段不存在或不完整", i))
		}
		if cm.ClaimKind == ClaimSource && len(cm.MaterialIDs) == 0 {
			errs = append(errs, fmt.Errorf("claimMap[%d] SourceClaim 必须关联材料", i))
		}
		for _, id := range cm.MaterialIDs {
			if !authorized[id] {
				errs = append(errs, fmt.Errorf("claimMap[%d] 引用了授权材料之外 %q 的 %q", i, id, cm.ClaimKind))
			}
		}
	}
	return errs
}

// writeClaimArticle 通用 v2 写作实现（由 Groq/OpenAI 调用方包装以获取 usage）。
func writeClaimArticle(ctx context.Context, req ClaimAwareWritingRequest, completeFn func(ctx context.Context, system, user string) (string, TaskUsage, error)) (*ClaimAwareWritingResult, TaskUsage, error) {
	content, usage, err := completeFn(ctx, claimSystemPrompt, BuildClaimPrompt(req))
	if err != nil {
		return nil, usage, err
	}
	result := &ClaimAwareWritingResult{}
	if err := parseJSONLoose(content, result); err != nil {
		return nil, usage, fmt.Errorf("解析 v2 写作输出: %w", err)
	}
	if errs := ValidateClaimMap(result, req); len(errs) > 0 {
		return nil, usage, fmt.Errorf("ClaimMap 校验失败: %v", errs)
	}
	return result, usage, nil
}

// WriteArticleWithClaims Groq v2 写作。
func (g *GroqProvider) WriteArticleWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error) {
	return writeClaimArticle(ctx, req, func(ctx context.Context, system, user string) (string, TaskUsage, error) {
		content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
			{"role": "system", "content": system + "\n必须只输出一个 JSON 对象。"},
			{"role": "user", "content": user},
		}, "object")
		return content, usage, err
	})
}

// WriteArticleWithClaims OpenAI v2 写作。
func (o *OpenAIProvider) WriteArticleWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error) {
	return writeClaimArticle(ctx, req, func(ctx context.Context, system, user string) (string, TaskUsage, error) {
		payload := map[string]any{
			"model":        o.effectiveAnalysisModel(),
			"instructions": system,
			"input":        user,
		}
		data, meta, err := o.chatCompleteWithMeta(ctx, payload, "v2 写作")
		if err != nil {
			return "", TaskUsage{}, err
		}
		var r struct {
			OutputText string `json:"output_text"`
		}
		_ = json.Unmarshal(data, &r)
		return r.OutputText, chatUsage(data, meta), nil
	})
}
