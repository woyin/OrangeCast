package store

import (
	"errors"
	"math"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestPlaybackModesAndRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	podcast, err := s.CreatePodcast(ctx, "https://example.com/progress.xml", "播放", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "playback", Title: "单集"}})
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	id := eps[0].ID
	p := &models.ListeningProgress{SourceType: models.SourceEpisode, SourceID: id, Mode: "original", ItemOffsetSeconds: 2300, Speed: 1.75}
	first, err := s.SaveListeningProgressCAS(ctx, p, 0)
	if err != nil || first.Revision != 1 {
		t.Fatalf("create: %+v %v", first, err)
	}
	p.ItemOffsetSeconds = 2301
	if _, err := s.SaveListeningProgressCAS(ctx, p, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale create: %v", err)
	}
	next, err := s.SaveListeningProgressCAS(ctx, p, 1)
	if err != nil || next.Revision != 2 {
		t.Fatalf("update: %+v %v", next, err)
	}
	if _, err := s.SaveListeningProgressCAS(ctx, p, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	if err := s.SaveListeningProgress(ctx, &models.ListeningProgress{SourceType: models.SourceEpisode, SourceID: id, Mode: "dj", PlanID: "legacy-plan", Speed: 1, Seq: 42}); err != nil {
		t.Fatal(err)
	}
	original, _ := s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "original")
	dj, _ := s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "dj")
	if original.ItemOffsetSeconds != 2301 || dj.PlanID != "legacy-plan" {
		t.Fatalf("modes mixed: %+v %+v", original, dj)
	}
	p.ItemOffsetSeconds = math.NaN()
	if _, err := s.SaveListeningProgressCAS(ctx, p, 2); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	p.ItemOffsetSeconds = 2
	p.SourceID = "missing"
	if _, err := s.SaveListeningProgressCAS(ctx, p, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	p.SourceID = id
	p.Mode = "dj"
	p.PlanID = "legacy-plan"
	if _, err := s.SaveListeningProgressCAS(ctx, p, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteListeningProgress(ctx, models.SourceEpisode, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetListeningProgressMode(ctx, models.SourceEpisode, id, "dj"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPlaybackUpgradeKeepsOnlyLegacyMode(t *testing.T) {
	s := newTestStore(t)
	// Exercise the exact migration SQL against a pre-upgrade schema and real row.
	_, err := s.DB.Exec(`DROP TABLE listening_progress; CREATE TABLE listening_progress(id TEXT,source_type TEXT,source_id TEXT,plan_id TEXT,plan_version INTEGER,item_position INTEGER,highlight_id TEXT,item_offset_seconds REAL,speed REAL,seq INTEGER,updated_at TEXT); INSERT INTO listening_progress VALUES('old','episode','ep','plan',2,3,'h',30,1.5,99,'2026-09-30')`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := migrationsFS.ReadFile("migrations/0057_playback_modes.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(string(sql)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`ALTER TABLE listening_progress ADD COLUMN audio_sha256 TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetListeningProgressMode(t.Context(), models.SourceEpisode, "ep", "dj")
	if err != nil || old.ItemPosition != 3 || old.Revision != 1 {
		t.Fatalf("backfill: %+v %v", old, err)
	}
	if _, err := s.GetListeningProgressMode(t.Context(), models.SourceEpisode, "ep", "original"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invented progress: %v", err)
	}
}
