package queue

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type updateQueueFake struct {
	calls  int
	before func(provider.KnowledgeArticleRequest)
	bad    bool
}

func (f *updateQueueFake) Name() string { return "pod" }
func (f *updateQueueFake) KnowledgeArticleStep(_ context.Context, req provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	f.calls++
	if f.before != nil {
		f.before(req)
	}
	usage := provider.TaskUsage{InputUnits: 100, OutputUnits: 50}
	if req.Stage == "update_propose" {
		id := req.Materials[0].ID
		if f.bad {
			id = "invented"
		}
		return &provider.KnowledgeArticleResult{Update: &provider.KnowledgeUpdateAnalysis{Decision: "update", Reason: "新增理解的边界", Changes: []provider.KnowledgeUpdateChange{{Action: "add", BlockID: req.Blocks[1].ID, Reason: "补充适用条件", MaterialIDs: []string{id}}}}}, usage, nil
	}
	if req.Stage == "revise" {
		return &provider.KnowledgeArticleResult{Title: req.Topic.Title, Blocks: []provider.KnowledgeBlock{{Kind: "reflection", Text: "个人记录补充了理解的限制条件。", MaterialIDs: []string{req.Materials[0].ID}}, {Kind: "synthesis", Text: "综合个人记录，回顾时需要考虑具体情境。", MaterialIDs: []string{req.Materials[0].ID, req.Materials[1].ID}}}}, usage, nil
	}
	passed := true
	return &provider.KnowledgeArticleResult{Passed: &passed}, usage, nil
}
func updateQueueFixture(t *testing.T) (*store.Store, *Worker, *store.KnowledgeUpdateProposal, *updateQueueFake, string) {
	t.Helper()
	s, w, a, _, ep := seedKnowledgeQueue(t)
	for i := 0; i < 3; i++ {
		if err := w.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.ListOwnerNotes(t.Context(), "episode", ep)
	if err != nil || len(notes) < 2 {
		t.Fatal(notes, err)
	}
	n := notes[0]
	if _, err = s.UpdateOwnerNote(t.Context(), n.ID, n.Content+" 需要考虑反例和适用条件。", n.CitationsJSON, n.ReferencesJSON, n.Revision); err != nil {
		t.Fatal(err)
	}
	stageModels := map[string]string{"discover": "analysis", "select": "selection", "write": "writer", "revise": "writer", "review": "reviewer", "review_final": "reviewer", "weekly_review": "learning"}
	p, _, err := s.ReserveKnowledgeUpdateProposal(t.Context(), a.ID, 1, stageModels)
	if err != nil || p.State != "pending" {
		t.Fatal(p, err)
	}
	fake := &updateQueueFake{}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	if err = s.QueueKnowledgeUpdateAnalysis(t.Context(), p.ID, false); err != nil {
		t.Fatal(err)
	}
	return s, w, p, fake, ep
}
func TestKnowledgeUpdateQueueKeepsPassedBodyUntilExactReview(t *testing.T) {
	s, w, p, fake, _ := updateQueueFixture(t)
	ctx := t.Context()
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if a.WorkingRevision != 2 || a.PassedRevision != 1 {
		t.Fatal(a)
	}
	old, _ := s.GetKnowledgeRevision(ctx, a.ID, 1)
	current, _ := s.GetKnowledgeRevision(ctx, a.ID, 2)
	if !old.Passed || current.Passed || current.ParentRevision != 1 {
		t.Fatal(old, current)
	}
	var workingVersion, passedVersion int
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(material_version) FROM knowledge_article_material_index WHERE article_id=? AND role='working'`, a.ID).Scan(&workingVersion); err != nil {
		t.Fatal(err)
	}
	s.DB.QueryRowContext(ctx, `SELECT MAX(material_version) FROM knowledge_article_material_index WHERE article_id=? AND role='passed'`, a.ID).Scan(&passedVersion)
	if workingVersion != 2 || passedVersion != 1 {
		t.Fatal(workingVersion, passedVersion)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetKnowledgeArticle(ctx, a.ID)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if a.PassedRevision != 2 || p.State != "completed" || p.GeneratedRevision != 2 || fake.calls != 3 {
		t.Fatal(a, p, fake.calls)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 3 {
		t.Fatal("completed proposal replayed")
	}
}
func TestKnowledgeUpdateAnalysisRecoveryPreservesOnePaidResponse(t *testing.T) {
	s, w, p, fake, _ := updateQueueFixture(t)
	ctx := t.Context()
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_update_analysis BEFORE UPDATE OF analysis_json ON knowledge_update_proposals BEGIN SELECT RAISE(FAIL,'analysis unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	exec, _ := s.GetJobExecution(ctx, p.JobID)
	if p.State != "failed" || exec.CheckpointJSON == "" || fake.calls != 1 {
		t.Fatal(p, exec, fake.calls)
	}
	s.DB.Exec(`DROP TRIGGER fail_update_analysis`)
	if err := s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "pending" || p.AnalysisJSON == "{}" || fake.calls != 1 {
		t.Fatal(p, fake.calls)
	}
	var receipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation='knowledge_article_update_propose'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal(receipts, err)
	}
}
func TestKnowledgeUpdateUnknownResponseRequiresExplicitRetry(t *testing.T) {
	s, w, p, fake, _ := updateQueueFixture(t)
	ctx := t.Context()
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if err := s.MarkJobRemoteCallStarted(ctx, p.JobID); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	exec, _ := s.GetJobExecution(ctx, p.JobID)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if exec.ResultState != models.JobResultUnknown || p.State != "failed" || fake.calls != 0 {
		t.Fatal(exec, p, fake.calls)
	}
	if err := s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	if fake.calls != 1 {
		t.Fatal(fake.calls)
	}
}
func TestKnowledgeUpdateLateResponseCannotReplaceOwnerDraft(t *testing.T) {
	for _, stage := range []string{"update_propose", "revise"} {
		t.Run(stage, func(t *testing.T) {
			s, w, p, fake, _ := updateQueueFixture(t)
			ctx := t.Context()
			if stage == "revise" {
				w.ProcessOne(ctx)
				if err := s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			fake.before = func(req provider.KnowledgeArticleRequest) {
				if req.Stage != stage {
					return
				}
				blocks := append([]provider.KnowledgeBlock(nil), req.Blocks...)
				blocks[0].Text = "Owner编辑了新的工作稿。"
				req.Update = nil
				if _, err := s.SaveKnowledgeDraft(ctx, p.ArticleID, 1, "Owner的稿件", blocks, req); err != nil {
					t.Fatal(err)
				}
			}
			w.ProcessOne(ctx)
			a, _ := s.GetKnowledgeArticle(ctx, p.ArticleID)
			p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
			if a.Title != "Owner的稿件" || a.WorkingRevision != 2 || a.PassedRevision != 1 || p.State != "parent_changed" {
				t.Fatal(a, p)
			}
			jobs, err := s.ListQueuedOrRunning(ctx)
			if err != nil || len(jobs) != 0 {
				t.Fatal(jobs, err)
			}
			var count int
			s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE operation=?`, "knowledge_article_"+stage).Scan(&count)
			if count != 1 {
				t.Fatal("lost paid response", count)
			}
			if stage == "revise" {
				revisions, _ := s.ListKnowledgeRevisions(ctx, a.ID)
				if len(revisions) != 3 || revisions[0].ParentRevision != 1 {
					t.Fatal("late branch lost", revisions)
				}
			}
		})
	}
}
func TestKnowledgeUpdatePolicyChangedDuringResponseRetainsFactsWithoutApply(t *testing.T) {
	s, w, p, fake, ep := updateQueueFixture(t)
	ctx := t.Context()
	fake.before = func(provider.KnowledgeArticleRequest) {
		if _, err := s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, ep); err != nil {
			t.Fatal(err)
		}
	}
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	exec, _ := s.GetJobExecution(ctx, p.JobID)
	a, _ := s.GetKnowledgeArticle(ctx, p.ArticleID)
	if p.State != "failed" || p.AnalysisJSON != "{}" || exec.CheckpointJSON == "" || a.PassedRevision != 1 || fake.calls != 1 {
		t.Fatal(p, exec, a)
	}
	if err := s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	if fake.calls != 1 {
		t.Fatal("policy failure replayed response")
	}
}
func TestKnowledgeUpdateMalformedAnalysisHasUsageAndExplicitNewAttempt(t *testing.T) {
	s, w, p, fake, _ := updateQueueFixture(t)
	ctx := t.Context()
	fake.bad = true
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "failed" || fake.calls != 1 {
		t.Fatal(p, fake.calls)
	}
	if err := s.RetryKnowledgeUpdate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	fake.bad = false
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	if p.State != "pending" || fake.calls != 2 {
		t.Fatal(p, fake.calls)
	}
	var analysis provider.KnowledgeUpdateAnalysis
	if json.Unmarshal([]byte(p.AnalysisJSON), &analysis) != nil || analysis.Decision != "update" {
		t.Fatal(analysis)
	}
}

