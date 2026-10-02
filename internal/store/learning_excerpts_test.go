package store

import (
	"encoding/json"
	"errors"
	"github.com/woyin/orangecast/internal/models"
	"path/filepath"
	"testing"
)

func seedLearningExcerpt(t *testing.T, s *Store) (string, string) {
	t.Helper()
	id := seedSnapshotEpisode(t, s)
	v := seedSnapshotTranscript(t, s, models.SourceEpisode, id, "真实窗口")
	// Set the seed's immutable payload before creating any snapshots.
	if _, err := s.DB.ExecContext(t.Context(), `UPDATE artifact_versions SET payload=? WHERE source_type='episode' AND source_id=? AND kind='transcript' AND version=?`, `{"segments":[{"id":"a","start":20,"end":30,"text":"甲"},{"id":"b","start":30,"end":40,"text":"乙"},{"id":"c","start":40,"end":50,"text":"丙"}]}`, id, v); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, "excerpt.mp3", "mp3", 100, "excerpt-sha"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, snap.ID
}
func TestLearningExcerptsFrozenWindowsQueueAndProgress(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep, snap := seedLearningExcerpt(t, s)
	for _, ids := range [][]string{{"fake"}, {"a", "c"}, {"b", "a"}, {"a", "a"}} {
		if _, err := s.CreateLearningExcerpt(ctx, snap, ids); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatalf("invented/noncontiguous accepted %v %v", ids, err)
		}
	}
	first, err := s.CreateLearningExcerpt(ctx, snap, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateLearningExcerpt(ctx, snap, []string{"a", "b"})
	if err != nil || again.ID != first.ID {
		t.Fatal(again, err)
	}
	second, err := s.CreateLearningExcerpt(ctx, snap, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*models.LearningExcerpt{first, second} {
		q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: e.ID})
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "original"})
	if len(q.Items) != 3 || q.Items[0].StartSeconds != 20 || q.Items[0].EndSeconds != 40 || q.Items[1].ExcerptID != second.ID || !q.Items[0].Available {
		t.Fatal(q)
	}
	// Frozen windows remain valid when a new transcript becomes current.
	seedSnapshotTranscript(t, s, models.SourceEpisode, ep, "新版不同文本")
	item, err := s.CheckListeningIdentity(ctx, models.ListeningQueueItem{SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: first.ID, AudioSHA256: first.AudioSHA256})
	if err != nil || !item.Available || item.SnapshotID != snap || item.SnapshotVersion != 1 {
		t.Fatal(item, err)
	}
	anchor, _ := json.Marshal(models.NoteAnchor{SnapshotID: snap, Version: 1, Position: 25, SegmentIDs: []string{"a", "b"}, Mode: "excerpt", AudioSHA256: first.AudioSHA256})
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "冻结区间中记下的理解", ReferencesJSON: `["a","b"]`, AnchorJSON: string(anchor)})
	if err != nil {
		t.Fatal(err)
	}
	var actual models.NoteAnchor
	if json.Unmarshal([]byte(note.AnchorJSON), &actual) != nil || actual.Position != 25 || actual.SnapshotID != snap || actual.Mode != "excerpt" {
		t.Fatal(note)
	}
	p := &models.ListeningProgress{Mode: "excerpt", SourceType: models.SourceEpisode, SourceID: ep, ExcerptID: first.ID, AudioSHA256: first.AudioSHA256, ItemOffsetSeconds: 25, Speed: 1}
	saved, err := s.SaveListeningProgressCAS(ctx, p, 0)
	if err != nil || saved.ItemOffsetSeconds != 25 || saved.SnapshotID != snap {
		t.Fatal(saved, err)
	}
	p.ItemOffsetSeconds = 26
	if _, err = s.SaveListeningProgressCAS(ctx, p, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("stale CAS", err)
	}
	p.ItemOffsetSeconds = 41
	if _, err = s.SaveListeningProgressCAS(ctx, p, saved.Revision); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("outside interval", err)
	}
	p.ItemOffsetSeconds = 26
	p.Seq = 2
	saved, err = s.SaveListeningProgressCAS(ctx, p, saved.Revision)
	if err != nil || saved.Revision != 2 || saved.ItemOffsetSeconds != 26 {
		t.Fatal("valid CAS update", saved, err)
	}
	p.ItemOffsetSeconds = 27
	p.Seq = 3
	if err = s.SaveListeningProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.ItemOffsetSeconds = 28
	p.Seq = 2
	if err = s.SaveListeningProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	latest, err := s.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, first.ID)
	if err != nil || latest.ItemOffsetSeconds != 27 || latest.Revision != 3 {
		t.Fatal("old seq overwrote excerpt", latest, err)
	}
	if _, err = s.GetListeningProgressMode(ctx, models.SourceEpisode, ep, "original"); !errors.Is(err, ErrNotFound) {
		t.Fatal("excerpt polluted original", err)
	}
	if _, err = s.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("excerpt polluted other interval", err)
	}
	if err = s.UpsertEvidenceAudio(ctx, models.SourceEpisode, ep, "new.mp3", "mp3", 100, "replaced"); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.Items[0].Available {
		t.Fatal("replaced original playable", q, err)
	}
	p.ItemOffsetSeconds = 26
	if _, err = s.SaveListeningProgressCAS(ctx, p, saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestLearningExcerptsUpgrade69PreservesQueueIDsAndSelection(t *testing.T) {
	ctx := t.Context()
	db := openRaw(t, filepath.Join(t.TempDir(), "v69.db"))
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version <= 69 {
			if err = applyOne(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Store{DB: db}
	ep := seedEpisodeForArtifact(t, s)
	if _, err = db.ExecContext(ctx, `INSERT INTO listening_queue_entries(id,source_type,source_id,mode,position)VALUES('old-a','episode',?,'original',17)`, ep); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE listening_queue_state SET current_item_id='old-a',revision=7,autoplay=1`); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > 69 {
			if err = applyOne(ctx, db, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil || q.CurrentItemID != "old-a" || q.Revision != 7 || !q.Autoplay || len(q.Items) != 1 || q.Items[0].ID != "old-a" || q.Items[0].Position != 17 {
		t.Fatal(q, err)
	}
}

func TestLearningExcerptsQueueCASLimitAndPurge(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	ep, snap := seedLearningExcerpt(t, s)
	e, err := s.CreateLearningExcerpt(ctx, snap, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.GetListeningQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	add := ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: e.ID}
	q = queueChange(t, s, q, add)
	id := q.Items[0].ID
	duplicate, err := s.ChangeListeningQueue(ctx, 0, add)
	if err != nil || len(duplicate.Items) != 1 || duplicate.Items[0].ID != id || duplicate.Revision != q.Revision {
		t.Fatal(duplicate, err)
	}
	if _, err = s.ChangeListeningQueue(ctx, 0, ListeningQueueChange{Action: "remove", ItemID: id}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	q = queueChange(t, s, q, ListeningQueueChange{Action: "play", ItemID: id})
	p := &models.ListeningProgress{Mode: "excerpt", SourceType: models.SourceEpisode, SourceID: ep, ExcerptID: e.ID, AudioSHA256: e.AudioSHA256, ItemOffsetSeconds: 25, Speed: 1}
	if _, err = s.SaveListeningProgressCAS(ctx, p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<499)INSERT INTO listening_queue_entries(id,source_type,source_id,mode,position)SELECT 'filler-'||x,'episode','filler-source-'||x,'original',x+1 FROM n`); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateLearningExcerpt(ctx, snap, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeListeningQueue(ctx, q.Revision, ListeningQueueChange{Action: "add", SourceType: models.SourceEpisode, SourceID: ep, Mode: "excerpt", ExcerptID: other.ID}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal("500 cap", err)
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM episodes WHERE id=?`, ep); err != nil {
		t.Fatal(err)
	}
	q, err = s.GetListeningQueue(ctx)
	if err != nil || q.CurrentItemID != "" || q.Items[0].Available {
		t.Fatal(q, err)
	}
	if _, err = s.GetLearningExcerptProgress(ctx, models.SourceEpisode, ep, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged source kept progress", err)
	}
	if _, err = s.CreateLearningExcerpt(ctx, snap, []string{"a"}); err == nil {
		t.Fatal("purged snapshot admitted")
	}
}
