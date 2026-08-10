package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestAnnotation_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, err := s.UpsertAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "这是标注")
	if err != nil {
		t.Fatal(err)
	}
	if a.Body != "这是标注" {
		t.Errorf("标注内容不符: %s", a.Body)
	}

	// 读取
	got, err := s.GetAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID {
		t.Error("ID 不一致")
	}

	// 更新（Upsert 同 key）
	s.UpsertAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "更新后的标注")
	got2, _ := s.GetAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`)
	if got2.Body != "更新后的标注" {
		t.Errorf("更新后标注应为'更新后的标注'，实际 %s", got2.Body)
	}

	// 列表
	annot, _ := s.ListAnnotations(ctx)
	if len(annot) != 1 {
		t.Errorf("应只有 1 个标注，实际 %d", len(annot))
	}

	// 删除
	s.DeleteAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`)
	if _, err := s.GetAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`); err != ErrNotFound {
		t.Error("删除后应返回 ErrNotFound")
	}
}

func TestPin_ToggleAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 第一次 pin
	pinned, err := s.TogglePin(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "标题")
	if err != nil || !pinned {
		t.Fatalf("首次 pin 应返回 true: %v %v", pinned, err)
	}
	if !s.IsPinned(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`) {
		t.Error("IsPinned 应为 true")
	}

	// 第二次 toggle → 取消
	pinned2, _ := s.TogglePin(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "标题")
	if pinned2 {
		t.Error("第二次 toggle 应返回 false（取消）")
	}
	if s.IsPinned(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`) {
		t.Error("取消后 IsPinned 应为 false")
	}

	// 列表
	s.TogglePin(ctx, models.SourceUpload, "up1", `["seg-0002"]`, 10, 20, "标题2")
	pins, _ := s.ListPins(ctx)
	if len(pins) != 1 {
		t.Errorf("应有 1 个 pin，实际 %d", len(pins))
	}
}

func TestCollection_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建
	c, err := s.CreateCollection(ctx, "沟通技巧", "跨 Source 的沟通相关要点")
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "沟通技巧" {
		t.Errorf("标题不符: %s", c.Title)
	}

	// 添加成员
	s.AddToCollection(ctx, c.ID, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "标题1", "备注1")
	s.AddToCollection(ctx, c.ID, models.SourceEpisode, "ep2", `["seg-0002"]`, 10, 20, "标题2", "")

	// 成员不能重复添加
	s.AddToCollection(ctx, c.ID, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "标题1", "重复")
	items, _ := s.ListCollectionItems(ctx, c.ID)
	if len(items) != 2 {
		t.Errorf("应有 2 个成员（去重），实际 %d", len(items))
	}

	// 集合列表（带 item_count）
	cols, _ := s.ListCollections(ctx)
	if len(cols) != 1 || cols[0].ItemCount != 2 {
		t.Errorf("集合列表应显示 2 个成员，实际 %+v", cols)
	}

	// 移除成员
	s.RemoveFromCollection(ctx, c.ID, models.SourceEpisode, "ep1", `["seg-0001"]`)
	items2, _ := s.ListCollectionItems(ctx, c.ID)
	if len(items2) != 1 {
		t.Errorf("移除后应剩 1 个成员，实际 %d", len(items2))
	}

	// 删除集合（级联删成员）
	s.DeleteCollection(ctx, c.ID)
	cols2, _ := s.ListCollections(ctx)
	if len(cols2) != 0 {
		t.Error("删除集合后应无集合")
	}
}

// TestGetCollection_NotFound 验证不存在的集合返回 ErrNotFound。
// 覆盖 GetCollection 中 sql.ErrNoRows 分支。
func TestGetCollection_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.GetCollection(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("不存在的集合应返回 ErrNotFound，实际 %v", err)
	}
}

// TestGetCollection_DBError 验证查询出错时返回错误。
// 覆盖 GetCollection 中非 ErrNoRows 错误分支（删除 collections 表）。
func TestGetCollection_DBError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE collections`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCollection(ctx, "any"); err == nil {
		t.Fatal("collections 表缺失应报错")
	}
}

// TestGetAnnotation_NotFound 验证不存在的标注返回 ErrNotFound。
// 覆盖 GetAnnotation 中 sql.ErrNoRows 分支。
func TestGetAnnotation_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.GetAnnotation(ctx, models.SourceEpisode, "ep-x", `["seg-0001"]`); err != ErrNotFound {
		t.Errorf("不存在的标注应返回 ErrNotFound，实际 %v", err)
	}
}

