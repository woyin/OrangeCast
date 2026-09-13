// digest_readiness.go 精读修订的就绪检查（G05 / ADR-0024 §5 / ADR-0023）。
// 只读接口：检查来源确认、依据有效性、块类型约束与笔记引用；
// 预览与导出不触发任何生成或确认动作，也不会创建发布历史。
// 个人草稿允许查看与导出，但必须显式携带草稿标记与具体问题清单。
package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
)

// DigestReadinessIssue 一个未达交付条件的具体问题。
type DigestReadinessIssue struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// DigestReadiness 一份精读修订的就绪结论。
type DigestReadiness struct {
	DigestID    string
	Version     int
	Deliverable bool
	Issues      []DigestReadinessIssue
}

// EvaluateDigestReadiness 只读评估一份修订是否满足交付条件（ADR-0023 来源确认规则）。
func (s *Store) EvaluateDigestReadiness(ctx context.Context, digestID string) (*DigestReadiness, error) {
	d, err := s.GetEpisodeDigest(ctx, digestID)
	if err != nil {
		return nil, err
	}
	out := &DigestReadiness{DigestID: d.ID, Version: d.Version}
	add := func(code, format string, args ...any) {
		out.Issues = append(out.Issues, DigestReadinessIssue{Code: code, Detail: fmt.Sprintf(format, args...)})
	}

	// 1) 检索落源必须全部确认（pending 未确认 → 不可交付）。
	rows, err := s.ListDigestSearchSources(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	pending := 0
	for _, row := range rows {
		if row.Status == "pending" {
			pending++
		}
	}
	if pending > 0 {
		add("unconfirmed_source", "有 %d 条检索落源未确认或剔除（ADR-0023 要求发布前逐条确认）", pending)
	}

	// 2) 来源快照血缘：legacy 或失效（Purge/版本缺失）→ 依据不可回溯。
	if d.SourceSnapshotID == "" {
		add("legacy_snapshot", "该修订生成于快照机制之前，依据无法定位到冻结版本")
	} else {
		snap, err := s.GetSourceSnapshot(ctx, d.SourceSnapshotID)
		if err != nil {
			add("invalidated_evidence", "来源快照不存在（%s）", d.SourceSnapshotID)
		} else if snap.Status == models.SnapshotPurged {
			add("invalidated_evidence", "来源已彻底删除，依据失效")
		} else {
			if _, _, _, err := s.SnapshotContent(ctx, d.SourceSnapshotID); err != nil {
				add("invalidated_evidence", "依据内容已不可读：%v", err)
			}
		}
	}

	// 3) 块约束：转述/引用事实必须有引用；笔记块必须能对应到既有笔记。
	blocks, err := s.ListDigestBlocks(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	for _, b := range blocks {
		switch b.Type {
		case models.DigestBlockParaphrase, models.DigestBlockCitedFact:
			if len(b.Citations) == 0 {
				add("missing_citations", "块 %d（%s）缺少依据引用", b.Position, b.Type)
			}
		case models.DigestBlockNote:
			if b.NoteID != "" {
				if _, err := s.GetOwnerNote(ctx, b.NoteID); err != nil {
					add("note_missing", "块 %d 引用的笔记已不存在", b.Position)
				}
			}
		}
	}

	out.Deliverable = len(out.Issues) == 0
	return out, nil
}

// Summary 返回给模板/导出的简短状态行。
func (r *DigestReadiness) Summary() string {
	if r.Deliverable {
		return "可交付：引用、来源确认与依据检查全部通过"
	}
	line := "草稿（不可交付）："
	for i, issue := range r.Issues {
		if i > 0 {
			line += "；"
		}
		line += issue.Detail
	}
	return line
}

// MarshalIssuesJSON 供模板/日志直接输出问题清单。
func (r *DigestReadiness) MarshalIssuesJSON() string {
	data, err := json.Marshal(r.Issues)
	if err != nil {
		return "[]"
	}
	return string(data)
}
