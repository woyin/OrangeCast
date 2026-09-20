package store

import (
	"context"
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"sync"
	"testing"
)

func TestCreationBriefCASRevisionAndValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	p, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "T", ProposedClaim: "C", OwnerClaim: "Owner", MaterialIDsJSON: `["kp-a","kp-b"]`})
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
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, 1, models.CreationBriefRevision{OwnerClaim: "Owner", Outline: "outline", MaterialPlanJSON: `{"selected":["kp-a"],"rejected":["kp-b"]}`, CuratorPromptVersion: "curator-v1", CuratorInputSnapshotJSON: `{"provider":"test"}`}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCreationBrief(ctx, b.ID)
	if got.CurrentVersion != 2 || got.Status != "draft" {
		t.Fatalf("pointer %+v", got)
	}
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, 1, models.CreationBriefRevision{OwnerClaim: "Owner", Outline: "stale", MaterialPlanJSON: `{"selected":["kp-a"]}`}); err != ErrCreationBriefVersionConflict {
		t.Fatalf("stale %v", err)
	}
	bad := []models.CreationBriefRevision{{OwnerClaim: "", Outline: "o", MaterialPlanJSON: `{"selected":["kp-a"]}`}, {OwnerClaim: "Owner", Outline: "", MaterialPlanJSON: `{"selected":["kp-a"]}`}, {OwnerClaim: "Owner", Outline: "o", MaterialPlanJSON: `{"selected":[]`}, {OwnerClaim: "Owner", Outline: "o", MaterialPlanJSON: `{"selected":["kp-a"],"rejected":["kp-a"]}`}, {OwnerClaim: "Owner", Outline: "o", MaterialPlanJSON: `{"selected":["x"]}`}}
	for i, v := range bad {
		if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, 2, v); err == nil {
			t.Fatalf("bad %d", i)
		}
	}
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, 2, models.CreationBriefRevision{OwnerClaim: "Owner", Outline: "O2", MaterialPlanJSON: `{"selected":["kp-a"]}`}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetCreationBriefRevision(ctx, b.ID, 3)
	if err != nil || r.CuratorPromptVersion != "curator-v1" || r.CuratorInputSnapshotJSON != `{"provider":"test"}` {
		t.Fatalf("metadata %+v %v", r, err)
	}
}

func makeConfirmFixture(t *testing.T) (*Store, *models.CreationBrief, string, string) {
	s := newTestStore(t)
	t.Helper()
	ctx := context.Background()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	pod, _ := s.CreatePodcast(ctx, "https://r17.example/feed", "R17", "", "")
	s.MergeEpisodes(ctx, pod.ID, []models.Episode{{GUID: "r17", Title: "R17", AudioURL: "https://a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, pod.ID)
	ep := eps[0].ID
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, ep, models.JobTranscribe)
	v, _ := s.CreateArtifactVersion(ctx, models.SourceEpisode, ep, KindTranscript, "test", "test", "1", job.ID, `{"language":"zh","text":"素材","segments":[{"id":"seg-1","start":0,"end":2,"text":"素材"}]}`)
	s.MarkJobRunning(ctx, job.ID)
	s.MarkJobSucceeded(ctx, job.ID)
	s.SetCurrentVersion(ctx, models.SourceEpisode, ep, KindTranscript, v)
	card := &provider.KnowledgeCard{Title: "T", Summary: provider.CitedText{Text: "S", Citations: []string{"seg-1"}}, KeyPoints: []provider.KeyPoint{{Content: "素材", Citations: []string{"seg-1"}}}, Chapters: []provider.Chapter{{Title: "C", Citations: []string{"seg-1"}}}}
	s.IndexKeyPoints(ctx, models.SourceEpisode, ep, "R17", 1, card, []provider.Segment{{ID: "seg-1", Start: 0, End: 2, Text: "素材"}})
	kps, _, _ := s.ListKeyPointsFiltered(ctx, KeyPointFilter{SourceID: ep}, 1, 10)
	kp := kps[0]
	s.SetKeyPointQualityStatus(ctx, kp.ID, models.KeyPointReady)
	ids, _ := json.Marshal([]string{kp.ID})
	p, _ := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "T", ProposedClaim: "C", MaterialIDsJSON: string(ids)})
	s.AcceptCreationProposal(ctx, p.ID, "Owner")
	b, _ := s.CreateCreationBriefDraftFromProposal(ctx, p.ID)
	snap, _ := json.Marshal(map[string]any{"provider": "test", "materials": []map[string]any{{"keyPointId": kp.ID, "sourceType": "episode", "sourceId": ep, "cardVersion": 1}}})
	s.CreateCreationBriefRevisionCAS(ctx, b.ID, 1, models.CreationBriefRevision{OwnerClaim: "Owner", Outline: "Outline", MaterialPlanJSON: `{"selected":["` + kp.ID + `"]}`, CuratorPromptVersion: "curator-v1", CuratorInputSnapshotJSON: string(snap)})
	fresh, _ := s.GetCreationBrief(ctx, b.ID)
	return s, fresh, ep, kp.ID
}

