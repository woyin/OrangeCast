// claim_writer.go 确认 Brief → v2 Writer 持久任务（C10 / ADR-0024 §6）。
// HTTP 只保存意图，关闭页面不取消后台写作；正文、ClaimMap 与用量原子保存；
// 结果 checkpoint 先于业务提交持久化，重启恢复不重复远端调用；
// 执行只读入队时冻结的强类型输入快照，不重读可变的 Brief/Proposal/KeyPoint。
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// claimWritingCheckpoint 是强类型步骤断点：完整结果 + 用量 + 产生结果的调用身份。
// 恢复时逐字段校验：stage/result 有效性、provider/model/prompt_version 必须与冻结
// 输入及本次实际 Provider 一致；不匹配的 checkpoint 不得被静默当作可用结果。
type claimWritingCheckpoint struct {
	Stage         string                            `json:"stage"`
	Result        *provider.ClaimAwareWritingResult `json:"result"`
	Usage         provider.TaskUsage                `json:"usage"`
	Provider      string                            `json:"provider"`
	Model         string                            `json:"model"`
	PromptVersion string                            `json:"prompt_version"`
}

// claimWriterProvider v2 写作接口（Groq/OpenAI 实现）。
type claimWriterProvider interface {
	WriteArticleWithClaims(ctx context.Context, req provider.ClaimAwareWritingRequest) (*provider.ClaimAwareWritingResult, provider.TaskUsage, error)
	Name() string
}

