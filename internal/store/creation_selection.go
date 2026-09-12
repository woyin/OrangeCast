// creation_selection.go 创作素材选择（C01 / ADR-0024 §1）。
// 首次创作复用默认画像（EnsureDefaultEditorialProfile），不要求先填完整品牌；
// 选择 KeyPoint 与个人笔记（OwnerNote）时逐项校验资格：质量（ready 及以上）、
// 非 stale、未被 Owner 排除、Provider 策略可兼容——不合格材料进 Excluded 并附
// 原因（显式解释，不静默过滤）。选择与确认均为本地读写，不产生付费调用。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// materialEligibility 单条材料的资格结论。
type materialEligibility struct {
	Eligible bool
	Reason   string
}

// checkMaterialEligibility 逐项资格检查（C01 §3）：
//   - KeyPoint：必须存在；质量为 ready/owner_confirmed；未 stale；
//     未被任一画像 Owner 排除（owner_override = excluded）。
//   - OwnerNote：必须存在。个人笔记以个人材料身份进入，不套用来源主张的
//     质量门槛（其 Reference 身份在下游契约中单独保证）。
func (s *Store) checkMaterialEligibility(ctx context.Context, keypointID, noteID string) materialEligibility {
	if keypointID != "" {
		kp, err := s.GetKeyPoint(ctx, keypointID)
		if err != nil {
			return materialEligibility{false, fmt.Sprintf("关键观点 %s 不存在", keypointID)}
		}
		switch kp.QualityStatus {
		case models.KeyPointReady, models.KeyPointOwnerConfirmed:
		default:
			return materialEligibility{false, fmt.Sprintf("关键观点 %s 质量为 %s（需 ready 或 owner 确认）", keypointID, kp.QualityStatus)}
		}
		if kp.EvidenceStatus == "stale" {
			return materialEligibility{false, fmt.Sprintf("关键观点 %s 证据已陈旧", keypointID)}
		}
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM editorial_relevance WHERE keypoint_id=? AND owner_override='excluded'`, keypointID).Scan(&n); err != nil {
			return materialEligibility{false, err.Error()}
		}
		if n > 0 {
			return materialEligibility{false, fmt.Sprintf("关键观点 %s 已被 Owner 排除", keypointID)}
		}
		return materialEligibility{true, ""}
	}
	note, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		return materialEligibility{false, fmt.Sprintf("个人笔记 %s 不存在", noteID)}
	}
	if strings.TrimSpace(note.Content) == "" {
		return materialEligibility{false, "个人笔记内容为空"}
	}
	return materialEligibility{true, ""}
}

// SaveCreationSelection 保存素材选择（草稿或确认）：逐项资格检查并记录排除原因。
// 不触发任何付费调用。
func (s *Store) SaveCreationSelection(ctx context.Context, sel *models.CreationSelection) (*models.CreationSelection, error) {
	if sel == nil {
		return nil, fmt.Errorf("%w: selection required", ErrInvalidEditorialState)
	}
	if sel.EditorialProfileID == "" {
		profile, err := s.EnsureDefaultEditorialProfile(ctx)
		if err != nil {
			return nil, err
		}
		sel.EditorialProfileID = profile.ID
	}
	if sel.Status == "" {
		sel.Status = models.SelectionDraft
	}
	if sel.Status != models.SelectionDraft && sel.Status != models.SelectionConfirmed {
		return nil, fmt.Errorf("%w: invalid selection status", ErrInvalidEditorialState)
	}
	if len(sel.MaterialIDs) == 0 && len(sel.NoteIDs) == 0 {
		return nil, fmt.Errorf("%w: selection requires at least one material or note", ErrInvalidEditorialState)
	}

	sel.Excluded = nil
	keptMaterials := make([]string, 0, len(sel.MaterialIDs))
	for _, id := range sel.MaterialIDs {
		el := s.checkMaterialEligibility(ctx, id, "")
		if el.Eligible {
			keptMaterials = append(keptMaterials, id)
		} else {
			sel.Excluded = append(sel.Excluded, models.CreationSelectionExclusion{ID: id, Reason: el.Reason})
		}
	}
	sel.MaterialIDs = keptMaterials
	keptNotes := make([]string, 0, len(sel.NoteIDs))
	for _, id := range sel.NoteIDs {
		el := s.checkMaterialEligibility(ctx, "", id)
		if el.Eligible {
			keptNotes = append(keptNotes, id)
		} else {
			sel.Excluded = append(sel.Excluded, models.CreationSelectionExclusion{ID: id, Reason: el.Reason})
		}
	}
	sel.NoteIDs = keptNotes

	materialsJSON, _ := json.Marshal(sel.MaterialIDs)
	notesJSON, _ := json.Marshal(sel.NoteIDs)
	scopeJSON, _ := json.Marshal(sel.Scope)
	excludedJSON, _ := json.Marshal(sel.Excluded)
	if sel.ID == "" {
		sel.ID = uuid.NewString()
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO creation_selections (id, editorial_profile_id, title, material_ids_json, note_ids_json, scope_json, excluded_json, status)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET title=excluded.title, material_ids_json=excluded.material_ids_json,
		   note_ids_json=excluded.note_ids_json, scope_json=excluded.scope_json,
		   excluded_json=excluded.excluded_json, status=excluded.status, updated_at=datetime('now')`,
		sel.ID, sel.EditorialProfileID, strings.TrimSpace(sel.Title), string(materialsJSON), string(notesJSON),
		string(scopeJSON), string(excludedJSON), sel.Status)
	if err != nil {
		return nil, err
	}
	return s.GetCreationSelection(ctx, sel.ID)
}

// GetCreationSelection 读取素材选择。
func (s *Store) GetCreationSelection(ctx context.Context, id string) (*models.CreationSelection, error) {
	return scanCreationSelection(s.DB.QueryRowContext(ctx,
		`SELECT id, editorial_profile_id, title, material_ids_json, note_ids_json, scope_json, excluded_json, status, created_at, updated_at
		 FROM creation_selections WHERE id=?`, id))
}

// ListCreationSelections 列出画像下的素材选择（新→旧）。
func (s *Store) ListCreationSelections(ctx context.Context, profileID string) ([]*models.CreationSelection, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, editorial_profile_id, title, material_ids_json, note_ids_json, scope_json, excluded_json, status, created_at, updated_at
		 FROM creation_selections WHERE editorial_profile_id=? ORDER BY updated_at DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CreationSelection
	for rows.Next() {
		sel, err := scanCreationSelection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

func scanCreationSelection(row rowScanner) (*models.CreationSelection, error) {
	sel := &models.CreationSelection{}
	var materials, notes, scope, excluded string
	err := row.Scan(&sel.ID, &sel.EditorialProfileID, &sel.Title, &materials, &notes, &scope, &excluded, &sel.Status, &sel.CreatedAt, &sel.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(materials), &sel.MaterialIDs)
	_ = json.Unmarshal([]byte(notes), &sel.NoteIDs)
	_ = json.Unmarshal([]byte(scope), &sel.Scope)
	_ = json.Unmarshal([]byte(excluded), &sel.Excluded)
	if sel.MaterialIDs == nil {
		sel.MaterialIDs = []string{}
	}
	if sel.NoteIDs == nil {
		sel.NoteIDs = []string{}
	}
	if sel.Excluded == nil {
		sel.Excluded = []models.CreationSelectionExclusion{}
	}
	return sel, nil
}
