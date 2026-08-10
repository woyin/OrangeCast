package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func seedEpisodeForArtifact(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	return eps[0].ID
}

func TestArtifactVersions_ImmutableAndIncrement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)

	// 需要一个真实 job id（FK）
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

	v1, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "whisper-large-v3", "1", job.ID, `{"text":"first"}`)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "whisper-large-v3", "1", job.ID, `{"text":"second"}`)
	if err != nil {
		t.Fatal(err)
	}
	if v1 != 1 || v2 != 2 {
		t.Fatalf("版本号应递增 1,2，实际 %d,%d", v1, v2)
	}

	// 重新处理不覆盖历史：两版都还在
	av1, _ := s.GetArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 1)
	av2, _ := s.GetArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 2)
	if av1.Payload != `{"text":"first"}` || av2.Payload != `{"text":"second"}` {
		t.Errorf("历史版本被覆盖: %s / %s", av1.Payload, av2.Payload)
	}
	if len(s.mustList(ctx, models.SourceEpisode, sourceID, KindTranscript, t)) != 2 {
		t.Error("应存在 2 个版本")
	}
}

func TestArtifactVersions_CurrentPointerAndRevert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "m", "1", job.ID, `{"v":1}`)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, "groq", "m", "1", job.ID, `{"v":2}`)

	// 指向 v2
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 2); err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != 2 {
		t.Errorf("当前版本应为 2，实际 %d", cur.Version)
	}
	// 回退到 v1
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 1); err != nil {
		t.Fatal(err)
	}
	cur, _ = s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript)
	if cur.Version != 1 {
		t.Errorf("回退后当前版本应为 1，实际 %d", cur.Version)
	}
	// 指向不存在版本应报错
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 99); err == nil {
		t.Error("指向不存在版本应报错")
	}
}

func (s *Store) mustList(ctx context.Context, st models.SourceType, sid string, kind ArtifactKind, t *testing.T) []*models.ArtifactVersion {
	t.Helper()
	vs, err := s.ListArtifactVersions(ctx, st, sid, kind)
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

// TestCreateArtifactVersion_InvalidJobID 验证引用不存在的 job 时因外键约束报错。
func TestCreateArtifactVersion_InvalidJobID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript,
		"groq", "m", "1", "nonexistent-job", `{"text":"x"}`); err == nil {
		t.Fatal("引用不存在的 job 应因外键约束报错")
	}
}

// TestGetArtifactVersion_NotFound 验证读取不存在的版本返回 ErrNotFound。
// 覆盖 GetArtifactVersion 中 sql.ErrNoRows 分支。
func TestGetArtifactVersion_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	if _, err := s.GetArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript, 99); err != ErrNotFound {
		t.Errorf("不存在的版本应返回 ErrNotFound，实际 %v", err)
	}
}

// TestGetCurrentVersion_NotSet 验证未设置当前版本时返回 ErrNotFound。
// 覆盖 GetCurrentVersion 中 version 未设置（NULL）分支。
func TestGetCurrentVersion_NotSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, KindTranscript); err != ErrNotFound {
		t.Errorf("未设置当前版本应返回 ErrNotFound，实际 %v", err)
	}
}

// TestCurrentVersion_Upload 验证 upload 源的当前版本指针读写。
// 覆盖 SetCurrentVersion/GetCurrentVersion 中 table = "uploads" 分支。
func TestCurrentVersion_Upload(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job, _ := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
	v, err := s.CreateArtifactVersion(ctx, models.SourceUpload, up.ID, KindTranscript, "groq", "m", "1", job.ID, `{"text":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceUpload, up.ID, KindTranscript, v); err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, KindTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != v {
		t.Errorf("upload 当前版本应为 %d，实际 %d", v, cur.Version)
	}
}

// TestArtifacts_DBErrors 验证 artifact 系列查询在表缺失时返回错误。
// 覆盖 ListArtifactVersions/GetArtifactVersion 查询错误分支。
func TestArtifacts_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE artifact_versions`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListArtifactVersions(ctx, models.SourceEpisode, "ep1", KindTranscript); err == nil {
		t.Error("artifact_versions 表缺失时 ListArtifactVersions 应报错")
	}
	if _, err := s.GetArtifactVersion(ctx, models.SourceEpisode, "ep1", KindTranscript, 1); err == nil {
		t.Error("artifact_versions 表缺失时 GetArtifactVersion 应报错")
	}
}

// TestListArtifactVersions_ScanError 验证 artifact 行数据异常时 Scan 失败。
// 覆盖 ListArtifactVersions 中 rows.Scan 失败分支（version 非整数）。
func TestListArtifactVersions_ScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	// 先创建合法版本（job_id 外键满足），再把 version 改为字符串 → Scan 到 int 失败
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript,
		"groq", "m", "1", job.ID, `{"text":"x"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE artifact_versions SET version = 'bad' WHERE source_id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListArtifactVersions(ctx, models.SourceEpisode, sourceID, KindTranscript); err == nil {
		t.Fatal("version 非整数应导致 Scan 失败")
	}
}

// TestCreateArtifactVersion_QueryError 验证查询最大版本失败时报错。
// 覆盖 CreateArtifactVersion 中 QueryRow 失败分支（删除 artifact_versions 表）。
func TestCreateArtifactVersion_QueryError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE artifact_versions`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript,
		"groq", "m", "1", job.ID, `{"text":"x"}`); err == nil {
		t.Fatal("artifact_versions 表缺失时 CreateArtifactVersion 应报错")
	}
}

// TestCreateArtifactVersion_InsertError 验证版本插入失败时报错。
// 覆盖 CreateArtifactVersion 中 "创建产物版本" 错误分支（INSERT 被触发器中止）。
func TestCreateArtifactVersion_InsertError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "a@b.com")
	sourceID := seedEpisodeForArtifact(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER abort_av BEFORE INSERT ON artifact_versions BEGIN SELECT RAISE(ABORT,'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, KindTranscript,
		"groq", "m", "1", job.ID, `{"text":"x"}`); err == nil {
		t.Fatal("artifact_versions INSERT 被中止时应报错")
	}
}
