package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type curatorBriefFake struct {
	calls  int
	result *provider.CuratorResult
}

func (f *curatorBriefFake) Curate(context.Context, provider.CuratorRequest) (*provider.CuratorResult, error) {
	f.calls++
	return f.result, nil
}
func (f *curatorBriefFake) Name() string { return "fake-curator" }

func curatorFixture(t *testing.T, s *store.Store) (*models.CreationBrief, *models.CreationProposal, string, string) {
	ctx := context.Background()
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ep := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, ep)
	completeSeedJobs(t, s)
	card := &provider.KnowledgeCard{Title: "T", Summary: provider.CitedText{Text: "S", Citations: []string{"seg-0001"}}, KeyPoints: []provider.KeyPoint{{Content: "材料", Citations: []string{"seg-0001"}}}, Chapters: []provider.Chapter{{Title: "C", Citations: []string{"seg-0001"}}}}
	if _, err := s.IndexKeyPoints(ctx, models.SourceEpisode, ep, "Ep", 1, card, []provider.Segment{{ID: "seg-0001", Start: 0, End: 2, Text: "材料"}}); err != nil {
		t.Fatal(err)
	}
	kps, _, _ := s.ListKeyPointsFiltered(ctx, store.KeyPointFilter{SourceID: ep}, 1, 10)
	if len(kps) != 1 {
		t.Fatal("keypoint fixture")
	}
	if err := s.SetKeyPointQualityStatus(ctx, kps[0].ID, models.KeyPointReady); err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal([]string{kps[0].ID})
	p, err := s.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "方向", ProposedClaim: "Owner", OwnerClaim: "Owner", Status: "proposed", MaterialIDsJSON: string(ids)})
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
	return b, p, kps[0].ID, ep
}

func curatorInput(b *models.CreationBrief, p *models.CreationProposal, kpid, sourceID string) string {
	v, _ := json.Marshal(map[string]any{"proposal_id": p.ID, "brief_id": b.ID, "base_version": b.CurrentVersion, "owner_claim": p.OwnerClaim, "title": "方向", "thesis": p.OwnerClaim, "materials": []provider.ArticleMaterial{{SourceType: "episode", CardVersion: 1, KeyPointID: kpid, SourceID: sourceID, Content: "材料", Citations: []string{"seg-0001"}}}, "provider": "fake-curator", "model": "fake", "prompt_version": provider.CuratorPromptVersion})
	return string(v)
}

func TestCuratorBriefJob_NormalProviderAndInvalidResult(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		invalid  bool
		missing  bool
		archived bool
	}{{"normal", false, false, false}, {"overlap", true, false, false}, {"missing source", true, true, false}, {"archived source", true, false, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			b, p, kpid, sourceID := curatorFixture(t, s)
			fake := &curatorBriefFake{result: &provider.CuratorResult{Thesis: "model", Outline: "outline", SelectedKeyPointIDs: []string{kpid}, ClaimType: "synthesis", Style: "clear", Usage: provider.TaskUsage{InputUnits: 5, OutputUnits: 2}}}
			if tc.invalid {
				fake.result.RejectedKeyPointIDs = []string{kpid}
			}
			if tc.archived {
				if _, err := s.DB.ExecContext(ctx, `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, sourceID); err != nil {
					t.Fatal(err)
				}
			}
			w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{Curator: fake}, nil
			}
			input := curatorInput(b, p, kpid, sourceID)
			if tc.missing {
				input = strings.Replace(input, sourceID, "missing-source", 1)
			}
			job, err := s.EnqueueCuratorBriefJob(ctx, p.ID, b.ID, input, "fake-curator", "fake")
			if err != nil {
				t.Fatal(err)
			}
			if err := w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			var n int
			_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_brief_revisions WHERE brief_id=?`, b.ID).Scan(&n)
			got, _ := s.GetJob(ctx, job.ID)
			if tc.invalid {
				if (tc.missing || tc.archived) && fake.calls != 0 {
					t.Fatalf("policy/source invalid must not call provider: %d", fake.calls)
				}
				if got.Status != "failed" || n != 1 {
					t.Fatalf("invalid: job=%+v revisions=%d", got, n)
				}
				return
			}
			if fake.calls != 1 || n != 2 {
				t.Fatalf("normal calls=%d revisions=%d", fake.calls, n)
			}
			rev, err := s.GetCreationBriefRevision(ctx, b.ID, 2)
			if err != nil {
				t.Fatal(err)
			}
			var plan map[string]any
			if err := json.Unmarshal([]byte(rev.ClaimPlanJSON), &plan); err != nil {
				t.Fatal(err)
			}
			if rev.OwnerClaim != p.OwnerClaim || plan["thesis"] != p.OwnerClaim || plan["curator_thesis"] != "model" {
				t.Fatalf("OwnerClaim identity must survive Curator: rev=%+v plan=%+v", rev, plan)
			}
			if got.Status != "succeeded" {
				t.Fatalf("job status=%s", got.Status)
			}
			var out map[string]any
			exec, _ := s.GetJobExecution(ctx, job.ID)
			_ = json.Unmarshal([]byte(exec.ResultJSON), &out)
			if out["version"] != float64(2) {
				t.Fatalf("result version=%v", out)
			}
		})
	}
}
func TestCuratorBriefJob_CheckpointRecoveryIsIdempotent(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	b, p, kpid, sourceID := curatorFixture(t, s)
	fake := &curatorBriefFake{result: &provider.CuratorResult{Thesis: "model", Outline: "outline", SelectedKeyPointIDs: []string{kpid}, Usage: provider.TaskUsage{InputUnits: 20, OutputUnits: 5}}}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Curator: fake}, nil
	}
	job, err := s.EnqueueCuratorBriefJob(ctx, p.ID, b.ID, curatorInput(b, p, kpid, sourceID), "fake-curator", "fake")
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := json.Marshal(map[string]any{"stage": "generated", "result": fake.result, "usage": fake.result.Usage})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cp)); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("checkpoint provider calls=%d", fake.calls)
	}
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_brief_revisions WHERE brief_id=?`, b.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("revisions=%d", n)
	}
	var receipts int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":curator_brief").Scan(&receipts)
	if receipts != 1 {
		t.Fatalf("receipts=%d", receipts)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET status='queued',result_state='',result_json='',lease_until=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("replay provider calls=%d", fake.calls)
	}
	var replayReceipts int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE receipt_id=?`, job.ID+":curator_brief").Scan(&replayReceipts); err != nil {
		t.Fatal(err)
	}
	if replayReceipts != 1 {
		t.Fatalf("replay receipts=%d", replayReceipts)
	}
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(exec.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result["version"] != float64(2) {
		t.Fatalf("replay result version=%v", result["version"])
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creation_brief_revisions WHERE brief_id=?`, b.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("replay revisions=%d", n)
	}
	got, err := s.GetCreationBrief(ctx, b.ID)
	if err != nil || got.CurrentVersion != 2 || got.OwnerClaim != p.OwnerClaim {
		t.Fatalf("brief=%+v err=%v", got, err)
	}
}
