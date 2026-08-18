package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestOwnerNotesPreserveSourceAttributionAndReflections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "notes@example.com")
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/notes.xml", "学习播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "note-episode", Title: "单集", AudioURL: "https://example.com/note.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, episodes[0].ID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, "test", "test", "1", job.ID, `{"language":"zh","text":"原始表达","segments":[{"id":"seg-1","start":0,"end":1,"text":"原始表达"}]}`)
	if err != nil || s.SetCurrentVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, version) != nil {
		t.Fatalf("seed transcript: version=%d err=%v", version, err)
	}
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, Kind: "source_note", Content: "来源明确表达了这个观点", CitationsJSON: `["seg-1"]`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, Kind: "owner_reflection", Content: "我不同意其中的前提"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, Kind: "source_note", Content: "没有证据的归因"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("source-faithful notes require citations: %v", err)
	}
	notes, err := s.ListOwnerNotes(ctx, models.SourceEpisode, episodes[0].ID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("notes should remain source-scoped and distinct: notes=%+v err=%v", notes, err)
	}
	if err := s.UpsertRightsConstraint(ctx, models.SourceEpisode, episodes[0].ID, "expression_reuse", "不得大段复制原文", true); err != nil {
		t.Fatal(err)
	}
	var details string
	if err := s.DB.QueryRowContext(ctx, `SELECT details FROM rights_constraints WHERE source_type=? AND source_id=?`, string(models.SourceEpisode), episodes[0].ID).Scan(&details); err != nil || details == "" {
		t.Fatalf("rights constraint should persist independently: details=%q err=%v", details, err)
	}
	constraints, err := s.ListRightsConstraints(ctx, models.SourceEpisode, episodes[0].ID)
	if err != nil || len(constraints) != 1 || !constraints[0].Active {
		t.Fatalf("rights constraints should remain source-scoped and inspectable: constraints=%+v err=%v", constraints, err)
	}
}
