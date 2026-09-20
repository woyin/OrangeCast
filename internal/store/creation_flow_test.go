package store

import (
	"context"
	"github.com/woyin/orangecast/internal/models"
	"strings"
	"testing"
)

func TestDirectedIdeationResearchNeedAndCreationBriefAuthorization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "flow@example.com")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "构思画像"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "比较两种学习方法"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMaterialDiagnosis(ctx, models.MaterialDiagnosis{IdeationSessionID: session.ID, DiagnosisJSON: `{"supports":[],"conflicts":[],"gaps":["缺少结果数据"]}`}); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, IdeationSessionID: session.ID, WorkingTitle: "学习方法的取舍", ProposedClaim: "不同学习方法适合不同反馈周期"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, proposal.ID, "我认为反馈周期应决定学习方法选择"); err != nil {
		t.Fatal(err)
	}
	need, err := s.CreateResearchNeed(ctx, models.ResearchNeed{CreationProposalID: proposal.ID, Severity: "blocking", Question: "不同反馈周期的可验证结果是什么？"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "我认为反馈周期应决定学习方法选择"}); err == nil || !strings.Contains(err.Error(), "blocking") {
		t.Fatalf("blocking research must prevent brief: %v", err)
	}
	plan, err := s.CreateResearchPlan(ctx, models.ResearchPlan{ResearchNeedID: need.ID, Question: need.Question, Scope: "只收集 Owner 导入的来源"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmResearchPlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	// C04：解决缺口需要真实已处理来源（Owner 导入并完成转录）。
	podcast, perr := s.CreatePodcast(ctx, "https://feed.example.com/owner-imported.xml", "Owner 导入", "", "")
	if perr != nil {
		t.Fatal(perr)
	}
	if _, merr := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "oi", Title: "Owner 导入单集", AudioURL: "https://a.mp3"}}); merr != nil {
		t.Fatal(merr)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	trJob, terr := s.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)
	if terr != nil {
		t.Fatal(terr)
	}
	trVersion, terr := s.CreateArtifactVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, "test", "test", "1", trJob.ID,
		`{"language":"zh","text":"素材","segments":[{"id":"seg-1","start":0,"end":2,"text":"素材"}]}`)
	if terr != nil {
		t.Fatal(terr)
	}
	if _, err := s.MarkJobRunning(ctx, trJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, trJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateEpisodeStatus(ctx, eps[0].ID, models.StatusProcessed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, trVersion); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceEpisode, eps[0].ID, trVersion, "seg-1"); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "我认为反馈周期应决定学习方法选择", ClaimPlanJSON: `["owner_claim"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.GetCreationBrief(ctx, brief.ID)
	if err != nil || confirmed.Status != "confirmed" || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirmed brief should be sole work authorization: brief=%+v err=%v", confirmed, err)
	}
}

// TestResearchNeed_TranscriptReanalysisReblocks R14：依据解决后转录重分析切换
// current 版本 → 旧版本依据重新阻断（need 重开 + Brief needs_review 同事务传播），
// HasBlockingResearchNeed 恢复 true；Owner 复核后可重新确认。
func TestResearchNeed_TranscriptReanalysisReblocks(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "重分析", ProposedClaim: "主张R"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, proposal.ID, "Owner 主张 R"); err != nil {
		t.Fatal(err)
	}
	need, err := s.CreateResearchNeed(ctx, models.ResearchNeed{CreationProposalID: proposal.ID, Severity: "blocking", Question: "缺口"})
	if err != nil {
		t.Fatal(err)
	}
	podcast, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "ra-1", Title: "E", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := s.ListEpisodes(ctx, podcast.ID)
	trJob, _ := s.EnqueueJob(ctx, models.SourceEpisode, eps[0].ID, models.JobTranscribe)
	v1, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, "test", "test", "1", trJob.ID,
		`{"language":"zh","text":"v1","segments":[{"id":"seg-1","start":0,"end":2,"text":"v1"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s.MarkJobRunning(ctx, trJob.ID)
	s.MarkJobSucceeded(ctx, trJob.ID)
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, v1); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceEpisode, eps[0].ID, v1, "seg-1"); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "Owner 主张", ClaimPlanJSON: `["owner_claim"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}

	// 重分析：新转录版本并切换 current → 旧版本依据失效（同事务传播）。
	v2, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, "test", "test", "1", trJob.ID,
		`{"language":"zh","text":"v2","segments":[{"id":"seg-2","start":0,"end":2,"text":"v2"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, eps[0].ID, KindTranscript, v2); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetResearchNeed(ctx, need.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" || !got.ResolutionInvalidated {
		t.Fatalf("旧版本依据应重新阻断: %+v", got)
	}
	blocked, err := s.HasBlockingResearchNeed(ctx, proposal.ID)
	if err != nil || !blocked {
		t.Fatalf("缺口应恢复阻断: blocked=%v err=%v", blocked, err)
	}
	b, _ := s.GetCreationBrief(ctx, brief.ID)
	if b.Status != "needs_review" {
		t.Fatalf("已确认 Brief 应标待复核: %+v", b)
	}
	// 其他提案的 Brief 不受误伤（对照）。
	otherProposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "无关提案", ProposedClaim: "无关主张"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, otherProposal.ID, "无关 Owner 主张"); err != nil {
		t.Fatal(err)
	}
	otherBrief, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: otherProposal.ID, OwnerClaim: "无关 Owner 主张", ClaimPlanJSON: `["owner_claim"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, otherBrief.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateResearchResolutions(ctx, models.SourceEpisode, "无命中来源"); err != nil {
		t.Fatal(err)
	}
	otherGot, _ := s.GetCreationBrief(ctx, otherBrief.ID)
	if otherGot.Status != "confirmed" {
		t.Fatalf("无关 Brief 不得被误伤: %+v", otherGot)
	}
	// Owner 用新版本有效依据重新解决缺口后，needs_review Brief 可复核确认。
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceEpisode, eps[0].ID, v2, "seg-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatalf("needs_review Brief 应可复核确认: %v", err)
	}
}

// TestResearchNeed_DocumentNewVersionReblocks R14：同系列新文档版本出现 →
// 旧版本文档的依据重新阻断；旧版本/旧 ID 依据不再被接受（系列最新版本校验）。
func TestResearchNeed_DocumentNewVersionReblocks(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "文档版本", ProposedClaim: "主张D"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, proposal.ID, "Owner 主张 D"); err != nil {
		t.Fatal(err)
	}
	need, err := s.CreateResearchNeed(ctx, models.ResearchNeed{CreationProposalID: proposal.ID, Severity: "blocking", Question: "缺口"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreatePastedDocument(ctx, "研究文档", "第一段内容足够长。\n\n第二段内容。")
	if err != nil {
		t.Fatal(err)
	}
	var segID string
	for _, seg := range DocumentSegments(doc) {
		segID = seg.ID
		break
	}
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceDocument, doc.ID, doc.Version, segID); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "Owner 主张", ClaimPlanJSON: `["owner_claim"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}

	// 同系列新版本（不可变、新 ID）出现 → 旧版本依据失效传播。
	doc2, err := s.CreateDocumentVersion(ctx, doc.ID, "研究文档（修订）", "修订后的内容，段落更新。\n\n第二段。")
	if err != nil {
		t.Fatal(err)
	}
	if doc2.Version <= doc.Version || doc2.ID == doc.ID {
		t.Fatalf("新版本应为新 ID 更高版本: %+v", doc2)
	}
	got, _ := s.GetResearchNeed(ctx, need.ID)
	if got.Status != "open" || !got.ResolutionInvalidated {
		t.Fatalf("旧版本文档依据应重新阻断: %+v", got)
	}
	b, _ := s.GetCreationBrief(ctx, brief.ID)
	if b.Status != "needs_review" {
		t.Fatalf("已确认 Brief 应标待复核: %+v", b)
	}
	// 旧文档 ID/版本不再被接受（系列最新版本校验）。
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceDocument, doc.ID, doc.Version, segID); err == nil {
		t.Fatal("旧版本文档依据应拒绝")
	}
	// 新版本的有效依据可再次解决。
	var newSeg string
	for _, seg := range DocumentSegments(doc2) {
		newSeg = seg.ID
		break
	}
	if err := s.ResolveResearchNeedWithEvidence(ctx, need.ID, models.SourceDocument, doc2.ID, doc2.Version, newSeg); err != nil {
		t.Fatalf("新版本有效依据应可解决: %v", err)
	}
}
