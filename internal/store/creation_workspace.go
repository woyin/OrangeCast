package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func optionalWorkspaceString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// CreateMaterialCandidate persists a learning insight before it becomes a KeyPoint.
func (s *Store) CreateMaterialCandidate(ctx context.Context, candidate models.MaterialCandidate) (*models.MaterialCandidate, error) {
	candidate.ID = uuid.NewString()
	candidate.SourceType, candidate.SourceID = strings.TrimSpace(candidate.SourceType), strings.TrimSpace(candidate.SourceID)
	candidate.OriginKind, candidate.Content = strings.TrimSpace(candidate.OriginKind), strings.TrimSpace(candidate.Content)
	if !validSourceType(models.SourceType(candidate.SourceType)) || candidate.SourceID == "" || candidate.OriginKind == "" || candidate.Content == "" {
		return nil, fmt.Errorf("%w: invalid material candidate", ErrInvalidEditorialState)
	}
	if candidate.CitationsJSON == "" {
		candidate.CitationsJSON = "[]"
	}
	var citationIDs []string
	if err := json.Unmarshal([]byte(candidate.CitationsJSON), &citationIDs); err != nil || len(citationIDs) == 0 {
		return nil, fmt.Errorf("%w: material candidate needs citation segment IDs", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, models.SourceType(candidate.SourceType), candidate.SourceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	valid, err := s.ValidateSourceCitations(ctx, models.SourceType(candidate.SourceType), candidate.SourceID, citationIDs)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("%w: citation does not resolve inside source", ErrInvalidEditorialState)
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	if candidate.Status != "pending" {
		return nil, fmt.Errorf("%w: material candidates must begin pending", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO material_candidates (id,source_type,source_id,origin_kind,origin_id,content,citations_json,status,rejection_reason) VALUES (?,?,?,?,?,?,?,?,?)`, candidate.ID, candidate.SourceType, candidate.SourceID, candidate.OriginKind, optionalWorkspaceString(candidate.OriginID), candidate.Content, candidate.CitationsJSON, candidate.Status, candidate.RejectionReason)
	if err != nil {
		return nil, err
	}
	return s.GetMaterialCandidate(ctx, candidate.ID)
}

// GetMaterialCandidate returns a candidate by stable identifier.
func (s *Store) GetMaterialCandidate(ctx context.Context, id string) (*models.MaterialCandidate, error) {
	c := &models.MaterialCandidate{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,source_type,source_id,origin_kind,COALESCE(origin_id,''),content,citations_json,status,rejection_reason,created_at,COALESCE(reviewed_at,'') FROM material_candidates WHERE id=?`, id).Scan(&c.ID, &c.SourceType, &c.SourceID, &c.OriginKind, &c.OriginID, &c.Content, &c.CitationsJSON, &c.Status, &c.RejectionReason, &c.CreatedAt, &c.ReviewedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return c, err
}

// ListMaterialCandidates lists candidates for one Source, newest first.
func (s *Store) ListMaterialCandidates(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.MaterialCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_id,origin_kind,COALESCE(origin_id,''),content,citations_json,status,rejection_reason,created_at,COALESCE(reviewed_at,'') FROM material_candidates WHERE source_type=? AND source_id=? ORDER BY created_at DESC,id DESC`, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.MaterialCandidate{}
	for rows.Next() {
		c := &models.MaterialCandidate{}
		if err := rows.Scan(&c.ID, &c.SourceType, &c.SourceID, &c.OriginKind, &c.OriginID, &c.Content, &c.CitationsJSON, &c.Status, &c.RejectionReason, &c.CreatedAt, &c.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetMaterialCandidateStatus records the quality-gate decision without deleting learning history.
func (s *Store) SetMaterialCandidateStatus(ctx context.Context, id, status, reason string) error {
	if status != "accepted" && status != "rejected" {
		return fmt.Errorf("%w: invalid material candidate decision", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status=?,rejection_reason=?,reviewed_at=datetime('now') WHERE id=?`, status, optionalWorkspaceString(reason), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PromoteMaterialCandidate turns an Owner-accepted, cited learning candidate
// into a durable manual KeyPoint and one discovery change. The candidate is
// retained as auditable learning history rather than deleted.
func (s *Store) PromoteMaterialCandidate(ctx context.Context, id string) (*KeyPointRow, error) {
	candidate, err := s.GetMaterialCandidate(ctx, id)
	if err != nil {
		return nil, err
	}
	if candidate.Status != "accepted" {
		return nil, fmt.Errorf("%w: only accepted material candidates can become KeyPoints", ErrInvalidEditorialState)
	}
	claim, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='promoting' WHERE id=? AND status='accepted'`, id)
	if err != nil {
		return nil, err
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		return nil, ErrInvalidEditorialState
	}
	keyPoint, err := s.materialCandidateKeyPoint(ctx, candidate)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='accepted' WHERE id=? AND status='promoting'`, id)
		return nil, err
	}
	created, err := s.CreateManualKeyPoint(ctx, keyPoint)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='accepted' WHERE id=? AND status='promoting'`, id)
		return nil, err
	}
	if _, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: created.ID, SourceType: string(created.SourceType), SourceID: created.SourceID, ChangeKind: "candidate_promoted", SnapshotHash: candidate.ID}); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE material_candidates SET status='promoted',reviewed_at=datetime('now') WHERE id=? AND status='promoting'`, id); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Store) materialCandidateKeyPoint(ctx context.Context, candidate *models.MaterialCandidate) (KeyPointRow, error) {
	var citations []string
	if err := json.Unmarshal([]byte(candidate.CitationsJSON), &citations); err != nil || len(citations) == 0 {
		return KeyPointRow{}, fmt.Errorf("%w: material candidate needs citations", ErrInvalidEditorialState)
	}
	keyPoint := KeyPointRow{SourceType: models.SourceType(candidate.SourceType), SourceID: candidate.SourceID, Content: candidate.Content, CitationsJSON: candidate.CitationsJSON}
	if keyPoint.SourceType == models.SourceDocument {
		document, err := s.GetDocument(ctx, candidate.SourceID)
		if err != nil {
			return KeyPointRow{}, err
		}
		positions := map[string]int{}
		for _, segment := range DocumentSegments(document) {
			positions[segment.ID] = segment.Position
		}
		start, end := 0, 0
		for _, citation := range citations {
			position := positions[citation]
			if position == 0 {
				return KeyPointRow{}, fmt.Errorf("%w: candidate citation does not resolve inside document", ErrInvalidEditorialState)
			}
			if start == 0 || position < start {
				start = position
			}
			if position > end {
				end = position
			}
		}
		keyPoint.SourceTitle, keyPoint.TimeStart, keyPoint.TimeEnd = document.Title, float64(start), float64(end)+.5
		return keyPoint, nil
	}
	version, err := s.GetCurrentVersion(ctx, keyPoint.SourceType, keyPoint.SourceID, KindTranscript)
	if err != nil {
		return KeyPointRow{}, err
	}
	var transcript provider.TranscriptPayload
	if err := json.Unmarshal([]byte(version.Payload), &transcript); err != nil {
		return KeyPointRow{}, err
	}
	segments := map[string]provider.Segment{}
	for _, segment := range transcript.Segments {
		segments[segment.ID] = segment
	}
	start, end := spanFromSegments(citations, segments)
	if end <= start {
		return KeyPointRow{}, fmt.Errorf("%w: candidate citation does not resolve inside transcript", ErrInvalidEditorialState)
	}
	keyPoint.SourceTitle, keyPoint.TimeStart, keyPoint.TimeEnd = candidate.SourceID, start, end
	if keyPoint.SourceType == models.SourceEpisode {
		if episode, err := s.GetEpisodeByID(ctx, keyPoint.SourceID); err == nil {
			keyPoint.SourceTitle = episode.Title
		}
	}
	return keyPoint, nil
}

// RecordMaterialChange writes one idempotent substantive change for discovery.
func (s *Store) RecordMaterialChange(ctx context.Context, change models.MaterialChange) (*models.MaterialChange, error) {
	change.ID = uuid.NewString()
	change.ChangeKind = strings.TrimSpace(change.ChangeKind)
	change.SnapshotHash = strings.TrimSpace(change.SnapshotHash)
	if change.KeyPointID == "" || change.ChangeKind == "" || change.SnapshotHash == "" || !validSourceType(models.SourceType(change.SourceType)) || strings.TrimSpace(change.SourceID) == "" {
		return nil, fmt.Errorf("%w: invalid material change", ErrInvalidEditorialState)
	}
	keyPoint, err := s.GetKeyPoint(ctx, change.KeyPointID)
	if err != nil {
		return nil, err
	}
	if keyPoint.SourceType != models.SourceType(change.SourceType) || keyPoint.SourceID != change.SourceID {
		return nil, fmt.Errorf("%w: material change source does not match KeyPoint", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO material_changes (id,keypoint_id,source_type,source_id,change_kind,snapshot_hash) VALUES (?,?,?,?,?,?) ON CONFLICT(keypoint_id,change_kind,snapshot_hash) DO NOTHING`, change.ID, change.KeyPointID, change.SourceType, change.SourceID, change.ChangeKind, change.SnapshotHash)
	if err != nil {
		return nil, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id,keypoint_id,source_type,source_id,change_kind,snapshot_hash,created_at FROM material_changes WHERE keypoint_id=? AND change_kind=? AND snapshot_hash=?`, change.KeyPointID, change.ChangeKind, change.SnapshotHash)
	if err := row.Scan(&change.ID, &change.KeyPointID, &change.SourceType, &change.SourceID, &change.ChangeKind, &change.SnapshotHash, &change.CreatedAt); err != nil {
		return nil, err
	}
	return &change, nil
}

// ---- 重点质量判定结果（K02）----

// SaveKeypointQualityResult 幂等保存一条质量判定结果：同 (keypoint, 指纹) 不产生新行。
// 本方法只持久化；更新 keypoint_index.quality_status 属于 K03 的消费路径。
func (s *Store) SaveKeypointQualityResult(ctx context.Context, r *models.KeypointQualityResult) error {
	if r.KeyPointID == "" || r.ContentFingerprint == "" || r.CardVersion <= 0 {
		return fmt.Errorf("%w: 质量结果缺少身份", ErrInvalidEditorialState)
	}
	switch r.Decision {
	case models.KPQualityReady, models.KPQualityNeedsReview, models.KPQualityInvalid:
	default:
		return fmt.Errorf("%w: 非法质量结论 %q", ErrInvalidEditorialState, r.Decision)
	}
	reasonsJSON, err := json.Marshal(r.Reasons)
	if err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO keypoint_quality_results
		   (id, keypoint_id, source_type, source_id, card_version, content_fingerprint,
		    decision, reasons_json, input_snapshot_json, provider, model, job_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.KeyPointID, string(r.SourceType), r.SourceID, r.CardVersion, r.ContentFingerprint,
		r.Decision, string(reasonsJSON), r.InputSnapshotJSON, r.Provider, r.Model, r.JobID)
	return err
}

// GetKeypointQualityResult 读取某重点在指定内容指纹下的判定；无则 ErrNotFound。
func (s *Store) GetKeypointQualityResult(ctx context.Context, keypointID, fingerprint string) (*models.KeypointQualityResult, error) {
	return s.scanKeypointQuality(s.DB.QueryRowContext(ctx,
		`SELECT id, keypoint_id, source_type, source_id, card_version, content_fingerprint,
		        decision, reasons_json, input_snapshot_json, provider, model, job_id, created_at
		 FROM keypoint_quality_results WHERE keypoint_id=? AND content_fingerprint=?`, keypointID, fingerprint))
}

// ListKeypointQualityResults 列出一个来源在指定卡片版本下的全部质量结果。
func (s *Store) ListKeypointQualityResults(ctx context.Context, sourceType models.SourceType, sourceID string, cardVersion int) ([]*models.KeypointQualityResult, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, keypoint_id, source_type, source_id, card_version, content_fingerprint,
		        decision, reasons_json, input_snapshot_json, provider, model, job_id, created_at
		 FROM keypoint_quality_results WHERE source_type=? AND source_id=? AND card_version=? ORDER BY created_at DESC`,
		string(sourceType), sourceID, cardVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.KeypointQualityResult
	for rows.Next() {
		r, err := s.scanKeypointQuality(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func (s *Store) scanKeypointQuality(row rowScanner) (*models.KeypointQualityResult, error) {
	r := &models.KeypointQualityResult{}
	var reasons string
	err := row.Scan(&r.ID, &r.KeyPointID, &r.SourceType, &r.SourceID, &r.CardVersion, &r.ContentFingerprint,
		&r.Decision, &reasons, &r.InputSnapshotJSON, &r.Provider, &r.Model, &r.JobID, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(reasons), &r.Reasons)
	if r.Reasons == nil {
		r.Reasons = []string{}
	}
	return r, nil
}

// ListKeyPointRowsByCardVersion 读取来自指定卡片版本的重点（K02 任务的输入）。
func (s *Store) ListKeyPointRowsByCardVersion(ctx context.Context, sourceType models.SourceType, sourceID string, cardVersion int) ([]*KeyPointRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, source_title, content, COALESCE(description,''), citations_json, relation_kind, time_start, time_end, card_version, origin
		 FROM keypoint_index WHERE source_type=? AND source_id=? AND card_version=?`,
		string(sourceType), sourceID, cardVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KeyPointRow
	for rows.Next() {
		r := &KeyPointRow{}
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.SourceTitle, &r.Content, &r.Description, &r.CitationsJSON, &r.RelationKind, &r.TimeStart, &r.TimeEnd, &r.CardVersion, &r.Origin); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- K03：质量判定结果 → 正式质量状态（R05）----

// KeypointQualityApplyStats 一次应用的结果统计（可观测，重跑可解释）。
type KeypointQualityApplyStats struct {
	Applied         int // 更新了正式质量状态的重点数
	MaterialChanges int // 本次写入的变化数（quality_approved，幂等去重）
	Unchanged       int // 判定与当前状态一致
	SkippedOwner    int // Owner 决策（owner_confirmed/排除/搁置/已用/人工修改）不覆盖
	SkippedNoMatch  int // 无匹配指纹或卡片版本的判定（旧结果不批准新内容）
}

// ApplyKeypointQualityResults 消费匹配当前内容指纹与卡片版本的质量判定，
// 在同一事务内更新正式质量状态；进入发现资格的通过写一次 quality_approved
// 变化（ON CONFLICT 幂等，重跑不增加变化）。约束：
//   - 只有 origin=automatic 且未被 Owner 挑选/排除/搁置的重点被自动判定更新；
//   - owner_confirmed 与 production 决策不被覆盖；
//   - 指纹不匹配（内容已改）或卡片版本不一致的旧结果不批准新内容；
//   - 无判定或被跳过时重点保持当前状态，仍可读，判定任务可重试。
func (s *Store) ApplyKeypointQualityResults(ctx context.Context, sourceType models.SourceType, sourceID string, cardVersion int) (*KeypointQualityApplyStats, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, content, COALESCE(description,''), citations_json, origin, production_status, quality_status
		 FROM keypoint_index WHERE source_type=? AND source_id=? AND card_version=?`,
		string(sourceType), sourceID, cardVersion)
	if err != nil {
		return nil, err
	}
	type kpRow struct {
		id, content, description, citations, origin, production, quality string
	}
	var kps []kpRow
	for rows.Next() {
		var r kpRow
		if err := rows.Scan(&r.id, &r.content, &r.description, &r.citations, &r.origin, &r.production, &r.quality); err != nil {
			rows.Close()
			return nil, err
		}
		kps = append(kps, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	stats := &KeypointQualityApplyStats{}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, kp := range kps {
		if kp.origin != string(models.KeyPointAutomatic) ||
			kp.quality == string(models.KeyPointOwnerConfirmed) ||
			kp.production == string(models.KeyPointShortlisted) ||
			kp.production == string(models.KeyPointUsed) ||
			kp.production == string(models.KeyPointDismissed) {
			stats.SkippedOwner++
			continue
		}
		var citations []string
		_ = json.Unmarshal([]byte(kp.citations), &citations)
		fingerprint := provider.FingerprintKeypoint(kp.content, kp.description, citations)
		// 事务内读取判定（不可用 s.DB：事务持写锁时会死锁）。
		row := tx.QueryRowContext(ctx,
			`SELECT id, keypoint_id, source_type, source_id, card_version, content_fingerprint,
			        decision, reasons_json, input_snapshot_json, provider, model, job_id, created_at
			 FROM keypoint_quality_results WHERE keypoint_id=? AND content_fingerprint=?`, kp.id, fingerprint)
		res, err := s.scanKeypointQuality(row)
		if errors.Is(err, ErrNotFound) {
			stats.SkippedNoMatch++ // 尚无当前内容的判定
			continue
		} else if err != nil {
			return nil, err
		}
		if res.CardVersion != cardVersion {
			stats.SkippedNoMatch++ // 旧卡片版本的判定不批准新内容
			continue
		}
		target := models.KeyPointQualityStatus(res.Decision)
		if !validKeyPointQualityStatus(target) {
			return nil, fmt.Errorf("%w: 判定结论 %q 不可映射为质量状态", ErrInvalidEditorialState, res.Decision)
		}
		if string(target) == kp.quality {
			stats.Unchanged++
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE keypoint_index SET quality_status=? WHERE id=? AND quality_status=?`,
			string(target), kp.id, kp.quality); err != nil {
			return nil, err
		}
		stats.Applied++
		if target == models.KeyPointReady {
			snapshot := sha256.Sum256([]byte(kp.content + "\x00" + kp.citations))
			res2, err := tx.ExecContext(ctx,
				`INSERT INTO material_changes (id,keypoint_id,source_type,source_id,change_kind,snapshot_hash) VALUES (?,?,?,?,?,?) ON CONFLICT(keypoint_id,change_kind,snapshot_hash) DO NOTHING`,
				uuid.NewString(), kp.id, string(sourceType), sourceID, "quality_approved", fmt.Sprintf("%x", snapshot))
			if err != nil {
				return nil, err
			}
			if n, _ := res2.RowsAffected(); n > 0 {
				stats.MaterialChanges++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stats, nil
}
