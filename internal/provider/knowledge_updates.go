package provider

import (
	"fmt"
	"strings"
)

// ValidateKnowledgeUpdate requires real material and paragraph identities for proposed changes.
func ValidateKnowledgeUpdate(req KnowledgeArticleRequest, analysis *KnowledgeUpdateAnalysis) error {
	if req.Update == nil || req.Update.ParentRevision < 1 || req.Update.ParentHash == "" || analysis == nil || strings.TrimSpace(analysis.Reason) == "" || len([]rune(analysis.Reason)) > 4000 || len(analysis.Changes) > 120 || len(analysis.Missing) > 20 {
		return fmt.Errorf("更新判断缺少有效父稿、结论或修改原因")
	}
	switch analysis.Decision {
	case "update", "new_direction", "insufficient", "no_change":
	default:
		return fmt.Errorf("更新判断类型无效")
	}
	blocks := map[string]KnowledgeBlock{}
	for _, b := range req.Blocks {
		if b.ID != "" {
			blocks[b.ID] = b
		}
	}
	materials := map[string]KnowledgeMaterial{}
	for _, m := range req.Materials {
		materials[m.ID] = m
	}
	substantive := false
	for _, c := range analysis.Changes {
		if strings.TrimSpace(c.Reason) == "" || len([]rune(c.Reason)) > 4000 || len(c.MaterialIDs) > 20 {
			return fmt.Errorf("更新操作缺少有界理由或材料")
		}
		switch c.Action {
		case "keep", "remove", "refute":
			if _, ok := blocks[c.BlockID]; !ok {
				return fmt.Errorf("更新操作的父段落不存在")
			}
		case "add":
			if c.BlockID != "" {
				if _, ok := blocks[c.BlockID]; !ok {
					return fmt.Errorf("补充操作的父段落不存在")
				}
			}
		default:
			return fmt.Errorf("更新操作类型无效")
		}
		for _, id := range c.MaterialIDs {
			if _, ok := materials[id]; !ok {
				return fmt.Errorf("更新操作引用未提供材料")
			}
		}
		if (c.Action == "add" || c.Action == "refute") && len(c.MaterialIDs) == 0 {
			return fmt.Errorf("补充或反驳必须有当前材料")
		}
		if c.Action == "refute" && blocks[c.BlockID].Kind == "source" {
			source := false
			for _, id := range c.MaterialIDs {
				source = source || materials[id].Kind != "owner_reflection"
			}
			if !source {
				return fmt.Errorf("个人理解不能作为反驳来源主张的事实证据")
			}
		}
		substantive = substantive || c.Action != "keep"
	}
	if analysis.Decision == "update" && !substantive {
		return fmt.Errorf("更新判断没有实质修改")
	}
	if analysis.Decision == "new_direction" {
		if analysis.NewDirection == nil {
			return fmt.Errorf("新方向缺少选题")
		}
		if err := ValidateKnowledgeTopics([]KnowledgeTopic{*analysis.NewDirection}, req.Materials); err != nil {
			return err
		}
	}
	if analysis.Decision == "no_change" && substantive {
		return fmt.Errorf("无变化判断不能同时要求实质修改")
	}
	return nil
}
