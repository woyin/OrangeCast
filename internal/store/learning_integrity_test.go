package store

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"path/filepath"
	"testing"
	"time"
)

func TestLearningPublicInterfacesSurfaceUnavailableDatabase(t *testing.T) {
	s, p, ep, noteID := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, _, _ := s.BuildKnowledgeArticleRequest(ctx, p, "pod")
	_ = s.DB.Close()
	checks := map[string]func() error{
		"discovery": func() error {
			_, _, _, e := s.BuildKnowledgeDiscoveryRequest(ctx, p, "pod", KnowledgeScope{}, true)
			return e
		}, "batch": func() error { _, e := s.EnsureKnowledgeDiscoveryBatch(ctx, p, "pod", "model", req, 1, true); return e }, "candidate": func() error { _, _, e := s.StartKnowledgeCandidate(ctx, "x", false); return e }, "candidates": func() error { _, e := s.ListKnowledgeTopicCandidates(ctx); return e }, "waiting": func() error { _, e := s.NextAutomaticKnowledgeCandidate(ctx, p); return e }, "recall": func() error {
			_, e := s.RecallKnowledgeMaterials(ctx, p, "pod", req, provider.KnowledgeTopic{Question: "来源"})
			return e
		}, "direction": func() error {
			return s.CheckKnowledgeDirection(ctx, p, "x", provider.KnowledgeTopic{Question: "来源"})
		}, "articles": func() error { _, e := s.ListKnowledgeArticles(ctx); return e }, "reserve": func() error { _, _, e := s.ReserveKnowledgeArticle(ctx, p, "pod", "model", req, false); return e }, "fail run": func() error { return s.FailKnowledgeArticleRun(ctx, "x", "failure") }, "rev list": func() error { _, e := s.ListKnowledgeRevisions(ctx, "x"); return e }, "review list": func() error { _, e := s.ListKnowledgeReviews(ctx, "x"); return e }, "feedback list": func() error { _, e := s.ListKnowledgeFeedback(ctx, "x"); return e }, "execution list": func() error { _, e := s.ListKnowledgeExecutions(ctx, "x"); return e }, "material usages": func() error { _, e := s.KnowledgeMaterialUsages(ctx, "episode", ep, noteID); return e }, "progress": func() error { _, e := s.RecentListening(ctx); return e }, "notes history": func() error { _, e := s.ListOwnerNoteRevisions(ctx, noteID); return e }, "review settings": func() error { _, e := s.GetLearningReviewSettings(ctx); return e }, "review preferences": func() error {
			return s.SetLearningReviewSettings(ctx, LearningReviewSettings{Timezone: "UTC", ClockTime: "18:00"})
		}, "weekly": func() error { _, _, e := s.ReserveLearningReview(ctx, p, "pod", "model", time.Now(), false); return e }, "weekly list": func() error { _, e := s.ListLearningReviewBatches(ctx); return e }, "weekly items": func() error { _, e := s.ListLearningReviewItems(ctx, "x"); return e }, "weekly fail": func() error { return s.FailLearningReview(ctx, "x", "fail") }, "weekly answers": func() error { _, e := s.LearningReviewAnswerHistory(ctx, "x"); return e }, "weekly answer": func() error { return s.AnswerLearningReview(ctx, "x", "answer", "partial", "answer", 0) }, "weekly commit": func() error { return s.CommitLearningReview(ctx, "x", "y", &provider.KnowledgeArticleResult{}) }, "weekly retry": func() error { return s.RetryLearningReview(ctx, "x") }, "weekly note": func() error { _, e := s.SaveLearningReviewNote(ctx, "x", 0); return e }, "rebuild": func() error { return s.RebuildKnowledgeSearch(ctx) }, "sources": func() error { _, e := s.ListKnowledgeSearchSources(ctx); return e }}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if check() == nil {
				t.Fatal("database failure silently accepted")
			}
		})
	}
}
func TestLearningEvidenceStateAndLateFailure(t *testing.T) {
	s, a, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	rev, e := s.GetKnowledgeRevision(ctx, a.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	state, _, e := s.KnowledgeEvidenceState(ctx, a, rev)
	if e != nil || state != "valid" {
		t.Fatal(state, e)
	}
	rev.InputJSON = "bad"
	if _, _, e = s.KnowledgeEvidenceState(ctx, a, rev); e == nil {
		t.Fatal("corrupt input accepted")
	}
	rev, _ = s.GetKnowledgeRevision(ctx, a.ID, 1)
	rev.BlocksJSON = "bad"
	if _, _, e = s.KnowledgeEvidenceState(ctx, a, rev); e == nil {
		t.Fatal("corrupt blocks accepted")
	}
	rev, _ = s.GetKnowledgeRevision(ctx, a.ID, 1)
	note, _ := s.GetOwnerNote(ctx, noteID)
	if _, e = s.UpdateOwnerNote(ctx, noteID, "变化后的理解", note.CitationsJSON, note.ReferencesJSON, note.Revision); e != nil {
		t.Fatal(e)
	}
	state, _, e = s.KnowledgeEvidenceState(ctx, a, rev)
	if e != nil || state != "outdated" {
		t.Fatal(state, e)
	}
	rev.EvidenceStatus = "unavailable"
	state, _, e = s.KnowledgeEvidenceState(ctx, a, rev)
	if e != nil || state != "unavailable" {
		t.Fatal(state, e)
	}
	jobs, _ := s.ListRecentCompleted(ctx, 10)
	if len(jobs) == 0 {
		t.Fatal("no executions")
	}
	for _, j := range jobs {
		if j.JobType != models.JobKnowledgeArticle {
			continue
		}
		if e = s.FailKnowledgeArticleRun(ctx, j.ID, "late error"); e != nil {
			t.Fatal(e)
		}
	}
	after, _ := s.GetKnowledgeArticle(ctx, a.ID)
	if after.Status != "ready" {
		t.Fatal("old failure changed ready article")
	}
	_ = s.DB.Close()
	rev.EvidenceStatus = "valid"
	if _, _, e = s.KnowledgeEvidenceState(ctx, a, rev); e == nil {
		t.Fatal("database failure reported as outdated")
	}
}
func TestLearningStoreAtomicFailureAndRetry(t *testing.T) {
	s, a, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	rev, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	_ = json.Unmarshal([]byte(rev.InputJSON), &req)
	_ = json.Unmarshal([]byte(rev.BlocksJSON), &blocks)
	for _, stmt := range []string{`CREATE TRIGGER learning_fault BEFORE INSERT ON knowledge_article_revisions BEGIN SELECT RAISE(FAIL,'revision unavailable');END`, `CREATE TRIGGER learning_fault BEFORE UPDATE ON knowledge_articles BEGIN SELECT RAISE(FAIL,'selection unavailable');END`} {
		if _, e := s.DB.ExecContext(ctx, stmt); e != nil {
			t.Fatal(e)
		}
		if _, e := s.SaveKnowledgeDraft(ctx, a.ID, 1, "new", blocks, req); e == nil {
			t.Fatal("partial draft accepted")
		}
		versions, e := s.ListKnowledgeRevisions(ctx, a.ID)
		if e != nil || len(versions) != 1 {
			t.Fatal("partial revision survived", e)
		}
		_, _ = s.DB.ExecContext(ctx, `DROP TRIGGER learning_fault`)
	}
	draft, e := s.SaveKnowledgeDraft(ctx, a.ID, 1, "new", blocks, req)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.QueueKnowledgeRevision(ctx, a.ID, draft.Revision, "", "model", false); e != nil {
		t.Fatal(e)
	}
	job, e := s.ClaimNextJob(ctx, "60 seconds")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FailKnowledgeArticleRun(ctx, job.ID, "transient"); e != nil {
		t.Fatal(e)
	}
	_ = s.MarkJobFailed(ctx, job.ID, "transient")
	if e = s.RetryKnowledgeArticle(ctx, a.ID); e != nil {
		t.Fatal(e)
	}
	latest, e := s.ClaimNextJob(ctx, "60 seconds")
	if e != nil || latest == nil {
		t.Fatal(e)
	}
	if e = s.FailKnowledgeArticleRun(ctx, latest.ID, "still transient"); e != nil {
		t.Fatal(e)
	}
	batches, _, e := s.ReserveLearningReview(ctx, a.ProfileID, "pod", "model", time.Now(), false)
	if e != nil || batches == nil {
		t.Fatal(e)
	}
	_ = s.FailLearningReview(ctx, batches.ID, "transient")
	_ = s.MarkJobFailed(ctx, batches.JobID, "transient")
	if e = s.RetryLearningReview(ctx, batches.ID); e != nil {
		t.Fatal(e)
	}
	next, e := s.GetLearningReviewBatch(ctx, batches.ID)
	if e != nil || next.JobID == batches.JobID || next.Status != "queued" {
		t.Fatal(next, e)
	}
	p := &models.ListeningProgress{SourceType: "episode", SourceID: req.Materials[0].SourceID, Mode: "original", ItemOffsetSeconds: 12, Speed: 1}
	if _, e = s.SaveListeningProgressCAS(ctx, p, 0); e != nil {
		t.Fatal(e)
	}
	progress, e := s.RecentListening(ctx)
	if e != nil || len(progress) != 1 || progress[0].Position != 12 {
		t.Fatal(progress, e)
	}
}

func TestPersonalLearningV2BackupRestoresFrozenStateAndContinues(t *testing.T) {
	s, profile, _, _ := knowledgeStoreFixture(t)
	ctx := t.Context()
	req, seq, _, err := s.BuildKnowledgeDiscoveryRequest(ctx, profile, "pod", KnowledgeScope{}, false)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.EnsureKnowledgeDiscoveryBatch(ctx, profile, "pod", "model", req, seq, false)
	if err != nil {
		t.Fatal(err)
	}
	req.DiscoveryBatchID = batch.ID
	article, _, err := s.ReserveKnowledgeArticle(ctx, profile, "pod", "model", req, false)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListQueuedOrRunning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var job *models.ProcessingJob
	for _, j := range jobs {
		if j.JobType == models.JobKnowledgeArticle {
			job = j
		}
	}
	ex, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input KnowledgeStageInput
	_ = json.Unmarshal([]byte(ex.InputSnapshotJSON), &input)
	ids := []string{req.Materials[0].ID, req.Materials[1].ID}
	result := &provider.KnowledgeArticleResult{Topics: []provider.KnowledgeTopic{{Title: "依据的边界", Question: "如何保留依据？", Thesis: "区分来源", Outline: "检查来源", Score: 90, Sufficient: true, MaterialIDs: ids}}}
	if err = s.CommitKnowledgeStage(ctx, job, input, result); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	review, _, err := s.ReserveLearningReview(ctx, profile, "pod", "model", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	q := &provider.KnowledgeArticleResult{Questions: []provider.LearningReviewQuestion{{Question: "如何解释证据边界？", AnswerBasis: "记录来源和个人理解的区别", MaterialIDs: ids}}}
	if err = s.CommitLearningReview(ctx, review.JobID, review.ID, q); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListLearningReviewItems(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AnswerLearningReview(ctx, items[0].ID, "我会核对原文", "partial", "answer", 0); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "restored.db")
	if err = ConsistencyBackup(ctx, s.DB, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	loaded, err := restored.GetKnowledgeArticle(ctx, article.ID)
	if err != nil || loaded.Stage != "select" {
		t.Fatal(loaded, err)
	}
	candidates, err := restored.ListKnowledgeTopicCandidates(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].ArticleID != article.ID {
		t.Fatal(candidates, err)
	}
	saved, err := restored.GetLearningReviewBatch(ctx, review.ID)
	if err != nil || saved.InputJSON != review.InputJSON {
		t.Fatal(saved, err)
	}
	answers, err := restored.LearningReviewAnswerHistory(ctx, items[0].ID)
	if err != nil || len(answers) != 1 || answers[0].Answer != "我会核对原文" {
		t.Fatal(answers, err)
	}
	if _, err = restored.SaveLearningReviewNote(ctx, items[0].ID, 1); err != nil {
		t.Fatal("restored application cannot continue", err)
	}
	if err = restored.RebuildKnowledgeSearch(ctx); err != nil {
		t.Fatal("restored UDF/index unavailable", err)
	}
}
