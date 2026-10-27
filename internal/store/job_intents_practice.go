// job_intents_practice.go 练习问题任务的幂等入队（K02 学习实践）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
)

// EnqueuePracticeReviewJob 按冻结的当前转录版本入队练习问题生成任务。
// 同一转录版本重复入队命中同一活跃意图，不重复调用模型；显式重新生成用新意图 ID。
func (s *Store) EnqueuePracticeReviewJob(ctx context.Context, sourceType models.SourceType, sourceID string, limit int, intentSuffix string) (*models.ProcessingJob, error) {
	av, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: 尚未完成转录，无法生成练习问题", ErrInvalidEditorialState)
		}
		return nil, err
	}
	snapshot, err := json.Marshal(map[string]any{
		"transcript_version":    av.Version,
		"transcript_version_id": av.ID,
		"limit":                 limit,
	})
	if err != nil {
		return nil, err
	}
	intentID := fmt.Sprintf("practice_review:%s:%s:v%d", sourceType, sourceID, av.Version)
	if intentSuffix != "" {
		intentID += ":" + intentSuffix
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobPracticeReview,
		IntentID: intentID, InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}
