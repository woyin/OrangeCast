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
