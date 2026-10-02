package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type rerankCheckpoint struct {
	JobID, Fingerprint string
	Result             *provider.RerankResult
}

func (w *Worker) doKnowledgeRerank(ctx context.Context, job *models.ProcessingJob) error {
	ex, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var in store.KnowledgeRerankInput
	if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil || in.Version != "knowledge-rerank-v1" || ex.ConfigVersion != in.Version || in.Config.ID != job.SourceID || ex.ConfiguredProvider != in.Config.Provider || ex.ConfiguredModel != in.Config.Model || in.Estimate == nil {
		return store.ErrInvalidEditorialState
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(ex.InputSnapshotJSON)))
	var cp rerankCheckpoint
	if ex.CheckpointJSON != "" {
		if json.Unmarshal([]byte(ex.CheckpointJSON), &cp) != nil || cp.JobID != job.ID || cp.Fingerprint != hash || cp.Result == nil {
			return store.ErrInvalidEditorialState
		}
	} else {
		if ex.RemoteCallStarted {
			if err = w.store.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
				return err
			}
			return errors.New("重排远端结果未知；保留任务，不自动再次计费")
		}
		p, e := w.selector.Reranker()
		if e != nil {
			return e
		}
		if p.Config() != in.Config {
			return store.ErrConflict
		}
		if err = w.store.HoldRerankBudget(ctx, job.ID, in); err != nil {
			return err
		}
		docs, e := w.store.StartKnowledgeRerank(ctx, job.ID, in)
		if e != nil {
			return e
		}
		result, e := p.Rerank(ctx, in.Request.Search.Text, docs)
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e != nil {
			var re *provider.RerankResponseError
			if errors.As(e, &re) {
				if err = w.store.RecordRerankReceipt(saveCtx, job.ID, in, re.Receipt); err != nil {
					return err
				}
			}
			if err = w.store.SaveJobResult(saveCtx, job.ID, "", models.JobResultUnknown); err != nil {
				return err
			}
			return e
		}
		cp = rerankCheckpoint{JobID: job.ID, Fingerprint: hash, Result: result}
		raw, _ := json.Marshal(cp)
		if err = w.store.SaveJobCheckpoint(saveCtx, job.ID, string(raw)); err != nil {
			if receiptErr := w.store.RecordRerankReceipt(saveCtx, job.ID, in, result); receiptErr != nil {
				return errors.New("重排响应与回执未持久化，不能自动重发")
			}
			return errors.New("重排响应断点保存失败，已保留实际用量，不能自动重发")
		}
	}
	if err = w.store.RecordRerankReceipt(ctx, job.ID, in, cp.Result); err != nil {
		return err
	}
	return w.store.CommitKnowledgeRerank(ctx, job.ID, in, cp.Result)
}
