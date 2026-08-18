package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestCreationBriefConfirmationRechecksNewBlockingResearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "brief-recheck@example.com")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "研究画像"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "待研究方向", ProposedClaim: "初始主张", MaterialIDsJSON: `["kp-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, proposal.ID, "Owner 主张"); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "Owner 主张", MaterialPlanJSON: `["kp-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	need, err := s.CreateResearchNeed(ctx, models.ResearchNeed{CreationProposalID: proposal.ID, Severity: "blocking", Question: "尚未验证的条件是什么？"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("new blocking research must still stop a draft brief: %v", err)
	}
	if err := s.ResolveResearchNeed(ctx, need.ID, "new-source"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
}
