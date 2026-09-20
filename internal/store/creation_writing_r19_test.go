package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// confirmBriefFixture 在 makeConfirmFixture（真实 episode/keypoint/revision v2）之上
// 走生产确认路径建立持久文章链接，返回可直接生成写作任务的精确确认 Brief。
func confirmBriefFixture(t *testing.T) (*Store, *models.CreationBrief, string, string) {
	t.Helper()
	s, b, ep, kpID := makeConfirmFixture(t)
	if _, err := s.ConfirmCreationBriefVersionAndEnsureLink(t.Context(), b.ID, b.CurrentVersion, "v2"); err != nil {
		t.Fatalf("confirm+link: %v", err)
	}
	fresh, err := s.GetCreationBrief(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = ep
	return s, fresh, ep, kpID
}

// TestEnqueueClaimWriting_UnconfirmedHasNoSideEffects R19：未确认（或编辑后回到
// draft）的 Brief 不能获得写作授权——零入队、零意图、零草稿。
func TestEnqueueClaimWriting_UnconfirmedHasNoSideEffects(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	p, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "T", ProposedClaim: "C", OwnerClaim: "Owner", MaterialIDsJSON: `["kp-x"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, p.ID, "Owner"); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateCreationBriefDraftFromProposal(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID); err == nil {
		t.Fatal("draft Brief 必须拒绝写作入队")
	}
	var jobs, intents, drafts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&jobs)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_writing_intents`).Scan(&intents)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts`).Scan(&drafts)
	if jobs != 0 || intents != 0 || drafts != 0 {
		t.Fatalf("未确认入队不得有副作用: jobs=%d intents=%d drafts=%d", jobs, intents, drafts)
	}
}

// TestEnqueueClaimWriting_LinkVersionMustMatch R19：link 版本与确认版本不一致时拒绝。
func TestEnqueueClaimWriting_LinkVersionMustMatch(t *testing.T) {
	s, b, _, _ := confirmBriefFixture(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `UPDATE creation_article_links SET creation_brief_version=99 WHERE creation_brief_id=?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID); err == nil {
		t.Fatal("link 版本不匹配必须拒绝")
	}
	var jobs int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("拒绝时不得入队: %d", jobs)
	}
}

// TestEnqueueClaimWriting_FrozenSnapshotExact R19：入队在同一事务冻结完整强类型
// 快照（Brief 精确版本、draft/profile/audience、OwnerClaim/提纲/风格/篇幅、完整
// ArticleMaterial 身份、Writer provider/model/prompt），job 配置同步冻结。
func TestEnqueueClaimWriting_FrozenSnapshotExact(t *testing.T) {
	s, b, ep, kpID := confirmBriefFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.JobType != models.JobClaimWriting {
		t.Fatalf("job=%+v", job)
	}
	kp, err := s.GetKeyPoint(ctx, kpID)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.ConfiguredProvider == "" || exec.ConfigVersion != provider.ClaimWriterPromptVersion {
		t.Fatalf("配置冻结缺失: %+v", exec)
	}
	var input ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	proposal, _ := s.GetCreationProposal(ctx, b.CreationProposalID)
	link, err := s.GetCreationArticleLinkByCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.GetArticleDraftByBrief(ctx, link.ArticleBriefID)
	if err != nil {
		t.Fatal(err)
	}
	wantModel := provider.EffectiveModel(input.Provider, "", string(models.JobClaimWriting))
	if input.CreationBriefID != b.ID || input.BriefVersion != b.ConfirmedVersion {
		t.Fatalf("Brief 身份不符: %+v", input)
	}
	if input.CreationProposalID != proposal.ID || input.ProfileID != proposal.EditorialProfileID {
		t.Fatalf("proposal/profile 不符: %+v", input)
	}
	if input.CreationArticleLinkID != link.ID || input.ArticleProposalID != link.ArticleProposalID || input.ArticleBriefID != link.ArticleBriefID {
		t.Fatalf("桥接身份不符: %+v link=%+v", input, link)
	}
	if input.DraftID == "" || input.DraftID != draft.ID {
		t.Fatalf("draft 身份不符: %+v", input)
	}
	if input.OwnerClaim != b.OwnerClaim || input.Outline == "" {
		t.Fatalf("claim/outline 不符: %+v", input)
	}
	if input.Provider == "" || input.Model != wantModel || input.PromptVersion != provider.ClaimWriterPromptVersion {
		t.Fatalf("Writer 配置不符: %+v", input)
	}
	if len(input.Materials) != 1 {
		t.Fatalf("材料数=%d", len(input.Materials))
	}
	m := input.Materials[0]
	if m.KeyPointID != kpID || m.SourceType != string(kp.SourceType) || m.SourceID != kp.SourceID ||
		m.CardVersion != kp.CardVersion || m.SourceTitle != kp.SourceTitle || m.Content != kp.Content ||
		m.Description != kp.Description || len(m.Citations) == 0 {
		t.Fatalf("材料快照不完整: %+v (kp=%+v)", m, kp)
	}
	_ = ep
	// 意图已持久：intent→job→draft 可回放复用。
	var intentJobID, intentDraftID string
	if err := s.DB.QueryRowContext(ctx, `SELECT job_id,draft_id FROM claim_writing_intents WHERE intent_id=?`, claimWritingIntentID(b.ID, b.ConfirmedVersion)).Scan(&intentJobID, &intentDraftID); err != nil {
		t.Fatalf("意图未持久: %v", err)
	}
	if intentJobID != job.ID || intentDraftID != input.DraftID {
		t.Fatalf("意图映射不符: %s/%s", intentJobID, intentDraftID)
	}
}