func TestKnowledgeUpdateSupersededQueueDoesNotSpend(t *testing.T) {
	s, w, p, fake, _ := updateQueueFixture(t)
	ctx := t.Context()
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	var req provider.KnowledgeArticleRequest
	json.Unmarshal([]byte(p.InputJSON), &req)
	req.Update = nil
	if _, err := s.SaveKnowledgeDraft(ctx, p.ArticleID, 1, "新的 Owner 父稿", req.Blocks, req); err != nil {
		t.Fatal(err)
	}
	w.ProcessOne(ctx)
	p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
	execution, _ := s.GetJobExecution(ctx, p.JobID)
	if fake.calls != 0 || p.State != "parent_changed" || execution.ResultState != models.JobResultComplete || execution.RemoteCallStarted {
		t.Fatal(fake.calls, p, execution)
	}
}

func TestKnowledgeUpdateInvalidFrozenIdentityStopsBeforeCall(t *testing.T) {
	for _, kind := range []string{"proposal", "request", "request_id", "parent_revision", "parent_hash", "article", "stage"} {
		t.Run(kind, func(t *testing.T) {
			s, w, p, fake, _ := updateQueueFixture(t)
			ctx := t.Context()
			p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
			execution, _ := s.GetJobExecution(ctx, p.JobID)
			var input store.KnowledgeStageInput
			json.Unmarshal([]byte(execution.InputSnapshotJSON), &input)
			switch kind {
			case "proposal":
				input.UpdateProposalID = "missing"
			case "request":
				input.Request.Update = nil
			case "request_id":
				input.Request.Update.ProposalID = "other"
			case "parent_revision":
				input.Request.Update.ParentRevision++
			case "parent_hash":
				input.Request.Update.ParentHash = "other"
			case "article":
				input.ArticleID = "other"
			case "stage":
				input.Request.Stage = "other"
			}
			payload, _ := json.Marshal(input)
			if _, err := s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(payload), p.JobID); err != nil {
				t.Fatal(err)
			}
			w.ProcessOne(ctx)
			execution, _ = s.GetJobExecution(ctx, p.JobID)
			if fake.calls != 0 || execution.RemoteCallStarted {
				t.Fatal("tampered input reached provider", kind, fake.calls, execution)
			}
		})
	}
}

