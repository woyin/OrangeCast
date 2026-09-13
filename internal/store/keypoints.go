package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// KeyPointRow keypoint_index 表的行。
type KeyPointRow struct {
	ID               string
	SourceType       models.SourceType
	SourceID         string
	SourceTitle      string
	Content          string
	Description      string
	CitationsJSON    string
	RelationKind     models.RelationKind
	TimeStart        float64
	TimeEnd          float64
	CardVersion      int
	Origin           models.KeyPointOrigin
	ProductionStatus models.KeyPointProductionStatus
	ParentKeyPointID *string
	EvidenceStatus   string
	QualityStatus    models.KeyPointQualityStatus
	StaleAt          string
	StaleReason      string
	CreatedAt        string
}

// IndexKeyPoints 把一个 Source 当前卡片版本的 KeyPoints 拆解写入索引表（先删后插，幂等）。
// 每个 KeyPoint 的 Citation（Segment ID 列表）被解析为聚合时间范围（min start – max end），
// 存入 keypoint_index 表 + keypoint_search FTS5 表。用于 /keypoints 全局视图。
// 真理来源是 artifact_versions.payload；本表是索引投影（ADR-0017）。

// KeyPointReconcileStats 重分析协调结果（K03）。
type KeyPointReconcileStats struct {
	Kept            int // 稳定匹配且内容未变：保留 ID 与全部 Owner 决策
	Updated         int // 稳定匹配但卡片版本/时间跨度更新
	New             int // 新自动重点
	Staled          int // 未匹配但被依赖/被 Owner 触碰：保留并标 stale
	Removed         int // 未匹配且无依赖：删除
	MaterialChanges int // 幂等写入的 MaterialChange 条数
}

// automaticKeyPointRow 旧自动重点的协调视图。
type automaticKeyPointRow struct {
	ID               string
	Content          string
	Description      string
	CardVersion      int
	TimeStart        float64
	TimeEnd          float64
	ProductionStatus string
	QualityStatus    string
	EvidenceStatus   string
}

