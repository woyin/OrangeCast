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

// KeyPointRow keypoint_index 表的行。
type KeyPointRow struct {
	ID            string
	SourceType    models.SourceType
	SourceID      string
	SourceTitle   string
	Content       string
	Description   string
	CitationsJSON string
	TimeStart     float64
	TimeEnd       float64
	CardVersion   int
	CreatedAt     string
}

// IndexKeyPoints 把一个 Source 当前卡片版本的 KeyPoints 拆解写入索引（先删后插，幂等）。
// segments 用于解析 Citation 时间范围。
func (s *Store) IndexKeyPoints(ctx context.Context, sourceType models.SourceType, sourceID, sourceTitle string, cardVersion int, card *provider.KnowledgeCard, segments []provider.Segment) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 先删旧索引 + FTS
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM keypoint_search WHERE keypoint_id IN (SELECT id FROM keypoint_index WHERE source_type=? AND source_id=?)`,
		string(sourceType), sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM keypoint_index WHERE source_type=? AND source_id=?`,
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
		kpID := uuid.NewString()
		created := fmt.Sprintf("%s", card.Title) // 用 card 标题做排序辅助
		_ = created
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO keypoint_index (id, source_type, source_id, source_title, content, description, citations_json, time_start, time_end, card_version, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			kpID, string(sourceType), sourceID, sourceTitle,
			strings.TrimSpace(kp.Content), strings.TrimSpace(kp.Description),
			string(citationsJSON), start, end, cardVersion); err != nil {
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
		`SELECT id, source_type, source_id, source_title, content, description, citations_json, time_start, time_end, card_version, created_at
		 FROM keypoint_index ORDER BY created_at DESC, id LIMIT ? OFFSET ?`, perPage, offset)
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
		`SELECT ki.id, ki.source_type, ki.source_id, ki.source_title, ki.content, ki.description, ki.citations_json, ki.time_start, ki.time_end, ki.card_version, ki.created_at
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
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.SourceTitle, &r.Content, &r.Description, &r.CitationsJSON, &r.TimeStart, &r.TimeEnd, &r.CardVersion, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, len(out), rows.Err()
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

var _ = errors.New

// GraphNode 图谱节点（一个 Episode）。
type GraphNode struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// GraphLink 图谱边（两个 Episode 共享的 Tag）。
type GraphLink struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Tags   []string `json:"tags"`
}

// GraphData 图谱完整数据。
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Links []GraphLink `json:"links"`
}

// GetTagGraph 返回 Episode + Tag 共现图谱。
// 节点 = processed 的 Episode；边 = 共享至少 1 个 Tag 的 Episode 对。
func (s *Store) GetTagGraph(ctx context.Context) (*GraphData, error) {
	// 1) 查询所有 processed Episode 的 Tag（从当前 knowledge_card 版本 payload）
	rows, err := s.DB.QueryContext(ctx,
		`SELECT e.id, e.title, av.payload
		 FROM episodes e
		 JOIN artifact_versions av ON av.source_type='episode' AND av.source_id=e.id
		 WHERE av.kind='knowledge_card' AND av.version = e.current_card_version
		 ORDER BY e.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type epTags struct {
		id    string
		title string
		tags  []string
	}
	var eps []epTags
	for rows.Next() {
		var id, title, payload string
		if err := rows.Scan(&id, &title, &payload); err != nil {
			return nil, err
		}
		var card struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal([]byte(payload), &card); err != nil {
			continue
		}
		eps = append(eps, epTags{id: id, title: title, tags: card.Tags})
	}

	// 2) 构造节点
	gd := &GraphData{}
	tagMap := map[string]map[string]bool{} // tag -> {episodeID: true}
	for _, ep := range eps {
		gd.Nodes = append(gd.Nodes, GraphNode{ID: ep.id, Title: ep.title, Status: "processed"})
		for _, tag := range ep.tags {
			if tagMap[tag] == nil {
				tagMap[tag] = map[string]bool{}
			}
			tagMap[tag][ep.id] = true
		}
	}

	// 3) 构造边：共享至少 1 个 Tag 的 Episode 对
	pairTags := map[string]map[string]bool{} // "idA|idB" -> {tag: true}
	for tag, eps := range tagMap {
		ids := make([]string, 0, len(eps))
		for id := range eps {
			ids = append(ids, id)
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				if a > b {
					a, b = b, a
				}
				key := a + "|" + b
				if pairTags[key] == nil {
					pairTags[key] = map[string]bool{}
				}
				pairTags[key][tag] = true
			}
		}
	}
	for pair, tags := range pairTags {
		parts := strings.SplitN(pair, "|", 2)
		if len(parts) != 2 {
			continue
		}
		tagList := make([]string, 0, len(tags))
		for t := range tags {
			tagList = append(tagList, t)
		}
		sort.Strings(tagList)
		gd.Links = append(gd.Links, GraphLink{Source: parts[0], Target: parts[1], Tags: tagList})
	}
	return gd, nil
}
