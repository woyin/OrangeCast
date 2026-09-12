// claim_writer.go 确认 Brief → v2 Writer 持久任务（C10 / ADR-0024 §6）。
// HTTP 只保存意图，关闭页面不取消后台写作；正文、ClaimMap 与用量原子保存；
// 结果缓存后崩溃恢复不重复产出。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// EnqueueClaimWritingJob 入队 v2 写作任务（C10）：HTTP 只保存意图。
func (w *Worker) EnqueueClaimWritingJob(ctx context.Context, draftID, briefID string) (*models.ProcessingJob, error) {
	snapshot, _ := json.Marshal(map[string]string{"draft_id": draftID, "brief_id": briefID})
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: draftID, JobType: models.JobClaimWriting,
		IntentID:          fmt.Sprintf("claim_write:%s:%s", draftID, briefID),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil
	}
	return job, nil
}

// claimWriterProvider v2 写作接口（Groq/OpenAI 实现）。
type claimWriterProvider interface {
	WriteArticleWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error)
	Name() string
}

// doClaimWritingJob 执行 v2 写作：从 Brief/Proposal 冻结素材 → compose →
// ClaimMap 校验（provider 内置）→ SaveClaimMap + SaveJobResult 原子保存。
func (w *Worker) doClaimWritingJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	prov, ok := bundle.Analysis.(claimWriterProvider)

	if !ok {
		return fmt.Errorf("Analysis Provider 不支持 v2 写作")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var snapshot struct {
		DraftID string `json:"draft_id"`
		BriefID string `json:"brief_id"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.BriefID == "" {
		return fmt.Errorf("任务缺少 Brief 快照")
	}
	brief, err := w.store.GetCreationBrief(ctx, snapshot.BriefID)
	if err != nil {
		return fmt.Errorf("读取 Brief: %w", err)
	}
	proposal, err := w.store.GetCreationProposal(ctx, brief.CreationProposalID)
	if err != nil {
		return fmt.Errorf("读取 Proposal: %w", err)
	}
	var materialIDs []string
	_ = json.Unmarshal([]byte(proposal.MaterialIDsJSON), &materialIDs)
	var materials []provider.ArticleMaterial
	for _, id := range materialIDs {
		if kp, err := w.store.GetKeyPoint(ctx, id); err == nil {
			materials = append(materials, provider.ArticleMaterial{
				KeyPointID: kp.ID, SourceTitle: kp.SourceTitle, Content: kp.Content,
			})
		}
	}
	req := provider.ClaimAwareWritingRequest{
		Title:             brief.OwnerClaim,
		Audience:          proposal.Audience,
		ConfirmedClaim:    brief.OwnerClaim,
		SourceAttribution: "轻量",
		Materials:         materials,
	}
	result, usage, err := prov.WriteArticleWithClaims(ctx, req)
	if err != nil {
		return fmt.Errorf("v2 写作: %w", err)
	}
	if err := w.store.SaveClaimMap(ctx, snapshot.DraftID, snapshot.DraftID, convertClaimMap(result.ClaimMap)); err != nil {
		return err
	}
	w.recordCallUsage(ctx, job, "claim_writing", prov.Name(), "", usage)
	resultJSON, _ := json.Marshal(map[string]any{"title": result.Title, "markdown_length": len(result.Markdown)})
	return w.store.SaveJobResult(ctx, job.ID, string(resultJSON), models.JobResultComplete)
}

func convertClaimMap(entries []provider.ClaimMapEntry) []models.ClaimMapEntry {
	out := make([]models.ClaimMapEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, models.ClaimMapEntry{
			Excerpt: e.Excerpt, ClaimKind: e.ClaimKind,
			MaterialIDs: e.MaterialIDs, SourceTitle: e.SourceTitle, CitationRefs: e.CitationRefs,
		})
	}
	return out
}
