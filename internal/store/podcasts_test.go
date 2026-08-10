package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestUpload_Lifecycle 验证上传元数据的创建/读取/列表/状态更新。
func TestUpload_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	// 创建
	u, err := s.CreateUpload(ctx, "音轨.mp3", "audio/mpeg", 1024)
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if u.ProcessingStatus != models.StatusUnprocessed {
		t.Errorf("新建 upload 应 Unprocessed，实际 %q", u.ProcessingStatus)
	}

	// 读取
	got, err := s.GetUploadByID(ctx, u.ID)
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetUploadByID: %v %+v", err, got)
	}
	if got.OriginalFilename != "音轨.mp3" || got.ContentType != "audio/mpeg" || got.SizeBytes != 1024 {
		t.Errorf("upload 内容不符: %+v", got)
	}
	// 未知名 → ErrNotFound
	if _, err := s.GetUploadByID(ctx, "nope"); err != ErrNotFound {
		t.Errorf("未知名应 ErrNotFound，实际 %v", err)
	}

	// 列表
	s.CreateUpload(ctx, "第二.mp3", "audio/mpeg", 2048)
	list, err := s.ListUploads(ctx)
	if err != nil {
		t.Fatalf("ListUploads: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个 upload，实际 %d", len(list))
	}

	// 状态更新
	if err := s.UpdateUploadStatus(ctx, u.ID, models.StatusQueuedEp); err != nil {
		t.Fatalf("UpdateUploadStatus: %v", err)
	}
	got, _ = s.GetUploadByID(ctx, u.ID)
	if got.ProcessingStatus != models.StatusQueuedEp {
		t.Errorf("状态应更新为 queued，实际 %q", got.ProcessingStatus)
	}
}

// TestPodcastAndEpisodes_Pagination 验证播客/单集分页与抓取时间更新。
func TestPodcastAndEpisodes_Pagination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	p, err := s.CreatePodcast(ctx, "https://f.xml", "Pod", "desc", "https://img.png")
	if err != nil {
		t.Fatalf("CreatePodcast: %v", err)
	}
	// 建 12 集（分页 perPage=10 → 2 页）
	eps := make([]models.Episode, 12)
	for i := range eps {
		eps[i] = models.Episode{GUID: "g" + string(rune('a'+i)), Title: "Ep", AudioURL: "https://a.mp3"}
	}
	if _, err := s.MergeEpisodes(ctx, p.ID, eps); err != nil {
		t.Fatalf("MergeEpisodes: %v", err)
	}

	// 第 1 页 10 条，总数 12
	page1, total, err := s.ListEpisodesPaginated(ctx, p.ID, 1, 10)
	if err != nil {
		t.Fatalf("ListEpisodesPaginated: %v", err)
	}
	if total != 12 || len(page1) != 10 {
		t.Errorf("第 1 页应 10 条/共 12，实际 %d/%d", len(page1), total)
	}
	// 第 2 页 2 条
	page2, total2, _ := s.ListEpisodesPaginated(ctx, p.ID, 2, 10)
	if len(page2) != 2 || total2 != 12 {
		t.Errorf("第 2 页应 2 条/共 12，实际 %d/%d", len(page2), total2)
	}

	// 抓取时间更新
	if err := s.UpdatePodcastFetched(ctx, p.ID); err != nil {
		t.Fatalf("UpdatePodcastFetched: %v", err)
	}
	after, err := s.GetPodcastByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPodcastByID: %v", err)
	}
	if after.LastFetchedAt == nil || *after.LastFetchedAt == "" {
		t.Error("UpdatePodcastFetched 后 LastFetchedAt 应非空")
	}

	// ListPodcastsForRefresh 应能取到该播客
	refresh, err := s.ListPodcastsForRefresh(ctx, 25)
	if err != nil {
		t.Fatalf("ListPodcastsForRefresh: %v", err)
	}
	if len(refresh) != 1 || refresh[0].ID != p.ID {
		t.Errorf("应能取到播客，实际 %+v", refresh)
	}
}

// TestMergeEpisodes_Dedup 验证按 GUID 去重：重复 GUID 不新增。
func TestMergeEpisodes_Dedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	eps := []models.Episode{{GUID: "g1", Title: "A", AudioURL: "https://a.mp3"}}
	if n, err := s.MergeEpisodes(ctx, p.ID, eps); err != nil || n != 1 {
		t.Fatalf("首次合并应新增 1，实际 n=%d err=%v", n, err)
	}
	eps2 := []models.Episode{{GUID: "g1", Title: "A 改", AudioURL: "https://a.mp3"}}
	if n, err := s.MergeEpisodes(ctx, p.ID, eps2); err != nil || n != 0 {
		t.Errorf("重复 GUID 应新增 0，实际 n=%d err=%v", n, err)
	}
	all, _ := s.ListEpisodes(ctx, p.ID)
	if len(all) != 1 {
		t.Errorf("去重后应 1 集，实际 %d", len(all))
	}
}

