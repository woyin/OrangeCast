package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
)

// EnqueueDigestJob 入队单集精读文生成（ADR-0023 T1 / G01）。
// 前提：音频 Source 已有当前 Transcript 版本；Document 直接可入队。
// 入队时冻结素材身份（B01 来源快照 + 卡片版本 + 选用笔记 ID）到 input_snapshot_json：
// 排队后重分析/改笔记不改变这次输入。幂等（B02）：并发请求命中同一活跃意图
// 返回 nil。
func (s *Store) EnqueueDigestJob(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.ProcessingJob, error) {
	snap, err := s.freezeDigestInput(ctx, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	snapshotJSON, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	job, created, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobDigest,
		IntentID:          fmt.Sprintf("digest:%s:%s", sourceType, sourceID),
		InputSnapshotJSON: string(snapshotJSON),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil // 已有进行中的精读文任务
	}
	return job, nil
}

// DigestInputSnapshot 精读素材的冻结身份（G01）。
type DigestInputSnapshot struct {
	SnapshotID        string   `json:"snapshot_id"` // B01 来源快照 ID
	TranscriptVersion int      `json:"transcript_version,omitempty"`
	CardVersion       int      `json:"card_version,omitempty"`
	NoteIDs           []string `json:"note_ids,omitempty"`
}

// freezeDigestInput 冻结素材身份：音频走转录当前版本快照；Document 走其证据版本。
func (s *Store) freezeDigestInput(ctx context.Context, sourceType models.SourceType, sourceID string) (*DigestInputSnapshot, error) {
	snapshot, err := s.FreezeSourceSnapshot(ctx, sourceType, sourceID)
	if err != nil {
		if errors.Is(err, ErrInvalidEditorialState) && sourceType != models.SourceDocument {
			return nil, fmt.Errorf("该内容尚未完成转录，无法生成精读文")
		}
		return nil, err
	}
	snap := &DigestInputSnapshot{SnapshotID: snapshot.ID}
	if snapshot.Kind == models.SnapshotKindAudio {
		snap.TranscriptVersion = snapshot.ContentVersion
		if cv, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindKnowledgeCard); err == nil {
			snap.CardVersion = cv.Version
		}
	}
	// 冻结入队时已存在的笔记（不含之后新增/修改的）。
	rows, err := s.ListOwnerNotes(ctx, sourceType, sourceID)
	if err == nil {
		for _, n := range rows {
			snap.NoteIDs = append(snap.NoteIDs, n.ID)
		}
	}
	return snap, nil
}

// EnqueueDigestRewriteJob 入队渠道改写独立任务（G06）：
// 输入冻结指定修订与渠道；同修订同渠道活跃期幂等去重。
func (s *Store) EnqueueDigestRewriteJob(ctx context.Context, digestID, channel string) (*models.ProcessingJob, error) {
	d, err := s.GetEpisodeDigest(ctx, digestID)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(map[string]string{"digest_id": d.ID, "digest_version": fmt.Sprintf("%d", d.Version), "channel": channel})
	if err != nil {
		return nil, err
	}
	job, created, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: d.SourceType, SourceID: d.SourceID, JobType: models.JobDigestRewrite,
		IntentID:          fmt.Sprintf("digest_rewrite:%s:%s:v%d", d.ID, channel, d.Version),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil
	}
	return job, nil
}
