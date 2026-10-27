// job_intents_translation.go 对齐译文任务的幂等入队（Translation）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// EnqueueTranslateJob 按冻结的当前转录版本与目标语言入队翻译任务。
// 同一版本同一语言重复入队命中同一活跃意图，不重复计费。
func (s *Store) EnqueueTranslateJob(ctx context.Context, sourceType models.SourceType, sourceID, targetLanguage string, intentSuffix string) (*models.ProcessingJob, error) {
	lang := strings.TrimSpace(targetLanguage)
	if lang == "" {
		return nil, fmt.Errorf("%w: 翻译必须指定目标语言", ErrInvalidEditorialState)
	}
	av, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: 尚未完成转录，无法翻译", ErrInvalidEditorialState)
		}
		return nil, err
	}
	snapshot, err := json.Marshal(map[string]any{
		"transcript_version":    av.Version,
		"transcript_version_id": av.ID,
		"target_language":       lang,
	})
	if err != nil {
		return nil, err
	}
	intentID := fmt.Sprintf("translate:%s:%s:v%d:%s", sourceType, sourceID, av.Version, lang)
	if intentSuffix != "" {
		intentID += ":" + intentSuffix
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobTranslate,
		IntentID: intentID, InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}