func indexCardKeyPoints(ctx context.Context, tx *sql.Tx, sourceType models.SourceType, sourceID, sourceTitle string, cardVersion int, card *provider.KnowledgeCard, segments []provider.Segment, oldRows map[string]*automaticKeyPointRow) (*KeyPointReconcileStats, error) {
	stats := &KeyPointReconcileStats{}
	segMap := make(map[string]provider.Segment, len(segments))
	for _, seg := range segments {
		segMap[seg.ID] = seg
	}
	matched := map[string]bool{}
	for _, kp := range card.KeyPoints {
		cites := validCitations(kp.Citations, segMap)
		if len(cites) == 0 {
			continue
		}
		citationsJSON, _ := json.Marshal(cites)
		start, end := spanFromSegments(cites, segMap)
		if end <= start {
			continue
		}
		content := strings.TrimSpace(kp.Content)
		description := strings.TrimSpace(kp.Description)
		key := keyPointReconciliationKey(cites, content)
		old, exists := oldRows[key]
		kpID := ""
		changed := false
		if exists {
			kpID = old.ID
			matched[key] = true
			if old.CardVersion != cardVersion || old.TimeStart != start || old.TimeEnd != end || old.Description != description {
				if _, err := tx.ExecContext(ctx,
					`UPDATE keypoint_index SET card_version=?, time_start=?, time_end=?, description=? WHERE id=?`,
					cardVersion, start, end, description, kpID); err != nil {
					return stats, fmt.Errorf("更新 keypoint_index: %w", err)
				}
				stats.Updated++
				changed = true
			} else {
				stats.Kept++
			}
			// 重现的历史 stale 重点恢复有效。
			if old.EvidenceStatus != "valid" {
				if _, err := tx.ExecContext(ctx,
					`UPDATE keypoint_index SET evidence_status='valid', stale_at=NULL, stale_reason='' WHERE id=?`, kpID); err != nil {
					return stats, err
				}
			}
		} else {
			kpID = uuid.NewString()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, evidence_status, quality_status, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
				kpID, string(sourceType), sourceID, sourceTitle,
				content, description,
				string(citationsJSON), string(models.RelationCitation), start, end, cardVersion,
				string(models.KeyPointAutomatic), string(models.KeyPointInbox), "valid", string(models.KeyPointNeedsReview)); err != nil {
				return stats, fmt.Errorf("写入 keypoint_index: %w", err)
			}
			stats.New++
			changed = true
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM keypoint_search WHERE keypoint_id=?`, kpID); err != nil {
			return stats, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO keypoint_search (keypoint_id, content, description, source_title) VALUES (?, ?, ?, ?)`,
			kpID, content, description, sourceTitle); err != nil {
			return stats, err
		}
		if err := indexLocalKeyPointEmbedding(ctx, tx, kpID, content+" "+description+" "+sourceTitle); err != nil {
			return stats, err
		}
		// K03：MaterialChange 只标记"已具备发现资格重点的实质变化"——
		// 新重点待质量判定通过时由 quality_approved 记录；原样重分析（Kept）
		// 不产生新发现价值。
		if changed && exists && (old.QualityStatus == string(models.KeyPointReady) || old.QualityStatus == string(models.KeyPointOwnerConfirmed) || old.ProductionStatus != string(models.KeyPointInbox)) {
			n, err := recordKeyPointMaterialChangeTx(ctx, tx, kpID, string(sourceType), sourceID, content, citationsJSON, cardVersion)
			if err != nil {
				return stats, err
			}
			stats.MaterialChanges += n
		}
	}

	// 未匹配旧自动重点：被人工派生/Owner 决策/Owner 触碰 → 标 stale 保留；否则删除。
	for key, old := range oldRows {
		if matched[key] {
			continue
		}
		dep, err := keyPointHasDependencies(ctx, tx, old.ID, old.ProductionStatus, old.QualityStatus)
		if err != nil {
			return stats, err
		}
		if dep {
			if _, err := tx.ExecContext(ctx,
				`UPDATE keypoint_index SET evidence_status='stale', stale_at=datetime('now'), stale_reason='重分析后未能匹配该重点' WHERE id=?`,
				old.ID); err != nil {
				return stats, err
			}
			stats.Staled++
		} else {
			if _, err := tx.ExecContext(ctx, `DELETE FROM keypoint_search WHERE keypoint_id=?`, old.ID); err != nil {
				return stats, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM keypoint_index WHERE id=?`, old.ID); err != nil {
				return stats, err
			}
			stats.Removed++
		}
	}
	return stats, nil
}

// keyPointHasDependencies 判断未被匹配的旧重点是否需要保留为 stale：
// 有人工派生（parent_keypoint_id 指向它）或 Owner 相关性决策，或 Owner 触碰过
// 生产状态/质量状态。
func keyPointHasDependencies(ctx context.Context, tx *sql.Tx, id, productionStatus, qualityStatus string) (bool, error) {
	if productionStatus != string(models.KeyPointInbox) || qualityStatus == string(models.KeyPointOwnerConfirmed) {
		return true, nil
	}
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM keypoint_index WHERE parent_keypoint_id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM editorial_relevance WHERE keypoint_id = ? AND owner_override IS NOT NULL AND owner_override != ''`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// recordKeyPointMaterialChangeTx 事务内幂等写 MaterialChange；返回实际新插入行数（0/1）。
func recordKeyPointMaterialChangeTx(ctx context.Context, tx *sql.Tx, keyPointID, sourceType, sourceID, content string, citationsJSON []byte, cardVersion int) (int, error) {
	hash := keyPointReconciliationKey([]string{string(citationsJSON)}, fmt.Sprintf("%s:%d", content, cardVersion))
	res, err := tx.ExecContext(ctx,
		`INSERT INTO material_changes (id,keypoint_id,source_type,source_id,change_kind,snapshot_hash) VALUES (?,?,?,?,?,?) ON CONFLICT(keypoint_id,change_kind,snapshot_hash) DO NOTHING`,
		uuid.NewString(), keyPointID, sourceType, sourceID, "auto_keypoint_changed", hash)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// IndexKeyPoints 重分析后协调全局重点投影（K03 / ADR-0022）：
// 稳定匹配相同来源与语义的自动重点（保留 ID、Owner 决策与生产状态），
// 新重点插入，未匹配重点按依赖 stale 或删除；状态、索引与 MaterialChange
// 在同一事务内完成。原样重分析不产生新 MaterialChange。
func (s *Store) IndexKeyPoints(ctx context.Context, sourceType models.SourceType, sourceID, sourceTitle string, cardVersion int, card *provider.KnowledgeCard, segments []provider.Segment) (*KeyPointReconcileStats, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	oldRows, err := loadAutomaticKeyPointRows(ctx, tx, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	stats, err := indexCardKeyPoints(ctx, tx, sourceType, sourceID, sourceTitle, cardVersion, card, segments, oldRows)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stats, nil
}

// loadAutomaticKeyPointRows 读取全部旧自动重点（协调输入，键为稳定协调键）。
func loadAutomaticKeyPointRows(ctx context.Context, tx *sql.Tx, sourceType models.SourceType, sourceID string) (map[string]*automaticKeyPointRow, error) {
	rows := map[string]*automaticKeyPointRow{}
	query, err := tx.QueryContext(ctx,
		`SELECT id, content, COALESCE(description,''), card_version, time_start, time_end,
		        production_status, quality_status, COALESCE(evidence_status,'valid'), citations_json
		 FROM keypoint_index WHERE source_type=? AND source_id=? AND origin='automatic'`,
		string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer query.Close()
	for query.Next() {
		r := &automaticKeyPointRow{}
		var citationsJSON string
		if err := query.Scan(&r.ID, &r.Content, &r.Description, &r.CardVersion, &r.TimeStart, &r.TimeEnd,
			&r.ProductionStatus, &r.QualityStatus, &r.EvidenceStatus, &citationsJSON); err != nil {
			return nil, err
		}
		var citations []string
		if err := json.Unmarshal([]byte(citationsJSON), &citations); err != nil {
			continue
		}
		rows[keyPointReconciliationKey(citations, r.Content)] = r
	}
	return rows, query.Err()
}

func validCitations(citations []string, segs map[string]provider.Segment) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range citations {
		c = strings.TrimSpace(c)
		if _, ok := segs[c]; ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func spanFromSegments(citations []string, segs map[string]provider.Segment) (float64, float64) {
	if len(citations) == 0 {
		return 0, 0
	}
	starts := make([]float64, 0, len(citations))
	ends := make([]float64, 0, len(citations))
	for _, c := range citations {
		if seg, ok := segs[c]; ok {
			starts = append(starts, seg.Start)
			ends = append(ends, seg.End)
		}
	}
	if len(starts) == 0 {
		return 0, 0
	}
	sort.Float64s(starts)
	sort.Float64s(ends)
	return starts[0], ends[len(ends)-1]
}

func keyPointReconciliationKey(citations []string, content string) string {
	ids := append([]string(nil), citations...)
	sort.Strings(ids)
	return strings.Join(ids, ",") + "\x00" + strings.TrimSpace(content)
}

// ListKeyPoints 分页查询全部 KeyPoint（按 created_at DESC）。
func (s *Store) ListKeyPoints(ctx context.Context, page, perPage int) ([]*KeyPointRow, int, error) {
	return s.ListKeyPointsFiltered(ctx, KeyPointFilter{}, page, perPage)
}

// KeyPointFilter is the unified Inbox query surface. Empty fields are ignored.
type KeyPointFilter struct {
	SourceType                   models.SourceType
	SourceID, PodcastID, ThemeID string
	Status                       models.KeyPointProductionStatus
	QualityStatus                models.KeyPointQualityStatus
	From, To                     string
}

// ListKeyPointsFiltered applies all Inbox dimensions in SQL so pagination and
// totals describe the same result set.
func (s *Store) ListKeyPointsFiltered(ctx context.Context, filter KeyPointFilter, page, perPage int) ([]*KeyPointRow, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}
	var clauses []string
	var args []any
	if filter.SourceType != "" {
		if !validSourceType(filter.SourceType) {
			return nil, 0, fmt.Errorf("%w: invalid source filter", ErrInvalidEditorialState)
		}
		clauses, args = append(clauses, "ki.source_type=?"), append(args, string(filter.SourceType))
	}
	if filter.SourceID != "" {
		clauses, args = append(clauses, "ki.source_id=?"), append(args, filter.SourceID)
	}
	if filter.PodcastID != "" {
		clauses, args = append(clauses, "ki.source_type='episode' AND EXISTS(SELECT 1 FROM episodes e WHERE e.id=ki.source_id AND e.podcast_id=?)"), append(args, filter.PodcastID)
	}
	if filter.ThemeID != "" {
		clauses, args = append(clauses, "EXISTS(SELECT 1 FROM theme_keypoints tk WHERE tk.keypoint_id=ki.id AND tk.theme_id=?)"), append(args, filter.ThemeID)
	}
	if filter.Status != "" {
		if !validKeyPointProductionStatus(filter.Status) {
			return nil, 0, fmt.Errorf("%w: invalid status filter", ErrInvalidEditorialState)
		}
		clauses, args = append(clauses, "ki.production_status=?"), append(args, string(filter.Status))
	}
	if filter.QualityStatus != "" {
		if !validKeyPointQualityStatus(filter.QualityStatus) {
			return nil, 0, fmt.Errorf("%w: invalid quality status filter", ErrInvalidEditorialState)
		}
		clauses, args = append(clauses, "ki.quality_status=?"), append(args, string(filter.QualityStatus))
	}
	if filter.From != "" {
		clauses, args = append(clauses, "ki.created_at>=?"), append(args, filter.From)
	}
	if filter.To != "" {
		clauses, args = append(clauses, "ki.created_at<datetime(?,'+1 day')"), append(args, filter.To)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_index ki`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * perPage
	queryArgs := append(append([]any{}, args...), perPage, offset)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, quality_status, COALESCE(stale_at,''), COALESCE(stale_reason,''), created_at
		 FROM keypoint_index ki`+where+` ORDER BY created_at DESC, time_start ASC, id LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	r, n, err := scanKeyPointRows(rows)
	return r, n, err
}

// SetKeyPointProductionStatuses applies one validated transition to a batch.
func (s *Store) SetKeyPointProductionStatuses(ctx context.Context, ids []string, status models.KeyPointProductionStatus) error {
	if len(ids) == 0 || !validKeyPointProductionStatus(status) {
		return fmt.Errorf("%w: invalid KeyPoint batch", ErrInvalidEditorialState)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE keypoint_index SET production_status=? WHERE id=?`, string(status), strings.TrimSpace(id))
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
	}
	return tx.Commit()
}

// SearchKeyPoints FTS5 全文搜索 KeyPoint。
func (s *Store) SearchKeyPoints(ctx context.Context, query string, page, perPage int) ([]*KeyPointRow, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}
	// 先算总数
	var total int
	countRow := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM keypoint_search WHERE keypoint_search MATCH ?`, query)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * perPage
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ki.id, ki.source_type, ki.source_id, ki.source_title, ki.content, ki.description, ki.citations_json, ki.relation_kind, ki.time_start, ki.time_end, ki.card_version, ki.origin, ki.production_status, ki.parent_keypoint_id, ki.evidence_status, ki.quality_status, COALESCE(ki.stale_at,''), COALESCE(ki.stale_reason,''), ki.created_at
		 FROM keypoint_search ks JOIN keypoint_index ki ON ks.keypoint_id = ki.id
		 WHERE keypoint_search MATCH ? ORDER BY rank LIMIT ? OFFSET ?`,
		query, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	r, n, err := scanKeyPointRows(rows)
	_ = n
	return r, total, err
}

func scanKeyPointRows(rows *sql.Rows) ([]*KeyPointRow, int, error) {
	var out []*KeyPointRow
	for rows.Next() {
		r := &KeyPointRow{}
		var rk string
		var origin, productionStatus string
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.SourceTitle, &r.Content, &r.Description, &r.CitationsJSON, &rk, &r.TimeStart, &r.TimeEnd, &r.CardVersion, &origin, &productionStatus, &r.ParentKeyPointID, &r.EvidenceStatus, &r.QualityStatus, &r.StaleAt, &r.StaleReason, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		r.RelationKind = models.RelationKind(rk)
		r.Origin = models.KeyPointOrigin(origin)
		r.ProductionStatus = models.KeyPointProductionStatus(productionStatus)
		out = append(out, r)
	}
	return out, len(out), rows.Err()
}

// GetKeyPoint reads a persistent KeyPoint from the production material layer.
func (s *Store) GetKeyPoint(ctx context.Context, id string) (*KeyPointRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, quality_status, COALESCE(stale_at,''), COALESCE(stale_reason,''), created_at
		 FROM keypoint_index WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, _, err := scanKeyPointRows(rows)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	return result[0], nil
}

// GetKeyPoints reads the requested KeyPoints in one query. Missing IDs are
// omitted, allowing callers to distinguish them from present rows by ID.
func (s *Store) GetKeyPoints(ctx context.Context, ids []string) (map[string]*KeyPointRow, error) {
	result := make(map[string]*KeyPointRow, len(ids))
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result[id] = nil
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return result, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	args := make([]any, len(unique))
	for i, id := range unique {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, quality_status, COALESCE(stale_at,''), COALESCE(stale_reason,''), created_at
		 FROM keypoint_index WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keyPoints, _, err := scanKeyPointRows(rows)
	if err != nil {
		return nil, err
	}
	for _, keyPoint := range keyPoints {
		result[keyPoint.ID] = keyPoint
	}
	return result, nil
}

// CreateManualKeyPoint adds an Owner-curated KeyPoint without replacing automatic analysis.
func (s *Store) prepareManualKeyPoint(ctx context.Context, keyPoint KeyPointRow) (KeyPointRow, error) {
	keyPoint.Content = strings.TrimSpace(keyPoint.Content)
	keyPoint.Description = strings.TrimSpace(keyPoint.Description)
	if !validSourceType(keyPoint.SourceType) || keyPoint.SourceID == "" || keyPoint.Content == "" || keyPoint.TimeEnd <= keyPoint.TimeStart {
		return KeyPointRow{}, fmt.Errorf("%w: invalid manual keypoint", ErrInvalidEditorialState)
	}
	var citationIDs []string
	if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citationIDs); err != nil || len(citationIDs) == 0 {
		return KeyPointRow{}, fmt.Errorf("%w: manual keypoint needs citation segment IDs", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, keyPoint.SourceType, keyPoint.SourceID)
	if err != nil {
		return KeyPointRow{}, err
	}
	if !exists {
		return KeyPointRow{}, ErrNotFound
	}
	valid, err := s.ValidateSourceCitations(ctx, keyPoint.SourceType, keyPoint.SourceID, citationIDs)
	if err != nil {
		return KeyPointRow{}, err
	}
	if !valid {
		return KeyPointRow{}, fmt.Errorf("%w: citation does not resolve inside source", ErrInvalidEditorialState)
	}
	keyPoint.ID = uuid.NewString()
	if keyPoint.ProductionStatus == "" {
		keyPoint.ProductionStatus = models.KeyPointInbox
	}
	if !validKeyPointProductionStatus(keyPoint.ProductionStatus) {
		return KeyPointRow{}, fmt.Errorf("%w: invalid keypoint production status", ErrInvalidEditorialState)
	}
	if keyPoint.EvidenceStatus == "" {
		keyPoint.EvidenceStatus = "valid"
	}
	return keyPoint, nil
}

func insertManualKeyPointTx(ctx context.Context, tx *sql.Tx, keyPoint KeyPointRow) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, quality_status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, datetime('now'))`,
		keyPoint.ID, string(keyPoint.SourceType), keyPoint.SourceID, keyPoint.SourceTitle, keyPoint.Content, keyPoint.Description, keyPoint.CitationsJSON, string(models.RelationCitation), keyPoint.TimeStart, keyPoint.TimeEnd, string(models.KeyPointManual), string(keyPoint.ProductionStatus), keyPoint.ParentKeyPointID, keyPoint.EvidenceStatus, string(models.KeyPointOwnerConfirmed)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO keypoint_search (keypoint_id, content, description, source_title) VALUES (?, ?, ?, ?)`, keyPoint.ID, keyPoint.Content, keyPoint.Description, keyPoint.SourceTitle); err != nil {
		return err
	}
	return indexLocalKeyPointEmbedding(ctx, tx, keyPoint.ID, keyPoint.Content+" "+keyPoint.Description+" "+keyPoint.SourceTitle)
}

// CreateManualKeyPoint persists an Owner-created or human-edited KeyPoint that re-analysis must never overwrite.
func (s *Store) CreateManualKeyPoint(ctx context.Context, keyPoint KeyPointRow) (*KeyPointRow, error) {
	keyPoint, err := s.prepareManualKeyPoint(ctx, keyPoint)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := insertManualKeyPointTx(ctx, tx, keyPoint); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKeyPoint(ctx, keyPoint.ID)
}

// ValidateSourceCitations proves that every citation resolves to a stable
// Segment in the cited Source. Non-empty JSON alone is not evidence validity.
func (s *Store) ValidateSourceCitations(ctx context.Context, sourceType models.SourceType, sourceID string, citationIDs []string) (bool, error) {
	if len(citationIDs) == 0 {
		return false, nil
	}
	available := map[string]bool{}
	if sourceType == models.SourceDocument {
		document, err := s.GetDocument(ctx, sourceID)
		if err != nil {
			return false, err
		}
		for _, segment := range DocumentSegments(document) {
			available[segment.ID] = true
		}
	} else {
		version, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
		if err != nil {
			return false, err
		}
		var transcript provider.TranscriptPayload
		if err := json.Unmarshal([]byte(version.Payload), &transcript); err != nil {
			return false, err
		}
		for _, segment := range transcript.Segments {
			available[segment.ID] = true
		}
	}
	for _, id := range citationIDs {
		if !available[strings.TrimSpace(id)] {
			return false, nil
		}
	}
	return true, nil
}

// SetKeyPointProductionStatus moves one KeyPoint through the material Inbox.
func (s *Store) SetKeyPointProductionStatus(ctx context.Context, id string, status models.KeyPointProductionStatus) error {
	if !validKeyPointProductionStatus(status) {
		return fmt.Errorf("%w: invalid keypoint production status", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE keypoint_index SET production_status=? WHERE id=?`, string(status), id)
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

func validKeyPointProductionStatus(status models.KeyPointProductionStatus) bool {
	return status == models.KeyPointInbox || status == models.KeyPointShortlisted || status == models.KeyPointUsed || status == models.KeyPointDismissed
}

func validKeyPointQualityStatus(status models.KeyPointQualityStatus) bool {
	switch status {
	case models.KeyPointNeedsReview, models.KeyPointReady, models.KeyPointOwnerConfirmed, models.KeyPointQualityDismissed:
		return true
	default:
		return false
	}
}

// SetKeyPointQualityStatus records the learning-quality decision. The first
// transition to a discovery-eligible state also creates an idempotent material
// change, so AutomaticDiscovery only sees reviewed learning results.
func (s *Store) SetKeyPointQualityStatus(ctx context.Context, id string, status models.KeyPointQualityStatus) error {
	if !validKeyPointQualityStatus(status) {
		return fmt.Errorf("%w: invalid KeyPoint quality status", ErrInvalidEditorialState)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sourceType models.SourceType
	var sourceID, content, citations, previous string
	if err := tx.QueryRowContext(ctx, `SELECT source_type,source_id,content,citations_json,quality_status FROM keypoint_index WHERE id=?`, id).Scan(&sourceType, &sourceID, &content, &citations, &previous); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE keypoint_index SET quality_status=? WHERE id=?`, string(status), id); err != nil {
		return err
	}
	wasEligible := previous == string(models.KeyPointReady) || previous == string(models.KeyPointOwnerConfirmed)
	isEligible := status == models.KeyPointReady || status == models.KeyPointOwnerConfirmed
	if isEligible && !wasEligible {
		snapshot := sha256.Sum256([]byte(content + "\x00" + citations))
		if _, err := tx.ExecContext(ctx, `INSERT INTO material_changes (id,keypoint_id,source_type,source_id,change_kind,snapshot_hash) VALUES (?,?,?,?,?,?) ON CONFLICT(keypoint_id,change_kind,snapshot_hash) DO NOTHING`, uuid.NewString(), id, sourceType, sourceID, "quality_approved", fmt.Sprintf("%x", snapshot)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkKeyPointStale keeps a potentially outdated learning result visible but
// excludes it from automatic discovery until the Owner re-evaluates it.
func (s *Store) MarkKeyPointStale(ctx context.Context, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: stale reason required", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE keypoint_index SET stale_at=datetime('now'),stale_reason=? WHERE id=?`, reason, id)
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

// ClearKeyPointStaleness records that the Owner has re-evaluated a stale
// learning result. It does not silently promote the quality status.
func (s *Store) ClearKeyPointStaleness(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE keypoint_index SET stale_at=NULL,stale_reason=NULL WHERE id=?`, id)
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

// DeleteKeyPointsForSource Purge 时删除该 Source 的全部 KeyPoint 索引。
func (s *Store) DeleteKeyPointsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM keypoint_search WHERE keypoint_id IN (SELECT id FROM keypoint_index WHERE source_type=? AND source_id=?)`,
		string(sourceType), sourceID)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`DELETE FROM keypoint_index WHERE source_type=? AND source_id=?`,
		string(sourceType), sourceID)
	return err
}