// doClaimWritingJob 执行 v2 写作（R19）：
//  1. 读取入队冻结的完整输入快照（Brief 精确版本、草稿、材料完整快照、Writer 配置）；
//  2. 逐 frozen material 动态校验 CanUseSourceForPublication + CanSendSourceToProvider
//     （冻结文本不绕过 Owner 撤销/LocalOnly；多来源校验在 do 内完成，全局 sendPolicy
//     不适用——job.SourceID 是 draft 身份，不能当作单一来源做策略判断）；
//  3. checkpoint 恢复：完整 result/usage 已持久时直接复用，不重复 Provider 调用；
//  4. 无 checkpoint：mark remote → 调用 → 校验（非空 title/body、ValidateClaimMap）→
//     先 SaveJobCheckpoint → 记账，再业务落库（SaveClaimWritingOutput 单事务）。
func (w *Worker) doClaimWritingJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	prov, ok := bundle.Writer.(claimWriterProvider)
	if !ok {
		return fmt.Errorf("Writer Provider 不支持 v2 写作")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.ClaimWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		return fmt.Errorf("解析冻结写作输入: %w", err)
	}
	if input.CreationBriefID == "" || input.DraftID == "" || input.OwnerClaim == "" || len(input.Materials) == 0 {
		return fmt.Errorf("写作任务缺少冻结输入快照")
	}
	authorizedIDs := make([]string, 0, len(input.Materials))
	for _, m := range input.Materials {
		authorizedIDs = append(authorizedIDs, m.KeyPointID)
		sourceType := models.SourceType(m.SourceType)
		if sourceType == "" {
			sourceType = models.SourceEpisode // legacy 快照兜底
		}
		usable, err := w.store.CanUseSourceForPublication(ctx, input.ProfileID, sourceType, m.SourceID)
		if err != nil {
			return fmt.Errorf("写作来源可用性查询失败（source_type=%s source_id=%s）: %w", sourceType, m.SourceID, err)
		}
		if !usable {
			return fmt.Errorf("写作来源已归档或不可用（source_type=%s source_id=%s）", sourceType, m.SourceID)
		}
		allowed, err := w.store.CanSendSourceToProvider(ctx, sourceType, m.SourceID, prov.Name())
		if err != nil {
			return fmt.Errorf("写作来源策略查询失败（source_type=%s source_id=%s）: %w", sourceType, m.SourceID, err)
		}
		if !allowed {
			return fmt.Errorf("写作来源策略禁止外发（source_type=%s source_id=%s）", sourceType, m.SourceID)
		}
	}
	req := provider.ClaimAwareWritingRequest{
		Title: input.OwnerClaim, Audience: input.Audience, Outline: input.Outline,
		Style: input.Style, SourceAttribution: "轻量", ConfirmedClaim: input.OwnerClaim,
		TargetLength: input.TargetLength, Materials: input.Materials,
	}
	var result *provider.ClaimAwareWritingResult
	if exec.CheckpointJSON != "" {
		var cp claimWritingCheckpoint
		if json.Unmarshal([]byte(exec.CheckpointJSON), &cp) == nil &&
			cp.Stage == "written" && validClaimWritingResult(cp.Result) &&
			cp.Provider == prov.Name() && cp.Model == input.Model && cp.PromptVersion == input.PromptVersion {
			// 元数据匹配外，还须对结果本体复核 ClaimMap：越权/片段不匹配的
			// checkpoint 不得被复用，按无断点重新调用。
			if len(provider.ValidateClaimMap(cp.Result, req)) == 0 {
				result = cp.Result
				w.recordCallUsage(ctx, job, "claim_writing", prov.Name(), input.Model, cp.Usage)
			}
		}
		// checkpoint 缺失/不完整/身份不匹配/结果非法：不静默复用，按无断点重新调用。
	}
	if result == nil {
		w.markRemoteCallStarted(ctx, job)
		r, usage, err := prov.WriteArticleWithClaims(ctx, req)
		if err != nil {
			return fmt.Errorf("v2 写作: %w", err)
		}
		if !validClaimWritingResult(r) {
			return fmt.Errorf("v2 写作返回空标题、正文或 ClaimMap")
		}
		if errs := provider.ValidateClaimMap(r, req); len(errs) > 0 {
			return fmt.Errorf("ClaimMap 校验失败: %v", errs)
		}
		result = r
		// 响应先缓存：checkpoint 持久化后业务提交才可推进；崩溃恢复复用缓存。
		checkpoint, marshalErr := json.Marshal(claimWritingCheckpoint{
			Stage: "written", Result: result, Usage: usage,
			Provider: prov.Name(), Model: input.Model, PromptVersion: input.PromptVersion,
		})
		if marshalErr != nil {
			return fmt.Errorf("序列化写作 checkpoint: %w", marshalErr)
		}
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
		w.recordCallUsage(ctx, job, "claim_writing", prov.Name(), input.Model, usage)
	}
	if _, err := w.store.SaveClaimWritingOutput(ctx, store.ClaimWritingOutput{
		DraftID: input.DraftID, JobID: job.ID,
		CreationBriefID: input.CreationBriefID, BriefVersion: input.BriefVersion,
		CreationArticleLinkID: input.CreationArticleLinkID,
		ArticleProposalID:     input.ArticleProposalID, ArticleBriefID: input.ArticleBriefID,
		OwnerClaim: input.OwnerClaim, Title: result.Title, Markdown: result.Markdown,
		ProviderName: prov.Name(), ModelName: input.Model, PromptVersion: input.PromptVersion,
		AuthorizedIDs: authorizedIDs, Entries: convertClaimMap(result.ClaimMap),
	}); err != nil {
		return fmt.Errorf("保存写作修订: %w", err)
	}
	return nil
}

