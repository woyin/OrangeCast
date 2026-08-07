package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// MaxParaphrasesPerAnchor 每个锚点保留的最近 Paraphrase 数量（ADR-0018 R2）。
const MaxParaphrasesPerAnchor = 3

// ParaphraseRow paraphrases 表的行（GeneratedDerivative，ADR-0018）。
type ParaphraseRow struct {
	ID           string
	SourceType   models.SourceType
	SourceID     string
	Anchor       string
	SegmentIDs   string
	RelationKind models.RelationKind
	TimeStart    float64
	TimeEnd      float64
	Question     string
	Body         string
	Provider     string
	Model        string
	CreatedAt    string
}

// ParaphraseAnchor 由一组 Segment ID 计算稳定锚点 key（排序后的 JSON 数组串）。
// 同一组参考片段（与顺序无关）映射到同一锚点，用于"最近 N 次淘汰"。
func ParaphraseAnchor(segmentIDs []string) string {
	dedup := map[string]bool{}
	var ids []string
	for _, id := range segmentIDs {
		id = strings.TrimSpace(id)
		if id == "" || dedup[id] {
			continue
		}
		dedup[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, _ := json.Marshal(ids)
	return string(b)
}

// CreateParaphrase 写入一条 Paraphrase，并在同锚点超过 MaxParaphrasesPerAnchor 时淘汰最旧的。
// relation_kind 恒为 'reference'（GeneratedDerivative）；时间范围由程序从参考 Segment 解析。
func (s *Store) CreateParaphrase(ctx context.Context, sourceType models.SourceType, sourceID, question, body, providerName, modelName string, references []string, segments []provider.Segment) (*ParaphraseRow, error) {
	if len(references) == 0 {
		return nil, fmt.Errorf("复述讲解至少需要一个参考片段")
	}
	anchor := ParaphraseAnchor(references)
	start, end := spanFromSegments(references, segmentMap(segments))
	if end <= start {
		return nil, fmt.Errorf("参考片段无法解析出有效时间范围")
	}
	segJSON, _ := json.Marshal(references)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO paraphrases (id, source_type, source_id, anchor, segment_ids, relation_kind, time_start, time_end, question, body, provider, model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(sourceType), sourceID, anchor, string(segJSON), string(models.RelationReference),
		start, end, question, body, providerName, modelName); err != nil {
		return nil, fmt.Errorf("写入 paraphrases: %w", err)
	}

	// 同锚点淘汰：保留最近 MaxParaphrasesPerAnchor 条，删除更旧的。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM paraphrases
		 WHERE source_type=? AND source_id=? AND anchor=?
		   AND id NOT IN (
		     SELECT id FROM paraphrases
		     WHERE source_type=? AND source_id=? AND anchor=?
		     ORDER BY created_at DESC, rowid DESC
		     LIMIT ?
		   )`,
		string(sourceType), sourceID, anchor,
		string(sourceType), sourceID, anchor,
		MaxParaphrasesPerAnchor); err != nil {
		return nil, fmt.Errorf("淘汰旧 Paraphrase: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetParaphrase(ctx, id)
}

// GetParaphrase 按 id 读取单条。
func (s *Store) GetParaphrase(ctx context.Context, id string) (*ParaphraseRow, error) {
	r := &ParaphraseRow{}
	var rk string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, anchor, segment_ids, relation_kind, time_start, time_end, question, body, provider, model, created_at
		 FROM paraphrases WHERE id=?`, id).
		Scan(&r.ID, &r.SourceType, &r.SourceID, &r.Anchor, &r.SegmentIDs, &rk, &r.TimeStart, &r.TimeEnd, &r.Question, &r.Body, &r.Provider, &r.Model, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.RelationKind = models.RelationKind(rk)
	return r, nil
}

// ListParaphrasesForAnchor 返回某锚点下的 Paraphrase（最近在前）。
func (s *Store) ListParaphrasesForAnchor(ctx context.Context, sourceType models.SourceType, sourceID, anchor string) ([]*ParaphraseRow, error) {
	return listParaphrases(ctx, s.DB,
		`WHERE source_type=? AND source_id=? AND anchor=? ORDER BY created_at DESC, rowid DESC`,
		string(sourceType), sourceID, anchor)
}

// ListParaphrasesForSource 返回某 Source 的全部 Paraphrase（按锚点分组，最近在前）。
func (s *Store) ListParaphrasesForSource(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*ParaphraseRow, error) {
	return listParaphrases(ctx, s.DB,
		`WHERE source_type=? AND source_id=? ORDER BY anchor, created_at DESC, rowid DESC`,
		string(sourceType), sourceID)
}

func listParaphrases(ctx context.Context, db *sql.DB, where string, args ...any) ([]*ParaphraseRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, source_type, source_id, anchor, segment_ids, relation_kind, time_start, time_end, question, body, provider, model, created_at
		 FROM paraphrases `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ParaphraseRow
	for rows.Next() {
		r := &ParaphraseRow{}
		var rk string
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.Anchor, &r.SegmentIDs, &rk, &r.TimeStart, &r.TimeEnd, &r.Question, &r.Body, &r.Provider, &r.Model, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.RelationKind = models.RelationKind(rk)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteParaphrasesForSource Purge 时删除该 Source 的全部 Paraphrase。
func (s *Store) DeleteParaphrasesForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM paraphrases WHERE source_type=? AND source_id=?`,
		string(sourceType), sourceID)
	return err
}

// segmentMap 把 Segment 切片转为 id→Segment 映射。
func segmentMap(segments []provider.Segment) map[string]provider.Segment {
	m := make(map[string]provider.Segment, len(segments))
	for _, seg := range segments {
		m[seg.ID] = seg
	}
	return m
}
