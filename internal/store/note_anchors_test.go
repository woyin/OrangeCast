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

func TestNoteAnchorNoPositionAndHistoricalReflection(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	id := seedEpisodeForArtifact(t, s)
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "original.mp3", "mp3", 100, "old-audio"); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, id, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, id, KindTranscript, "test", "model", "1", job.ID, `{"segments":[{"id":"old-seg","start":10,"end":20,"text":"旧原文"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, id, KindTranscript, v); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "owner_reflection", Content: "无音频位置理解", AnchorJSON: `{"no_position":true}`})
	if err != nil {
		t.Fatal(err)
	}
	var anchor models.NoteAnchor
	_ = json.Unmarshal([]byte(note.AnchorJSON), &anchor)
	if !anchor.NoPosition || anchor.SnapshotID == "" || len(anchor.SegmentIDs) > 0 {
		t.Fatal(anchor)
	}
	old := models.NoteAnchor{SnapshotID: snap.ID, Version: snap.ContentVersion, Position: 12, SegmentIDs: []string{"old-seg"}, Mode: "original", AudioSHA256: "old-audio"}
	raw, _ := json.Marshal(old)
	v, err = s.CreateArtifactVersion(ctx, models.SourceEpisode, id, KindTranscript, "test", "model", "2", job.ID, `{"segments":[{"id":"new-seg","start":1,"end":2,"text":"新原文"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, id, KindTranscript, v); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "replacement.mp3", "mp3", 100, "new-audio"); err != nil {
		t.Fatal(err)
	}
	note, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "owner_reflection", Content: "仍是旧音频的理解", ReferencesJSON: `["old-seg"]`, AnchorJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(note.AnchorJSON), &anchor)
	if anchor.AudioSHA256 != "old-audio" || anchor.SnapshotID != snap.ID || anchor.Position != 12 {
		t.Fatal(anchor)
	}
	old.AudioSHA256 = "new-audio"
	raw, _ = json.Marshal(old)
	if _, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "owner_reflection", Content: "不能冒充新音频", AnchorJSON: string(raw)}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"no_position":true,"position":3}`, `{"mode":"other"}`, `{"mode":"original","plan_id":"p"}`, `{"mode":"dj"}`, `{"no_position":true,"mode":"original"}`} {
		if _, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: id, Kind: "owner_reflection", Content: "非法锚点", AnchorJSON: invalid}); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(invalid, err)
		}
	}
	podcast, err := s.CreatePodcast(ctx, "https://anchor.test/other", "另一个来源", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "other", Title: "其他", AudioURL: "https://other.mp3"}}); err != nil {
		t.Fatal(err)
	}
	otherEpisodes, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := otherEpisodes[0].ID
	old.AudioSHA256 = "old-audio"
	raw, _ = json.Marshal(old)
	if _, err = s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: other, Kind: "owner_reflection", Content: "跨来源伪造", AnchorJSON: string(raw)}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}

func TestCheckListeningIdentityWithoutQueueMembership(t *testing.T) {
	s := newTestStore(t)
	id := seedEpisodeForArtifact(t, s)
	ctx := t.Context()
	if err := s.UpsertEvidenceAudio(ctx, models.SourceEpisode, id, "identity.mp3", "mp3", 100, "current"); err != nil {
		t.Fatal(err)
	}
	identity := models.ListeningQueueItem{SourceType: models.SourceEpisode, SourceID: id, Mode: "original", AudioSHA256: "current"}
	got, err := s.CheckListeningIdentity(ctx, identity)
	if err != nil || !got.Available || got.Unfrozen {
		t.Fatal(got, err)
	}
	identity.AudioSHA256 = "old"
	got, err = s.CheckListeningIdentity(ctx, identity)
	if err != nil || got.Available || !strings.Contains(got.Reason, "原音已变化") {
		t.Fatal(got, err)
	}
	identity.SourceID = "deleted"
	got, err = s.CheckListeningIdentity(ctx, identity)
	if err != nil || got.Available {
		t.Fatal(got, err)
	}
	identity.Mode = "unsupported"
	if _, err = s.CheckListeningIdentity(ctx, identity); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil || len(q.Items) != 0 || q.Revision != 0 {
		t.Fatal(q, err)
	}
}
