package store

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"sync"
	"testing"
	"time"
)

func TestLearningReviewWeekScheduleAndDST(t *testing.T) {
	now := time.Date(2026, 3, 8, 18, 0, 0, 0, time.UTC)
	key, start, end, err := LearningReviewWindow(now, "America/New_York")
	if err != nil || key != "2026-03-02" || end.Sub(start) != 167*time.Hour {
		t.Fatal(key, end.Sub(start), err)
	}
	_, start, end, err = LearningReviewWindow(time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC), "America/New_York")
	if err != nil || end.Sub(start) != 169*time.Hour {
		t.Fatal(end.Sub(start), err)
	}
	prefs := LearningReviewSettings{Enabled: true, Timezone: "Asia/Singapore", Weekday: 1, ClockTime: "18:00"}
	monday := time.Date(2026, 9, 28, 9, 59, 0, 0, time.UTC)
	if LearningReviewDue(monday, prefs) || !LearningReviewDue(monday.Add(time.Minute), prefs) || !LearningReviewDue(monday.Add(24*time.Hour), prefs) {
		t.Fatal("local due boundary")
	}
	prefs.Weekday = 0
	if LearningReviewDue(monday, prefs) {
		t.Fatal("Sunday admitted Monday")
	}
	prefs.Enabled = false
	if LearningReviewDue(monday.Add(6*24*time.Hour), prefs) {
		t.Fatal("disabled schedule")
	}
	if _, _, _, err = LearningReviewWindow(now, "bad/zone"); err == nil {
		t.Fatal("invalid zone")
	}
}
func TestLearningReviewFrozenWeekAnswersNotesAndPurge(t *testing.T) {
	s, profile, ep, noteID := knowledgeStoreFixture(t)
	ctx := t.Context()
	now := time.Now()
	prefs, err := s.GetLearningReviewSettings(ctx)
	if err != nil || prefs.Enabled || prefs.Timezone != "UTC" {
		t.Fatal(prefs, err)
	}
	if err = s.SetLearningReviewSettings(ctx, LearningReviewSettings{Timezone: "bad", ClockTime: "18:00"}); err == nil {
		t.Fatal("invalid timezone")
	}
	if err = s.SetLearningReviewSettings(ctx, LearningReviewSettings{Timezone: "UTC", Weekday: 7, ClockTime: "xx"}); err == nil {
		t.Fatal("invalid schedule")
	}
	batch, fresh, err := s.ReserveLearningReview(ctx, profile, "pod", "model", now, false)
	if err != nil || !fresh || batch == nil {
		t.Fatal(err)
	}
	originalInput := batch.InputJSON
	same, fresh, err := s.ReserveLearningReview(ctx, profile, "pod", "different-model", now, false)
	if err != nil || fresh || same.ID != batch.ID || same.InputJSON != originalInput {
		t.Fatal("week identity replay", err)
	}
	note, _ := s.GetOwnerNote(ctx, noteID)
	if _, err = s.UpdateOwnerNote(ctx, noteID, "现在我更关注来源与解释的边界", note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(batch.InputJSON), &req)
	result := &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "怎样解释来源与个人理解的区别？", AnswerBasis: "先核对来源，再说明个人解释的边界。", MaterialIDs: []string{req.Materials[0].ID}}}}
	if err = provider.ValidateKnowledgeResult(req, result); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitLearningReview(ctx, batch.JobID, batch.ID, result); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitLearningReview(ctx, batch.JobID, batch.ID, result); err != nil {
		t.Fatal("replayed saved questions", err)
	}
	items, err := s.ListLearningReviewItems(ctx, batch.ID)
	if err != nil || len(items) != 1 || items[0].Revealed {
		t.Fatal(items, err)
	}
	item := items[0]
	if err = s.AnswerLearningReview(ctx, item.ID, "自己的解释", "partial", "answer", 0); err != nil {
		t.Fatal(err)
	}
	if err = s.AnswerLearningReview(ctx, item.ID, "旧标签页覆盖", "explain", "answer", 0); err == nil {
		t.Fatal("stale answer overwritten")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	noteIDs := map[string]bool{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := s.SaveLearningReviewNote(ctx, item.ID, 1)
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			noteIDs[n.ID] = true
			mu.Unlock()
			if n.Kind != "owner_reflection" || n.Content != "自己的解释" {
				t.Error(n)
			}
		}()
	}
	wg.Wait()
	if len(noteIDs) != 1 {
		t.Fatal("duplicate reflection", noteIDs)
	}
	if err = s.AnswerLearningReview(ctx, item.ID, "", "", "later", 1); err != nil {
		t.Fatal(err)
	}
	history, err := s.LearningReviewAnswerHistory(ctx, item.ID)
	if err != nil || len(history) != 2 || history[1].Answer != "自己的解释" {
		t.Fatal(history, err)
	}
	next, fresh, err := s.ReserveLearningReview(ctx, profile, "pod", "model", now.AddDate(0, 0, 7), false)
	if err != nil || !fresh || next == nil {
		t.Fatal("deferred not carried", err)
	}
	var nextReq provider.KnowledgeArticleRequest
	_ = json.Unmarshal([]byte(next.InputJSON), &nextReq)
	if len(nextReq.Materials) == 0 {
		t.Fatal("no deferred evidence")
	}
	if err = s.AnswerLearningReview(ctx, item.ID, "", "", "reveal", 2); err != nil {
		t.Fatal(err)
	}
	if err = s.AnswerLearningReview(ctx, item.ID, "", "", "answer", 3); err == nil {
		t.Fatal("empty answer")
	}
	if err = s.AnswerLearningReview(ctx, item.ID, "x", "partial", "wrong", 3); err == nil {
		t.Fatal("unknown action")
	}
	batches, err := s.ListLearningReviewBatches(ctx)
	if err != nil || len(batches) != 2 {
		t.Fatal(err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	batches, err = s.ListLearningReviewBatches(ctx)
	if err != nil || len(batches) != 0 {
		t.Fatal("review retained private evidence", err)
	}
}
func TestLearningReviewEmptyAndOptOut(t *testing.T) {
	s := newTestStore(t)
	p, e := s.EnsureDefaultEditorialProfile(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	v, fresh, e := s.ReserveLearningReview(t.Context(), p.ID, "pod", "model", time.Now(), false)
	if e != nil || fresh || v != nil {
		t.Fatal("empty generated", e)
	}
	v, fresh, e = s.ReserveLearningReview(t.Context(), p.ID, "pod", "model", time.Now(), true)
	if e != nil || fresh || v != nil {
		t.Fatal("disabled admitted", e)
	}
}