// validClaimWritingResult 校验输出最小契约：非 nil、标题/正文非空、ClaimMap 非空。
func validClaimWritingResult(r *provider.ClaimAwareWritingResult) bool {
	return r != nil && strings.TrimSpace(r.Title) != "" && strings.TrimSpace(r.Markdown) != "" && len(r.ClaimMap) > 0
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

// revisionWritingCheckpoint 是 AI 修订的强类型步骤断点（R21）：绑定 base revision
// 与完整 request hash；复用前校验全部绑定字段 + 结果契约，篡改不静默复用。
type revisionWritingCheckpoint struct {
	Stage          string                            `json:"stage"`
	Result         *provider.ClaimAwareWritingResult `json:"result"`
	Usage          provider.TaskUsage                `json:"usage"`
	Provider       string                            `json:"provider"`
	Model          string                            `json:"model"`
	PromptVersion  string                            `json:"prompt_version"`
	BaseRevisionID string                            `json:"base_revision_id"`
	RequestHash    string                            `json:"request_hash"`
}

// claimRevisionWriterProvider claim-aware AI 修订接口（R21）：
// 公开能力为 provider.ClaimAwareRevisionWriter，queue 本地别名使用。
type claimRevisionWriterProvider = provider.ClaimAwareRevisionWriter

// doClaimRevisionJob 执行 durable AI 修订（R21）：只读冻结快照 → 逐冻结来源重验
// publication/send policy → checkpoint 恢复或远端调用 → 校验 → CAS 原子落库。
func (w *Worker) doClaimRevisionJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	prov, ok := bundle.Writer.(claimRevisionWriterProvider)
	if !ok {
		return fmt.Errorf("Writer Provider 不支持 claim-aware AI 修订")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.RevisionWritingTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		return fmt.Errorf("解析冻结 AI 修订输入: %w", err)
	}
	if input.Kind != "ai_revision" || input.BaseRevisionID == "" || input.ExistingMarkdown == "" || len(input.Materials) == 0 {
		return fmt.Errorf("AI 修订任务缺少冻结输入快照")
	}
	// 逐冻结来源动态重验策略（R04：冻结文本不绕过 Owner 撤销/LocalOnly）。
	for _, m := range input.Materials {
		if err := reviewMaterialPolicy(ctx, w, store.ReviewTaskInput{ProfileID: input.ProfileID, Materials: input.Materials}, m, prov.Name(), "AI 修订"); err != nil {
			return err
		}
	}
	req := provider.ClaimAwareWritingRequest{
		Title: input.Title, Audience: input.Audience, Outline: input.Outline,
		Style: input.Style, SourceAttribution: "轻量", ConfirmedClaim: input.OwnerClaim,
		TargetLength: input.TargetLength, Materials: input.Materials,
		ExistingMarkdown: input.ExistingMarkdown, RevisionFeedback: input.ReviewFeedback,
		ExistingClaimMap: entriesToProvider(input.ExistingClaimMap),
	}
	requestHash := reviewRequestHash(req)
	var result *provider.ClaimAwareWritingResult
	if exec.CheckpointJSON != "" {
		var cp revisionWritingCheckpoint
		if json.Unmarshal([]byte(exec.CheckpointJSON), &cp) == nil &&
			cp.Stage == "written" && cp.Provider == prov.Name() &&
			cp.Model == input.Model && cp.PromptVersion == input.PromptVersion &&
			cp.BaseRevisionID == input.BaseRevisionID && cp.RequestHash == requestHash {
			// 绑定匹配外，还须复核结果本体（专用修订校验，与保存同契约）。
			if validClaimWritingResult(cp.Result) && provider.ValidateClaimRevisionMap(cp.Result, req) == nil {
				result = cp.Result
				w.recordCallUsage(ctx, job, "claim_revision", prov.Name(), input.Model, cp.Usage)
			}
		}
		// checkpoint 缺失/不完整/身份不匹配/结果非法：不静默复用，按无断点重新调用。
	}
	if result == nil {
		w.markRemoteCallStarted(ctx, job)
		r, usage, err := prov.WriteArticleRevisionWithClaims(ctx, req)
		if err != nil {
			return fmt.Errorf("claim-aware AI 修订: %w", err)
		}
		if !validClaimWritingResult(r) {
			return fmt.Errorf("claim-aware AI 修订返回空标题、正文或 ClaimMap")
		}
		if err := provider.ValidateClaimRevisionMap(r, req); err != nil {
			return fmt.Errorf("AI 修订校验失败: %w", err)
		}
		result = r
		checkpoint, marshalErr := json.Marshal(revisionWritingCheckpoint{
			Stage: "written", Result: result, Usage: usage,
			Provider: prov.Name(), Model: input.Model, PromptVersion: input.PromptVersion,
			BaseRevisionID: input.BaseRevisionID, RequestHash: requestHash,
		})
		if marshalErr != nil {
			return fmt.Errorf("序列化 AI 修订 checkpoint: %w", marshalErr)
		}
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
		w.recordCallUsage(ctx, job, "claim_revision", prov.Name(), input.Model, usage)
	}
	if _, err := w.store.SaveClaimRevisionOutput(ctx, job.ID, result, prov.Name(), input.Model); err != nil {
		return fmt.Errorf("保存 AI 修订: %w", err)
	}
	return nil
}