// TestListPodcasts 验证按标题排序列出全部订阅。
func TestListPodcasts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")

	s.CreatePodcast(ctx, "https://b.xml", "乙播客", "", "")
	s.CreatePodcast(ctx, "https://a.xml", "甲播客", "", "")
	list, err := s.ListPodcasts(ctx)
	if err != nil {
		t.Fatalf("ListPodcasts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个播客，实际 %d", len(list))
	}
	// 按标题排序（SQLite 按 Unicode 码点：乙 U+4E59 < 甲 U+7532）
	if list[0].Title != "乙播客" || list[1].Title != "甲播客" {
		t.Errorf("应按标题排序，实际 %+v", list)
	}
}

// TestGetPodcastByID_NotFound 验证查询不存在的播客返回 ErrNotFound。
func TestGetPodcastByID_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	if _, err := s.GetPodcastByID(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("不存在的播客应 ErrNotFound，实际 %v", err)
	}
}

// TestListEpisodesPaginated_InvalidParams 验证 page/perPage 边界归一化（0/负数回退默认）。
func TestListEpisodesPaginated_InvalidParams(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "Pod", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep1", AudioURL: "https://a.mp3"}})

	// 非法 page/perPage（0/负数）应归一化不报错
	eps, total, err := s.ListEpisodesPaginated(ctx, p.ID, 0, 0)
	if err != nil {
		t.Fatalf("page=0/perPage=0 应归一化不报错: %v", err)
	}
	if total != 1 || len(eps) != 1 {
		t.Errorf("应归一化后返回 1 集，实际 total=%d len=%d", total, len(eps))
	}
	if _, _, err := s.ListEpisodesPaginated(ctx, p.ID, -1, -5); err != nil {
		t.Fatalf("负数 page/perPage 应归一化不报错: %v", err)
	}
}

// TestPodcasts_DBErrors 验证 podcasts 系列查询在表缺失时返回错误。
// 覆盖 GetPodcastByID/ListPodcasts/ListPodcastsForRefresh 查询失败分支。
func TestPodcasts_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE podcasts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPodcastByID(ctx, "p1"); err == nil {
		t.Error("podcasts 表缺失时 GetPodcastByID 应报错")
	}
	if _, err := s.ListPodcasts(ctx); err == nil {
		t.Error("podcasts 表缺失时 ListPodcasts 应报错")
	}
	if _, err := s.ListPodcastsForRefresh(ctx, 10); err == nil {
		t.Error("podcasts 表缺失时 ListPodcastsForRefresh 应报错")
	}
}

// TestListPodcasts_ScanError 验证 podcasts 行数据异常时 Scan 失败。
// 覆盖 ListPodcasts 中 rows.Scan 失败分支。
func TestListPodcasts_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 重建缺列的表使 SELECT 查询失败（COUNT 等不适用，直接用缺列验证 Scan/Query）
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE podcasts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE podcasts (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListPodcasts(ctx); err == nil {
		t.Fatal("podcasts 表缺列时 ListPodcasts 应报错")
	}
}

// TestMergeEpisodes_Empty 验证空 episode 列表直接返回 0。
// 覆盖 MergeEpisodes 中 len(eps)==0 分支。
func TestMergeEpisodes_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	n, err := s.MergeEpisodes(ctx, p.ID, nil)
	if err != nil {
		t.Fatalf("MergeEpisodes(nil): %v", err)
	}
	if n != 0 {
		t.Errorf("空列表应返回 0，实际 %d", n)
	}
}

// TestMergeEpisodes_InsertError 验证插入 episode 失败时报错。
// 覆盖 MergeEpisodes 中 "插入 episode" 分支（删除 episodes 表）。
func TestMergeEpisodes_InsertError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}}); err == nil {
		t.Fatal("episodes 表缺失时 MergeEpisodes 应报错")
	}
}

// TestListEpisodes_QueryError 验证分页查询失败时报错。
// 覆盖 ListEpisodes 中查询失败分支（删除 episodes 表）。
func TestListEpisodes_QueryError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListEpisodes(ctx, "p1"); err == nil {
		t.Fatal("episodes 表缺失时 ListEpisodes 应报错")
	}
}

// TestEpisodesUploads_DBErrors 验证 episodes/uploads 系列查询在表缺失时返回错误。
// 覆盖 GetEpisodeByID/CreateUpload/GetUploadByID/ListUploads 错误分支。
func TestEpisodesUploads_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEpisodeByID(ctx, "ep1"); err == nil {
		t.Error("episodes 表缺失时 GetEpisodeByID 应报错")
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE uploads`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUpload(ctx, "a.mp3", "audio/mpeg", 10); err == nil {
		t.Error("uploads 表缺失时 CreateUpload 应报错")
	}
	if _, err := s.GetUploadByID(ctx, "up1"); err == nil {
		t.Error("uploads 表缺失时 GetUploadByID 应报错")
	}
	if _, err := s.ListUploads(ctx); err == nil {
		t.Error("uploads 表缺失时 ListUploads 应报错")
	}
}
