// Annotation / Pin / Collection 数据访问层（ADR-0017）。
// 锚定方式：所有三种实体锚定在 (source_type, source_id, segment_ids) 上——
// 即"某 Source 的一组 Segment 引用"。不锚定 KeyPoint 文字（重新分析后 KeyPoint
// 文字会变，但 Segment 在同一 Transcript 版本内稳定）。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ---- Annotation ----

// Annotation 用户在某个 Citation 上附加的个人文字注解（ADR-0017）。
// 锚定在 (source_type, source_id, segment_ids) 上，保证证据不变则标注不丢。
type Annotation struct {
	ID           string
	SourceType   models.SourceType
	SourceID     string
	SegmentIDs   string
	RelationKind models.RelationKind
	TimeStart    float64
	TimeEnd      float64
	Body         string
	CreatedAt    string
	UpdatedAt    string
}

// UpsertAnnotation 新增或（按同一锚点）更新一条标注。
func (s *Store) UpsertAnnotation(ctx context.Context, sourceType models.SourceType, sourceID, segmentIDs string, timeStart, timeEnd float64, body string) (*Annotation, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO annotations (id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(source_type, source_id, segment_ids) DO UPDATE SET body = excluded.body, relation_kind = excluded.relation_kind, updated_at = datetime('now')`,
		id, string(sourceType), sourceID, segmentIDs, string(models.RelationCitation), timeStart, timeEnd, body)
	if err != nil {
		return nil, fmt.Errorf("写入标注: %w", err)
	}
	return s.GetAnnotation(ctx, sourceType, sourceID, segmentIDs)
}

// GetAnnotation 按锚点查询单条标注。
func (s *Store) GetAnnotation(ctx context.Context, sourceType models.SourceType, sourceID, segmentIDs string) (*Annotation, error) {
	a := &Annotation{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, body, created_at, updated_at
		 FROM annotations WHERE source_type=? AND source_id=? AND segment_ids=?`,
		string(sourceType), sourceID, segmentIDs).
		Scan(&a.ID, &a.SourceType, &a.SourceID, &a.SegmentIDs, &a.RelationKind, &a.TimeStart, &a.TimeEnd, &a.Body, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// ListAnnotations 返回全部标注（按更新时间倒序）。
func (s *Store) ListAnnotations(ctx context.Context) ([]*Annotation, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, body, created_at, updated_at
		 FROM annotations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Annotation
	for rows.Next() {
		a := &Annotation{}
		if err := rows.Scan(&a.ID, &a.SourceType, &a.SourceID, &a.SegmentIDs, &a.RelationKind, &a.TimeStart, &a.TimeEnd, &a.Body, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAnnotation 按锚点删除一条标注。
func (s *Store) DeleteAnnotation(ctx context.Context, sourceType models.SourceType, sourceID, segmentIDs string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM annotations WHERE source_type=? AND source_id=? AND segment_ids=?`,
		string(sourceType), sourceID, segmentIDs)
	return err
}

// ---- Pin ----

// Pin 用户标记某个 Citation“值得记住”的轻量动作（ADR-0017）。
// Pin 是 CloudWisePod 内标记，不等于沉淀到知识库的 KnowledgeNote。
type Pin struct {
	SourceType   models.SourceType
	SourceID     string
	SegmentIDs   string
	RelationKind models.RelationKind
	TimeStart    float64
	TimeEnd      float64
	SourceTitle  string
	Note         *string
	CreatedAt    string
}

// TogglePin 切换某锚点的收藏状态（已收藏则取消，未收藏则添加），并返回新状态。
func (s *Store) TogglePin(ctx context.Context, sourceType models.SourceType, sourceID, segmentIDs string, timeStart, timeEnd float64, sourceTitle string) (bool, error) {
	// 检查是否已 pin
	var existing string
	err := s.DB.QueryRowContext(ctx,
		`SELECT segment_ids FROM pins WHERE source_type=? AND source_id=? AND segment_ids=?`,
		string(sourceType), sourceID, segmentIDs).Scan(&existing)
	if err == nil {
		// 已 pin → 取消
		_, err := s.DB.ExecContext(ctx,
			`DELETE FROM pins WHERE source_type=? AND source_id=? AND segment_ids=?`,
			string(sourceType), sourceID, segmentIDs)
		return false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// 未 pin → 添加
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO pins (source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(sourceType), sourceID, segmentIDs, string(models.RelationCitation), timeStart, timeEnd, sourceTitle)
	return true, err
}

// ListPins 返回全部收藏（按创建时间倒序）。
func (s *Store) ListPins(ctx context.Context) ([]*Pin, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title, note, created_at
		 FROM pins ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Pin
	for rows.Next() {
		p := &Pin{}
		if err := rows.Scan(&p.SourceType, &p.SourceID, &p.SegmentIDs, &p.RelationKind, &p.TimeStart, &p.TimeEnd, &p.SourceTitle, &p.Note, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// IsPinned 判断某锚点当前是否已收藏。
func (s *Store) IsPinned(ctx context.Context, sourceType models.SourceType, sourceID, segmentIDs string) bool {
	var n int
	s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pins WHERE source_type=? AND source_id=? AND segment_ids=?`,
		string(sourceType), sourceID, segmentIDs).Scan(&n)
	return n > 0
}

// ---- Collection ----

// Collection 用户按自定义主题组织跨 Source 的 Citation 的组（ADR-0017）。
// 按主题组织而非按 Source；ItemCount 是聚合字段，仅在 ListCollections 中填充。
type Collection struct {
	ID          string
	Title       string
	Description string
	CreatedAt   string
	UpdatedAt   string
	ItemCount   int // 聚合字段
}

// CollectionItem 一个 Collection 的成员（跨 Source 的 Citation 关联）。
// ItemCount 是聚合字段，仅在 ListCollections 中填充。
type CollectionItem struct {
	CollectionID string
	SourceType   models.SourceType
	SourceID     string
	SegmentIDs   string
	RelationKind models.RelationKind
	TimeStart    float64
	TimeEnd      float64
	SourceTitle  string
	Note         *string
	AddedAt      string
}

// CreateCollection 创建新集合。
func (s *Store) CreateCollection(ctx context.Context, title, description string) (*Collection, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO collections (id, title, description) VALUES (?, ?, ?)`,
		id, title, description)
	if err != nil {
		return nil, err
	}
	return s.GetCollection(ctx, id)
}

// GetCollection 按 ID 查询单个集合。
func (s *Store) GetCollection(ctx context.Context, id string) (*Collection, error) {
	c := &Collection{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, title, COALESCE(description,''), created_at, updated_at FROM collections WHERE id=?`, id).
		Scan(&c.ID, &c.Title, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListCollections 返回全部集合（含每个集合的成员数，按更新时间倒序）。
func (s *Store) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT c.id, c.title, COALESCE(c.description,''), c.created_at, c.updated_at,
		        (SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id) AS item_count
		 FROM collections c ORDER BY c.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Collection
	for rows.Next() {
		c := &Collection{}
		if err := rows.Scan(&c.ID, &c.Title, &c.Description, &c.CreatedAt, &c.UpdatedAt, &c.ItemCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddToCollection 把一个 Citation 关联加入集合（已存在则忽略）。
func (s *Store) AddToCollection(ctx context.Context, collectionID string, sourceType models.SourceType, sourceID, segmentIDs string, timeStart, timeEnd float64, sourceTitle, note string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO collection_items (collection_id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		collectionID, string(sourceType), sourceID, segmentIDs, string(models.RelationCitation), timeStart, timeEnd, sourceTitle, note)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE collections SET updated_at = datetime('now') WHERE id = ?`, collectionID)
	return err
}

// ListCollectionItems 返回某集合的全部成员（按加入时间倒序）。
func (s *Store) ListCollectionItems(ctx context.Context, collectionID string) ([]*CollectionItem, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT collection_id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title, note, added_at
		 FROM collection_items WHERE collection_id=? ORDER BY added_at DESC`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CollectionItem
	for rows.Next() {
		ci := &CollectionItem{}
		if err := rows.Scan(&ci.CollectionID, &ci.SourceType, &ci.SourceID, &ci.SegmentIDs, &ci.RelationKind, &ci.TimeStart, &ci.TimeEnd, &ci.SourceTitle, &ci.Note, &ci.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

// RemoveFromCollection 把某个 Citation 关联移出集合。
func (s *Store) RemoveFromCollection(ctx context.Context, collectionID string, sourceType models.SourceType, sourceID, segmentIDs string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM collection_items WHERE collection_id=? AND source_type=? AND source_id=? AND segment_ids=?`,
		collectionID, string(sourceType), sourceID, segmentIDs)
	return err
}

// DeleteCollection 删除整个集合。
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id)
	return err
}
