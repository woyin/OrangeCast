package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// seedCuratorRevisionForTest applies a realistic Curator result from the frozen
// durable job snapshot, producing v2 before HTTP edit/confirm tests.
func seedCuratorRevisionForTest(t *testing.T, srv *Server, brief *models.CreationBrief) *models.CreationBrief {
	t.Helper()
	ctx := context.Background()
	jobs, err := srv.store.ListQueuedOrRunning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var job *models.ProcessingJob
	for _, candidate := range jobs {
		if candidate.JobType == models.JobCuratorBrief {
			job = candidate
			break
		}
	}
	if job == nil {
		t.Fatal("missing curator job for test revision")
	}
	exec, err := srv.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		OwnerClaim string `json:"owner_claim"`
		Prompt     string `json:"prompt_version"`
		Materials  []struct {
			SourceType, KeyPointID, SourceID string
			CardVersion                      int
		} `json:"materials"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		t.Fatal(err)
	}
	selected := make([]string, 0, len(input.Materials))
	for _, m := range input.Materials {
		selected = append(selected, m.KeyPointID)
	}
	plan, _ := json.Marshal(map[string]any{"selected": selected, "rejected": []string{}})
	if _, err := srv.store.ApplyCuratorResultRevision(ctx, brief.ID, job.ID+":test-seed", brief.CurrentVersion, models.CreationBriefRevision{
		OwnerClaim: input.OwnerClaim, ClaimPlanJSON: `{"claim_type":"synthesis"}`, MaterialPlanJSON: string(plan),
		Outline: "Curator 提纲", Style: "清晰", TargetLength: intPtrHelper(900), ClaimType: "synthesis", UnresolvedQuestionsJSON: `["待核对问题"]`, Notes: "Curator 备注",
		CuratorPromptVersion: input.Prompt, CuratorInputSnapshotJSON: exec.InputSnapshotJSON,
	}); err != nil {
		t.Fatal(err)
	}
	fresh, err := srv.store.GetCreationBrief(ctx, brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

func intPtrHelper(v int) *int { return &v }
