package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

func reflectionFixture(t *testing.T) (*Store, ListeningCapture) {
	t.Helper()
	s, v := voiceStoreFixture(t)
	var a models.NoteAnchor
	if err := json.Unmarshal([]byte(v.AnchorJSON), &a); err != nil {
		t.Fatal(err)
	}
	return s, ListeningCapture{SourceType: string(v.SourceType), SourceID: v.SourceID, Title: "原音资料", Anchor: a}
}

func TestListeningReflectionDraftReplayCASAndPrivacy(t *testing.T) {
	s, c := reflectionFixture(t)
	ctx := t.Context()
	id := uuid.NewString()
	r, err := s.StartListeningReflection(ctx, id, c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 1 || r.State != "draft" || r.Capture.SourceID != c.SourceID {
		t.Fatal(r)
	}
	again, err := s.StartListeningReflection(ctx, id, c, "", 0)
	if err != nil || again.ID != id {
		t.Fatal(again, err)
	}
	changed := c
	changed.Title = "改变捕获"
	if _, err = s.StartListeningReflection(ctx, id, changed, "", 0); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	key := uuid.NewString()
	a := ReflectionAnswers{Remember: "  我的理解  ", Uncertain: "条件是否成立"}
	edited, err := s.ChangeListeningReflection(ctx, id, key, "edit", 1, a)
	if err != nil || edited.Answers.Remember != "我的理解" || edited.Revision != 2 {
		t.Fatal(edited, err)
	}
	replay, err := s.ChangeListeningReflection(ctx, id, key, "edit", 1, a)
	if err != nil || replay.Revision != 2 {
		t.Fatal(replay, err)
	}
	if _, err = s.ChangeListeningReflection(ctx, id, key, "edit", 1, ReflectionAnswers{Apply: "不同内容"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ChangeListeningReflection(ctx, id, uuid.NewString(), "edit", 1, a); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	list, err := s.ListListeningReflections(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	cancelKey := uuid.NewString()
	cancelled, err := s.ChangeListeningReflection(ctx, id, cancelKey, "cancel", 2, a)
	if err != nil || cancelled.State != "cancelled" || cancelled.Answers.Remember != "" {
		t.Fatal(cancelled, err)
	}
	replay, err = s.ChangeListeningReflection(ctx, id, key, "edit", 1, a)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(replay, err)
	}
	if _, err = s.ChangeListeningReflection(ctx, id, uuid.NewString(), "edit", 3, a); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	list, err = s.ListListeningReflections(ctx)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}

func TestListeningReflectionDraftBoundsAndQuestion(t *testing.T) {
	s, c := reflectionFixture(t)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "如何理解？"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id      string
		capture ListeningCapture
		q       string
		rev     int
	}{
		{"bad", c, "", 0}, {uuid.NewString(), c, q.ID, 0}, {uuid.NewString(), c, "", 1},
		{uuid.NewString(), ListeningCapture{}, "", 0},
	} {
		if _, err = s.StartListeningReflection(ctx, tc.id, tc.capture, tc.q, tc.rev); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(tc, err)
		}
	}
	if _, err = s.StartListeningReflection(ctx, uuid.NewString(), c, q.ID, q.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.StartListeningReflection(ctx, uuid.NewString(), c, "missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r, err := s.StartListeningReflection(ctx, uuid.NewString(), c, q.ID, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, action string
		rev         int
		a           ReflectionAnswers
	}{
		{"bad", "edit", 1, ReflectionAnswers{}}, {uuid.NewString(), "unknown", 1, ReflectionAnswers{}}, {uuid.NewString(), "edit", 0, ReflectionAnswers{}}, {uuid.NewString(), "edit", 1, ReflectionAnswers{Remember: strings.Repeat("x", 40001)}},
	} {
		if _, err = s.ChangeListeningReflection(ctx, r.ID, tc.key, tc.action, tc.rev, tc.a); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(tc, err)
		}
	}
	for i := 1; i < 20; i++ {
		if _, err = s.StartListeningReflection(ctx, uuid.NewString(), c, "", 0); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err = s.StartListeningReflection(ctx, uuid.NewString(), c, "", 0); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}

func TestListeningReflectionDraftConcurrentStart(t *testing.T) {
	s, c := reflectionFixture(t)
	id := uuid.NewString()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r, err := s.StartListeningReflection(t.Context(), id, c, "", 0)
			if err != nil || r.ID != id {
				t.Errorf("%v %v", r, err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM listening_reflections`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestListeningReflectionDraftExpiryPurgeAndBackup(t *testing.T) {
	s, c := reflectionFixture(t)
	ctx := t.Context()
	r, err := s.StartListeningReflection(ctx, uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	a := ReflectionAnswers{Remember: "私人草稿"}
	if _, err = s.ChangeListeningReflection(ctx, r.ID, key, "edit", 1, a); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if err = ConsistencyBackup(ctx, s.DB, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetListeningReflection(ctx, r.ID)
	if err != nil || got.Answers.Remember != "私人草稿" || got.Revision != 2 {
		t.Fatal(got, err)
	}
	if err = s.ExpireListeningReflections(ctx, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetListeningReflection(ctx, r.ID)
	if err != nil || got.State != "expired" || got.Answers.Remember != "" || got.Capture.SourceID != "" {
		t.Fatal(got, err)
	}
	r, err = s.StartListeningReflection(ctx, uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeListeningReflection(ctx, r.ID, uuid.NewString(), "edit", 1, a); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceEpisode, c.SourceID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetListeningReflection(ctx, r.ID)
	if err != nil || got.State != "unavailable" || got.Answers.Remember != "" {
		t.Fatal(got, err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM listening_reflection_actions`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err = s.GetListeningReflection(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestListeningReflectionDraftUpgrade(t *testing.T) {
	old, path := historicalTestStore(t, 69)
	q, err := old.CreateLearningQuestion(t.Context(), LearningQuestion{Body: "旧资料"})
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetLearningQuestion(t.Context(), q.ID)
	if err != nil || got.Revision != q.Revision {
		t.Fatal(got, err)
	}
	list, err := s.ListListeningReflections(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}

func TestListeningReflectionDraftHomeActionIsReadonly(t *testing.T) {
	s, c := reflectionFixture(t)
	r, err := s.StartListeningReflection(t.Context(), uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	actions, err := s.LearningNextActions(t.Context(), time.Now())
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || before != after || len(actions) != 1 || actions[0].Kind != "reflection" || actions[0].Href != "/listening-reflections/"+r.ID {
		t.Fatal(actions, err, before, after)
	}
}

func TestListeningReflectionSaveAtomicReplayAndConflict(t *testing.T) {
	s, c := reflectionFixture(t)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "有哪些适用条件？"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartListeningReflection(ctx, uuid.NewString(), c, q.ID, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	a := ReflectionAnswers{Remember: "我的新理解", Apply: "明天用一个具体例子验证"}
	if _, err = s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), 1, ReflectionAnswers{}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, "invalid", 1, a); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), 0, a); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), 2, a); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TEMP TRIGGER fail_reflection_save BEFORE UPDATE OF state ON listening_reflections WHEN NEW.state='saved' BEGIN SELECT RAISE(ABORT,'injected failure');END`); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, err = s.SaveListeningReflection(ctx, r.ID, key, 1, a); err == nil {
		t.Fatal("fault unexpectedly committed")
	}
	var notes, links, receipts int
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&notes)
	s.DB.QueryRow(`SELECT count(*) FROM learning_question_links WHERE question_id=?`, q.ID).Scan(&links)
	s.DB.QueryRow(`SELECT count(*) FROM listening_reflection_actions`).Scan(&receipts)
	if notes != 0 || links != 0 || receipts != 0 {
		t.Fatal(notes, links, receipts)
	}
	unchanged, _ := s.GetLearningQuestion(ctx, q.ID)
	draft, _ := s.GetListeningReflection(ctx, r.ID)
	if unchanged.Revision != q.Revision || draft.State != "draft" || draft.Revision != 1 {
		t.Fatal(unchanged, draft)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_reflection_save`); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveListeningReflection(ctx, r.ID, key, 1, a)
	if err != nil || saved.State != "saved" || saved.SavedNoteID == "" {
		t.Fatal(saved, err)
	}
	replay, err := s.SaveListeningReflection(ctx, r.ID, key, 1, a)
	if err != nil || replay.SavedNoteID != saved.SavedNoteID {
		t.Fatal(replay, err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, key, 1, ReflectionAnswers{Remember: "异hash"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), 1, a); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	note, err := s.GetOwnerNote(ctx, saved.SavedNoteID)
	if err != nil || note.Content != a.Content() || note.Kind != "owner_reflection" || note.CitationsJSON != "[]" || note.ReferencesJSON != `["seg-1"]` {
		t.Fatal(note, err)
	}
	updated, _ := s.GetLearningQuestion(ctx, q.ID)
	if updated.Revision != q.Revision+1 {
		t.Fatal(updated)
	}
	s.DB.QueryRow(`SELECT count(*) FROM learning_question_links WHERE question_id=? AND object_id=? AND state='confirmed'`, q.ID, note.ID).Scan(&links)
	if links != 1 {
		t.Fatal(links)
	}
	if err = s.DeleteOwnerNote(ctx, note.ID, note.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, key, 1, a); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted note replay recreated", err)
	}
}

func TestListeningReflectionSaveQuestionAndSourceInvalidation(t *testing.T) {
	for _, kind := range []string{"question_changed", "question_deleted", "source_archived", "source_purged", "expired", "unlinked"} {
		t.Run(kind, func(t *testing.T) {
			s, c := reflectionFixture(t)
			ctx := t.Context()
			q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "问题"})
			if err != nil {
				t.Fatal(err)
			}
			qid, qrev := q.ID, q.Revision
			if kind == "unlinked" {
				qid = ""
				qrev = 0
			}
			r, err := s.StartListeningReflection(ctx, uuid.NewString(), c, qid, qrev)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "question_changed":
				_, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "status", Status: "paused"})
			case "question_deleted":
				_, err = s.DB.Exec(`DELETE FROM learning_questions WHERE id=?`, q.ID)
			case "source_archived":
				_, err = s.DB.Exec(`UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, c.SourceID)
			case "source_purged":
				err = s.DeleteSourceRows(ctx, models.SourceEpisode, c.SourceID)
			case "expired":
				err = s.ExpireListeningReflections(ctx, time.Now().Add(8*24*time.Hour))
			}
			if err != nil {
				t.Fatal(err)
			}
			saved, err := s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), 1, ReflectionAnswers{Uncertain: "还不知道"})
			if kind == "unlinked" {
				if err != nil || saved.State != "saved" {
					t.Fatal(saved, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid scope adopted")
			}
			var n int
			s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&n)
			if n != 0 {
				t.Fatal(n)
			}
			draft, _ := s.GetListeningReflection(ctx, r.ID)
			if kind != "source_purged" && kind != "expired" && draft.State != "draft" {
				t.Fatal(draft)
			}
		})
	}
}
