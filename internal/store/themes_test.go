package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestThemeLifecycleRequiresScopedPublicationEligibleKeyPoints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "themes@example.com")
	podcast, _ := s.CreatePodcast(ctx, "https://feed.example.com/rss", "播客", "", "")
	s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "episode", Title: "单集", AudioURL: "https://cdn.example.com/ep.mp3"}})
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episodes[0].ID, "单集", 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "审查成本", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, _ := s.ListKeyPoints(ctx, 1, 10)
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "技术周刊"})
	if err != nil {
		t.Fatal(err)
	}
	theme, err := s.CreateTheme(ctx, models.Theme{EditorialProfileID: profile.ID, Name: "AI 工程成本", Description: "速度与审查"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddKeyPointToTheme(ctx, theme.ID, keyPoints[0].ID, "supports"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("unscoped KeyPoint should be rejected: %v", err)
	}
	if err := s.GrantSourceScope(ctx, profile.ID, models.SourceEpisode, episodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddKeyPointToTheme(ctx, theme.ID, keyPoints[0].ID, "supports"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddKeyPointToTheme(ctx, theme.ID, keyPoints[0].ID, "conflicts"); err != nil {
		t.Fatal(err)
	}
	relations, err := s.ListThemeKeyPoints(ctx, theme.ID)
	if err != nil || len(relations) != 1 || relations[0].Relationship != "conflicts" {
		t.Fatalf("theme relation should be durable and updatable: relations=%+v err=%v", relations, err)
	}
	if err := s.SetThemeStatus(ctx, theme.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	themes, err := s.ListThemes(ctx, profile.ID)
	if err != nil || len(themes) != 1 || themes[0].Status != "confirmed" {
		t.Fatalf("theme status should persist: themes=%+v err=%v", themes, err)
	}
	if err := s.SetThemeStatus(ctx, theme.ID, "wrong"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid theme status should be rejected: %v", err)
	}
}

func TestThemesRejectInvalidAndMissingState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateTheme(ctx, models.Theme{EditorialProfileID: "missing"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("blank theme should be invalid: %v", err)
	}
	if _, err := s.CreateTheme(ctx, models.Theme{EditorialProfileID: "missing", Name: "有效主题"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile should be rejected: %v", err)
	}
	if _, err := s.GetTheme(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing theme should be not found: %v", err)
	}
	if err := s.SetThemeStatus(ctx, "missing", "confirmed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing theme status should be not found: %v", err)
	}
	if err := s.AddKeyPointToTheme(ctx, "missing", "missing", "wrong"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid relation should be rejected before lookup: %v", err)
	}
	if err := s.AddKeyPointToTheme(ctx, "missing", "missing", "supports"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing theme should be rejected: %v", err)
	}
}

func TestThemeQueriesSurfaceDatabaseFailures(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE themes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListThemes(ctx, "profile"); err == nil {
		t.Fatal("missing themes table should fail list")
	}
	if _, err := s.GetTheme(ctx, "theme"); err == nil {
		t.Fatal("missing themes table should fail lookup")
	}
	if err := s.SetThemeStatus(ctx, "theme", "confirmed"); err == nil {
		t.Fatal("missing themes table should fail update")
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE theme_keypoints`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListThemeKeyPoints(ctx, "theme"); err == nil {
		t.Fatal("missing theme_keypoints table should fail list")
	}
}

func TestListThemesReturnsScanError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE themes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE themes (id TEXT, editorial_profile_id TEXT, name TEXT, description TEXT, status TEXT, created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO themes (editorial_profile_id, name, status) VALUES ('profile', '主题', 'suggested')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListThemes(ctx, "profile"); err == nil {
		t.Fatal("NULL id should return theme scan error")
	}
}
