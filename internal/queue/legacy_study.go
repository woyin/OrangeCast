package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// One stage retains one paid response identity. A durable response is restored
// before resolving any live connection, and never replayed through the supplier.
func (w *Worker) doLegacyStudy(ctx context.Context, job *models.ProcessingJob) (err error) {
	defer func() {
		if err != nil {
			_ = w.store.FailLegacyStudy(context.WithoutCancel(ctx), job.ID)
		}
	}()
	execution, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var in store.LegacyStudyJobInput
	if json.Unmarshal([]byte(execution.InputSnapshotJSON), &in) != nil || in.Version != store.LegacyStudyTaskVersion || execution.ConfigVersion != in.Version || (in.Stage != "generate" && in.Stage != "review") || in.SessionID != job.SourceID || execution.ConfiguredProvider != in.Config.Provider || execution.ConfiguredModel != in.Model() || in.Estimate == nil {
		return store.ErrInvalidEditorialState
	}
	estimate, err := provider.EstimateLegacyStudy(in.Scope, in.Stage, in.Answer)
	if err != nil || estimate.InputFingerprint != in.Estimate.InputFingerprint || estimate.Method != in.Estimate.Method || estimate.InputTokens != in.Estimate.InputTokens || estimate.OutputTokens != in.Estimate.OutputTokens {
		return store.ErrInvalidEditorialState
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(execution.InputSnapshotJSON)))
	var checkpoint store.QuestionStudyCheckpoint
	if execution.CheckpointJSON != "" {
		if json.Unmarshal([]byte(execution.CheckpointJSON), &checkpoint) != nil || checkpoint.Version != in.Version || checkpoint.JobID != job.ID || checkpoint.InputHash != hash || checkpoint.Response == nil {
			return fmt.Errorf("旧学习对话断点与冻结输入不匹配，需要核对")
		}
	} else {
		if execution.RemoteCallStarted {
			if err = w.store.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
				return err
			}
			return fmt.Errorf("旧学习对话远端结果未知，未自动再次调用；需明确核对")
		}
		client, err := w.selector.LegacyStudy(provider.TaskConfig{Provider: in.Config.Provider, Model: in.Config.GenerationModel})
		if err != nil {
			return err
		}
		if client.Config() != in.Config {
			return fmt.Errorf("旧学习对话连接已变化，不能替换冻结端点")
		}
		if _, err = w.store.HoldKnowledgeBudget(ctx, job.ID, in.Operation(), false, in.Config.Provider, in.Model(), in.Estimate); err != nil {
			return err
		}
		if err = w.store.MarkLegacyStudyCallStarted(ctx, job.ID, in); err != nil {
			return err
		}
		var response *provider.QuestionStudyResponse
		if in.Stage == "review" {
			response, err = client.Review(ctx, in.Scope, *in.Answer)
		} else {
			response, err = client.Generate(ctx, in.Scope)
		}
		if err != nil {
			return err
		}
		checkpoint = store.QuestionStudyCheckpoint{Version: in.Version, JobID: job.ID, InputHash: hash, Response: response}
		raw, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err = w.store.SaveLegacyStudyCheckpoint(saveCtx, job.ID, execution.InputSnapshotJSON, string(raw)); err != nil {
			origin := in.OriginJobID
			if origin == "" {
				origin = job.ID
			}
			if receiptErr := w.store.RecordLegacyStudyReceipt(saveCtx, origin, in, response); receiptErr != nil {
				return fmt.Errorf("旧学习对话响应及实际用量未持久化；未知结果不能自动重发")
			}
			return fmt.Errorf("旧学习对话响应保存失败，已保留实际用量；不能自动重发")
		}
	}
	origin := in.OriginJobID
	if origin == "" {
		origin = job.ID
	}
	if err = w.store.RecordLegacyStudyReceipt(ctx, origin, in, checkpoint.Response); err != nil {
		return fmt.Errorf("旧学习对话已知响应记账失败: %w", err)
	}
	if origin != job.ID {
		w.settleJobBudget(ctx, &models.ProcessingJob{ID: origin, JobType: models.JobQuestionStudy})
	}
	if checkpoint.Response.Failure != "" || checkpoint.Response.UnverifiedModel || checkpoint.Response.Model != in.Model() {
		return fmt.Errorf("旧学习对话响应不合格，已保留实际用量；未采用为回答")
	}
	if in.Stage == "review" {
		review, err := provider.ParseLegacyStudyReview(checkpoint.Response.Content)
		if err != nil {
			return err
		}
		return w.store.CommitLegacyStudyReview(ctx, job.ID, in, review)
	}
	answer, err := provider.ParseLegacyStudyAnswer(in.Scope, checkpoint.Response.Content)
	if err != nil {
		return err
	}
	return w.store.CommitLegacyStudyGeneration(ctx, job.ID, in, answer)
}
