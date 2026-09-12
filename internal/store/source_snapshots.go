// Package store：来源快照（B01 / ADR-0024 §4）。
// 按 Source 及明确版本冻结不可变读取身份：正文 Segment、原音身份（哈希）、标题
// 与版本血缘。读取按冻结版本解析——新版切换不让旧链接静默指向新转录；
// 原音更新不覆盖历史引用依赖的内容（保留身份或明确标为不可回听）；
// 同 ID 不同版本的 Segment 不视为同一证据。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// ErrSnapshotInvalidated 快照已明确失效（Purge / 版本行不存在），调用方不得回退到当前版本。
var ErrSnapshotInvalidated = errors.New("source snapshot invalidated")

// FreezeSourceSnapshot 冻结一个 Source 的当前读取身份，返回快照。
// 音频（Episode/Upload）：转录当前版本 + EvidenceAudio 哈希；原音身份缺失时标 legacy。
// 文档：documents 精确行（series 内版本）。同版本重复冻结幂等返回已有快照。
func (s *Store) FreezeSourceSnapshot(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.SourceSnapshot, error) {
	switch sourceType {
	case models.SourceEpisode, models.SourceUpload:
		return s.freezeAudioSnapshot(ctx, sourceType, sourceID)
	case models.SourceDocument:
		return s.freezeDocumentSnapshot(ctx, sourceID)
	default:
		return nil, fmt.Errorf("%w: 未知 source_type: %s", ErrInvalidEditorialState, sourceType)
	}
}

func (s *Store) freezeAudioSnapshot(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.SourceSnapshot, error) {
	av, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: 尚无可冻结的转录版本", ErrInvalidEditorialState)
		}
		return nil, err
	}
	title := ""
	switch sourceType {
	case models.SourceEpisode:
		if ep, err := s.GetEpisodeByID(ctx, sourceID); err == nil {
			title = ep.Title
		}
	case models.SourceUpload:
		if up, err := s.GetUploadByID(ctx, sourceID); err == nil {
			title = up.OriginalFilename
		}
	}
	audioSHA, legacy, status := "", false, models.SnapshotActive
	if ea, err := s.GetEvidenceAudio(ctx, sourceType, sourceID); err == nil {
		audioSHA = ea.SHA256
		if ea.Status == "missing" {
			status = models.SnapshotMissingAudio
		}
	} else if errors.Is(err, ErrNotFound) {
		// 旧数据缺少原音身份：可回听性不可推定，标 legacy。
		legacy = true
	} else {
		return nil, err
	}
	return s.insertSnapshot(ctx, &models.SourceSnapshot{
		SourceType: sourceType, SourceID: sourceID, Kind: models.SnapshotKindAudio,
		Title: title, ContentVersion: av.Version, ContentVersionID: av.ID,
		AudioSHA256: audioSHA, Legacy: legacy, Status: status,
	})
}

func (s *Store) freezeDocumentSnapshot(ctx context.Context, documentID string) (*models.SourceSnapshot, error) {
	doc, err := s.GetDocument(ctx, documentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: 文档不存在", ErrInvalidEditorialState)
		}
		return nil, err
	}
	return s.insertSnapshot(ctx, &models.SourceSnapshot{
		SourceType: models.SourceDocument, SourceID: doc.ID, Kind: models.SnapshotKindDocument,
		Title: doc.Title, ContentVersion: doc.Version, ContentVersionID: doc.ID,
		Status: models.SnapshotActive,
	})
}

// insertSnapshot 写入快照；同 (source, kind, version) 幂等返回已有行。
func (s *Store) insertSnapshot(ctx context.Context, snap *models.SourceSnapshot) (*models.SourceSnapshot, error) {
	id := snap.ID
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO source_snapshots (id, source_type, source_id, kind, title, content_version, content_version_id, audio_sha256, legacy, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(snap.SourceType), snap.SourceID, string(snap.Kind), snap.Title,
		snap.ContentVersion, snap.ContentVersionID, snap.AudioSHA256, snap.Legacy, snap.Status); err != nil {
		return nil, fmt.Errorf("写入来源快照: %w", err)
	}
	return s.getSourceSnapshotByUniqueKey(ctx, snap.SourceType, snap.SourceID, snap.Kind, snap.ContentVersion)
}

func (s *Store) getSourceSnapshotByUniqueKey(ctx context.Context, sourceType models.SourceType, sourceID string, kind models.SourceSnapshotKind, version int) (*models.SourceSnapshot, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, kind, title, content_version, content_version_id, audio_sha256, legacy, status, created_at
		 FROM source_snapshots WHERE source_type=? AND source_id=? AND kind=? AND content_version=?`,
		string(sourceType), sourceID, string(kind), version)
	return scanSourceSnapshot(row)
}

// GetSourceSnapshot 按 ID 读取快照。
func (s *Store) GetSourceSnapshot(ctx context.Context, id string) (*models.SourceSnapshot, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, source_type, source_id, kind, title, content_version, content_version_id, audio_sha256, legacy, status, created_at
		 FROM source_snapshots WHERE id=?`, id)
	snap, err := scanSourceSnapshot(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	return snap, err
}

