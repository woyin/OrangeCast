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

// ClaimRevisionWriterPromptVersion is the durable contract version for R21
// claim-aware AI revision jobs (ExistingMarkdown + ReviewFeedback + original ClaimMap).
const ClaimRevisionWriterPromptVersion = "claim-writer-v2-rev"

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

	// R21 AI 修订扩展：ExistingMarkdown 非空时进入修订模式——基于现有正文与
	// 原始 ClaimMap 修改，处理审校反馈；仍要求返回完整新正文 + 完整 ClaimMap。
	ExistingMarkdown string          `json:"existingMarkdown,omitempty"`
	RevisionFeedback []string        `json:"revisionFeedback,omitempty"`
	ExistingClaimMap []ClaimMapEntry `json:"existingClaimMap,omitempty"`
}

// ClaimOwnerNote 个人笔记快照（R23）：独立于 KeyPoint/ArticleMaterial 的个人材料
// 身份，保留 note ID/内容/kind/来源身份/修订版本；不得冒充来源主张或引用。
type ClaimOwnerNote struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Kind       string `json:"kind"` // source_note | owner_reflection
	SourceType string `json:"sourceType"`
	SourceID   string `json:"sourceId"`
	Revision   int    `json:"revision"`
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

// BuildClaimPrompt 构造 v2 用户消息（R21：修订模式附带现有正文/反馈/原 ClaimMap）。
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
	if req.ExistingMarkdown != "" {
		sb.WriteString("\n这是一次修订任务：基于下面的现有正文修改，处理审校反馈。\n")
		sb.WriteString("审校反馈（逐条处理）：\n")
		for _, f := range req.RevisionFeedback {
			sb.WriteString("- " + f + "\n")
		}
		sb.WriteString("\n现有正文：\n" + req.ExistingMarkdown + "\n")
		sb.WriteString("\n现有 ClaimMap（修改后必须重新输出完整映射；仍在正文中的片段保持身份，已删除/修改的片段移除）：\n")
		for _, cm := range req.ExistingClaimMap {
			sb.WriteString(fmt.Sprintf("片段：%s\n类型：%s\n材料：%s\n\n", cm.Excerpt, cm.ClaimKind, strings.Join(cm.MaterialIDs, ",")))
		}
	}
	sb.WriteString("\n可用材料（引用只能用这些 KeyPoint ID）：\n")
	for _, m := range req.Materials {
		sb.WriteString(fmt.Sprintf("[%s] %s：%s\n", m.KeyPointID, m.SourceTitle, m.Content))
	}
	for _, n := range req.OwnerNotes {
		sb.WriteString(fmt.Sprintf("个人笔记 [%s]（%s，%s/%s，revision %d）：%s\n",
			n.ID, n.Kind, n.SourceType, n.SourceID, n.Revision, n.Content))
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
	ownerNoteIDs := map[string]bool{}
	for _, m := range req.Materials {
		authorized[m.KeyPointID] = true
	}
	for _, n := range req.OwnerNotes {
		authorized[n.ID] = true
		ownerNoteIDs[n.ID] = true
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
			// R23：OwnerNote 是个人理解身份，只能支撑 owner_claim/synthesis_claim，
			// 不得冒充来源主张（source_claim）或已核验事实。
			if ownerNoteIDs[id] && (cm.ClaimKind == ClaimSource || cm.ClaimKind == ClaimVerified) {
				errs = append(errs, fmt.Errorf("claimMap[%d] OwnerNote %q 不得用于 %s", i, id, cm.ClaimKind))
			}
		}
	}
	return errs
}

