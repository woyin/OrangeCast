package queue

import (
	"context"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func (w *Worker) doKnowledgeUpdateAnalysis(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle, exec *models.ProcessingJobExecution, input store.KnowledgeStageInput, article *store.KnowledgeArticleRecord) error {
	p, err := w.store.GetKnowledgeUpdateProposal(ctx, input.UpdateProposalID)
	if err != nil {
		return err
	}
	if p.ArticleID != article.ID || input.Request.Update == nil || input.Request.Update.ProposalID != p.ID || p.ParentRevision != input.Request.Update.ParentRevision || p.ParentHash != input.Request.Update.ParentHash {
		return fmt.Errorf("更新提案任务的冻结身份不匹配")
	}
	if exec.CheckpointJSON == "" && article.WorkingRevision != p.ParentRevision {
		if err = w.store.MarkKnowledgeUpdateParentChanged(ctx, p.ID); err != nil {
			return err
		}
		return w.store.SaveJobResult(ctx, job.ID, `{"superseded":true}`, models.JobResultComplete)
	}
	result, err := w.groundedTextStep(ctx, job, bundle, input.Request, exec, func() error { return w.store.CheckKnowledgeUpdateExecution(ctx, p, input.Request, job.Automated) })
	if err != nil {
		return err
	}
	return w.store.CommitKnowledgeUpdateAnalysis(ctx, job, input, result)
}
