// style_review.go 独立风格审校持久任务（R20）。
// 新契约文章的 StyleReview 走 durable worker：入队冻结（store.ReviewTaskInput）
// 精确 revision 正文、画像风格约束、目标篇幅、provider/model/prompt；执行只消费
// 快照，逐冻结 material 动态校验外发策略（正文可能衍生自来源内容），checkpoint
// 先于业务落库，SaveStyleReviewOutput 单事务原子保存 + origin 幂等。
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// styleReviewCheckpoint 是强类型步骤断点（语义与 claim review checkpoint 相同）。
// RevisionID + RequestHash（完整 provider request JSON 的 sha256）绑定冻结输入；
// 复用前必须同时校验 revision、hash、stage、provider/model/prompt 与结论自洽。
type styleReviewCheckpoint struct {
	Stage         string                      `json:"stage"`
	Result        *provider.StyleReviewResult `json:"result"`
	Usage         provider.TaskUsage          `json:"usage"`
	Provider      string                      `json:"provider"`
	Model         string                      `json:"model"`
	PromptVersion string                      `json:"prompt_version"`
	RevisionID    string                      `json:"revision_id"`
	RequestHash   string                      `json:"request_hash"`
}

// doStyleReviewJob 执行独立风格审校（R20）。
func (w *Worker) doStyleReviewJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle == nil || bundle.StyleEditor == nil {
		return fmt.Errorf("StyleEditor Provider 不可用")
	}
	prov := bundle.StyleEditor
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.ReviewTaskInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		return fmt.Errorf("解析冻结风格审校输入: %w", err)
	}
	if input.Kind != store.ReviewKindStyle || input.RevisionID == "" || input.Markdown == "" {
		return fmt.Errorf("风格审校任务缺少冻结输入快照")
	}

	// 逐冻结 material 动态校验来源策略（正文衍生自来源内容，冻结不绕过撤销）。
	for _, m := range input.Materials {
		if err := reviewMaterialPolicy(ctx, w, input, m, prov.Name(), "风格审校"); err != nil {
			return err
		}
	}
	if err := frozenNotesPolicy(ctx, w, input.ProfileID, input.OwnerNotes, prov.Name(), "风格审校"); err != nil {
		return err
	}

	req := provider.StyleReviewRequest{
		Title: input.Title, Markdown: input.Markdown,
		TargetAudience: input.TargetAudience, Voice: input.Voice,
		StyleGuide: input.StyleGuide, TargetLength: input.TargetLength,
	}
	requestHash := reviewRequestHash(req)
	var result *provider.StyleReviewResult
	if exec.CheckpointJSON != "" {
		var cp styleReviewCheckpoint
		if json.Unmarshal([]byte(exec.CheckpointJSON), &cp) == nil &&
			cp.Stage == "reviewed" && cp.Provider == prov.Name() &&
			cp.Model == input.Model && cp.PromptVersion == input.PromptVersion &&
			cp.RevisionID == input.RevisionID && cp.RequestHash == requestHash {
			// 绑定匹配外，还须复核结论自洽（passed 无 issues、failed 有）。
			if styleResultSelfConsistent(cp.Result) {
				result = cp.Result
				w.recordCallUsage(ctx, job, "style_review", prov.Name(), input.Model, cp.Usage)
			}
		}
		// checkpoint 缺失/不完整/身份不匹配/指纹不一致：不静默复用。
	}
	if result == nil {
		w.markRemoteCallStarted(ctx, job)
		r, err := prov.ReviewStyle(ctx, req)
		if err != nil {
			return fmt.Errorf("独立风格审校: %w", err)
		}
		// 远端返回先校验（nil/非法 status 不 panic、不 checkpoint、不落库）。
		if !styleResultSelfConsistent(r) {
			return fmt.Errorf("风格审校返回非法结论")
		}
		result = r
		checkpoint, marshalErr := json.Marshal(styleReviewCheckpoint{
			Stage: "reviewed", Result: result, Usage: result.Usage,
			Provider: prov.Name(), Model: input.Model, PromptVersion: input.PromptVersion,
			RevisionID: input.RevisionID, RequestHash: requestHash,
		})
		if marshalErr != nil {
			return fmt.Errorf("序列化风格审校 checkpoint: %w", marshalErr)
		}
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
		w.recordCallUsage(ctx, job, "style_review", prov.Name(), input.Model, result.Usage)
	}
	if _, err := w.store.SaveStyleReviewOutput(ctx, job.ID, result, prov.Name(), input.Model); err != nil {
		return fmt.Errorf("保存风格审校: %w", err)
	}
	return nil
}

// styleResultSelfConsistent checkpoint 结果自洽复核：passed 必须无 issues、
// failed 必须有 issues（与 SaveStyleReviewOutput 校验同契约）。
func styleResultSelfConsistent(result *provider.StyleReviewResult) bool {
	if result == nil {
		return false
	}
	switch result.Status {
	case "passed":
		return len(result.Issues) == 0
	case "failed":
		return len(result.Issues) > 0
	default:
		return false
	}
}
