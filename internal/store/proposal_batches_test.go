package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func TestProposalBatchIsIdempotentAndAppliesAttentionBackpressure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "batch@example.com")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "批次画像"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "snapshot-1", MaterialSnapshotJSON: `["keypoint-1"]`})
	if err != nil || first.Status != "ready" {
		t.Fatalf("batch should start ready: batch=%+v err=%v", first, err)
	}
	second, err := s.CreateProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "snapshot-1", MaterialSnapshotJSON: `["keypoint-1"]`})
	if err != nil || second.ID != first.ID {
		t.Fatalf("same snapshot must not create another batch: first=%+v second=%+v err=%v", first, second, err)
	}
	if open, err := s.HasOpenProposalBatch(ctx, profile.ID); err != nil || !open {
		t.Fatalf("ready batch should pause automatic discovery: open=%v err=%v", open, err)
	}
	if err := s.SetProposalBatchStatus(ctx, first.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if open, err := s.HasOpenProposalBatch(ctx, profile.ID); err != nil || open {
		t.Fatalf("completed batch should release backpressure: open=%v err=%v", open, err)
	}
	reserved, created, err := s.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "reserved-snapshot", MaterialSnapshotJSON: `["keypoint-2"]`})
	if err != nil || !created || reserved.Status != "reviewing" {
		t.Fatalf("first automatic snapshot must receive the durable pre-call claim: batch=%+v created=%v err=%v", reserved, created, err)
	}
	again, created, err := s.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "reserved-snapshot", MaterialSnapshotJSON: `["keypoint-2"]`})
	if err != nil || created || again.ID != reserved.ID {
		t.Fatalf("same automatic snapshot must not receive another paid-task claim: first=%+v again=%+v created=%v err=%v", reserved, again, created, err)
	}
	cost := int64(7)
	if err := s.FinalizeAutomaticProposalBatch(ctx, reserved.ID, "fake", "fake-scout", "素材只能支持一条方向", &cost, []models.CreationProposal{{WorkingTitle: "可审阅方向", ProposedClaim: "Owner 尚未承担的主张", MaterialIDsJSON: `["keypoint-2"]`}}); err != nil {
		t.Fatal(err)
	}
	finalized, err := s.GetProposalBatchByIdempotencyKey(ctx, "reserved-snapshot")
	if err != nil || finalized.Status != "ready" || finalized.CostCents == nil || *finalized.CostCents != cost {
		t.Fatalf("finalized batch should expose provider result and cost: batch=%+v err=%v", finalized, err)
	}
	proposals, err := s.ListCreationProposals(ctx, profile.ID)
	if err != nil || len(proposals) != 1 || proposals[0].ProposalBatchID != reserved.ID {
		t.Fatalf("finalized batch must create batch-owned creation proposals: proposals=%+v err=%v", proposals, err)
	}
}
