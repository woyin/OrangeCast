// claim_review.go 独立主张审校持久任务（R20 / C11 / ADR-0024 §6）。
// 入队冻结见 store.ReviewTaskInput：worker 只读该强类型快照（精确 revision 正文、
// ClaimMap、confirmed claim、授权材料原文），不重读可变正文/Brief/Profile。
//
// 执行契约（与 R19 claim writer 同级）：
//   - 逐冻结 material 动态校验 CanUseSourceForPublication + CanSendSourceToProvider
//     （冻结文本不绕过 Owner 撤销/LocalOnly）；
//   - checkpoint 先于业务落库持久化（强类型 result+usage+provider/model/prompt），
//     重启/业务事务失败后复用且不重复调用 Provider；checkpoint 元数据或结果校验
//     不通过时不静默复用；
//   - SaveClaimReviewOutput 单事务原子保存审校 + job complete result（origin 幂等）。
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// claimReviewCheckpoint 是强类型步骤断点：完整结果 + 用量 + 产生结果的调用身份。
// RevisionID + RequestHash（完整 provider request JSON 的 sha256）把 checkpoint
// 绑定到该次冻结输入：passed 结果无 findings、单靠结果校验无法证明归属，
// 复用前必须同时校验 revision、hash、stage、provider/model/prompt 与结果契约；
// 跨任务/被篡改的 checkpoint 一律不得放行。
type claimReviewCheckpoint struct {
	Stage         string                      `json:"stage"`
	Result        *provider.ClaimReviewResult `json:"result"`
	Usage         provider.TaskUsage          `json:"usage"`
	Provider      string                      `json:"provider"`
	Model         string                      `json:"model"`
	PromptVersion string                      `json:"prompt_version"`
	RevisionID    string                      `json:"revision_id"`
	RequestHash   string                      `json:"request_hash"`
}