// writeClaimArticle 通用 v2 写作实现（由 Groq/OpenAI 调用方包装以获取 usage）。
func writeClaimArticle(ctx context.Context, req ClaimAwareWritingRequest, completeFn func(ctx context.Context, system, user string) (string, TaskUsage, error)) (*ClaimAwareWritingResult, TaskUsage, error) {
	return writeClaimArticleWithSystem(ctx, req, claimSystemPrompt, completeFn)
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

// claimRevisionExtraPrompt R21 修订模式的系统提示词补充（与 v2 主提示词拼接使用）。
const claimRevisionExtraPrompt = `

这是一次修订任务：只允许使用给定材料（不得引入新事实/新主张），输出完整的新正文与完整的 ClaimMap。现有 ClaimMap 中仍逐字保留在正文里的片段必须保持其身份（类型与材料不变）；被删除或改写的片段从映射中移除；新增表达必须按规则标注。`

// WriteArticleRevisionWithClaims Groq v2 修订写作（R21）：复用 v2 契约与严格
// ValidateClaimMap，仅切换系统提示词（主提示词 + 修订补充）与提示词版本语义。
func (g *GroqProvider) WriteArticleRevisionWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error) {
	return writeClaimArticleWithSystem(ctx, req, claimSystemPrompt+claimRevisionExtraPrompt, func(ctx context.Context, system, user string) (string, TaskUsage, error) {
		content, _, _, usage, err := g.completeContextWithUsage(ctx, []map[string]string{
			{"role": "system", "content": system + "\n必须只输出一个 JSON 对象。"},
			{"role": "user", "content": user},
		}, "object")
		return content, usage, err
	})
}