// ListSourceSnapshots 列出一个 Source 的全部快照（新→旧）。
func (s *Store) ListSourceSnapshots(ctx context.Context, sourceType models.SourceType, sourceID string) ([]*models.SourceSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_type, source_id, kind, title, content_version, content_version_id, audio_sha256, legacy, status, created_at
		 FROM source_snapshots WHERE source_type=? AND source_id=? ORDER BY created_at DESC, content_version DESC`,
		string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.SourceSnapshot
	for rows.Next() {
		snap, err := scanSourceSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func scanSourceSnapshot(row interface{ Scan(...any) error }) (*models.SourceSnapshot, error) {
	snap := &models.SourceSnapshot{}
	var legacy bool
	err := row.Scan(&snap.ID, &snap.SourceType, &snap.SourceID, &snap.Kind, &snap.Title,
		&snap.ContentVersion, &snap.ContentVersionID, &snap.AudioSHA256, &legacy, &snap.Status, &snap.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	snap.Legacy = legacy
	return snap, nil
}

// SnapshotContent 按快照冻结的确切版本读取正文：v1 链接在 v2 成为 current 后仍读 v1。
// 音频快照返回带起止秒数的 Segment；文档快照返回段落位置（无秒数，二者不互为替代）。
// 快照已 Purge 或版本行不存在时返回 ErrSnapshotInvalidated，绝不回退当前版本。
func (s *Store) SnapshotContent(ctx context.Context, snapshotID string) (*models.SourceSnapshot, []provider.Segment, []models.DocumentSegment, error) {
	snap, err := s.GetSourceSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, nil, nil, err
	}
	if snap.Status == models.SnapshotPurged {
		return nil, nil, nil, fmt.Errorf("%w: 来源已彻底删除，快照 %s 失效", ErrSnapshotInvalidated, snap.ID)
	}
	switch snap.Kind {
	case models.SnapshotKindAudio:
		av, err := s.GetArtifactVersion(ctx, snap.SourceType, snap.SourceID, KindTranscript, snap.ContentVersion)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, nil, nil, fmt.Errorf("%w: 转录版本 %d 不存在", ErrSnapshotInvalidated, snap.ContentVersion)
			}
			return nil, nil, nil, err
		}
		var payload provider.TranscriptPayload
		if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
			return nil, nil, nil, fmt.Errorf("解析快照转录载荷: %w", err)
		}
		return snap, payload.Segments, nil, nil
	case models.SnapshotKindDocument:
		doc, err := s.GetDocument(ctx, snap.ContentVersionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, nil, nil, fmt.Errorf("%w: 文档版本不存在", ErrSnapshotInvalidated)
			}
			return nil, nil, nil, err
		}
		if doc.Version != snap.ContentVersion {
			// documents 行按 ID 寻址；version 不一致说明行身份被破坏，宁可显式失效。
			return nil, nil, nil, fmt.Errorf("%w: 文档版本 %d 与快照 %d 不一致", ErrSnapshotInvalidated, doc.Version, snap.ContentVersion)
		}
		return snap, nil, DocumentSegments(doc), nil
	default:
		return nil, nil, nil, fmt.Errorf("%w: 未知快照类别 %s", ErrInvalidEditorialState, snap.Kind)
	}
}

// SnapshotAudioIdentity 判定快照原音的可回听性：哈希一致才可回放；
// 原音更新不覆盖历史引用依赖的内容——替换后明确返回 audio_replaced，
// 不用新文件冒充旧引用。
func (s *Store) SnapshotAudioIdentity(ctx context.Context, snapshotID string) (*models.SnapshotAudioIdentity, error) {
	snap, err := s.GetSourceSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if snap.Kind != models.SnapshotKindAudio {
		return nil, fmt.Errorf("%w: 文档快照没有原音身份", ErrInvalidEditorialState)
	}
	if snap.Status == models.SnapshotPurged {
		return nil, fmt.Errorf("%w: 来源已彻底删除，快照 %s 失效", ErrSnapshotInvalidated, snap.ID)
	}
	identity := &models.SnapshotAudioIdentity{FrozenSHA: snap.AudioSHA256, Status: models.AudioIdentityUnknown}
	if snap.AudioSHA256 == "" {
		return identity, nil // legacy：身份不可推定
	}
	ea, err := s.GetEvidenceAudio(ctx, snap.SourceType, snap.SourceID)
	if errors.Is(err, ErrNotFound) {
		identity.Status = models.AudioIdentityUnknown
		return identity, nil
	}
	if err != nil {
		return nil, err
	}
	identity.CurrentSHA, identity.Format, identity.SizeBytes = ea.SHA256, ea.Format, ea.SizeBytes
	if ea.SHA256 != snap.AudioSHA256 {
		identity.Status = models.AudioReplaced
		return identity, nil
	}
	identity.Status = models.AudioPlayable
	identity.RelPath = ea.RelPath
	if ea.Status == "missing" {
		identity.Status = models.AudioIdentityUnknown
		identity.RelPath = ""
	}
	return identity, nil
}

// MarkSourceSnapshotsPurged 将一个 Source 的快照标为 purged（明确失效，保留审计行）。
func (s *Store) MarkSourceSnapshotsPurged(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE source_snapshots SET status='purged' WHERE source_type=? AND source_id=? AND status!='purged'`,
		string(sourceType), sourceID)
	return err
}
