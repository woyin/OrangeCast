package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
)

// EnqueueDigestJob 入队单集精读文生成（ADR-0023 T1）。
// 前提：该 Source 已有当前 Transcript 版本（无转录无素材）。
// 幂等（B02）：按意图 digest:{type}:{id} 以部分唯一索引做并发 CAS——重复/并发
// 请求命中同一活跃意图时返回 nil（不产生重复任务）。
func (s *Store) EnqueueDigestJob(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ProcessingJob, error) {
	if sourceType != models.SourceDocument {
		if _, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("该内容尚未完成转录，无法生成精读文")
			}
			return nil, err
		}
	}
	job, created, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobDigest,
		IntentID: fmt.Sprintf("digest:%s:%s", sourceType, sourceID),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil // 已有进行中的精读文任务
	}
	return job, nil
}
