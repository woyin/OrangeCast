package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func dailyReviewFixture(t *testing.T, n int) (*Store, []LearningReviewItem, string, time.Time) {
	t.Helper()
	s, profile, _, note := knowledgeStoreFixture(t)
	batch, fresh, err := s.ReserveLearningReview(t.Context(), profile, "pod", "fixture", time.Now(), false)
	if err != nil || !fresh || batch == nil {
		t.Fatal(batch, err)
	}
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(batch.InputJSON), &req)
	found := false
	for _, m := range req.Materials {
		if m.ID == note {
			found = true
		}
	}
	if !found {
		t.Fatal("fixture note not selected")
	}
	result := &provider.KnowledgeArticleResult{}
	for i := 0; i < n; i++ {
		result.Questions = append(result.Questions, provider.LearningReviewQuestion{Question: fmt.Sprintf("怎样解释来源与个人理解？第%d题", i+1), AnswerBasis: "冻结依据标记：来源表达与个人解释不同。", MaterialIDs: []string{note}})
	}
	if err = s.CommitLearningReview(t.Context(), batch.JobID, batch.ID, result); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListLearningReviewItems(t.Context(), batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.DB.Exec(`UPDATE review_schedules SET due_utc=?`, reviewTime(now.AddDate(0, 0, -1)))
	return s, items, note, now
}

func TestReviewCalendarRulesAcrossLeapDayDSTAndZones(t *testing.T) {
	for _, tt := range []struct {
		assessment               string
		streak, days, nextStreak int
	}{{"revisit", 3, 1, 0}, {"partial", 3, 3, 0}, {"explain", 0, 7, 1}, {"explain", 1, 14, 2}} {
		now := time.Date(2024, 2, 28, 12, 0, 0, 0, time.UTC)
		due, streak, err := NextReviewDue(now, "Asia/Singapore", tt.assessment, tt.streak)
		if err != nil || !due.Equal(now.AddDate(0, 0, tt.days)) || streak != tt.nextStreak {
			t.Fatal(tt, due, streak, err)
		}
	}
	for _, tt := range []struct {
		now   time.Time
		hours int
	}{{time.Date(2026, 3, 7, 17, 0, 0, 0, time.UTC), 23}, {time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), 25}} {
		due, _, err := NextReviewDue(tt.now, "America/New_York", "revisit", 0)
		if err != nil || due.Sub(tt.now) != time.Duration(tt.hours)*time.Hour {
			t.Fatal("calendar DST", due.Sub(tt.now), err)
		}
	}
	if _, _, err := NextReviewDue(time.Now(), "bad/zone", "revisit", 0); err == nil {
		t.Fatal("invalid zone")
	}
	if _, _, err := NextReviewDue(time.Now(), "UTC", "unknown", 0); err == nil {
		t.Fatal("invalid assessment")
	}
}

