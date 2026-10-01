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

type embeddingCheckpoint struct {
	Version          string                    `json:"version"`
	JobID            string                    `json:"job_id"`
	InputFingerprint string                    `json:"input_fingerprint"`
	Result           *provider.EmbeddingResult `json:"result"`
}

// ScheduleKnowledgeEmbeddings creates only locally frozen work for an already
// enabled exact connection. No supplier request is made by the scheduler.
func (w *Worker) ScheduleKnowledgeEmbeddings(ctx context.Context) error {
	p, err := w.selector.Embedding()
	if err != nil {
		return nil
	}
	configs, err := w.store.EnabledKnowledgeEmbeddingConfigs(ctx, p.Config())
	if err != nil {
		return err
	}
	for _, cfg := range configs {
		if _, _, err = w.store.ReserveKnowledgeEmbeddingBatch(ctx, cfg.ID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) doKnowledgeEmbedding(ctx context.Context, job *models.ProcessingJob) error {
	ex, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var in store.KnowledgeEmbeddingJobInput
	if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil || in.Version != store.KnowledgeEmbeddingJobVersion || ex.ConfigVersion != in.Version || (in.Kind != "content" && in.Kind != "preflight" && in.Kind != "query") || in.Config.ID != job.SourceID || in.Config.Provider != ex.ConfiguredProvider || in.Config.Model != ex.ConfiguredModel || in.Estimate == nil {
		return store.ErrInvalidEditorialState
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON)))
	var cp embeddingCheckpoint
	if ex.CheckpointJSON != "" {
		if json.Unmarshal([]byte(ex.CheckpointJSON), &cp) != nil || cp.Version != in.Version || cp.JobID != job.ID || cp.InputFingerprint != fingerprint || cp.Result == nil || cp.Result.Model != in.Config.Model || len(cp.Result.Vectors) != len(in.Inputs()) || (in.Config.Dimensions != 0 && cp.Result.Dimensions != in.Config.Dimensions) {
			return fmt.Errorf("embedding响应断点与冻结输入不一致；保留原记录，需明确恢复")
		}
	} else {
		if ex.RemoteCallStarted {
			if err = w.store.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
				return err
			}
			return fmt.Errorf("embedding远端结果未知；没有自动再次调用，请核对供应商后明确重试")
		}
		if err = w.store.CheckRunControl(ctx, job.ID); err != nil {
			return err
		}
		p, err := w.selector.Embedding()
		if err != nil {
			return err
		}
		original := p.Config()
		if original.ConnectionID != in.Config.ConnectionID || original.Model != in.Config.Model || (original.Dimensions != 0 && original.Dimensions != in.Config.Dimensions) {
			return fmt.Errorf("独立embedding连接或维度已变化，不能替换冻结端点")
		}
		if in.Config.Dimensions != 0 {
			p, err = p.WithDimensions(in.Config.Dimensions)
			if err != nil {
				return err
			}
		}
		if _, err = w.store.HoldEmbeddingBudget(ctx, job.ID, in.Operation(), in.Config, in.Estimate); err != nil {
			return err
		}
		if err = w.store.MarkKnowledgeEmbeddingCallStarted(ctx, job.ID, in); err != nil {
			return err
		}
		result, err := p.Embed(ctx, in.Inputs())
		if err != nil {
			return err
		}
		cp = embeddingCheckpoint{Version: in.Version, JobID: job.ID, InputFingerprint: fingerprint, Result: result}
		raw, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err = w.store.SaveKnowledgeEmbeddingCheckpoint(saveCtx, job.ID, ex.InputSnapshotJSON, string(raw)); err != nil {
			origin := in.OriginJobID
			if origin == "" {
				origin = job.ID
			}
			if receiptErr := w.store.RecordEmbeddingReceipt(saveCtx, origin, in, result); receiptErr != nil {
				return fmt.Errorf("embedding响应与记账均未持久化；未知结果不能自动重发")
			}
			if current, e := w.store.GetJobExecution(saveCtx, job.ID); e == nil && current.InputSnapshotJSON == "{}" {
				_ = w.store.SaveJobResult(saveCtx, job.ID, `{"purged":true}`, models.JobResultComplete)
			}
			return fmt.Errorf("保存embedding响应失败，已保留实际用量；不能自动重发: %w", err)
		}
	}
	if err = provider.ValidateEmbeddingResult(cp.Result, in.Config.Model, len(in.Inputs()), in.Config.Dimensions); err != nil {
		return err
	}
	origin := in.OriginJobID
	if origin == "" {
		origin = job.ID
	}
	if err = w.store.RecordEmbeddingReceipt(ctx, origin, in, cp.Result); err != nil {
		return fmt.Errorf("embedding已知响应记账失败: %w", err)
	}
	if origin != job.ID {
		w.settleJobBudget(ctx, &models.ProcessingJob{ID: origin, JobType: models.JobKnowledgeEmbedding})
	}
	// Applicability and stop/pause checks occur after paid facts are durable.
	return w.store.CommitKnowledgeEmbeddingResponse(ctx, job.ID, in, cp.Result)
}