func TestOwnerNoteFlowsIntoWritingAndReviewSnapshots(t *testing.T) {
	s, b, episodeID, kpID := confirmBriefFixture(t)
	ctx := t.Context()
	proposal, err := s.GetCreationProposal(ctx, b.CreationProposalID)
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: episodeID,
		Kind: "owner_reflection", Content: "这是我的个人判断",
		CitationsJSON: "[]", ReferencesJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.SaveCreationSelection(ctx, &models.CreationSelection{
		EditorialProfileID: proposal.EditorialProfileID, Title: "带个人理解的素材",
		MaterialIDs: []string{kpID}, NoteIDs: []string{note.ID}, Status: models.SelectionConfirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	selectionIDs, _ := json.Marshal([]string{selection.ID})
	session, err := s.CreateIdeationSession(ctx, models.IdeationSession{
		EditorialProfileID: proposal.EditorialProfileID, Intent: "写作",
		ConstraintsJSON: "{}", SelectionsJSON: string(selectionIDs),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE creation_proposals SET ideation_session_id=? WHERE id=?`, session.ID, proposal.ID); err != nil {
		t.Fatal(err)
	}

	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var writeInput ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &writeInput); err != nil {
		t.Fatal(err)
	}
	if len(writeInput.OwnerNotes) != 1 || writeInput.OwnerNotes[0].ID != note.ID ||
		writeInput.OwnerNotes[0].Revision != note.Revision || writeInput.OwnerNotes[0].SourceID != episodeID {
		t.Fatalf("写作笔记快照不完整: %+v", writeInput.OwnerNotes)
	}
	link, err := s.GetCreationArticleLinkByCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	const markdown = "# 个人理解\n\n来源说过这句话。我的个人判断。"
	revision, err := s.SaveClaimWritingOutput(ctx, ClaimWritingOutput{
		DraftID: job.SourceID, JobID: job.ID,
		CreationBriefID: b.ID, BriefVersion: b.ConfirmedVersion,
		CreationArticleLinkID: link.ID, ArticleProposalID: link.ArticleProposalID, ArticleBriefID: link.ArticleBriefID,
		OwnerClaim: b.OwnerClaim, Title: "个人理解", Markdown: markdown,
		ProviderName: "test", ModelName: "m", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{kpID, note.ID},
		Entries: []models.ClaimMapEntry{
			{Excerpt: "来源说过这句话。", ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpID}, SourceTitle: "R17", CitationRefs: []string{"seg-1"}},
			{Excerpt: "我的个人判断。", ClaimKind: provider.ClaimOwner, MaterialIDs: []string{note.ID}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{ReviewKindClaim, ReviewKindStyle} {
		reviewJob, err := s.EnqueueRevisionReview(ctx, revision.ID, kind)
		if err != nil {
			t.Fatalf("enqueue %s: %v", kind, err)
		}
		reviewExec, err := s.GetJobExecution(ctx, reviewJob.ID)
		if err != nil {
			t.Fatal(err)
		}
		var reviewInput ReviewTaskInput
		if err := json.Unmarshal([]byte(reviewExec.InputSnapshotJSON), &reviewInput); err != nil {
			t.Fatal(err)
		}
		if len(reviewInput.OwnerNotes) != 1 || reviewInput.OwnerNotes[0].ID != note.ID || reviewInput.OwnerNotes[0].Revision != note.Revision {
			t.Fatalf("%s 笔记快照不完整: %+v", kind, reviewInput.OwnerNotes)
		}
		if kind == ReviewKindClaim {
			found := false
			for _, id := range reviewInput.AuthorizedIDs {
				found = found || id == note.ID
			}
			if !found {
				t.Fatalf("主张审校授权集合缺少笔记: %v", reviewInput.AuthorizedIDs)
			}
			if _, err := s.MarkJobRunning(ctx, reviewJob.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveClaimReviewOutput(ctx, reviewJob.ID, &provider.ClaimReviewResult{Status: provider.ClaimReviewPassed}, "reviewer", "m"); err != nil {
				t.Fatal(err)
			}
			passedInput, err := s.GetPassedClaimReviewInput(ctx, revision.ID)
			if err != nil || len(passedInput.OwnerNotes) != 1 || passedInput.OwnerNotes[0].ID != note.ID {
				t.Fatalf("passed review snapshot: %+v %v", passedInput, err)
			}
		}
	}
}

// TestEnqueueClaimWriting_EightConcurrentOneJob R19：8+ 并发点击安全复用，
// 全部返回同一 draft+job；任务完成后重复点击仍复用，不创建新 job。
func TestEnqueueClaimWriting_EightConcurrentOneJob(t *testing.T) {
	s, b, _, _ := confirmBriefFixture(t)
	ctx := t.Context()
	const n = 12
	var wg sync.WaitGroup
	jobs := make(chan string, n)
	drafts := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
			if err != nil {
				errs <- err
				return
			}
			jobs <- job.ID
			drafts <- job.SourceID
		}()
	}
	wg.Wait()
	close(jobs)
	close(drafts)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seenJobs, seenDrafts := map[string]bool{}, map[string]bool{}
	for j := range jobs {
		seenJobs[j] = true
	}
	for d := range drafts {
		seenDrafts[d] = true
	}
	if len(seenJobs) != 1 || len(seenDrafts) != 1 {
		t.Fatalf("并发点击必须复用同一 draft+job: jobs=%v drafts=%v", seenJobs, seenDrafts)
	}
	var jobCount, draftCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&jobCount)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts`).Scan(&draftCount)
	if jobCount != 1 || draftCount != 1 {
		t.Fatalf("jobCount=%d draftCount=%d", jobCount, draftCount)
	}
}

// TestEnqueueClaimWriting_CompletedJobReuse R19：任务完成后重复点击复用同一 job。
func TestEnqueueClaimWriting_CompletedJobReuse(t *testing.T) {
	s, b, _, _ := confirmBriefFixture(t)
	ctx := t.Context()
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != job.ID {
		t.Fatalf("完成态重复点击必须复用: %s != %s", again.ID, job.ID)
	}
	var jobCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&jobCount)
	if jobCount != 1 {
		t.Fatalf("jobCount=%d", jobCount)
	}
}

// writerOutputFixture 构造一次合法 Writer 输出（片段取自正文）。
func writerOutputFixture(markdown, kpID string) ClaimWritingOutput {
	return ClaimWritingOutput{
		DraftID: "d1", JobID: "j1", CreationBriefID: "b1", BriefVersion: 1,
		CreationArticleLinkID: "link-1", ArticleProposalID: "ap-1", ArticleBriefID: "ab-1",
		OwnerClaim: "Owner 主张", Title: "标题", Markdown: markdown,
		ProviderName: "fake-writer", ModelName: "m1", PromptVersion: provider.ClaimWriterPromptVersion,
		AuthorizedIDs: []string{kpID},
		Entries: []models.ClaimMapEntry{
			{Excerpt: markdown[:6], ClaimKind: provider.ClaimSource, MaterialIDs: []string{kpID}, SourceTitle: "来源", CitationRefs: []string{"seg-1"}},
			{Excerpt: markdown[len(markdown)-6:], ClaimKind: provider.ClaimOwner},
		},
	}
}

// setupWriterTarget 建立真实确认 Brief + draft + queued job（同一 store）。
func setupWriterTarget(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	s, b, _, kpID := confirmBriefFixture(t)
	job, err := s.EnqueueClaimWritingForCreationBrief(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, job.SourceID, job.ID, kpID
}

// TestSaveClaimWritingOutput_AtomicAndIdempotent R19：单事务落库真实 revision、
// 双 ClaimMap 表（真 revision ID）与 job complete result；重放幂等补齐 result。
func TestSaveClaimWritingOutput_AtomicAndIdempotent(t *testing.T) {
	s, draftID, jobID, kpID := setupWriterTarget(t)
	ctx := t.Context()
	markdown := "# 标题\n\n来源说过这句话。Owner 的判断在此。"
	out := writerOutputFixture(markdown, kpID)
	out.DraftID, out.JobID = draftID, jobID

	rev, err := s.SaveClaimWritingOutput(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if rev.ID == "" || rev.DraftID != draftID || rev.OriginJobID != jobID || rev.Version != 1 || rev.Markdown != markdown {
		t.Fatalf("revision=%+v", rev)
	}
	if _, err := s.GetArticleRevision(ctx, rev.ID); err != nil {
		t.Fatalf("真实 revision UUID 应可读: %v", err)
	}
	var cmCount, entryCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_maps WHERE work_revision_id=?`, rev.ID).Scan(&cmCount); err != nil || cmCount != 2 {
		t.Fatalf("canonical claim_maps=%d err=%v", cmCount, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_map_entries WHERE draft_id=? AND revision_id=?`, draftID, rev.ID).Scan(&entryCount); err != nil || entryCount != 2 {
		t.Fatalf("claim_map_entries=%d err=%v", entryCount, err)
	}
	var ownerClaim string
	s.DB.QueryRowContext(ctx, `SELECT owner_claim FROM claim_maps WHERE work_revision_id=? LIMIT 1`, rev.ID).Scan(&ownerClaim)
	if ownerClaim != out.OwnerClaim {
		t.Fatalf("claim_maps owner_claim=%q", ownerClaim)
	}
	draft, err := s.GetArticleDraft(ctx, draftID)
	if err != nil || draft.CurrentRevisionID == nil || *draft.CurrentRevisionID != rev.ID || draft.Status != "reviewing" {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
	exec, _ := s.GetJobExecution(ctx, jobID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("result_state=%q", exec.ResultState)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(exec.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result["creation_brief_id"] != out.CreationBriefID || result["brief_version"] != float64(1) ||
		result["draft_id"] != draftID || result["revision_id"] != rev.ID ||
		result["title"] != out.Title || result["claim_count"] != float64(2) ||
		result["url"] != "/workbench/drafts/"+draftID || result["intent_id"] == "" {
		t.Fatalf("result JSON 不完整: %+v", result)
	}

	// 重放：同 (draft, job) 再次保存 → 同一修订、无重复行、result 保持 complete。
	replay, err := s.SaveClaimWritingOutput(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != rev.ID || replay.Version != rev.Version {
		t.Fatalf("重放产生新修订: %+v", replay)
	}
	var replayCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&replayCount)
	if replayCount != 1 {
		t.Fatalf("重放后修订数=%d", replayCount)
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_map_entries WHERE revision_id=?`, rev.ID).Scan(&replayCount)
	if replayCount != 2 {
		t.Fatalf("重放后条目数=%d", replayCount)
	}
	exec2, _ := s.GetJobExecution(ctx, jobID)
	if exec2.ResultState != models.JobResultComplete {
		t.Fatalf("重放 result_state=%q", exec2.ResultState)
	}
}

// TestSaveClaimWritingOutput_ReplayConfirmsMissingResult R19：已存在 revision 但
// job result 丢失（重置为 queued）时，重放仍补齐 result 后提交，不 early return。
func TestSaveClaimWritingOutput_ReplayConfirmsMissingResult(t *testing.T) {
	s, draftID, jobID, kpID := setupWriterTarget(t)
	ctx := t.Context()
	markdown := "# 标题\n\n来源说过这句话。Owner 的判断在此。"
	out := writerOutputFixture(markdown, kpID)
	out.DraftID, out.JobID = draftID, jobID
	rev, err := s.SaveClaimWritingOutput(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟终态丢失：result 被清空、任务重置回 queued（修订与映射保留）。
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='queued',result_state='',result_json='' WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	// 重放只传 1 条 Entries（同 title/markdown）：最终 claim_count 仍必须为 2，
	// 证明来自持久 maps 而非重放参数。
	partial := out
	partial.Entries = out.Entries[:1]
	replay, err := s.SaveClaimWritingOutput(ctx, partial)
	if err != nil {
		t.Fatalf("重放必须补齐 result 后提交: %v", err)
	}
	if replay.ID != rev.ID {
		t.Fatalf("重放修订不符: %+v", replay)
	}
	exec, _ := s.GetJobExecution(ctx, jobID)
	if exec.ResultState != models.JobResultComplete {
		t.Fatalf("补齐后 result_state=%q", exec.ResultState)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(exec.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result["revision_id"] != rev.ID {
		t.Fatalf("补齐 result 不含真实 revision: %+v", result)
	}
	// claim_count 以持久 maps 为准（=2），不取重放传入值（1）。
	if result["claim_count"] != float64(2) {
		t.Fatalf("claim_count 应来自持久 maps: %+v", result)
	}
	// 重放传入与持久 revision 不一致的 title/markdown 必须被拒，不得改写。
	drifted := out
	drifted.Title = "被篡改的标题"
	if _, err := s.SaveClaimWritingOutput(ctx, drifted); err == nil {
		t.Fatal("重放 title 与持久 revision 不一致必须拒绝")
	}
	drifted2 := out
	drifted2.Markdown = out.Markdown + "\n额外内容"
	if _, err := s.SaveClaimWritingOutput(ctx, drifted2); err == nil {
		t.Fatal("重放 markdown 与持久 revision 不一致必须拒绝")
	}
	var still int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&still)
	if still != 1 {
		t.Fatalf("被拒重放不得改动修订: %d", still)
	}
}

// TestSaveClaimWritingOutput_InvalidOutputRejected R19：nil/空/越权 ClaimMap、
// 空标题/正文被拒——零修订、零 result。
func TestSaveClaimWritingOutput_InvalidOutputRejected(t *testing.T) {
	s, draftID, jobID, kpID := setupWriterTarget(t)
	ctx := t.Context()
	markdown := "# 标题\n\n来源说过这句话。Owner 的判断在此。"
	base := writerOutputFixture(markdown, kpID)
	base.DraftID, base.JobID = draftID, jobID
	// 意图映射基线：必须始终完好且指向原 job，不得被任何 case 破坏。
	intentJobID := ""
	if err := s.DB.QueryRowContext(ctx, `SELECT job_id FROM claim_writing_intents WHERE draft_id=?`, draftID).Scan(&intentJobID); err != nil || intentJobID != jobID {
		t.Fatalf("意图映射基线: %q err=%v", intentJobID, err)
	}
	cases := []struct {
		name   string
		mutate func(o ClaimWritingOutput) ClaimWritingOutput
	}{
		{"empty title", func(o ClaimWritingOutput) ClaimWritingOutput { o.Title = " "; return o }},
		{"empty body", func(o ClaimWritingOutput) ClaimWritingOutput { o.Markdown = ""; return o }},
		{"nil entries", func(o ClaimWritingOutput) ClaimWritingOutput { o.Entries = nil; return o }},
		{"excerpt missing", func(o ClaimWritingOutput) ClaimWritingOutput {
			o.Entries[0].Excerpt = "正文里没有这句话"
			return o
		}},
		{"unauthorized material", func(o ClaimWritingOutput) ClaimWritingOutput {
			o.Entries[0].MaterialIDs = []string{kpID, "kp-fabricated"}
			return o
		}},
		{"invalid kind", func(o ClaimWritingOutput) ClaimWritingOutput {
			o.Entries[1].ClaimKind = "synthesis_as_source"
			return o
		}},
	}
	for i, tc := range cases {
		// 每个 case 独立 queued job（独立意图），不改任何既有主键/身份。
		caseJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
			SourceType: models.SourceEpisode, SourceID: draftID, JobType: models.JobClaimWriting,
			IntentID: fmt.Sprintf("claim_writing:invalid:%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		bad := tc.mutate(base)
		bad.JobID = caseJob.ID
		if _, err := s.SaveClaimWritingOutput(ctx, bad); err == nil {
			t.Fatalf("%s: 必须拒绝", tc.name)
		}
		var revCount int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&revCount)
		var resultState string
		s.DB.QueryRowContext(ctx, `SELECT COALESCE(result_state,'') FROM processing_jobs WHERE id=?`, bad.JobID).Scan(&resultState)
		if revCount != 0 || resultState != "" {
			t.Fatalf("%s: 不得产生修订或 result: rev=%d state=%q", tc.name, revCount, resultState)
		}
		// 拒绝不得产生意图映射；原 job 与 case job 身份均不被破坏。
		var dangling int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_writing_intents WHERE intent_id=?`, fmt.Sprintf("claim_writing:invalid:%d", i)).Scan(&dangling)
		if dangling != 0 {
			t.Fatalf("%s: 拒绝不得产生意图映射", tc.name)
		}
		if after, aerr := s.GetJob(ctx, caseJob.ID); aerr != nil || after.ID != caseJob.ID {
			t.Fatalf("%s: case job 身份异常: %v", tc.name, aerr)
		}
	}
	// 全部拒绝后：原意图映射仍完好指向原 job。
	if err := s.DB.QueryRowContext(ctx, `SELECT job_id FROM claim_writing_intents WHERE draft_id=?`, draftID).Scan(&intentJobID); err != nil || intentJobID != jobID {
		t.Fatalf("意图映射被破坏: %q err=%v", intentJobID, err)
	}
}

// TestSaveClaimWritingOutput_MissingIdentityRejected：Brief/link/proposal/article
// 身份缺失（或版本非正）一律拒绝，零修订、零 result。
func TestSaveClaimWritingOutput_MissingIdentityRejected(t *testing.T) {
	s, draftID, jobID, kpID := setupWriterTarget(t)
	ctx := t.Context()
	markdown := "# 标题\n\n来源说过这句话。Owner 的判断在此。"
	base := writerOutputFixture(markdown, kpID)
	base.DraftID, base.JobID = draftID, jobID
	cases := map[string]func(o ClaimWritingOutput) ClaimWritingOutput{
		"missing brief":    func(o ClaimWritingOutput) ClaimWritingOutput { o.CreationBriefID = ""; return o },
		"blank brief":      func(o ClaimWritingOutput) ClaimWritingOutput { o.CreationBriefID = "  "; return o },
		"zero version":     func(o ClaimWritingOutput) ClaimWritingOutput { o.BriefVersion = 0; return o },
		"negative version": func(o ClaimWritingOutput) ClaimWritingOutput { o.BriefVersion = -1; return o },
		"missing link":     func(o ClaimWritingOutput) ClaimWritingOutput { o.CreationArticleLinkID = ""; return o },
		"missing ap":       func(o ClaimWritingOutput) ClaimWritingOutput { o.ArticleProposalID = ""; return o },
		"missing ab":       func(o ClaimWritingOutput) ClaimWritingOutput { o.ArticleBriefID = ""; return o },
	}
	for name, mutate := range cases {
		caseJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
			SourceType: models.SourceEpisode, SourceID: draftID, JobType: models.JobClaimWriting,
			IntentID: "claim_writing:identity:" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		bad := mutate(base)
		bad.JobID = caseJob.ID
		if _, err := s.SaveClaimWritingOutput(ctx, bad); err == nil {
			t.Fatalf("%s: 必须拒绝", name)
		}
		var revCount int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_revisions WHERE draft_id=?`, draftID).Scan(&revCount)
		var resultState string
		s.DB.QueryRowContext(ctx, `SELECT COALESCE(result_state,'') FROM processing_jobs WHERE id=?`, caseJob.ID).Scan(&resultState)
		var draftRev *string
		d, _ := s.GetArticleDraft(ctx, draftID)
		draftRev = d.CurrentRevisionID
		if revCount != 0 || resultState != "" || draftRev != nil {
			t.Fatalf("%s: 缺失身份不得有副作用: rev=%d state=%q current=%v", name, revCount, resultState, draftRev)
		}
	}
}

// TestEnqueueClaimWriting_ImmutableMaterialSnapshot R19：确认后修改 keypoint 正文/
// 卡片版本，入队输入仍使用确认 revision 冻结的不可变快照内容与卡片版本，
// 不静默采用未授权的新内容。
func TestEnqueueClaimWriting_ImmutableMaterialSnapshot(t *testing.T) {
	s, b, _, kpID := confirmBriefFixture(t)
	ctx := t.Context()
	kpBefore, err := s.GetKeyPoint(ctx, kpID)
	if err != nil {
		t.Fatal(err)
	}
	// 确认后内容漂移：修改 keypoint 正文与卡片版本。
	if _, err := s.DB.ExecContext(ctx, `UPDATE keypoint_index SET content='被篡改的新内容', card_version=card_version+1 WHERE id=?`, kpID); err != nil {
		t.Fatal(err)
	}
	kpAfter, err := s.GetKeyPoint(ctx, kpID)
	if err != nil {
		t.Fatal(err)
	}
	if kpAfter.Content == kpBefore.Content || kpAfter.CardVersion == kpBefore.CardVersion {
		t.Fatalf("漂移未生效: before=%+v after=%+v", kpBefore, kpAfter)
	}
	job, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	var input ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Materials) != 1 {
		t.Fatalf("materials=%d", len(input.Materials))
	}
	if input.Materials[0].Content != kpBefore.Content ||
		input.Materials[0].CardVersion != kpBefore.CardVersion {
		t.Fatalf("入队必须使用确认时冻结的不可变快照: got content=%q card=%d want %q card=%d",
			input.Materials[0].Content, input.Materials[0].CardVersion, kpBefore.Content, kpBefore.CardVersion)
	}
}

// TestEnqueueClaimWritingVersion_StaleExpectedRejected R19：expected_version 与
// current/confirmed 不一致返回版本冲突，零副作用。
func TestEnqueueClaimWritingVersion_StaleExpectedRejected(t *testing.T) {
	s, b, _, _ := confirmBriefFixture(t)
	ctx := t.Context()
	before, err := s.EnqueueClaimWritingForCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueClaimWritingForCreationBriefVersion(ctx, b.ID, b.ConfirmedVersion-1); !errors.Is(err, ErrCreationBriefVersionConflict) {
		t.Fatalf("stale expected 应版本冲突: %v", err)
	}
	if _, err := s.EnqueueClaimWritingForCreationBriefVersion(ctx, b.ID, b.ConfirmedVersion+1); !errors.Is(err, ErrCreationBriefVersionConflict) {
		t.Fatalf("超前 expected 应版本冲突: %v", err)
	}
	var jobCount, intentCount, draftCount int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&jobCount)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_writing_intents`).Scan(&intentCount)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts`).Scan(&draftCount)
	if jobCount != 1 || intentCount != 1 || draftCount != 1 {
		t.Fatalf("stale 入队不得有副作用: jobs=%d intents=%d drafts=%d", jobCount, intentCount, draftCount)
	}
	if before == nil {
		t.Fatal("baseline job 缺失")
	}
}

// TestMigration0053 R19：origin_job_id 列与部分唯一索引、claim_writing_intents 表/
// 主键/完成态映射可读；同 Brief 历史 duplicate 草稿升级不被破坏，Writer 走专属映射。
func TestMigration0053(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "mig.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyThrough := func(version int) error {
		for _, m := range ms {
			if m.version <= version {
				if err := applyOne(ctx, db, m); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	if err := applyThrough(52); err != nil {
		t.Fatalf("应用 1..52: %v", err)
	}
	// 0052 下构造同 Brief 两个历史草稿（旧 schema/API 允许）。
	if _, err = db.ExecContext(ctx, `INSERT INTO editorial_profiles (id, name) VALUES ('p1','P')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO article_proposals (id, editorial_profile_id, kind, status, title) VALUES ('ap1','p1','deep_read','accepted','T')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO article_briefs (id, proposal_id, status, thesis) VALUES ('ab1','ap1','confirmed','X')`); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"d-legacy-1", "d-legacy-2"} {
		if _, err = db.ExecContext(ctx, `INSERT INTO article_drafts (id, editorial_profile_id, brief_id, title, status) VALUES (?, 'p1', 'ab1', 'T', 'drafting')`, d); err != nil {
			t.Fatal(err)
		}
	}
	// 应用 0053（仅该条迁移）：不因历史重复失败，且不删除旧数据。
	for _, m := range ms {
		if m.version == 53 {
			if err := applyOne(ctx, db, m); err != nil {
				t.Fatalf("0053 升级应成功: %v", err)
			}
		}
	}
	var draftCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts WHERE brief_id='ab1'`).Scan(&draftCount); err != nil || draftCount != 2 {
		t.Fatalf("历史重复草稿必须保留: %d err=%v", draftCount, err)
	}
	// origin_job_id 列存在；部分唯一索引生效（同 draft+origin 只一修订）。
	for _, stmt := range []string{
		`INSERT INTO article_revisions (id, draft_id, version, origin_job_id, markdown, origin) VALUES ('r1','d-legacy-1',1,'j1','# m','writer')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO article_revisions (id, draft_id, version, origin_job_id, markdown, origin) VALUES ('r2','d-legacy-1',2,'j1','# m','writer')`); err == nil {
		t.Fatal("同 (draft, origin_job_id) 重复必须被唯一索引拒绝")
	}
	// claim_writing_intents：主键 + 完成态映射可读。
	if _, err := db.ExecContext(ctx, `INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, intent_id) VALUES ('jx','episode','d-legacy-1','claim_writing','succeeded','claim_writing:b1:v1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO claim_writing_intents (intent_id, job_id, draft_id) VALUES ('claim_writing:b1:v1','jx','d-legacy-1')`); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := db.QueryRowContext(ctx, `SELECT job_id FROM claim_writing_intents WHERE intent_id='claim_writing:b1:v1'`).Scan(&jobID); err != nil || jobID != "jx" {
		t.Fatalf("完成态意图映射不可读: %s %v", jobID, err)
	}
	var ddl string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='claim_writing_intents'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(ddl), "intent_id") || !strings.Contains(strings.ToLower(ddl), "primary key") {
		t.Fatalf("claim_writing_intents 应以 intent_id 为主键: %s", ddl)
	}
	// claim_writing_drafts：主键映射可写可读，重复建立被拒。
	if _, err := db.ExecContext(ctx, `INSERT INTO claim_writing_drafts (article_brief_id, draft_id) VALUES ('ab1','d-legacy-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO claim_writing_drafts (article_brief_id, draft_id) VALUES ('ab1','d-legacy-2')`); err == nil {
		t.Fatal("每兼容 Brief 只允许一个 Writer 草稿映射")
	}
	var writerDraft string
	if err := db.QueryRowContext(ctx, `SELECT draft_id FROM claim_writing_drafts WHERE article_brief_id='ab1'`).Scan(&writerDraft); err != nil || writerDraft != "d-legacy-1" {
		t.Fatalf("Writer 草稿映射不可读: %s %v", writerDraft, err)
	}
}

// TestMigration0054 R20：origin_job_id 列与部分唯一索引（claim_reviews /
// article_reviews）、revision_review_intents 主键与 (revision, kind) 幂等映射；
// 旧数据 origin_job_id=” 不受索引约束，历史多行审校保留。
func TestMigration0054(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "mig54.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyThrough := func(version int) error {
		for _, m := range ms {
			if m.version <= version {
				if err := applyOne(ctx, db, m); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	if err := applyThrough(53); err != nil {
		t.Fatalf("应用 1..53: %v", err)
	}
	// 0053 下构造历史审校行（origin_job_id 缺省 ''）。
	if _, err = db.ExecContext(ctx, `INSERT INTO claim_reviews (id,work_revision_id,status) VALUES ('cr-old','r-old','passed')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO article_reviews (id,revision_id,kind,status,issues_json) VALUES ('ar-old','r-old','style','advisory','["建议"]')`); err != nil {
		t.Fatal(err)
	}
	// 应用 0054（仅该条迁移）。
	for _, m := range ms {
		if m.version == 54 {
			if err := applyOne(ctx, db, m); err != nil {
				t.Fatalf("0054 升级应成功: %v", err)
			}
		}
	}
	// 旧行不受部分唯一索引约束：同修订多行历史审校保留。
	if _, err = db.ExecContext(ctx, `INSERT INTO claim_reviews (id,work_revision_id,status) VALUES ('cr-old2','r-old','failed')`); err != nil {
		t.Fatal(err)
	}
	// origin_job_id 非空时同 (revision, job) 唯一。
	if _, err = db.ExecContext(ctx, `INSERT INTO claim_reviews (id,work_revision_id,status,origin_job_id) VALUES ('cr-j1','r-new','passed','job-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO claim_reviews (id,work_revision_id,status,origin_job_id) VALUES ('cr-j2','r-new','failed','job-1')`); err == nil {
		t.Fatal("同 (revision, origin_job_id) 重复必须被唯一索引拒绝")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,origin_job_id) VALUES ('ar-j1','r-new','style','failed','["x"]','job-2')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO article_reviews (id,revision_id,kind,status,issues_json,origin_job_id) VALUES ('ar-j2','r-new','style','passed','[]','job-2')`); err == nil {
		t.Fatal("article_reviews 同 (revision, origin_job_id) 重复必须被唯一索引拒绝")
	}
	// revision_review_intents：主键幂等 + (revision, kind) 映射可读。
	if _, err = db.ExecContext(ctx, `INSERT INTO processing_jobs (id, source_type, source_id, job_type, status, intent_id) VALUES ('jr','episode','d','claim_review','succeeded','claim_review:r-new')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO revision_review_intents (intent_id, job_id, revision_id, kind) VALUES ('claim_review:r-new','jr','r-new','claim')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO revision_review_intents (intent_id, job_id, revision_id, kind) VALUES ('claim_review:r-new','jr2','r-new','style')`); err == nil {
		t.Fatal("同 intent 重复必须被主键拒绝")
	}
	var jobID, kind string
	if err := db.QueryRowContext(ctx, `SELECT job_id,kind FROM revision_review_intents WHERE intent_id='claim_review:r-new'`).Scan(&jobID, &kind); err != nil || jobID != "jr" || kind != "claim" {
		t.Fatalf("审校意图映射不可读: %s %s %v", jobID, kind, err)
	}
	// 历史审校行仍可读（含 advisory 兼容状态）。
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id='r-old'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("历史 style 审校必须保留: %d err=%v", n, err)
	}
}