// reviewRequestHash 对 provider request 的 JSON 序列化做 sha256（结构体字段序
// 固定，序列化确定）。
func reviewRequestHash(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// claimReviewerProvider 独立审校接口（Groq/OpenAI 实现）。
type claimReviewerProvider interface {
	ReviewClaims(ctx context.Context, req provider.ClaimReviewRequest) (*provider.ClaimReviewResult, provider.TaskUsage, error)
	Name() string
}

// doClaimReviewJob 执行独立主张审校（R20）：读冻结快照 → 逐 material 策略校验 →
// checkpoint 恢复或远端调用 → 校验结果 → 原子落库。
func (w *Worker) doClaimReviewJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle == nil || bundle.ClaimReviewer == nil {
		return fmt.Errorf("ClaimReviewer Provider 不可用")
	}
	prov, ok := bundle.ClaimReviewer.(claimReviewerProvider)
	if !ok {
		return fmt.Errorf("ClaimReviewer Provider 不支持独立审校")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.ReviewTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		return fmt.Errorf("解析冻结审校输入: %w", err)
	}
	if input.Kind != store.ReviewKindClaim || input.RevisionID == "" || input.Markdown == "" {
		return fmt.Errorf("审校任务缺少冻结输入快照")
	}
	if len(input.ClaimMap) == 0 || len(input.Materials) == 0 {
		return fmt.Errorf("审校任务冻结输入缺少 ClaimMap 或授权材料")
	}

	// 逐冻结 material 动态校验来源可用性与外发策略（R04：冻结不绕过撤销）。
	for _, m := range input.Materials {
		if err := reviewMaterialPolicy(ctx, w, input, m, prov.Name(), "主张审校"); err != nil {
			return err
		}
	}

	req := provider.ClaimReviewRequest{
		Markdown: input.Markdown, ClaimMap: entriesToProvider(input.ClaimMap),
		ConfirmedClaim: input.ConfirmedClaim,
		AuthorizedIDs:  input.AuthorizedIDs, Materials: input.Materials,
	}
	requestHash := reviewRequestHash(req)
	var result *provider.ClaimReviewResult
	if exec.CheckpointJSON != "" {
		var cp claimReviewCheckpoint
		if json.Unmarshal([]byte(exec.CheckpointJSON), &cp) == nil &&
			cp.Stage == "reviewed" && cp.Provider == prov.Name() &&
			cp.Model == input.Model && cp.PromptVersion == input.PromptVersion &&
			cp.RevisionID == input.RevisionID && cp.RequestHash == requestHash {
			// 绑定匹配外，还须对结果本体对照冻结输入复核；越权/片段不匹配的
			// checkpoint 不得被复用，按无断点重新调用。
			if provider.ValidateClaimReviewAgainstInput(cp.Result, &req) == nil {
				result = cp.Result
				w.recordCallUsage(ctx, job, "claim_review", prov.Name(), input.Model, cp.Usage)
			}
		}
		// checkpoint 缺失/不完整/身份不匹配/结果非法：不静默复用，按无断点重新调用。
	}
	if result == nil {
		w.markRemoteCallStarted(ctx, job)
		r, usage, err := prov.ReviewClaims(ctx, req)
		if err != nil {
			return fmt.Errorf("独立主张审校: %w", err)
		}
		if err := provider.ValidateClaimReviewAgainstInput(r, &req); err != nil {
			return fmt.Errorf("主张审校结果校验失败: %w", err)
		}
		result = r
		// 响应先缓存：checkpoint 持久化后业务提交才可推进；崩溃恢复复用缓存。
		checkpoint, marshalErr := json.Marshal(claimReviewCheckpoint{
			Stage: "reviewed", Result: result, Usage: usage,
			Provider: prov.Name(), Model: input.Model, PromptVersion: input.PromptVersion,
			RevisionID: input.RevisionID, RequestHash: requestHash,
		})
		if marshalErr != nil {
			return fmt.Errorf("序列化主张审校 checkpoint: %w", marshalErr)
		}
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
		w.recordCallUsage(ctx, job, "claim_review", prov.Name(), input.Model, usage)
	}
	if _, err := w.store.SaveClaimReviewOutput(ctx, job.ID, result, prov.Name(), input.Model); err != nil {
		return fmt.Errorf("保存主张审校: %w", err)
	}
	return nil
}

// entriesToProvider models→provider ClaimMap 条目转换。
func entriesToProvider(entries []models.ClaimMapEntry) []provider.ClaimMapEntry {
	out := make([]provider.ClaimMapEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, provider.ClaimMapEntry{
			Excerpt: e.Excerpt, ClaimKind: e.ClaimKind,
			MaterialIDs: e.MaterialIDs, SourceTitle: e.SourceTitle, CitationRefs: e.CitationRefs,
		})
	}
	return out
}

// reviewMaterialPolicy 逐冻结材料的动态策略校验（publication + send policy）。
func reviewMaterialPolicy(ctx context.Context, w *Worker, input store.ReviewTaskInput, m provider.ArticleMaterial, providerName, label string) error {
	sourceType := models.SourceType(m.SourceType)
	if sourceType == "" {
		sourceType = models.SourceEpisode // legacy 快照兜底
	}
	usable, err := w.store.CanUseSourceForPublication(ctx, input.ProfileID, sourceType, m.SourceID)
	if err != nil {
		return fmt.Errorf("%s 来源可用性查询失败（source_type=%s source_id=%s）: %w", label, sourceType, m.SourceID, err)
	}
	if !usable {
		return fmt.Errorf("%s 来源已归档或不可用（source_type=%s source_id=%s）", label, sourceType, m.SourceID)
	}
	allowed, err := w.store.CanSendSourceToProvider(ctx, sourceType, m.SourceID, providerName)
	if err != nil {
		return fmt.Errorf("%s 来源策略查询失败（source_type=%s source_id=%s）: %w", label, sourceType, m.SourceID, err)
	}
	if !allowed {
		return fmt.Errorf("%s 来源策略禁止外发（source_type=%s source_id=%s）", label, sourceType, m.SourceID)
	}
	return nil
}