// WriteArticleRevisionWithClaims OpenAI v2 修订写作（R21）。
func (o *OpenAIProvider) WriteArticleRevisionWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error) {
	return writeClaimArticleWithSystem(ctx, req, claimSystemPrompt+claimRevisionExtraPrompt, func(ctx context.Context, system, user string) (string, TaskUsage, error) {
		payload := map[string]any{
			"model":        o.effectiveAnalysisModel(),
			"instructions": system,
			"input":        user, // 使用传入的 user（BuildClaimPrompt 只构造一次）
		}
		data, meta, err := o.chatCompleteWithMeta(ctx, payload, "v2 修订写作")
		if err != nil {
			return "", TaskUsage{}, err
		}
		var r struct {
			OutputText string `json:"output_text"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", TaskUsage{}, fmt.Errorf("解析 openai v2 修订写作响应: %w", err)
		}
		return r.OutputText, chatUsage(data, meta), nil
	})
}

// writeClaimArticleWithSystem 与 writeClaimArticle 相同，但允许指定系统提示词；
// 校验复用严格 ValidateClaimMap（完整正文 + 完整 ClaimMap + 材料成员身份）。
func writeClaimArticleWithSystem(ctx context.Context, req ClaimAwareWritingRequest, system string, completeFn func(ctx context.Context, system, user string) (string, TaskUsage, error)) (*ClaimAwareWritingResult, TaskUsage, error) {
	content, usage, err := completeFn(ctx, system, BuildClaimPrompt(req))
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
	if req.ExistingMarkdown != "" {
		if err := ValidateClaimRevisionMap(result, req); err != nil {
			return nil, usage, err
		}
	}
	return result, usage, nil
}

// ValidateClaimRevisionMap R21 AI 修订输出的专用严格校验（provider/queue checkpoint
// 复用/store 保存共用同一份契约）：
//   - 先复用 ValidateClaimMap（完整正文 + 完整 ClaimMap + 材料成员身份）；
//   - 输出的每个 excerpt 必须在新正文恰好出现一次（无重复句歧义）；
//   - 输出映射不得有重复 excerpt（身份唯一）；
//   - 原 ClaimMap 中在旧正文唯一、且在新正文也唯一保留的 excerpt（且原映射中该
//     excerpt 身份唯一），输出必须恰有一条并完整保持原身份（claim kind、material
//     IDs、source title、citation refs）；
//   - 原 ClaimMap 自身存在重复 excerpt（身份歧义）视为无效修订请求，直接拒绝。
func ValidateClaimRevisionMap(result *ClaimAwareWritingResult, req ClaimAwareWritingRequest) error {
	if errs := ValidateClaimMap(result, req); len(errs) > 0 {
		return fmt.Errorf("ClaimMap 校验失败: %v", errs)
	}
	// base 映射自身重复 excerpt = 身份歧义 → 拒绝。
	baseCounts := map[string]int{}
	for _, cm := range req.ExistingClaimMap {
		baseCounts[cm.Excerpt]++
	}
	for excerpt, n := range baseCounts {
		if n > 1 {
			return fmt.Errorf("原 ClaimMap excerpt %q 存在 %d 条映射（身份歧义），不能修订", excerpt, n)
		}
	}
	// 输出 excerpt：新正文唯一 + 映射无重复。
	outCounts := map[string]int{}
	byExcerpt := map[string]ClaimMapEntry{}
	for _, cm := range result.ClaimMap {
		outCounts[cm.Excerpt]++
		if _, dup := byExcerpt[cm.Excerpt]; dup {
			return fmt.Errorf("输出 ClaimMap 存在重复 excerpt %q", cm.Excerpt)
		}
		byExcerpt[cm.Excerpt] = cm
	}
	for excerpt, n := range outCounts {
		if n != 1 || strings.Count(result.Markdown, excerpt) != 1 {
			return fmt.Errorf("输出 excerpt %q 在新正文必须恰好出现一次", excerpt)
		}
	}
	// 逐条原 excerpt（旧正文唯一者）按新正文出现次数矩阵校验：
	//   count=0：允许删除/改写（输出映射也不得保留该 excerpt）；
	//   count=1：输出必须恰有一条完整同身份映射；
	//   count>1：直接拒绝（无论输出是否映射）。
	for _, orig := range req.ExistingClaimMap {
		if strings.Count(req.ExistingMarkdown, orig.Excerpt) != 1 {
			// 旧正文不唯一（base 歧义）：输出不得引用该 excerpt。
			if _, referenced := byExcerpt[orig.Excerpt]; referenced {
				return fmt.Errorf("原 excerpt %q 在旧正文不唯一，修订输出不得保留", orig.Excerpt)
			}
			continue
		}
		newCount := strings.Count(result.Markdown, orig.Excerpt)
		if newCount > 1 {
			return fmt.Errorf("原 excerpt %q 在新正文出现 %d 次（重复句），必须改写", orig.Excerpt, newCount)
		}
		out, kept := byExcerpt[orig.Excerpt]
		if newCount == 0 {
			if kept {
				return fmt.Errorf("excerpt %q 已从新正文删除，输出映射不得保留", orig.Excerpt)
			}
			continue
		}
		if !kept {
			return fmt.Errorf("excerpt %q 仍在新正文唯一保留，但输出映射省略了它", orig.Excerpt)
		}
		if out.ClaimKind != orig.ClaimKind || out.SourceTitle != orig.SourceTitle ||
			!sameStringSet(out.MaterialIDs, orig.MaterialIDs) ||
			!sameStringSet(out.CitationRefs, orig.CitationRefs) {
			return fmt.Errorf("保留 excerpt %q 的身份被篡改（kind/materials/source/citations）", orig.Excerpt)
		}
	}
	return nil
}

// sameStringSet 判定两个字符串集合（顺序无关、无重复）相等。
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, v := range a {
		set[v]++
	}
	for _, v := range b {
		if set[v] == 0 {
			return false
		}
		set[v]--
	}
	return true
}

// ClaimAwareRevisionWriter claim-aware AI 修订公开接口（R21）：与普通写作
// （ClaimAwareWriter）分离的修订调用能力，由 Groq/OpenAI 实现。
type ClaimAwareRevisionWriter interface {
	WriteArticleRevisionWithClaims(ctx context.Context, req ClaimAwareWritingRequest) (*ClaimAwareWritingResult, TaskUsage, error)
	Name() string
}

// 编译期断言：两个 provider 实现修订能力。
var (
	_ ClaimAwareRevisionWriter = (*GroqProvider)(nil)
	_ ClaimAwareRevisionWriter = (*OpenAIProvider)(nil)
)
