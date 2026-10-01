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
	if err != nil || replay.State != "cancelled" || replay.Answers.Remember != "" {
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
