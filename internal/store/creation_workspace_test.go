package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestMaterialCandidateQualityGateAndIdempotentMaterialChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "workspace@example.com")
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/workspace.xml", "学习播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "workspace-episode", Title: "学习单集", AudioURL: "https://cdn.example.com/workspace.mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, _ := s.ListEpisodes(ctx, podcast.ID)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, episodes[0].ID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	transcriptVersion, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, "test", "test", "1", job.ID, `{"language":"zh","text":"学习素材","segments":[{"id":"seg-1","start":0,"end":1,"text":"学习素材"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, transcriptVersion); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexKeyPoints(ctx, models.SourceEpisode, episodes[0].ID, episodes[0].Title, 1, &provider.KnowledgeCard{KeyPoints: []provider.KeyPoint{{Content: "可复用观点", Citations: []string{"seg-1"}}}}, []provider.Segment{{ID: "seg-1", End: 1}}); err != nil {
		t.Fatal(err)
	}
	keyPoints, _, err := s.ListKeyPoints(ctx, 1, 10)
	if err != nil || len(keyPoints) != 1 {
		t.Fatalf("keypoint setup failed: points=%+v err=%v", keyPoints, err)
	}
	if keyPoints[0].QualityStatus != models.KeyPointNeedsReview {
		t.Fatalf("automatic KeyPoint must wait for quality review: %+v", keyPoints[0])
	}
	if err := s.SetKeyPointQualityStatus(ctx, keyPoints[0].ID, models.KeyPointReady); err != nil {
		t.Fatal(err)
	}
	approved, err := s.GetKeyPoint(ctx, keyPoints[0].ID)
	if err != nil || approved.QualityStatus != models.KeyPointReady {
		t.Fatalf("quality decision should persist: keypoint=%+v err=%v", approved, err)
	}
	if err := s.SetKeyPointQualityStatus(ctx, keyPoints[0].ID, models.KeyPointReady); err != nil {
		t.Fatal(err)
	}
	var qualityChanges int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM material_changes WHERE keypoint_id=? AND change_kind='quality_approved'`, keyPoints[0].ID).Scan(&qualityChanges); err != nil || qualityChanges != 1 {
		t.Fatalf("quality approval must make one idempotent discovery change: count=%d err=%v", qualityChanges, err)
	}
	if err := s.MarkKeyPointStale(ctx, keyPoints[0].ID, "source superseded"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearKeyPointStaleness(ctx, keyPoints[0].ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateMaterialCandidate(ctx, models.MaterialCandidate{SourceType: string(models.SourceEpisode), SourceID: "missing-source", OriginKind: "highlight", Content: "不能成为孤儿", CitationsJSON: `["seg-1"]`}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("candidate must require an existing source: %v", err)
	}
	if _, err := s.CreateMaterialCandidate(ctx, models.MaterialCandidate{SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, OriginKind: "highlight", Content: "必须引用自身来源", CitationsJSON: `["missing-segment"]`}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("candidate must require citations within its source: %v", err)
	}

	candidate, err := s.CreateMaterialCandidate(ctx, models.MaterialCandidate{SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, OriginKind: "highlight", Content: "可独立表达的学习候选", CitationsJSON: `["seg-1"]`})
	if err != nil || candidate.Status != "pending" {
		t.Fatalf("candidate should begin pending: candidate=%+v err=%v", candidate, err)
	}
	if err := s.SetMaterialCandidateStatus(ctx, candidate.ID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMaterialCandidate(ctx, candidate.ID)
	if err != nil || got.Status != "accepted" || got.ReviewedAt == "" {
		t.Fatalf("accepted candidate should preserve its review: candidate=%+v err=%v", got, err)
	}
	promoted, err := s.PromoteMaterialCandidate(ctx, candidate.ID)
	if err != nil || promoted.QualityStatus != models.KeyPointOwnerConfirmed {
		t.Fatalf("accepted candidate should become an Owner-confirmed KeyPoint: keypoint=%+v err=%v", promoted, err)
	}
	got, err = s.GetMaterialCandidate(ctx, candidate.ID)
	if err != nil || got.Status != "promoted" {
		t.Fatalf("promotion should preserve candidate provenance: candidate=%+v err=%v", got, err)
	}
	if _, err := s.PromoteMaterialCandidate(ctx, candidate.ID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("candidate promotion must be one-time: %v", err)
	}
	if err := s.SetMaterialCandidateStatus(ctx, candidate.ID, "pending", ""); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("only quality decisions should be accepted: %v", err)
	}
	if _, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: keyPoints[0].ID, SourceType: string(models.SourceEpisode), SourceID: "different-source", ChangeKind: "ready", SnapshotHash: "wrong-source"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("material change source must match its KeyPoint: %v", err)
	}
	changes, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: keyPoints[0].ID, SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, ChangeKind: "ready", SnapshotHash: "same-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RecordMaterialChange(ctx, models.MaterialChange{KeyPointID: keyPoints[0].ID, SourceType: string(models.SourceEpisode), SourceID: episodes[0].ID, ChangeKind: "ready", SnapshotHash: "same-snapshot"})
	if err != nil || changes.ID != again.ID {
		t.Fatalf("same substantive snapshot must be idempotent: first=%+v again=%+v err=%v", changes, again, err)
	}
	listed, err := s.ListMaterialCandidates(ctx, models.SourceEpisode, episodes[0].ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("candidate should remain source-scoped learning history: listed=%+v err=%v", listed, err)
	}
}
