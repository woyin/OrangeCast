package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func updateModels() map[string]string {
	return map[string]string{"discover": "discovery", "select": "selection", "write": "writer", "revise": "writer", "review": "reviewer", "review_final": "reviewer", "weekly_review": "learning"}
}
func updateProposalFixture(t *testing.T) (*Store, *KnowledgeArticleRecord, *KnowledgeUpdateProposal, string) {
	t.Helper()
	s, a, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	n, err := s.GetOwnerNote(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateOwnerNote(ctx, noteID, n.Content+" 我还想区分适用边界与反例。", n.CitationsJSON, n.ReferencesJSON, n.Revision); err != nil {
		t.Fatal(err)
	}
	p, created, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, a.WorkingRevision, updateModels())
	if err != nil || !created || p.State != "pending" {
		t.Fatal(p, created, err)
	}
	return s, a, p, noteID
}
func updateAnalysisInput(t *testing.T, s *Store, p *KnowledgeUpdateProposal) (*models.ProcessingJob, KnowledgeStageInput) {
	t.Helper()
	if err := s.QueueKnowledgeUpdateAnalysis(t.Context(), p.ID, false); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetKnowledgeUpdateProposal(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextJob(t.Context(), "60 seconds")
	if err != nil || claimed == nil || claimed.ID != p.JobID {
		t.Fatalf("claim analysis: %+v %v", claimed, err)
	}
	jobs, err := s.ListQueuedOrRunning(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID == p.JobID {
			exec, err := s.GetJobExecution(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			var input KnowledgeStageInput
			if err = json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
				t.Fatal(err)
			}
			return job, input
		}
	}
	t.Fatal("missing analysis job")
	return nil, KnowledgeStageInput{}
}
func supportedUpdateDecision(req provider.KnowledgeArticleRequest) *provider.KnowledgeArticleResult {
	return &provider.KnowledgeArticleResult{Update: &provider.KnowledgeUpdateAnalysis{Decision: "update", Reason: "个人笔记新增适用边界，补充限定条件。", Changes: []provider.KnowledgeUpdateChange{{Action: "add", BlockID: req.Blocks[1].ID, Reason: "增加个人理解的边界", MaterialIDs: []string{req.Materials[0].ID}}}}}
}
func TestKnowledgeUpdateFreezesParentAndDoesNotDuplicateAdmission(t *testing.T) {
	s, a, p, note := updateProposalFixture(t)
	ctx := t.Context()
	var req provider.KnowledgeArticleRequest
	if err := json.Unmarshal([]byte(p.InputJSON), &req); err != nil {
		t.Fatal(err)
	}
	if req.Update.ParentRevision != 1 || p.PassedRevision != 1 || req.Update.ParentHash == "" || len(req.Update.Changes) != 1 || req.Update.Changes[0].MaterialID != note {
		t.Fatal(req.Update, p)
	}
	again, created, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil || created || again.ID != p.ID {
		t.Fatal(again, created, err)
	}
	settings, err := s.GetKnowledgeUpdateSettings(ctx)
	if err != nil || settings.Enabled || settings.DailyLimit != 2 {
		t.Fatal(settings, err)
	}
	job, input := updateAnalysisInput(t, s, p)
	exec, _ := s.GetJobExecution(ctx, job.ID)
	if exec.ConfiguredModel != "discovery" || input.Request.Estimate == nil || input.Request.Estimate.OutputTokens != 4096 {
		t.Fatal(exec, input.Request.Estimate)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatal("same analysis enqueued twice", len(jobs))
	}
	if err = s.CommitKnowledgeUpdateAnalysis(ctx, job, input, supportedUpdateDecision(input.Request)); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitKnowledgeUpdateAnalysis(ctx, job, input, supportedUpdateDecision(input.Request)); err != nil {
		t.Fatal("known commit not idempotent", err)
	}
	if err = s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
		t.Fatal("accept repeated", err)
	}
	after, _ := s.GetKnowledgeArticle(ctx, a.ID)
	if after.ID != a.ID || after.WorkingRevision != 1 || after.PassedRevision != 1 || after.Stage != "revise" {
		t.Fatal(after)
	}
	jobs, _ = s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	exec, _ = s.GetJobExecution(ctx, jobs[0].ID)
	if exec.ConfiguredModel != "writer" {
		t.Fatal(exec)
	}
	var revise KnowledgeStageInput
	json.Unmarshal([]byte(exec.InputSnapshotJSON), &revise)
	if revise.UpdateProposalID != p.ID || revise.Request.Update.Analysis == nil || revise.ExpectedRevision == nil || *revise.ExpectedRevision != 1 {
		t.Fatal(revise)
	}
	old, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
	if !old.Passed {
		t.Fatal("old review erased")
	}
}
func TestKnowledgeUpdateNoChangeIgnoreAndSignals(t *testing.T) {
	s, a, _, noteID := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	p, created, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil || !created || p.State != "no_change" {
		t.Fatal(p, err)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, p.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.CollectKnowledgeUpdateSignals(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingKnowledgeUpdateArticles(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	n, _ := s.GetOwnerNote(ctx, noteID)
	s.UpdateOwnerNote(ctx, noteID, n.Content+" 增加边界。", n.CitationsJSON, n.ReferencesJSON, n.Revision)
	if err = s.CollectKnowledgeUpdateSignals(ctx); err != nil {
		t.Fatal(err)
	}
	next, created, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil || !created || next.ID == p.ID {
		t.Fatal(next, created, err)
	}
	if err = s.SetKnowledgeUpdateDisposition(ctx, next.ID, "defer", "稍后核对"); err != nil {
		t.Fatal(err)
	}
	deferred, _ := s.GetKnowledgeUpdateProposal(ctx, next.ID)
	if deferred.State != "deferred" || deferred.OwnerReason != "稍后核对" {
		t.Fatal(deferred)
	}
	if err = s.SetKnowledgeUpdateDisposition(ctx, next.ID, "ignore", "没有需要补充的内容"); err != nil {
		t.Fatal(err)
	}
	repeated, created, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil || created || repeated.State != "ignored" {
		t.Fatal(repeated, created, err)
	}
	changedModels := updateModels()
	changedModels["discover"] = "different-analysis-model"
	if repeated, created, err = s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, changedModels); err != nil || created || repeated.ID != next.ID {
		t.Fatal("model change revived ignored material", repeated, created, err)
	}
	list, err := s.ListKnowledgeUpdateProposals(ctx, a.ID, "")
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	if err = s.SetKnowledgeUpdateSettings(ctx, KnowledgeUpdateSettings{Enabled: true, DailyLimit: 0}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
	if err = s.SetKnowledgeUpdateDisposition(ctx, next.ID, "invalid", ""); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatal(err)
	}
}
func TestKnowledgeUpdateParentChangeAndPolicyRevocation(t *testing.T) {
	for _, change := range []string{"parent", "policy", "delete_note"} {
		t.Run(change, func(t *testing.T) {
			s, a, p, noteID := updateProposalFixture(t)
			ctx := t.Context()
			job, input := updateAnalysisInput(t, s, p)
			switch change {
			case "parent":
				parent, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
				var req provider.KnowledgeArticleRequest
				var blocks []provider.KnowledgeBlock
				json.Unmarshal([]byte(parent.InputJSON), &req)
				json.Unmarshal([]byte(parent.BlocksJSON), &blocks)
				// Update the personal note snapshot used by the manual draft after the edit.
				for i, m := range req.Materials {
					if m.ID == noteID {
						current, e := s.knowledgeMaterial(ctx, a.ProfileID, a.Provider, noteID)
						if e != nil {
							t.Fatal(e)
						}
						req.Materials[i] = *current
					}
				}
				if _, err := s.SaveKnowledgeDraft(ctx, a.ID, 1, "Owner的新父稿", blocks, req); err != nil {
					t.Fatal(err)
				}
				if err := s.CommitKnowledgeUpdateAnalysis(ctx, job, input, supportedUpdateDecision(input.Request)); err != nil {
					t.Fatal(err)
				}
				p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
				if p.State != "parent_changed" || p.AnalysisJSON == "{}" {
					t.Fatal(p)
				}
			case "policy":
				n, _ := s.GetOwnerNote(ctx, noteID)
				s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, n.SourceID)
				if err := s.CheckKnowledgeUpdateExecution(ctx, p, input.Request, false); err == nil {
					t.Fatal("old prose bypassed policy")
				}
			case "delete_note":
				s.DB.Exec(`DELETE FROM owner_notes WHERE id=?`, noteID)
				if err := s.CheckKnowledgeUpdateExecution(ctx, p, input.Request, false); err == nil {
					t.Fatal("deleted personal note copied through parent")
				}
			}
		})
	}
}
func TestKnowledgeUpdateDifferentDirectionAndInsufficientDecisions(t *testing.T) {
	for _, decision := range []string{"insufficient", "no_change", "new_direction"} {
		t.Run(decision, func(t *testing.T) {
			s, a, p, _ := updateProposalFixture(t)
			job, input := updateAnalysisInput(t, s, p)
			analysis := &provider.KnowledgeUpdateAnalysis{Decision: decision, Reason: "需要缩小或改变问题", Missing: []string{"案例"}}
			if decision == "new_direction" {
				ids := []string{}
				for _, m := range input.Request.Materials {
					ids = append(ids, m.ID)
				}
				analysis.NewDirection = &provider.KnowledgeTopic{Title: "另一种学习用途", Question: "如何做素材的适用边界目录？", Thesis: "按使用情景整理来源", Rationale: "用途不同", Outline: "目录与边界", MaterialIDs: ids, Score: 85, Sufficient: true}
			}
			if err := s.CommitKnowledgeUpdateAnalysis(t.Context(), job, input, &provider.KnowledgeArticleResult{Update: analysis}); err != nil {
				t.Fatal(err)
			}
			p, _ = s.GetKnowledgeUpdateProposal(t.Context(), p.ID)
			if p.State != decision {
				t.Fatal(p.State)
			}
			if decision == "insufficient" {
				changedModels := updateModels()
				changedModels["discover"] = "different-analysis-model"
				if same, created, err := s.ReserveKnowledgeUpdateProposal(t.Context(), a.ID, 1, changedModels); err != nil || created || same.ID != p.ID {
					t.Fatal("configuration revived insufficient input", same, created, err)
				}
			}
			if err := s.AcceptKnowledgeUpdate(t.Context(), p.ID, false); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			after, _ := s.GetKnowledgeArticle(t.Context(), a.ID)
			if after.WorkingRevision != 1 || after.PassedRevision != 1 {
				t.Fatal("decision overwrote article", after)
			}
			if decision == "new_direction" {
				c, err := s.ListKnowledgeTopicCandidatesPage(t.Context(), KnowledgeListQuery{Status: "waiting"})
				if err != nil || len(c.Items) != 1 {
					t.Fatal(c, err)
				}
			}
		})
	}
}
func TestKnowledgeUpdateLegacyBlocksAndBoundedParent(t *testing.T) {
	blocks, err := updateParentBlocks(&KnowledgeRevision{BlocksJSON: `[{"kind":"reflection","text":"old","material_ids":["a"]}]`, ContentHash: "short"})
	if err != nil || blocks[0].ID == "" {
		t.Fatal(blocks, err)
	}
	if _, err = updateParentBlocks(&KnowledgeRevision{BlocksJSON: strings.Repeat(" ", 100001), ContentHash: "h"}); err == nil {
		t.Fatal("oversized parent accepted")
	}
}

func TestKnowledgeUpdateDailyAdmissionAndDeferredResume(t *testing.T) {
	s, a, p, note := updateProposalFixture(t)
	ctx := t.Context()
	if err := s.QueueKnowledgeUpdateAnalysis(ctx, p.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("default switch allowed AI", err)
	}
	if err := s.SetKnowledgeUpdateSettings(ctx, KnowledgeUpdateSettings{Enabled: true, DailyLimit: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKnowledgeUpdateDisposition(ctx, p.ID, "defer", "稍后核对"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeKnowledgeUpdate(ctx, p.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("double resume", err)
	}
	if err := s.QueueKnowledgeUpdateAnalysis(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetOwnerNote(ctx, note)
	if _, err := s.UpdateOwnerNote(ctx, note, n.Content+" 又多一个边界。", n.CitationsJSON, n.ReferencesJSON, n.Revision); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, second.ID, true); !errors.Is(err, ErrAutoDailyLimitReached) {
		t.Fatal("daily cap", err)
	}
	if err = s.SetKnowledgeUpdateDisposition(ctx, second.ID, "defer", "先处理其它事"); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeKnowledgeUpdate(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	// A changed parent prevents resuming an old frozen proposal.
	if err = s.SetKnowledgeUpdateDisposition(ctx, second.ID, "defer", ""); err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(second.InputJSON), &req)
	if _, err = s.SaveKnowledgeDraft(ctx, a.ID, 1, "Owner 新稿", req.Blocks, req); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeKnowledgeUpdate(ctx, second.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("stale resume", err)
	}
}

func TestKnowledgeUpdateDeletedQuestionDoesNotBroadenScopeOrStayPending(t *testing.T) {
	s, a, _, _ := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	parent, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	json.Unmarshal([]byte(parent.InputJSON), &req)
	json.Unmarshal([]byte(parent.BlocksJSON), &blocks)
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "如何理解来源？", Goal: "解释边界"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range req.Materials {
		kind := "note"
		if m.Kind == "keypoint" {
			kind = "keypoint"
		}
		q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: kind, ObjectID: m.ID}})
		if err != nil {
			t.Fatal(err)
		}
	}
	req.Question, err = s.FreezeLearningQuestion(ctx, q.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveKnowledgeDraft(ctx, a.ID, 1, parent.Title, blocks, req); err != nil {
		t.Fatal(err)
	}
	q, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "link", Link: provider.LearningQuestionLink{Kind: "article", ObjectID: a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeLearningQuestion(ctx, q.ID, q.Revision, LearningQuestionChange{Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectKnowledgeUpdateSignals(ctx); err != nil {
		t.Fatal(err)
	}
	p, _, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 2, updateModels())
	if err != nil || p.State != "insufficient" || !strings.Contains(p.Reason, "问题已删除") {
		t.Fatal(p, err)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, p.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	ids, err := s.PendingKnowledgeUpdateArticles(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatal("deleted question keeps retrying", ids, err)
	}
}

func TestKnowledgeUpdatePurgesNewMaterialsOutsideParent(t *testing.T) {
	s, a, p, _ := updateProposalFixture(t)
	ctx := t.Context()
	other := seedEpisodeForJob(t, s)
	n, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: other, Kind: "owner_reflection", Content: "学习 来源 依据 理解 上下文。需要保留反例与边界。"})
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil {
		t.Fatal(err)
	}
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(next.InputJSON), &req)
	found := false
	for _, m := range req.Materials {
		found = found || m.ID == n.ID
	}
	if !found {
		t.Fatal("fixture did not recall new source", req.Materials)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, next.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSourceRows(ctx, models.SourceEpisode, other); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetKnowledgeUpdateProposal(ctx, next.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged proposal text survived", err)
	}
	if _, err = s.GetKnowledgeUpdateProposal(ctx, p.ID); err != nil {
		t.Fatal("unrelated proposal removed", err)
	}
	if _, err = s.GetKnowledgeArticle(ctx, a.ID); err != nil {
		t.Fatal("unrelated parent removed", err)
	}
	jobs, _ := s.ListQueuedOrRunning(ctx)
	if len(jobs) != 0 {
		t.Fatal("purged proposal task survived", jobs)
	}
}

func acceptUpdateFixture(t *testing.T) (*Store, *KnowledgeArticleRecord, *KnowledgeUpdateProposal) {
	t.Helper()
	s, a, p, _ := updateProposalFixture(t)
	ctx := t.Context()
	job, input := updateAnalysisInput(t, s, p)
	if err := s.CommitKnowledgeUpdateAnalysis(ctx, job, input, supportedUpdateDecision(input.Request)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	return s, a, p
}
func TestKnowledgeUpdateFailedReviewAndExplicitOwnerReview(t *testing.T) {
	s, a, p := acceptUpdateFixture(t)
	ctx := t.Context()
	finishKnowledgeTestStage(t, s, true)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "needs_review" || p.GeneratedRevision != 2 {
		t.Fatal(p)
	}
	finishKnowledgeTestStage(t, s, false)
	a, _ = s.GetKnowledgeArticle(ctx, a.ID)
	if a.Status != "needs_review" || a.PassedRevision != 1 || a.WorkingRevision != 2 {
		t.Fatal(a)
	}
	if err := s.QueueKnowledgeRevision(ctx, a.ID, 2, "检查解释的边界", "reviewer", false); err != nil {
		t.Fatal(err)
	}
	job, input := finishKnowledgeTestStage(t, s, true)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "completed" {
		t.Fatal(p)
	}
	passed := true
	if err := s.CommitKnowledgeStage(ctx, job, input, &provider.KnowledgeArticleResult{Passed: &passed}); err != nil {
		t.Fatal("complete update stage not idempotent", err)
	}
	// A subsequent ordinary revision no longer resends the old update context.
	if err := s.QueueKnowledgeRevision(ctx, a.ID, 2, "让解释更清楚", "reviewer", true); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.ListQueuedOrRunning(ctx)
	execution, _ := s.GetJobExecution(ctx, jobs[0].ID)
	var ordinary KnowledgeStageInput
	json.Unmarshal([]byte(execution.InputSnapshotJSON), &ordinary)
	if ordinary.Request.Update != nil || ordinary.UpdateProposalID != "" {
		t.Fatal("completed update context leaked", ordinary)
	}
}
func TestKnowledgeUpdateRevisionRetryKeepsFrozenConfiguration(t *testing.T) {
	s, a, p := acceptUpdateFixture(t)
	ctx := t.Context()
	job, err := s.ClaimNextJob(ctx, "60 seconds")
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	old, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkJobFailed(ctx, job.ID, "连接中断"); err != nil {
		t.Fatal(err)
	}
	if err = s.FailKnowledgeArticleRun(ctx, job.ID, "连接中断"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "failed" {
		t.Fatal(p)
	}
	if err = s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "generating" || p.Automated {
		t.Fatal(p)
	}
	jobs, _ := s.ListQueuedOrRunning(ctx)
	if len(jobs) != 1 || jobs[0].ID == job.ID {
		t.Fatal(jobs)
	}
	next, _ := s.GetJobExecution(ctx, jobs[0].ID)
	if next.InputSnapshotJSON != old.InputSnapshotJSON || next.ConfiguredModel != "writer" || next.RemoteCallStarted {
		t.Fatal(next, old)
	}
	finishKnowledgeTestStage(t, s, true)
	finishKnowledgeTestStage(t, s, true)
	a, _ = s.GetKnowledgeArticle(ctx, a.ID)
	if a.PassedRevision != 2 {
		t.Fatal(a)
	}
}
func TestKnowledgeUpdateAnalysisExplicitRetryAndUnknownAutomationBlock(t *testing.T) {
	s, a, p, _ := updateProposalFixture(t)
	ctx := t.Context()
	job, _ := updateAnalysisInput(t, s, p)
	if err := s.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
		t.Fatal(err)
	}
	s.MarkJobFailed(ctx, job.ID, "远端未知")
	s.FailKnowledgeArticleRun(ctx, job.ID, "远端未知")
	if err := s.SetKnowledgeUpdateSettings(ctx, KnowledgeUpdateSettings{Enabled: true, DailyLimit: 2}); err != nil {
		t.Fatal(err)
	}
	// A materially new proposal must still not auto replay this article's
	// unresolved remote operation.
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(p.InputJSON), &req)
	var noteID string
	for _, m := range req.Materials {
		if m.Kind == "owner_reflection" {
			noteID = m.ID
			break
		}
	}
	n, _ := s.GetOwnerNote(ctx, noteID)
	if _, err := s.UpdateOwnerNote(ctx, n.ID, n.Content+" 还有新的限制。", n.CitationsJSON, n.ReferencesJSON, n.Revision); err != nil {
		t.Fatal(err)
	}
	other, _, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 1, updateModels())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueKnowledgeUpdateAnalysis(ctx, other.ID, true); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "未知") {
		t.Fatal(err)
	}
	if err = s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "analysing" || p.Automated || p.JobID == job.ID {
		t.Fatal(p)
	}
	execution, _ := s.GetJobExecution(ctx, p.JobID)
	if execution.RemoteCallStarted || execution.ResultState != "" || execution.ConfiguredModel != "discovery" {
		t.Fatal(execution)
	}
}

func TestKnowledgeUpdateSourceVersionDoesNotInheritOldQuote(t *testing.T) {
	s, a, _, reflection := readyKnowledgeRevisionFixture(t)
	ctx := t.Context()
	n, _ := s.GetOwnerNote(ctx, reflection)
	source, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: "episode", SourceID: n.SourceID, Kind: "source_note", Content: "来源强调保留上下文", CitationsJSON: `["seg-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.knowledgeMaterial(ctx, a.ProfileID, a.Provider, source.ID)
	if err != nil || m == nil {
		t.Fatal(m, err)
	}
	parent, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
	var req provider.KnowledgeArticleRequest
	var blocks []provider.KnowledgeBlock
	json.Unmarshal([]byte(parent.InputJSON), &req)
	json.Unmarshal([]byte(parent.BlocksJSON), &blocks)
	req.Materials = append(req.Materials, *m)
	quote := "保留来源上下文可以避免误读。"
	blocks = append(blocks, provider.KnowledgeBlock{Kind: "source", Text: "来源写道：" + quote, MaterialIDs: []string{source.ID}, Quotes: []provider.KnowledgeQuote{{MaterialID: source.ID, Text: quote}}})
	if _, err = s.SaveKnowledgeDraft(ctx, a.ID, 1, parent.Title, blocks, req); err != nil {
		t.Fatal(err)
	}
	var sourceJob string
	if err = s.DB.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_type='episode' AND source_id=? AND job_type='transcribe' LIMIT 1`, n.SourceID).Scan(&sourceJob); err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, n.SourceID, KindTranscript, "test", "new", "1", sourceJob, `{"segments":[{"id":"seg-1","start":12,"end":20,"text":"保留上下文仍需核对实际情景中的反例。"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, n.SourceID, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectKnowledgeUpdateSignals(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err := s.PendingKnowledgeUpdateArticles(ctx)
	if err != nil || len(ids) != 1 || ids[0] != a.ID {
		t.Fatal(ids, err)
	}
	p, _, err := s.ReserveKnowledgeUpdateProposal(ctx, a.ID, 2, updateModels())
	if err != nil || p.State != "pending" {
		t.Fatal(p, err)
	}
	var refreshed provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(p.InputJSON), &refreshed)
	found := false
	for _, current := range refreshed.Materials {
		if current.ID == source.ID {
			found = true
			if current.SnapshotID == m.SnapshotID || strings.Contains(current.Evidence, quote) || !strings.Contains(current.Evidence, "实际情景") {
				t.Fatal(current, m)
			}
		}
	}
	if !found {
		t.Fatal("source note was not refreshed", refreshed.Materials)
	}
	if provider.ValidateKnowledgeBlocks(parent.Title, blocks, refreshed.Materials) == nil {
		t.Fatal("same segment ID silently inherited old quote")
	}
}