func TestKnowledgeUpdateRevisionGateBeforeRemoteCall(t *testing.T) {
	for _, kind := range []string{"policy", "missing_proposal", "superseded"} {
		t.Run(kind, func(t *testing.T) {
			s, w, p, fake, ep := updateQueueFixture(t)
			ctx := t.Context()
			w.ProcessOne(ctx)
			if err := s.AcceptKnowledgeUpdate(ctx, p.ID, false); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "policy":
				s.DB.Exec(`UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, ep)
			case "missing_proposal":
				s.DB.Exec(`DELETE FROM knowledge_update_proposals WHERE id=?`, p.ID)
			case "superseded":
				p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
				var req provider.KnowledgeArticleRequest
				json.Unmarshal([]byte(p.InputJSON), &req)
				req.Update = nil
				if _, err := s.SaveKnowledgeDraft(ctx, p.ArticleID, 1, "Owner再次改稿", req.Blocks, req); err != nil {
					t.Fatal(err)
				}
			}
			w.ProcessOne(ctx)
			if fake.calls != 1 {
				t.Fatal("invalid revision called provider", kind, fake.calls)
			}
			a, _ := s.GetKnowledgeArticle(ctx, p.ArticleID)
			if a.PassedRevision != 1 {
				t.Fatal("old review lost", a)
			}
			if kind == "superseded" {
				p, _ = s.GetKnowledgeUpdateProposal(ctx, p.ID)
				if p.State != "parent_changed" {
					t.Fatal(p)
				}
			}
		})
	}
}