func TestDailyReviewFrozenSessionRevealAnswerAndRetryIdentity(t *testing.T) {
	s, items, _, now := dailyReviewFixture(t, 5)
	ctx := t.Context()
	if size, err := s.GetReviewSessionSize(ctx); err != nil || size != 3 {
		t.Fatal(size, err)
	}
	for _, size := range []int{0, 6} {
		if err := s.SetReviewSessionSize(ctx, size); err == nil {
			t.Fatal("invalid size")
		}
	}
	if err := s.SetReviewSessionSize(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueReviewCount(ctx, now); err != nil || due != 5 {
		t.Fatal(due, err)
	}
	key := uuid.NewString()
	session, err := s.StartReviewSession(ctx, key, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.StartReviewSession(ctx, key, 3, now)
	if err != nil || same.ID != session.ID {
		t.Fatal("duplicate start", same, err)
	}
	same, err = s.StartReviewSession(ctx, uuid.NewString(), 1, now)
	if err != nil || same.ID != session.ID {
		t.Fatal("active session replaced", err)
	}
	selected, err := s.ListReviewSessionItems(ctx, session.ID)
	if err != nil || len(selected) != 3 {
		t.Fatal(selected, err)
	}
	item := selected[0]
	if item.Revealed || item.Answer != "" || item.State != "pending" {
		t.Fatal("previous answer revealed", item)
	}
	reveal := uuid.NewString()
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "reveal", "还在写的解释", "partial", reveal, 1, now); err != nil {
		t.Fatal(err)
	}
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "reveal", "还在写的解释", "partial", reveal, 1, now); err != nil {
		t.Fatal("reveal retry", err)
	}
	history, _ := s.LearningReviewAnswerHistory(ctx, item.ItemID)
	if len(history) != 0 {
		t.Fatal("view counted as answer", history)
	}
	selected, _ = s.ListReviewSessionItems(ctx, session.ID)
	if !selected[0].Revealed || selected[0].Answer != "还在写的解释" || selected[0].State != "pending" {
		t.Fatal("reveal lost draft", selected[0])
	}
	answerKey := uuid.NewString()
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "用自己的例子解释", "partial", answerKey, 2, now); err != nil {
		t.Fatal(err)
	}
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "用自己的例子解释", "partial", answerKey, 2, now); err != nil {
		t.Fatal("same answer repeated", err)
	}
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "different", "explain", answerKey, 2, now); !errors.Is(err, ErrConflict) {
		t.Fatal("request identity reused", err)
	}
	if err = s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "old tab", "explain", uuid.NewString(), 2, now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale answer", err)
	}
	batches, answers, e := s.ReviewActivityCounts(ctx)
	if e != nil || batches != 1 || answers != 1 {
		t.Fatal("separate counters", batches, answers, e)
	}
	history, _ = s.LearningReviewAnswerHistory(ctx, item.ItemID)
	if len(history) != 1 || history[0].Answer != "用自己的例子解释" {
		t.Fatal(history)
	}
	schedules, _ := s.ListReviewSchedules(ctx, "", 0)
	for _, v := range schedules {
		if v.ItemID == item.ItemID && v.DueUTC != reviewTime(now.AddDate(0, 0, 3)) {
			t.Fatal(v)
		}
	}
	for _, v := range selected[1:] {
		if err = s.AnswerReviewSession(ctx, session.ID, v.ItemID, "later", "未完成的草稿", "", uuid.NewString(), 1, now); err != nil {
			t.Fatal(err)
		}
	}
	finished, _ := s.GetReviewSession(ctx, session.ID)
	if finished.Status != "complete" {
		t.Fatal(finished)
	}
	same, err = s.StartReviewSession(ctx, key, 3, now)
	if err != nil || same.ID != session.ID {
		t.Fatal("retry revived complete session", err)
	}
	next, err := s.StartReviewSession(ctx, uuid.NewString(), 5, now)
	if err != nil || next.ID == session.ID {
		t.Fatal(next, err)
	}
	remaining, _ := s.ListReviewSessionItems(ctx, next.ID)
	if len(remaining) != 2 {
		t.Fatal("already answered/later consumed due slots", remaining)
	}
	if len(items) != 5 {
		t.Fatal(items)
	}
}

