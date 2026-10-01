package queue

import (
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"testing"
)

func questionQueueFixture(t *testing.T) (*store.Store, *Worker, *store.KnowledgeArticleRecord, *knowledgeQueueFake, *store.LearningQuestion) {
	t.Helper()
	s, w, old, fake, ep := seedKnowledgeQueue(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`UPDATE knowledge_articles SET status='insufficient' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE processing_jobs SET status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "如何把听过的材料表达清楚？", Goal: "写下理解并核对来源"})
	if err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "source", SourceType: "episode", SourceID: ep}})
	if err != nil {
		t.Fatal(err)
	}
	prefs, _ := s.GetKnowledgeArticleSettings(ctx)
	prefs.Enabled = true
	prefs.DailyLimit = 10
	prefs.DebounceMinutes = 0
	prefs.QuestionID = q.ID
	if err = s.SetKnowledgeArticleSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	req, _, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, old.ProfileID, "pod", store.KnowledgeScope{QuestionID: q.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	article, _, err := s.ReserveKnowledgeArticle(ctx, old.ProfileID, "pod", "model", req, true)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, article, fake, q
}
func TestLearningQuestionQueuedPauseBlocksPaidCallAndResumeUsesFrozenInput(t *testing.T) {
	s, w, a, f, q := questionQueueFixture(t)
	ctx := t.Context()
	q, err := s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "status", Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, a.ID)
	if f.calls != 0 || v.Status != "failed" {
		t.Fatal(f.calls, v.Status)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "edit", Body: "修改后的问题", Goal: "新目标"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "status", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RetryKnowledgeArticle(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatal("resume failed", f.calls)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	executions, err := s.ListKnowledgeExecutions(ctx, a.ID)
	if err != nil || len(executions) < 2 {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err = w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	q, err = s.GetLearningQuestion(ctx, q.ID)
	if err != nil || q.Status != "active" {
		t.Fatal("AI decided question state", q, err)
	}
}
func TestLearningQuestionPaidCheckpointSurvivesPauseWithoutRecallOrCharge(t *testing.T) {
	s, w, a, f, q := questionQueueFixture(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_question_successor BEFORE INSERT ON processing_jobs WHEN NEW.intent_id LIKE '%:write' BEGIN SELECT RAISE(FAIL,'stop after paid discovery');END`); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetKnowledgeArticle(ctx, a.ID)
	if v.Status != "failed" || f.calls != 1 {
		t.Fatal(v.Status, f.calls)
	}
	q, err := s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "status", Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_question_successor`); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryKnowledgeArticle(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatal("paused checkpoint invoked provider")
	}
	var receipts, checkpoints int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='knowledge_article_discover'`).Scan(&receipts)
	s.DB.QueryRow(`SELECT count(*) FROM processing_jobs WHERE source_id=? AND checkpoint_json!=''`, a.ID).Scan(&checkpoints)
	if receipts != 1 || checkpoints < 1 {
		t.Fatal(receipts, checkpoints)
	}
	_, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, store.LearningQuestionChange{Action: "status", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	// The explicit Owner retry has already consumed the known discovery result.
	v, _ = s.GetKnowledgeArticle(ctx, a.ID)
	if v.Stage != "write" || f.calls != 1 {
		t.Fatal(v.Stage, f.calls)
	}
}