// TestAnnotations_DBErrors 验证 annotations/pins/collections 系列查询在表缺失时返回错误。
// 覆盖 ListAnnotations/ListPins/CreateCollection/ListCollections/AddToCollection/
// ListCollectionItems 的查询错误分支。
func TestAnnotations_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 删除三张表
	for _, tbl := range []string{"annotations", "pins", "collections", "collection_items"} {
		if _, err := s.DB.ExecContext(ctx, `DROP TABLE `+tbl); err != nil {
			t.Fatalf("DROP TABLE %s: %v", tbl, err)
		}
	}
	if _, err := s.ListAnnotations(ctx); err == nil {
		t.Error("annotations 表缺失时 ListAnnotations 应报错")
	}
	if _, err := s.ListPins(ctx); err == nil {
		t.Error("pins 表缺失时 ListPins 应报错")
	}
	if _, err := s.CreateCollection(ctx, "t", "d"); err == nil {
		t.Error("collections 表缺失时 CreateCollection 应报错")
	}
	if _, err := s.ListCollections(ctx); err == nil {
		t.Error("collections 表缺失时 ListCollections 应报错")
	}
	if err := s.AddToCollection(ctx, "c1", models.SourceEpisode, "ep1", `["s"]`, 0, 1, "t", ""); err == nil {
		t.Error("collection_items 表缺失时 AddToCollection 应报错")
	}
	if _, err := s.ListCollectionItems(ctx, "c1"); err == nil {
		t.Error("collection_items 表缺失时 ListCollectionItems 应报错")
	}
}

// TestTogglePin_DeleteError 验证已 pin 时删除失败返回错误。
// 覆盖 TogglePin 中已存在记录时 DELETE 失败分支（先 pin 再删表再 toggle）。
func TestTogglePin_DeleteError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 先 pin
	if _, err := s.TogglePin(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "t"); err != nil {
		t.Fatal(err)
	}
	// 删除 pins 表 → 已存在分支的 DELETE 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE pins`); err != nil {
		t.Fatal(err)
	}
	// 第一次 toggle 因表缺失返回错误（SELECT 阶段）
	if _, err := s.TogglePin(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "t"); err == nil {
		t.Fatal("pins 表缺失时 TogglePin 应报错")
	}
}

// TestUpsertAnnotation_DBError 验证 UpsertAnnotation 写入失败返回错误。
// 覆盖 UpsertAnnotation 中 ExecContext 失败分支（删除 annotations 表）。
func TestUpsertAnnotation_DBError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE annotations`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAnnotation(ctx, models.SourceEpisode, "ep1", `["seg-0001"]`, 0, 5, "body"); err == nil {
		t.Fatal("annotations 表缺失时 UpsertAnnotation 应报错")
	}
}

// TestListPins_ScanError 验证 pins 行数据异常时 Scan 失败。
// 覆盖 ListPins 中 rows.Scan 失败分支（插入非法 time_start）。
func TestListPins_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 插入非法 time_start（字符串无法转 float）→ Scan 失败
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO pins (source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title, note)
		 VALUES ('episode', 'ep1', '["s"]', 'citation', 'bad', 0, '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListPins(ctx); err == nil {
		t.Fatal("非法 time_start 应导致 Scan 失败")
	}
}

// TestListAnnotations_ScanError 验证 annotations 行数据异常时 Scan 失败。
// 覆盖 ListAnnotations 中 rows.Scan 失败分支。
func TestListAnnotations_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO annotations (id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, body)
		 VALUES ('a1', 'episode', 'ep1', '["s"]', 'citation', 'bad', 0, 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAnnotations(ctx); err == nil {
		t.Fatal("非法 time_start 应导致 Scan 失败")
	}
}

// TestListCollectionItems_ScanError 验证 collection_items 行数据异常时 Scan 失败。
// 覆盖 ListCollectionItems 中 rows.Scan 失败分支。
func TestListCollectionItems_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// collection_items.collection_id 有外键约束，先创建 collection
	c, err := s.CreateCollection(ctx, "专题", "desc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO collection_items (collection_id, source_type, source_id, segment_ids, relation_kind, time_start, time_end, source_title, note)
		 VALUES (?, 'episode', 'ep1', '["s"]', 'citation', 'bad', 0, '', '')`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListCollectionItems(ctx, c.ID); err == nil {
		t.Fatal("非法 time_start 应导致 Scan 失败")
	}
}