func TestDailyReviewAtomicRollbackAndTwoWindows(t *testing.T) {
	s, _, _, now := dailyReviewFixture(t, 1)
	ctx := t.Context()
	session, _ := s.StartReviewSession(ctx, uuid.NewString(), 1, now)
	items, _ := s.ListReviewSessionItems(ctx, session.ID)
	item := items[0]
	key := uuid.NewString()
	s.DB.Exec(`CREATE TRIGGER fail_schedule BEFORE UPDATE OF due_utc ON review_schedules BEGIN SELECT RAISE(ABORT,'disk full'); END`)
	if err := s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "不可半提交", "explain", key, 1, now); err == nil {
		t.Fatal("partial commit")
	}
	history, _ := s.LearningReviewAnswerHistory(ctx, item.ItemID)
	if len(history) != 0 {
		t.Fatal("answer escaped rollback")
	}
	s.DB.Exec(`DROP TRIGGER fail_schedule`)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.AnswerReviewSession(ctx, session.ID, item.ItemID, "answer", "相同解释", "explain", key, 1, now)
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("idempotent race", err)
		}
	}
	history, _ = s.LearningReviewAnswerHistory(ctx, item.ItemID)
	if len(history) != 1 {
		t.Fatal(history)
	}
	n1, err := s.SaveLearningReviewAnswerNote(ctx, item.ItemID, history[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := s.SaveLearningReviewAnswerNote(ctx, item.ItemID, history[0].Revision)
	if err != nil || n1.ID != n2.ID {
		t.Fatal("duplicate note", n1, n2, err)
	}
	if err = s.AnswerLearningReviewAt(ctx, item.ItemID, "之后的新解释", "revisit", "answer", history[0].Revision, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	old, err := s.SaveLearningReviewAnswerNote(ctx, item.ItemID, history[0].Revision)
	if err != nil || old.ID != n1.ID || old.Content != "相同解释" {
		t.Fatal("old answer replaced", old, err)
	}
	current, err := s.SaveLearningReviewNote(ctx, item.ItemID, history[0].Revision+1)
	if err != nil || current.ID == n1.ID || current.Content != "之后的新解释" {
		t.Fatal(current, err)
	}
}

func TestReviewSchedulesCASQuestionsManualDueAndTimezoneChanges(t *testing.T) {
	s, items, _, now := dailyReviewFixture(t, 2)
	ctx := t.Context()
	item := items[0].ID
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "关联问题", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 1, ReviewScheduleChange{Action: "question", QuestionID: q.ID, QuestionRevision: q.Revision + 1}, now); !errors.Is(err, ErrConflict) {
		t.Fatal("question CAS", err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 1, ReviewScheduleChange{Action: "question", QuestionID: q.ID, QuestionRevision: q.Revision}, now); err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "status", Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueReviewCount(ctx, now); err != nil || due != 1 {
		t.Fatal("independent item paused", due, err)
	}
	schedules, _ := s.ListReviewSchedules(ctx, "active", 0)
	for _, v := range schedules {
		if v.ItemID == item && v.Available {
			t.Fatal("paused parent still eligible")
		}
	}
	q, _ = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "status", Status: "active"})
	key := uuid.NewString()
	if err = s.ChangeReviewSchedule(ctx, item, key, 2, ReviewScheduleChange{Action: "pause"}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, key, 2, ReviewScheduleChange{Action: "pause"}, now); err != nil {
		t.Fatal("duplicate pause", err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 2, ReviewScheduleChange{Action: "resume"}, now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale schedule", err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 3, ReviewScheduleChange{Action: "resume"}, now); err != nil {
		t.Fatal(err)
	}
	prefs, _ := s.GetLearningReviewSettings(ctx)
	prefs.Timezone = "America/New_York"
	s.SetLearningReviewSettings(ctx, prefs)
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 4, ReviewScheduleChange{Action: "due", LocalDue: "2026-03-08T02:30"}, now); err == nil {
		t.Fatal("nonexistent local clock accepted")
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 4, ReviewScheduleChange{Action: "due", LocalDue: "2026-10-02T08:00"}, now); err != nil {
		t.Fatal(err)
	}
	var due string
	s.DB.QueryRow(`SELECT due_utc FROM review_schedules WHERE item_id=?`, item).Scan(&due)
	if due != "2026-10-02T12:00:00Z" {
		t.Fatal(due)
	}
	prefs.Timezone = "Asia/Singapore"
	s.SetLearningReviewSettings(ctx, prefs)
	var after string
	s.DB.QueryRow(`SELECT due_utc FROM review_schedules WHERE item_id=?`, item).Scan(&after)
	if after != due {
		t.Fatal("zone change moved existing due")
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 5, ReviewScheduleChange{Action: "later"}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 6, ReviewScheduleChange{Action: "end"}, now); err != nil {
		t.Fatal(err)
	}
	ended, _ := s.ListReviewSchedules(ctx, "ended", 0)
	if len(ended) != 1 {
		t.Fatal(ended)
	}
	if err = s.ChangeReviewSchedule(ctx, item, uuid.NewString(), 7, ReviewScheduleChange{Action: "question"}, now); err != nil {
		t.Fatal("unlink question", err)
	}
}

