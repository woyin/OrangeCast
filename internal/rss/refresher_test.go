package rss

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRefreshAll 验证 RefreshAll 抓取 feed 并把 episode 合并入库、更新抓取时间。
// 通过注入 fetchFeed stub 隔离网络（仿 bundleFor 可注入模式）。
func TestRefreshAll(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, err := s.CreatePodcast(ctx, "https://feed.example.com/pod.xml", "测试播客", "desc", "")
	if err != nil {
		t.Fatalf("创建播客: %v", err)
	}

	r := NewRefresher(s)
	r.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL, Title: "测试播客"}, []models.Episode{
			{GUID: "ep-1", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"},
			{GUID: "ep-2", Title: "第二集", AudioURL: "https://cdn.example.com/2.mp3"},
		}, nil
	}

	if err := r.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll 失败: %v", err)
	}

	eps, err := s.ListEpisodes(ctx, p.ID)
	if err != nil {
		t.Fatalf("读取 episodes: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("应合并入 2 个 episode，实际 %d", len(eps))
	}

	// 再次刷新同一 feed → 已存在的 guid 被去重，不新增。
	if err := r.RefreshAll(ctx); err != nil {
		t.Fatalf("二次 RefreshAll 失败: %v", err)
	}
	eps, _ = s.ListEpisodes(ctx, p.ID)
	if len(eps) != 2 {
		t.Fatalf("重复刷新不应新增 episode，实际 %d", len(eps))
	}
}

func TestRefreshAll_AutoIngestionRespectsPodcastPolicy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, err := s.CreatePodcast(ctx, "https://feed.example.com/pod.xml", "测试播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPodcastIngestionPolicy(ctx, p.ID, models.IngestionAllNew); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "historical", Title: "历史单集", AudioURL: "https://cdn.example.com/old.mp3"}}); err != nil {
		t.Fatal(err)
	}
	r := NewRefresher(s)
	r.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL}, []models.Episode{{GUID: "ep-1", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"}}, nil
	}
	if err := r.RefreshAll(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].JobType != models.JobTranscribe || !jobs[0].Automated {
		t.Fatalf("all_new should queue one transcription job: jobs=%+v err=%v", jobs, err)
	}
	// 再刷新同一 feed：没有新 Episode，也不能重复入队。
	if err := r.RefreshAll(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, _ = s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatalf("repeat refresh should not duplicate jobs: %d", len(jobs))
	}
	old, err := s.ListUnprocessedEpisodes(ctx, p.ID)
	if err != nil || len(old) != 1 || old[0].GUID != "historical" {
		t.Fatalf("historical candidates must remain manual: episodes=%+v err=%v", old, err)
	}
}

// TestRefreshAll_FetchErrorContinues 验证单个 feed 抓取失败不影响其余 feed（continue 语义）。
func TestRefreshAll_FetchErrorContinues(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p1, _ := s.CreatePodcast(ctx, "https://feed.example.com/a.xml", "A", "", "")
	_, _ = s.CreatePodcast(ctx, "https://feed.example.com/b.xml", "B", "", "")

	r := NewRefresher(s)
	r.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		if feedURL == p1.FeedURL {
			return nil, nil, &feedErr{}
		}
		return &models.Podcast{FeedURL: feedURL}, []models.Episode{
			{GUID: "b-1", Title: "B 第一集", AudioURL: "https://cdn.example.com/b.mp3"},
		}, nil
	}

	// RefreshAll 遇错应跳过并继续，不返回整体错误。
	if err := r.RefreshAll(ctx); err != nil {
		t.Fatalf("RefreshAll 应吞掉单 feed 错误并返回 nil，实际 %v", err)
	}
	eps, _ := s.ListEpisodes(ctx, p1.ID)
	if len(eps) != 0 {
		t.Fatalf("失败的 feed 不应合并 episode，实际 %d", len(eps))
	}
}

type feedErr struct{}

func (e *feedErr) Error() string { return "抓取失败" }

// TestRefreshOne_MergeError 验证 MergeEpisodes 失败时 refreshOne 返回错误。
// 覆盖 refreshOne 中 MergeEpisodes err 分支（删除 episodes 表）。
func TestRefreshOne_MergeError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, _ := s.CreatePodcast(ctx, "https://feed.example.com/pod.xml", "P", "", "")
	r := NewRefresher(s)
	r.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL}, []models.Episode{
			{GUID: "ep-1", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"},
		}, nil
	}
	// 删除 episodes 表 → MergeEpisodes 写入失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatalf("DROP TABLE episodes: %v", err)
	}
	if err := r.refreshOne(ctx, p); err == nil {
		t.Fatal("episodes 表缺失时 refreshOne 应报错")
	}
}

// TestRefresher_StartStop 验证 Refresher 的 cron 启动/停止可安全调用（不 panic）。
func TestRefresher_StartStop(t *testing.T) {
	r := NewRefresher(nil)
	r.Start()
	r.Start() // 重复启动应安全
	r.Stop()
	r.Stop() // 重复停止应安全
}

// TestRefresher_RunScheduled 验证 cron 定时入口触发刷新并合并 episode。
func TestRefresher_RunScheduled(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, _ := s.CreatePodcast(ctx, "https://feed.example.com/pod.xml", "测试播客", "desc", "")
	r := NewRefresher(s)
	r.fetchFeed = func(feedURL string) (*models.Podcast, []models.Episode, error) {
		return &models.Podcast{FeedURL: feedURL}, []models.Episode{
			{GUID: "ep-1", Title: "第一集", AudioURL: "https://cdn.example.com/1.mp3"},
		}, nil
	}
	// 直接调用 cron 入口（不等待 30 分钟调度）
	r.runScheduled()
	eps, _ := s.ListEpisodes(ctx, p.ID)
	if len(eps) != 1 {
		t.Errorf("runScheduled 应合并 1 集，实际 %d", len(eps))
	}
}

// TestRefresher_RunScheduled_Error 验证 RefreshAll 失败时 runScheduled 记录日志且不 panic。
func TestRefresher_RunScheduled_Error(t *testing.T) {
	s := newTestStore(t)
	r := NewRefresher(s)
	// 关闭 DB → ListPodcastsForRefresh 失败 → RefreshAll 返回错误 → runScheduled 记录日志
	s.Close()
	r.runScheduled()
}
