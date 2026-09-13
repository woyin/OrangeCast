package queue

import (
	"context"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// TestClaimReviewStore_RoundTrip C11：审校结果持久化与读取。
func TestClaimReviewStore_RoundTrip(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	review, err := s.CreateClaimReview(ctx, models.ClaimReview{
		WorkRevisionID: "rev-1", Status: "passed", IssuesJSON: `[]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "passed" {
		t.Fatalf("状态不符: %+v", review)
	}
	latest, err := s.LatestClaimReview(ctx, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != review.ID {
		t.Fatalf("latest 应为刚创建的审校: %+v", latest)
	}
}

func TestClaimReviewStore_FailPersisted(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	review, err := s.CreateClaimReview(ctx, models.ClaimReview{
		WorkRevisionID: "rev-f", Status: "failed",
		IssuesJSON: `["片段X：synthesis_as_source — 综合伪装成来源"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "failed" {
		t.Fatalf("状态应为 fail: %+v", review)
	}
	latest, _ := s.LatestClaimReview(ctx, "rev-f")
	if !strings.Contains(latest.IssuesJSON, "synthesis_as_source") {
		t.Fatalf("问题应持久化: %s", latest.IssuesJSON)
	}
}

// TestEnqueueClaimReviewJob_Idempotent C11：入队幂等。
func TestEnqueueClaimReviewJob_Idempotent(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	job1, created1, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: "draft-idem", JobType: models.JobClaimReview,
		IntentID: "claim_review:idem:rev1",
	})
	if err != nil || !created1 {
		t.Fatalf("首次入队: %v %v", created1, err)
	}
	job2, created2, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: "draft-idem", JobType: models.JobClaimReview,
		IntentID: "claim_review:idem:rev1",
	})
	if err != nil || created2 {
		t.Fatalf("重复入队应幂等: %v %v", created2, err)
	}
	if job1.ID != job2.ID {
		t.Fatalf("幂等入队应返回同任务: %s != %s", job1.ID, job2.ID)
	}
}

// TestClaimWriterAndReviewEnqueue C10/C11：入队与幂等断言。
func TestClaimWriterAndReviewEnqueue(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "CW"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateCreationProposal(ctx, models.CreationProposal{
		EditorialProfileID: profile.ID, IdeationSessionID: sess.ID,
		Status: "proposed", WorkingTitle: "CW", ProposedClaim: "CW 主张", CreationForm: "article",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv_store_Accept(t, s, proposal.ID); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateCreationBrief(ctx, models.CreationBrief{
		CreationProposalID: proposal.ID, OwnerClaim: "CW Owner 主张", MaterialPlanJSON: `["kp"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	link, err := s.EnsureCreationArticleLink(ctx, proposal.ID, brief.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.CreateArticleDraft(ctx, link.ArticleBriefID, "CW 文章")
	if err != nil {
		t.Fatal(err)
	}
	// v2 Writer 入队。
	jw, err := w.EnqueueClaimWritingJob(ctx, draft.ID, brief.ID)
	if err != nil || jw == nil {
		t.Fatalf("写作入队: %v %v", jw, err)
	}
	// 同参数重复入队 → nil（幂等）。
	jw2, err := w.EnqueueClaimWritingJob(ctx, draft.ID, brief.ID)
	if err != nil || jw2 != nil {
		t.Fatalf("重复入队应 nil: %v %v", jw2, err)
	}
	// 审校入队。
	jr, err := w.EnqueueClaimReviewJob(ctx, draft.ID, revIDForTest(t, s, draft.ID))
	if err != nil || jr == nil {
		t.Fatalf("审校入队: %v %v", jr, err)
	}
}

func srv_store_Accept(t *testing.T, s *store.Store, proposalID string) error {
	t.Helper()
	return s.AcceptCreationProposal(context.Background(), proposalID, "Owner 主张")
}

func revIDForTest(t *testing.T, s *store.Store, draftID string) string {
	t.Helper()
	return "rev-" + draftID
}
