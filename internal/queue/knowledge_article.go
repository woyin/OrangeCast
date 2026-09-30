package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type knowledgeCheckpoint struct {
	OriginJobID string                           `json:"origin_job_id"`
	InputHash   string                           `json:"input_hash"`
	Provider    string                           `json:"provider"`
	Model       string                           `json:"model"`
	Version     string                           `json:"version"`
	Result      *provider.KnowledgeArticleResult `json:"result"`
	Usage       provider.TaskUsage               `json:"usage"`
}

func (w *Worker) doKnowledgeArticle(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle == nil || bundle.KnowledgeArticle == nil {
		return fmt.Errorf("自动文章模型连接不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.KnowledgeStageInput
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &input); err != nil {
		return err
	}
	if input.ArticleID != job.SourceID || input.Stage != input.Request.Stage || !provider.KnowledgeArticlePromptSupported(exec.ConfigVersion) {
		return fmt.Errorf("自动文章任务快照或提示词版本无效")
	}
	article, err := w.store.GetKnowledgeArticle(ctx, input.ArticleID)
	if err != nil {
		return err
	}
	if err := w.store.CheckKnowledgeMaterials(ctx, article.ProfileID, exec.ConfiguredProvider, input.Request.Materials); err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(exec.InputSnapshotJSON)))
	cp := knowledgeCheckpoint{}
	if exec.CheckpointJSON != "" {
		if err := json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil {
			return fmt.Errorf("自动文章断点损坏")
		}
		if cp.InputHash != hash || cp.Provider != exec.ConfiguredProvider || cp.Model != exec.ConfiguredModel || cp.Version != exec.ConfigVersion || cp.Result == nil {
			return fmt.Errorf("自动文章断点与冻结配置不匹配")
		}
	}
	if cp.Result == nil && (article.Stage != input.Stage || (input.ExpectedRevision != nil && article.WorkingRevision != *input.ExpectedRevision)) {
		return w.store.SaveJobResult(ctx, job.ID, `{"superseded":true}`, models.JobResultComplete)
	}
	if cp.Result == nil {
		if exec.RemoteCallStarted {
			if err := w.store.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
				return err
			}
			return fmt.Errorf("远端结果未知，已停止自动重放；可以显式重试此阶段")
		}
		if err := w.store.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
			return err
		}
		result, usage, err := bundle.KnowledgeArticle.KnowledgeArticleStep(ctx, input.Request)
		if err != nil || result == nil {
			// A malformed response can still contain billable token usage.
			// Record that known usage even though no draft can be persisted.
			if usage.InputUnits > 0 || usage.OutputUnits > 0 {
				if receiptErr := w.recordKnowledgeUsage(ctx, job, input.Stage, knowledgeCheckpoint{Provider: exec.ConfiguredProvider, Model: exec.ConfiguredModel, Usage: usage}); receiptErr != nil {
					return fmt.Errorf("模型响应失败且用量记录失败: %w", receiptErr)
				}
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("模型返回空结果")
		}
		cp = knowledgeCheckpoint{OriginJobID: job.ID, InputHash: hash, Provider: exec.ConfiguredProvider, Model: exec.ConfiguredModel, Version: exec.ConfigVersion, Result: result, Usage: usage}
		checkpoint, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		if err := w.store.SaveJobCheckpoint(ctx, job.ID, string(checkpoint)); err != nil {
			return err
		}
	}
	if err := w.recordKnowledgeUsage(ctx, job, input.Stage, cp); err != nil {
		return err
	}
	if err := provider.ValidateKnowledgeResult(input.Request, cp.Result); err != nil {
		return err
	}
	return w.store.CommitKnowledgeStage(ctx, job, input, cp.Result)
}

func (w *Worker) recordKnowledgeUsage(ctx context.Context, job *models.ProcessingJob, stage string, cp knowledgeCheckpoint) error {
	origin := cp.OriginJobID
	if origin == "" {
		origin = job.ID
	}
	receipt := origin + ":knowledge_article_" + stage
	cost, known := int64(0), false
	if cp.Usage.InputUnits > 0 || cp.Usage.OutputUnits > 0 {
		var err error
		cost, known, err = w.store.ResolveUsageCost(ctx, cp.Provider, cp.Model, cp.Usage.InputUnits, cp.Usage.OutputUnits)
		if err != nil {
			return err
		}
	}
	if err := w.store.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: receipt, AttemptID: origin, Operation: "knowledge_article_" + stage, Provider: cp.Provider, Model: cp.Model, InputUnits: cp.Usage.InputUnits, OutputUnits: cp.Usage.OutputUnits, CostCents: cost, CostKnown: known}); err != nil {
		return err
	}
	if origin != job.ID {
		old := *job
		old.ID = origin
		w.settleJobBudget(ctx, &old)
	}
	return nil
}
