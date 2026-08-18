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
	if err := s.ResolveResearchNeed(ctx, need.ID, "owner-imported-source"); err != nil {
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
