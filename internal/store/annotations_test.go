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
