package store

import (
	"context"
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
	CreatedAt        string
}

// IndexKeyPoints 把一个 Source 当前卡片版本的 KeyPoints 拆解写入索引表（先删后插，幂等）。
// 每个 KeyPoint 的 Citation（Segment ID 列表）被解析为聚合时间范围（min start – max end），
// 存入 keypoint_index 表 + keypoint_search FTS5 表。用于 /keypoints 全局视图。
// 真理来源是 artifact_versions.payload；本表是索引投影（ADR-0017）。
func (s *Store) IndexKeyPoints(ctx context.Context, sourceType models.SourceType, sourceID, sourceTitle string, cardVersion int, card *provider.KnowledgeCard, segments []provider.Segment) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 自动 KeyPoint 可以随 KnowledgeCard 重建；手工/编辑 KeyPoint 是 Owner 的成果，
	// 必须保留。对相同引用和正文的自动 KeyPoint 尽量复用 ID，避免文章关系无故漂移。
	oldIDs := map[string]string{}
	oldRows, err := tx.QueryContext(ctx,
		`SELECT id, citations_json, content FROM keypoint_index WHERE source_type=? AND source_id=? AND origin='automatic'`,
		string(sourceType), sourceID)
	if err != nil {
		return err
	}
	for oldRows.Next() {
		var id, citationsJSON, content string
		if err := oldRows.Scan(&id, &citationsJSON, &content); err != nil {
			oldRows.Close()
			return err
		}
		var citations []string
		if err := json.Unmarshal([]byte(citationsJSON), &citations); err == nil {
			oldIDs[keyPointReconciliationKey(citations, content)] = id
		}
	}
	if err := oldRows.Err(); err != nil {
		oldRows.Close()
		return err
	}
	if err := oldRows.Close(); err != nil {
		return err
	}

	// 先删旧自动索引 + FTS
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM keypoint_search WHERE keypoint_id IN (SELECT id FROM keypoint_index WHERE source_type=? AND source_id=? AND origin='automatic')`,
		string(sourceType), sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM keypoint_index WHERE source_type=? AND source_id=? AND origin='automatic'`,
		string(sourceType), sourceID); err != nil {
		return err
	}

	segMap := make(map[string]provider.Segment, len(segments))
	for _, seg := range segments {
		segMap[seg.ID] = seg
	}

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
		kpID := oldIDs[keyPointReconciliationKey(cites, kp.Content)]
		if kpID == "" {
			kpID = uuid.NewString()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, evidence_status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			kpID, string(sourceType), sourceID, sourceTitle,
			strings.TrimSpace(kp.Content), strings.TrimSpace(kp.Description),
			string(citationsJSON), string(models.RelationCitation), start, end, cardVersion,
			string(models.KeyPointAutomatic), string(models.KeyPointInbox), "valid"); err != nil {
			return fmt.Errorf("写入 keypoint_index: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO keypoint_search (keypoint_id, content, description, source_title) VALUES (?, ?, ?, ?)`,
			kpID, strings.TrimSpace(kp.Content), strings.TrimSpace(kp.Description), sourceTitle); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM keypoint_index`).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * perPage
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, created_at
		 FROM keypoint_index ORDER BY created_at DESC, time_start ASC, id LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	r, n, err := scanKeyPointRows(rows)
	return r, n, err
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
		`SELECT ki.id, ki.source_type, ki.source_id, ki.source_title, ki.content, ki.description, ki.citations_json, ki.relation_kind, ki.time_start, ki.time_end, ki.card_version, ki.origin, ki.production_status, ki.parent_keypoint_id, ki.evidence_status, ki.created_at
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
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.SourceTitle, &r.Content, &r.Description, &r.CitationsJSON, &rk, &r.TimeStart, &r.TimeEnd, &r.CardVersion, &origin, &productionStatus, &r.ParentKeyPointID, &r.EvidenceStatus, &r.CreatedAt); err != nil {
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
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, created_at
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

// CreateManualKeyPoint adds an Owner-curated KeyPoint without replacing automatic analysis.
func (s *Store) CreateManualKeyPoint(ctx context.Context, keyPoint KeyPointRow) (*KeyPointRow, error) {
	keyPoint.Content = strings.TrimSpace(keyPoint.Content)
	keyPoint.Description = strings.TrimSpace(keyPoint.Description)
	if !validSourceType(keyPoint.SourceType) || keyPoint.SourceID == "" || keyPoint.Content == "" || keyPoint.TimeEnd <= keyPoint.TimeStart {
		return nil, fmt.Errorf("%w: invalid manual keypoint", ErrInvalidEditorialState)
	}
	var citationIDs []string
	if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citationIDs); err != nil || len(citationIDs) == 0 {
		return nil, fmt.Errorf("%w: manual keypoint needs citation segment IDs", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, keyPoint.SourceType, keyPoint.SourceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	keyPoint.ID = uuid.NewString()
	if keyPoint.ProductionStatus == "" {
		keyPoint.ProductionStatus = models.KeyPointInbox
	}
	if !validKeyPointProductionStatus(keyPoint.ProductionStatus) {
		return nil, fmt.Errorf("%w: invalid keypoint production status", ErrInvalidEditorialState)
	}
	if keyPoint.EvidenceStatus == "" {
		keyPoint.EvidenceStatus = "valid"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, relation_kind, time_start, time_end, card_version, origin, production_status, parent_keypoint_id, evidence_status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, datetime('now'))`,
		keyPoint.ID, string(keyPoint.SourceType), keyPoint.SourceID, keyPoint.SourceTitle, keyPoint.Content, keyPoint.Description, keyPoint.CitationsJSON, string(models.RelationCitation), keyPoint.TimeStart, keyPoint.TimeEnd, string(models.KeyPointManual), string(keyPoint.ProductionStatus), keyPoint.ParentKeyPointID, keyPoint.EvidenceStatus)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO keypoint_search (keypoint_id, content, description, source_title) VALUES (?, ?, ?, ?)`, keyPoint.ID, keyPoint.Content, keyPoint.Description, keyPoint.SourceTitle); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKeyPoint(ctx, keyPoint.ID)
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
