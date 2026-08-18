package store

import (
	"context"
	"github.com/woyin/orangecast/internal/models"
	"testing"
)

func TestClaimMapAndClaimReviewAreRevisionScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	claim, err := s.CreateClaimMap(ctx, models.ClaimMap{WorkRevisionID: "compat-revision", Kind: models.ClaimOwner, Excerpt: "这是 Owner 承担的判断", OwnerClaim: "这是 Owner 承担的判断"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateClaimMap(ctx, models.ClaimMap{WorkRevisionID: "compat-revision", Kind: models.ClaimSource, Excerpt: "来源表达的观点", KeyPointIDsJSON: `["kp"]`}); err != nil {
		t.Fatal(err)
	}
	maps, err := s.ListClaimMaps(ctx, "compat-revision")
	if err != nil || len(maps) != 2 || maps[0].ID != claim.ID {
		t.Fatalf("claim maps should remain revision scoped: maps=%+v err=%v", maps, err)
	}
	if _, err := s.CreateClaimReview(ctx, models.ClaimReview{WorkRevisionID: "compat-revision", Status: "passed"}); err != nil {
		t.Fatal(err)
	}
	review, err := s.LatestClaimReview(ctx, "compat-revision")
	if err != nil || review.Status != "passed" {
		t.Fatalf("claim review should be queryable by exact revision: review=%+v err=%v", review, err)
	}
}