func TestReviewHistoricalBasisWithdrawalAndInterruptedSession(t *testing.T) {
	s, items, note, now := dailyReviewFixture(t, 2)
	ctx := t.Context()
	session, _ := s.StartReviewSession(ctx, uuid.NewString(), 1, now)
	selected, _ := s.ListReviewSessionItems(ctx, session.ID)
	frozen := selected[0].InputJSON
	original, _ := s.GetOwnerNote(ctx, note)
	s.UpdateOwnerNote(ctx, note, "现在的理解不同", original.CitationsJSON, original.ReferencesJSON, original.Revision)
	selected, err := s.ListReviewSessionItems(ctx, session.ID)
	if err != nil || !selected[0].Available || selected[0].Warning == "" || selected[0].InputJSON != frozen {
		t.Fatal("historical basis changed", selected, err)
	}
	active, _ := s.ActiveReviewSession(ctx)
	endKey := uuid.NewString()
	if err = s.EndReviewSession(ctx, active.ID, endKey, active.Revision, now); err != nil {
		t.Fatal(err)
	}
	if err = s.EndReviewSession(ctx, active.ID, endKey, active.Revision, now); err != nil {
		t.Fatal("end replay", err)
	}
	next, err := s.StartReviewSession(ctx, uuid.NewString(), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	nextItems, _ := s.ListReviewSessionItems(ctx, next.ID)
	if nextItems[0].ItemID == selected[0].ItemID {
		t.Fatal("abandoned hard item dominated")
	}
	changed, _ := s.GetOwnerNote(ctx, note)
	if err = s.DeleteOwnerNote(ctx, note, changed.Revision); err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueReviewCount(ctx, now); err != nil || due != 0 {
		t.Fatal("withdrawn basis in new sessions", due, err)
	}
	if err = s.AnswerReviewSession(ctx, next.ID, nextItems[0].ItemID, "answer", "旧依据已删除", "explain", uuid.NewString(), 1, now); !errors.Is(err, ErrConflict) {
		t.Fatal("withdrawn evidence answered", err)
	}
	if len(items) != 2 {
		t.Fatal(items)
	}
}

func TestDailyReviewRestartAndHistoricalAnswerNote(t *testing.T) {
	s, items, _, now := dailyReviewFixture(t, 1)
	ctx := t.Context()
	item := items[0].ID
	if err := s.AnswerLearningReviewAt(ctx, item, "第一版个人解释", "explain", "answer", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerLearningReviewAt(ctx, item, "第二版个人解释", "explain", "answer", 1, now.AddDate(0, 0, 7)); err != nil {
		t.Fatal(err)
	}
	var due string
	var streak int
	s.DB.QueryRow(`SELECT due_utc,streak FROM review_schedules WHERE item_id=?`, item).Scan(&due, &streak)
	if due != reviewTime(now.AddDate(0, 0, 21)) || streak != 2 {
		t.Fatal("continuous explain", due, streak)
	}
	old, err := s.SaveLearningReviewAnswerNote(ctx, item, 1)
	if err != nil || old.Content != "第一版个人解释" {
		t.Fatal(old, err)
	}
	if _, err = s.SaveLearningReviewNote(ctx, item, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("weekly stale note admission", err)
	}
	session, err := s.StartReviewSession(ctx, uuid.NewString(), 1, now.AddDate(0, 0, 22))
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err = s.DB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	active, err := reopened.ActiveReviewSession(ctx)
	if err != nil || active.ID != session.ID {
		t.Fatal("restart lost session", active, err)
	}
	history, err := reopened.ListReviewSessions(ctx, 0)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	if _, err = reopened.ListReviewSessions(ctx, -1); err == nil {
		t.Fatal("negative history offset")
	}
	frozen, err := reopened.ListReviewSessionItems(ctx, active.ID)
	if err != nil || len(frozen) != 1 || frozen[0].Answer != "" {
		t.Fatal("restart exposed old answer", frozen, err)
	}
}

func TestReviewEvidenceRejectsWithdrawalsAndMarksHistoricalVersions(t *testing.T) {
	for _, kind := range []string{"corrupt-input", "missing-material", "source-identity", "archived-source", "deleted-source", "deleted-note", "note-version", "keypoint-withdrawn", "keypoint-version", "snapshot-purged", "snapshot-identity", "transcript-version"} {
		t.Run(kind, func(t *testing.T) {
			s, items, note, _ := dailyReviewFixture(t, 1)
			ctx := t.Context()
			batch, _ := s.GetLearningReviewBatch(ctx, items[0].BatchID)
			var req provider.KnowledgeArticleRequest
			json.Unmarshal([]byte(batch.InputJSON), &req)
			var selected provider.KnowledgeMaterial
			for _, m := range req.Materials {
				if m.ID == note {
					selected = m
				}
				if strings.HasPrefix(kind, "keypoint") && m.Kind == "keypoint" {
					selected = m
					break
				}
			}
			input, ids := batch.InputJSON, jsonString([]string{selected.ID})
			expected := false
			switch kind {
			case "corrupt-input":
				input = "not-json"
			case "missing-material":
				ids = `["missing"]`
			case "source-identity":
				selected.SourceType = "invalid"
				req.Materials = []provider.KnowledgeMaterial{selected}
				input = jsonString(req)
			case "archived-source":
				s.ArchiveSource(ctx, models.SourceType(selected.SourceType), selected.SourceID, true)
			case "deleted-source":
				s.DB.Exec(`DELETE FROM episodes WHERE id=?`, selected.SourceID)
			case "deleted-note":
				n, _ := s.GetOwnerNote(ctx, note)
				s.DeleteOwnerNote(ctx, note, n.Revision)
			case "note-version":
				n, _ := s.GetOwnerNote(ctx, note)
				s.UpdateOwnerNote(ctx, note, "修改后版本", n.CitationsJSON, n.ReferencesJSON, n.Revision)
				expected = true
			case "keypoint-withdrawn":
				s.DB.Exec(`UPDATE keypoint_index SET evidence_status='stale' WHERE id=?`, selected.ID)
			case "keypoint-version":
				s.DB.Exec(`UPDATE keypoint_index SET card_version=card_version+1 WHERE id=?`, selected.ID)
				expected = true
			case "snapshot-purged":
				s.DB.Exec(`UPDATE source_snapshots SET status='purged' WHERE id=?`, selected.SnapshotID)
			case "snapshot-identity":
				selected.SnapshotID = "missing"
				req.Materials = []provider.KnowledgeMaterial{selected}
				input = jsonString(req)
			case "transcript-version":
				s.DB.Exec(`UPDATE episodes SET current_transcript_version=current_transcript_version+1 WHERE id=?`, selected.SourceID)
				expected = true
			}
			warning, available, err := reviewEvidence(ctx, s.DB, input, ids)
			if kind == "corrupt-input" {
				if err == nil {
					t.Fatal("corrupt input accepted")
				}
				return
			}
			if err != nil || available != expected || warning == "" {
				t.Fatal(kind, warning, available, err)
			}
		})
	}
}

func TestDailyReviewUpgradePreservesOldQuestionAndAnswer(t *testing.T) {
	s, path := historicalTestStore(t, 67)
	ctx := t.Context()
	doc, err := s.CreatePastedDocument(ctx, "历史资料", "历史正文")
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "迁移前的解释"})
	if err != nil {
		t.Fatal(err)
	}
	id := "historical-review-item"
	for _, statement := range []string{`INSERT INTO editorial_profiles(id,name)VALUES('historical-profile','旧资料')`, `INSERT INTO learning_review_batches(id,profile_id,week_key,timezone,start_utc,end_utc,input_json,provider,model,prompt_version)VALUES('historical-batch','historical-profile','2026-W40','UTC','2026-09-28','2026-10-05','{}','pod','old','old')`, `INSERT INTO learning_review_items(id,batch_id,position,question,answer_basis,material_ids_json,state,answer,assessment,revision)VALUES('historical-review-item','historical-batch',1,'旧问题','旧依据','[]','answered','迁移前的解释','partial',1)`, `INSERT INTO learning_review_answers(id,item_id,revision,answer,assessment,state)VALUES('historical-answer','historical-review-item',1,'迁移前的解释','partial','answered')`} {
		if _, err = s.DB.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	if _, err = s.DB.Exec(`UPDATE learning_review_items SET note_id=? WHERE id=?`, note.ID, id); err != nil {
		t.Fatal(err)
	}
	s.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	history, err := upgraded.LearningReviewAnswerHistory(ctx, id)
	if err != nil || len(history) != 1 || history[0].Answer != "迁移前的解释" || history[0].NoteID != note.ID {
		t.Fatal("old answer changed", history, err)
	}
	schedules, err := upgraded.ListReviewSchedules(ctx, "", 0)
	if err != nil || len(schedules) != 1 || schedules[0].RuleVersion != ReviewRuleVersion {
		t.Fatal(schedules, err)
	}
}
