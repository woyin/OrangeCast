package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func (w *Worker) doWeeklyReview(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle == nil || bundle.KnowledgeArticle == nil {
		return fmt.Errorf("回顾模型连接不可用")
	}
	execution, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var input store.LearningReviewStageInput
	if json.Unmarshal([]byte(execution.InputSnapshotJSON), &input) != nil || input.BatchID != job.SourceID || input.Request.Stage != "weekly_review" || !provider.KnowledgeArticlePromptSupported(execution.ConfigVersion) {
		return fmt.Errorf("回顾任务冻结输入无效")
	}
	batch, err := w.store.GetLearningReviewBatch(ctx, input.BatchID)
	if err != nil {
		return err
	}
	result, err := w.groundedTextStep(ctx, job, bundle, input.Request, execution, func() error {
		return w.store.CheckKnowledgeMaterials(ctx, batch.ProfileID, execution.ConfiguredProvider, input.Request.Materials)
	})
	if err != nil {
		return err
	}
	return w.store.CommitLearningReview(ctx, job.ID, batch.ID, result)
}
