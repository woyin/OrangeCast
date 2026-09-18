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

// DigestFrozenNote 入队时冻结的笔记确切内容（R06）：运行时只消费这份输入，
// 编辑/删除/新增笔记不改变已入队任务。
type DigestFrozenNote struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Content  string   `json:"content"`
	Revision int      `json:"revision"`
	Anchors  []string `json:"anchors,omitempty"` // 引用锚点（citations）
}

// DigestInputSnapshot 精读素材的冻结身份（G01/R06）。
type DigestInputSnapshot struct {
	SnapshotID        string   `json:"snapshot_id"` // B01 来源快照 ID
	TranscriptVersion int      `json:"transcript_version,omitempty"`
	CardVersion       int      `json:"card_version,omitempty"`
	NoteIDs           []string `json:"note_ids,omitempty"` // 旧字段：保留读取兼容
	// NotesRecorded 区分“未记录笔记选择的旧数据”（false，运行时按 NoteIDs 兼容解析）
	// 与“本次确切选择”（true，含空选择），新任务始终为 true。
	NotesRecorded bool               `json:"notes_recorded"`
	Notes         []DigestFrozenNote `json:"notes,omitempty"`
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
	// 冻结入队时已存在笔记的确切内容（R06）：文本、类型、版本与引用锚点；
	// 排队后编辑/删除/新增笔记不改变这次输入。空选择也显式记录。
	rows, err := s.ListOwnerNotes(ctx, sourceType, sourceID)
	if err == nil {
		snap.NotesRecorded = true
		for _, n := range rows {
			var anchors []string
			_ = json.Unmarshal([]byte(n.CitationsJSON), &anchors)
			snap.Notes = append(snap.Notes, DigestFrozenNote{
				ID: n.ID, Kind: n.Kind, Content: n.Content, Revision: n.Revision, Anchors: anchors,
			})
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
