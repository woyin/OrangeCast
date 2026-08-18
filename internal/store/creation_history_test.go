package store

import (
	"context"
	"github.com/woyin/orangecast/internal/models"
	"testing"
)

func TestCreationHistoryImportsPublishedAndUnpublishedWork(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "history@example.com")
	profile, _ := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "历史画像"})
	published, err := s.CreateCreationHistory(ctx, models.CreationHistory{EditorialProfileID: profile.ID, Status: "published", Title: "审查成本", CoreClaim: "自动化会把成本转移到审查", Content: "完整正文", SourceURL: "https://example.com/work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCreationHistory(ctx, models.CreationHistory{EditorialProfileID: profile.ID, Status: "unpublished", Title: "审查成本续篇", CoreClaim: "自动化会把成本转移到审查的反馈环", Content: "未发布正文"}); err != nil {
		t.Fatal(err)
	}
	found, err := s.FindCreationHistoryCandidates(ctx, profile.ID, "自动化会把成本转移到审查")
	if err != nil || len(found) != 2 || found[0].ID == "" || published.ID == "" {
		t.Fatalf("history should provide transparent duplicate candidates: found=%+v err=%v", found, err)
	}
}
