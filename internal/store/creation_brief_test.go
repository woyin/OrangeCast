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
	// C04：解决缺口需要真实已处理来源（转录就绪），字符串占位不再足够。
	podcast, perr := s.CreatePodcast(ctx, "https://feed.example.com/brief-recheck.xml", "研究来源", "", "")
	if perr != nil {
		t.Fatal(perr)
	}
	if _, merr := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "br", Title: "研究单集", AudioURL: "https://a.mp3"}}); merr != nil {
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
	if err := s.ResolveResearchNeed(ctx, need.ID, eps[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCreationBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
}
