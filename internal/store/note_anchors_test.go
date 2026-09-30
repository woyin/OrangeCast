package store

import (
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"strings"
	"testing"
)

func TestNoteAnchorAndImmutableHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	podcast, err := s.CreatePodcast(ctx, "https://example.com/anchor.xml", "来源", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "anchor", Title: "音频"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	id := eps[0].ID
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, id, models.JobTranscribe)
	v, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, id, KindTranscript, "test", "model", "1", job.ID, `{"segments":[{"id":"seg-1","start":2262,"end":2268,"text":"旧原文"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, id, KindTranscript, v); err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "source_note", Content: "旧理解", CitationsJSON: `["seg-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	var a models.NoteAnchor
	if err = json.Unmarshal([]byte(note.AnchorJSON), &a); err != nil || a.Position != 2262 || a.SnapshotID == "" {
		t.Fatalf("anchor: %+v %v", a, err)
	}
	v2, _ := s.CreateArtifactVersion(ctx, models.SourceEpisode, id, KindTranscript, "test", "model", "1", job.ID, `{"segments":[{"id":"seg-1","start":3,"end":4,"text":"新原文"}]}`)
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, id, KindTranscript, v2); err != nil {
		t.Fatal(err)
	}
	edited, err := s.UpdateOwnerNote(ctx, note.ID, "新的理解", note.CitationsJSON, "[]", 1)
	if err != nil || edited.AnchorJSON != note.AnchorJSON {
		t.Fatalf("anchor changed: %+v %v", edited, err)
	}
	if _, err = s.UpdateOwnerNote(ctx, note.ID, "过期草稿", note.CitationsJSON, "[]", 1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	history, err := s.ListOwnerNoteRevisions(ctx, note.ID)
	if err != nil || len(history) != 2 || history[1].Content != "旧理解" {
		t.Fatalf("history %+v %v", history, err)
	}
	_, old, _, err := s.SnapshotContent(ctx, a.SnapshotID)
	if err != nil || old[0].Text != "旧原文" {
		t.Fatalf("silently rebound: %+v %v", old, err)
	}
	_, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "source_note", Content: "过期来源", CitationsJSON: `["seg-1"]`, AnchorJSON: note.AnchorJSON})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.DeleteOwnerNote(ctx, note.ID, 2); err != nil {
		t.Fatal(err)
	}
	history, err = s.ListOwnerNoteRevisions(ctx, note.ID)
	if err != nil || len(history) != 2 {
		t.Fatal("delete lost history", err)
	}
	_, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "owner_reflection", Content: "我的理解", AnchorJSON: `{"position":-1}`})
	if !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if strings.Contains(note.AnchorJSON, "新原文") {
		t.Fatal("snapshot should be identity, not new content")
	}
}
