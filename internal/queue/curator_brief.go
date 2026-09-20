package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// doCuratorBriefJob executes the durable R17 Curator contract from its frozen proposal/material snapshot.
func (w *Worker) doCuratorBriefJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.Curator == nil {
		return fmt.Errorf("Curator Provider 不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input struct {
		ProposalID    string                     `json:"proposal_id"`
		BriefID       string                     `json:"brief_id"`
		BaseVersion   int                        `json:"base_version"`
		OwnerClaim    string                     `json:"owner_claim"`
		ProfileID     string                     `json:"profile_id"`
		Thesis        string                     `json:"thesis"`
		Title         string                     `json:"title"`
		Audience      string                     `json:"audience"`
		Voice         string                     `json:"voice"`
		Materials     []provider.ArticleMaterial `json:"materials"`
		Provider      string                     `json:"provider"`
		Model         string                     `json:"model"`
		PromptVersion string                     `json:"prompt_version"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil || input.BriefID == "" || input.ProposalID == "" {
		return fmt.Errorf("Curator 任务缺少冻结方案快照")
	}
	// Policy is dynamic: frozen text does not bypass Owner revocation.
	for _, m := range input.Materials {
		sourceType := models.SourceType(m.SourceType)
		if sourceType == "" {
			sourceType = models.SourceEpisode
		} // legacy snapshots
		usable, err := w.store.CanUseSourceForPublication(ctx, input.ProfileID, sourceType, m.SourceID)
		if err != nil {
			return fmt.Errorf("Curator 来源可用性查询失败（source_type=%s source_id=%s）: %w", sourceType, m.SourceID, err)
		}
		if !usable {
			return fmt.Errorf("Curator 来源已归档或不可用（source_type=%s source_id=%s）", sourceType, m.SourceID)
		}
		allowed, err := w.store.CanSendSourceToProvider(ctx, sourceType, m.SourceID, bundle.Curator.Name())
		if err != nil {
			return fmt.Errorf("Curator 材料来源策略查询失败（source_type=%s source_id=%s）: %w", sourceType, m.SourceID, err)
		}
		if !allowed {
			return fmt.Errorf("Curator 材料来源策略禁止外发（source_type=%s source_id=%s）", sourceType, m.SourceID)
		}
	}
	req := provider.CuratorRequest{Title: input.Title, Thesis: input.Thesis, Audience: input.Audience, Voice: input.Voice, Materials: input.Materials}
	var result *provider.CuratorResult
	if exec.CheckpointJSON != "" {
		var checkpoint struct {
			Result *provider.CuratorResult `json:"result"`
			Usage  provider.TaskUsage      `json:"usage"`
		}
		if json.Unmarshal([]byte(exec.CheckpointJSON), &checkpoint) == nil && checkpoint.Result != nil {
			result = checkpoint.Result
			w.recordCallUsage(ctx, job, "curator_brief", bundle.Curator.Name(), input.Model, checkpoint.Usage)
		}
	}
	if result == nil {
		w.markRemoteCallStarted(ctx, job)
		result, err = bundle.Curator.Curate(ctx, req)
		if err != nil {
			return fmt.Errorf("Curator: %w", err)
		}
		if result == nil {
			return fmt.Errorf("Curator 返回空结果")
		}
		checkpoint, _ := json.Marshal(map[string]any{"stage": "generated", "result": result, "usage": result.Usage})
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
		w.recordCallUsage(ctx, job, "curator_brief", bundle.Curator.Name(), input.Model, result.Usage)
	}
	valid, err := provider.ValidateCuratorResult(result, req)
	if err != nil {
		return fmt.Errorf("Curator 输出校验失败: %w", err)
	}
	result = valid
	claimPlan, _ := json.Marshal(map[string]any{
		// OwnerClaim is the authorization identity; model thesis is advisory metadata.
		"thesis": input.OwnerClaim, "curator_thesis": result.Thesis,
		"audience": result.Audience, "claim_type": result.ClaimType,
		"unresolved_questions": result.UnresolvedQuestions,
	})
	materialPlan, _ := json.Marshal(map[string]any{
		"selected": result.SelectedKeyPointIDs, "rejected": result.RejectedKeyPointIDs,
		"conflicts": result.ConflictPlan,
	})
	questions, _ := json.Marshal(result.UnresolvedQuestions)
	revision, err := w.store.ApplyCuratorResultRevision(ctx, input.BriefID, job.ID, input.BaseVersion, models.CreationBriefRevision{
		OwnerClaim: input.OwnerClaim, ClaimPlanJSON: string(claimPlan), MaterialPlanJSON: string(materialPlan),
		Outline: result.Outline, Style: result.Style, TargetLength: result.TargetLength,
		ClaimType: result.ClaimType, UnresolvedQuestionsJSON: string(questions),
		Notes: result.Notes, CuratorPromptVersion: input.PromptVersion,
		CuratorInputSnapshotJSON: exec.InputSnapshotJSON, OriginJobID: job.ID,
	})
	if err != nil {
		return fmt.Errorf("保存 Curator Brief 修订: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"brief_id": input.BriefID, "version": revision.Version})
	return w.store.SaveJobResult(ctx, job.ID, string(payload), models.JobResultComplete)
}
