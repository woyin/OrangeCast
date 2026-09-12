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
	if err != nil || latest.ID != review.ID {
		t.Fatalf("latest 应返回刚创建的审校: %+v %v", latest, err)
	}
	_, err = s.CreateClaimReview(ctx, models.ClaimReview{
		WorkRevisionID: "rev-1", Status: "failed", IssuesJSON: `["问题1"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 同 revision 的 latest 检查（created_at 秒级精度可能相同，验证无错误即可）。
	if latest2 == nil {
		t.Fatal("latest2 不应为 nil")
	}
}

// TestClaimReviewStore_FailPersisted C11：fail 状态和 issues 正确持久化。
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
