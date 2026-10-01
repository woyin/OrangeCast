package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLearningHomeUpgradeAndBackup(t *testing.T) {
	old, path := historicalTestStore(t, 69)
	q, err := old.CreateLearningQuestion(t.Context(), LearningQuestion{Body: "旧问题"})
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	up, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	unchanged, err := up.GetLearningQuestion(t.Context(), q.ID)
	if err != nil || unchanged.Revision != q.Revision {
		t.Fatal(unchanged, err)
	}
	p, err := up.GetLearningPreferences(t.Context())
	if err != nil || p.Revision != 1 || p.ReflectionPrompt {
		t.Fatal(p, err)
	}
	p.CurrentQuestionID = q.ID
	if err = up.SaveLearningPreferences(t.Context(), p, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "backup.db")
	if err = ConsistencyBackup(t.Context(), up.DB, copyPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetLearningPreferences(t.Context())
	if err != nil || got.CurrentQuestionID != q.ID || got.Revision != 2 {
		t.Fatal(got, err)
	}
}

func TestLearningHomePreferencesReplayCASAndQuestionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, err := s.GetLearningPreferences(ctx)
	if err != nil || p.Revision != 1 || p.ReflectionPrompt || p.CurrentQuestionID != "" {
		t.Fatal(p, err)
	}
	a, err := s.LearningNextActions(ctx, time.Now())
	if err != nil || len(a) != 2 || a[0].Kind != "start" {
		t.Fatal(a, err)
	}
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "如何检查自己的理解？"})
	if err != nil {
		t.Fatal(err)
	}
	p.CurrentQuestionID = q.ID
	key := uuid.NewString()
	if err = s.SaveLearningPreferences(ctx, p, key); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLearningPreferences(ctx, p, key); err != nil {
		t.Fatal("replay", err)
	}
	if err = s.SaveLearningPreferences(ctx, p, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision", err)
	}
	changed := p
	changed.ReflectionPrompt = true
	if err = s.SaveLearningPreferences(ctx, changed, key); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay", err)
	}
	a, err = s.LearningNextActions(ctx, time.Now())
	if err != nil || a[0].Kind != "question" {
		t.Fatal(a, err)
	}
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "status", Status: "paused"}); err != nil {
		t.Fatal(err)
	}
	a, err = s.LearningNextActions(ctx, time.Now())
	if err != nil || a[0].Kind != "start" {
		t.Fatal(a, err)
	}
	current, _ := s.GetLearningPreferences(ctx)
	if err = s.SaveLearningPreferences(ctx, current, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("paused selection", err)
	}
	if _, err = s.DB.Exec(`DELETE FROM learning_questions WHERE id=?`, q.ID); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.GetLearningPreferences(ctx)
	if err != nil || cleared.CurrentQuestionID != "" || cleared.Revision != current.Revision+1 {
		t.Fatal(cleared, err)
	}
	for _, key := range []string{"", "invalid"} {
		if err = s.SaveLearningPreferences(ctx, cleared, key); !errors.Is(err, ErrInvalidEditorialState) {
			t.Fatal(err)
		}
	}
	cleared.CurrentQuestionID = "missing"
	if err = s.SaveLearningPreferences(ctx, cleared, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLearningHomeActiveReviewTakesPriorityAndReadHasNoWrites(t *testing.T) {
	s, _, _, now := dailyReviewFixture(t, 1)
	ctx := t.Context()
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "当前问题"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetLearningPreferences(ctx)
	p.CurrentQuestionID = q.ID
	if err = s.SaveLearningPreferences(ctx, p, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	a, err := s.LearningNextActions(ctx, now)
	if err != nil || a[0].Kind != "review" || a[1].Kind != "question" {
		t.Fatal(a, err)
	}
	if _, err = s.StartReviewSession(ctx, uuid.NewString(), 1, now); err != nil {
		t.Fatal(err)
	}
	var before, after int
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	a, err = s.LearningNextActions(ctx, now)
	s.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if err != nil || a[0].Title != "继续短回顾" || before != after || len(a) > 3 {
		t.Fatal(a, err, before, after)
	}
}
