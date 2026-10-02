package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// KnowledgeArticlePurposePromptVersion adds purpose rules without rewriting v1-v4.
const KnowledgeArticlePurposePromptVersion = "knowledge-article-v5"

// WritingPurposeRuleVersion identifies the immutable built-in structure and review rules.
const WritingPurposeRuleVersion = "writing-purpose-v1"

// WritingMode describes one built-in article structure without granting material permissions.
type WritingMode struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Sections []string `json:"sections"`
	Rules    string   `json:"rules"`
}

// FrozenWritingPurpose binds a mode and exact rule fingerprint to an admitted request.
type FrozenWritingPurpose struct {
	Mode        string `json:"mode"`
	RuleVersion string `json:"rule_version"`
	Fingerprint string `json:"fingerprint"`
}

// WritingModes returns independent copies of the built-in, permission-neutral catalog.
func WritingModes() []WritingMode {
	return []WritingMode{
		{"synthesis", "默认综合", nil, "延续来源整理、个人理解与AI综合的身份边界，围绕具体问题形成清楚的综合解释；不要求凑章节或字数。"},
		{"explanation", "知识解释", []string{"question", "concept", "example", "boundary"}, "说明问题与概念、材料支持的例子和边界。缺少例子时在example段明确材料不足；不编造专名、机制或案例。"},
		{"comparison", "观点对比", []string{"positions", "agreement", "disagreement", "conditions"}, "分别呈现至少两个来源的表达，说明共识、分歧和适用条件。没有共识或分歧须明确说明；不同条件不自动构成矛盾，不强行判胜负。"},
		{"practice", "实践指南", []string{"scenario", "steps", "limits", "checks"}, "说明适用场景、资料支持的可执行步骤、限制和检查方式。资料建议与Owner经历明确区分；亲身经历只能来自owner_reflection并用reflection身份，不编造实践经历或效果。缺实践证据时明确不足，不把建议写成验证有效。"},
	}
}

func writingMode(id string) (WritingMode, bool) {
	for _, m := range WritingModes() {
		if m.ID == id {
			return m, true
		}
	}
	return WritingMode{}, false
}

// FreezeWritingPurpose binds the exact catalog rule to every admitted model stage.
func FreezeWritingPurpose(mode string) (FrozenWritingPurpose, error) {
	if mode == "" {
		mode = "synthesis"
	}
	m, ok := writingMode(mode)
	if !ok {
		return FrozenWritingPurpose{}, fmt.Errorf("未知文章用途: %s", mode)
	}
	encoded, _ := json.Marshal(struct {
		Version string      `json:"version"`
		Mode    WritingMode `json:"mode"`
	}{WritingPurposeRuleVersion, m})
	sum := sha256.Sum256(encoded)
	return FrozenWritingPurpose{Mode: mode, RuleVersion: WritingPurposeRuleVersion, Fingerprint: hex.EncodeToString(sum[:])}, nil
}

// ValidateWritingPurpose rejects unknown modes, changed rules and mismatched fingerprints.
func ValidateWritingPurpose(p FrozenWritingPurpose) error {
	frozen, err := FreezeWritingPurpose(p.Mode)
	if err != nil {
		return err
	}
	if p.Mode == "" || p.RuleVersion != frozen.RuleVersion || p.Fingerprint != frozen.Fingerprint {
		return fmt.Errorf("文章用途规则版本或指纹无效")
	}
	return nil
}

// ValidateWritingPurposeRequest preserves legacy contracts and requires valid purpose rules for v5.
func ValidateWritingPurposeRequest(req KnowledgeArticleRequest) error {
	if req.PromptVersion != KnowledgeArticlePurposePromptVersion {
		for _, material := range req.Materials {
			if material.Kind == "understanding" || len(material.UnderstandingReferences) > 0 {
				return fmt.Errorf("旧文章提示版本不接受理解快照")
			}
		}
		if req.WritingPurpose != nil {
			return fmt.Errorf("旧文章提示版本不接受用途规则")
		}
		return nil
	}
	if req.WritingPurpose == nil {
		return fmt.Errorf("新文章提示缺少冻结用途")
	}
	return ValidateWritingPurpose(*req.WritingPurpose)
}

func writingPurposeInstructions(p FrozenWritingPurpose) string {
	m, _ := writingMode(p.Mode)
	rules := "\nwriting_purpose 是冻结的文章用途，不授予额外资料权限。用途：" + m.Label + "。规则：" + m.Rules
	if len(m.Sections) > 0 {
		rules += "\n发现和选材的大纲必须覆盖这些结构；写作/修订每个段落必须返回purpose_section，值仅限：" + strings.Join(m.Sections, ",") + "；每个结构至少有一段。审校必须核对相同结构及用途独有判据，缺少证据时说明不足，不能靠编造通过。"
	}
	return rules
}

// ValidateWritingPurposeResult checks mechanical structure, not factual correctness.
// Groundedness and substantive sufficiency still require independent review.
func ValidateWritingPurposeResult(req KnowledgeArticleRequest, result *KnowledgeArticleResult) error {
	if req.PromptVersion != KnowledgeArticlePurposePromptVersion || req.WritingPurpose == nil || result == nil {
		return nil
	}
	m, ok := writingMode(req.WritingPurpose.Mode)
	if !ok {
		return fmt.Errorf("未知文章用途")
	}
	var blocks []KnowledgeBlock
	switch req.Stage {
	case "write", "revise":
		blocks = result.Blocks
	case "review", "review_final":
		if result.Passed == nil || !*result.Passed {
			return nil
		}
		blocks = req.Blocks
	default:
		return nil
	}

	allowed := map[string]bool{}
	for _, section := range m.Sections {
		allowed[section] = true
	}
	seen := map[string]bool{}
	sources := map[string]bool{}
	for _, b := range blocks {
		for _, id := range b.MaterialIDs {
			for _, material := range req.Materials {
				if material.ID == id && material.Kind == "understanding" && (b.Kind != "reflection" || len(b.Quotes) > 0) {
					return fmt.Errorf("个人理解不可冒充来源或综合事实")
				}
			}
		}
		if len(m.Sections) == 0 {
			continue
		}
		if !allowed[b.PurposeSection] || strings.TrimSpace(b.Text) == "" {
			return fmt.Errorf("用途结构缺失或未知: %s", b.PurposeSection)
		}
		seen[b.PurposeSection] = true
		for _, id := range b.MaterialIDs {
			for _, material := range req.Materials {
				if material.ID == id && material.Kind != "owner_reflection" && material.Kind != "understanding" && material.SourceID != "" {
					sources[material.SourceType+":"+material.SourceID] = true
				}
			}
		}
	}
	for _, section := range m.Sections {
		if !seen[section] {
			return fmt.Errorf("用途结构缺少章节: %s", section)
		}
	}
	if m.ID == "comparison" && len(sources) < 2 {
		return fmt.Errorf("观点对比缺少两个不同来源的依据")
	}
	return nil
}
