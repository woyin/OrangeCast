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