func TestConfirmCreationBriefVersionRechecksCurrentEligibility(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Store, string, string)
		bad   bool
		old   bool
	}{{"valid", func(*Store, string, string) {}, false, false}, {"stale", func(s *Store, e, k string) { s.MarkKeyPointStale(context.Background(), k, "changed") }, true, false}, {"excluded", func(s *Store, e, k string) {
		profile, _ := s.EnsureDefaultEditorialProfile(context.Background())
		s.SetEditorialRelevance(context.Background(), models.EditorialRelevance{EditorialProfileID: profile.ID, KeyPointID: k, Assessment: "relevant", OwnerOverride: "excluded"})
	}, true, false}, {"archived", func(s *Store, e, k string) {
		s.DB.ExecContext(context.Background(), `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, e)
	}, true, false}, {"allowlist deny", func(s *Store, e, k string) {
		s.SetSourceProductionPolicy(context.Background(), models.SourceEpisode, e, "internal", models.ModelDataApprovedProvidersOnly)
		s.SetSourceApprovedProviders(context.Background(), models.SourceEpisode, e, []string{"other"})
	}, true, false}, {"allowlist allow", func(s *Store, e, k string) {
		s.SetSourceProductionPolicy(context.Background(), models.SourceEpisode, e, "internal", models.ModelDataApprovedProvidersOnly)
		s.SetSourceApprovedProviders(context.Background(), models.SourceEpisode, e, []string{"test"})
	}, false, false}, {"local only", func(s *Store, e, k string) {
		s.SetSourceProductionPolicy(context.Background(), models.SourceEpisode, e, "internal", models.ModelDataLocalOnly)
	}, true, false}, {"old", func(*Store, string, string) {}, true, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, b, e, k := makeConfirmFixture(t)
			tc.setup(s, e, k)
			v := b.CurrentVersion
			if tc.old {
				v--
			}
			err := s.ConfirmCreationBriefVersion(context.Background(), b.ID, v)
			if tc.bad {
				if err == nil {
					t.Fatal("expected reject")
				}
			} else if err != nil {
				t.Fatal(err)
			} else {
				g, _ := s.GetCreationBrief(context.Background(), b.ID)
				if g.Status != "confirmed" || g.ConfirmedVersion != g.CurrentVersion {
					t.Fatalf("confirm %+v", g)
				}
			}
		})
	}
	s, b, _, _ := makeConfirmFixture(t)
	p, _ := s.GetCreationProposal(context.Background(), b.CreationProposalID)
	s.CreateResearchNeed(context.Background(), models.ResearchNeed{CreationProposalID: p.ID, Severity: "blocking", Question: "gap"})
	if err := s.ConfirmCreationBriefVersion(context.Background(), b.ID, b.CurrentVersion); err == nil {
		t.Fatal("blocking")
	}
}

// TestGetCreationBrief_FailsWhenCurrentRevisionMissing R17 regression：current 指针
// 指向的不可变 revision 被删除时，读取不得静默回退废弃旧列内容。
func TestGetCreationBrief_FailsWhenCurrentRevisionMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	p, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "T", ProposedClaim: "C", OwnerClaim: "Owner", MaterialIDsJSON: `["x"]`})
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
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM creation_brief_revisions WHERE brief_id=? AND version=?`, b.ID, b.CurrentVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCreationBrief(ctx, b.ID); err == nil {
		t.Fatal("missing current revision must fail")
	}
}

func TestCreateCreationBriefDraftFromProposal_ConcurrentSingleDraft(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile, _ := s.EnsureDefaultEditorialProfile(ctx)
	p, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "T", ProposedClaim: "C", MaterialIDsJSON: `["kp"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCreationProposal(ctx, p.ID, "Owner"); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e := s.CreateCreationBriefDraftFromProposal(ctx, p.ID)
			if e != nil {
				errs <- e
			} else {
				ids <- b.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("draft IDs=%v", seen)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_briefs WHERE creation_proposal_id=?`, p.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("draft rows=%d err=%v", n, err)
	}
}

func TestCreationBrief_EditInvalidatesPriorConfirmation(t *testing.T) {
	s, b, _, _ := makeConfirmFixture(t)
	ctx := context.Background()
	if err := s.ConfirmCreationBriefVersion(ctx, b.ID, b.CurrentVersion); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.GetCreationBrief(ctx, b.ID)
	if err != nil || confirmed.Status != "confirmed" || confirmed.ConfirmedVersion != confirmed.CurrentVersion || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirm fixture: %+v %v", confirmed, err)
	}
	if _, err := s.CreateCreationBriefRevisionCAS(ctx, b.ID, confirmed.CurrentVersion, models.CreationBriefRevision{OwnerClaim: confirmed.OwnerClaim, Outline: "编辑后提纲", MaterialPlanJSON: confirmed.MaterialPlanJSON, ClaimPlanJSON: confirmed.ClaimPlanJSON}); err != nil {
		t.Fatal(err)
	}
	edited, err := s.GetCreationBrief(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Status != "draft" || edited.ConfirmedVersion != 0 || edited.ConfirmedAt != nil {
		t.Fatalf("editing must invalidate authorization: %+v", edited)
	}
}
