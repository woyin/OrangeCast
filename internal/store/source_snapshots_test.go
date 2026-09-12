package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// seedSnapshotEpisode 建播客+单集+转录版本+原音。返回 (store, episodeID)。
func seedSnapshotEpisode(t *testing.T, s *Store) string {
	t.Helper()
	podcast, err := s.CreatePodcast(t.Context(), "https://feed.example.com/snap.xml", "快照播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "snap-1", Title: "快照单集", AudioURL: "https://cdn.example.com/s.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, err := s.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("单集 setup: %v", err)
	}
	return eps[0].ID
}

func seedSnapshotTranscript(t *testing.T, s *Store, sourceType models.SourceType, sourceID, segText string) int {
	t.Helper()
	// 乐观锁只允许 unprocessed/failed 入队；重复种子先把状态退回。
	if sourceType == models.SourceEpisode {
		if err := s.UpdateEpisodeStatus(t.Context(), sourceID, models.StatusUnprocessed); err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.EnqueueJob(t.Context(), sourceType, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"language":"zh","text":"t","segments":[{"id":"seg-0001","start":0,"end":10,"text":"` + segText + `"}]}`
	version, err := s.CreateArtifactVersion(t.Context(), sourceType, sourceID, KindTranscript, "fake", "m", "1", job.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	// 结束任务并推进状态，允许同一 Source 再次入队（v2 种子）。
	if _, err := s.MarkJobRunning(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if sourceType == models.SourceEpisode {
		if err := s.UpdateEpisodeStatus(t.Context(), sourceID, models.StatusProcessed); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetCurrentVersion(t.Context(), sourceType, sourceID, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	return version
}

// TestSourceSnapshot_FreezeAudio 冻结记录转录版本、原音哈希与标题。
func TestSourceSnapshot_FreezeAudio(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	v := seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "第一版要点")
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/a.mp3", "mp3", 100, "sha-v1"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != models.SnapshotKindAudio || snap.ContentVersion != v || snap.AudioSHA256 != "sha-v1" || snap.Title != "快照单集" {
		t.Fatalf("快照血缘不正确: %+v", snap)
	}
	if snap.Legacy || snap.Status != models.SnapshotActive {
		t.Fatalf("正常冻结不应是 legacy/异常状态: %+v", snap)
	}
	// 幂等：同版本重复冻结返回同一快照
	again, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil || again.ID != snap.ID {
		t.Fatalf("重复冻结应幂等: %+v %v", again, err)
	}
}

// TestSourceSnapshot_V1ReadsV1AfterV2Current 核心验收：v2 成为 current 后 v1 快照仍读 v1 内容。
func TestSourceSnapshot_V1ReadsV1AfterV2Current(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	v1 := seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "第一版独有表述")
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/a.mp3", "mp3", 100, "sha-v1"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}

	// 生成 v2 并切换 current
	v2 := seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "第二版改写后的表述")
	if err := s.SetCurrentVersion(t.Context(), models.SourceEpisode, epID, KindTranscript, v2); err != nil {
		t.Fatal(err)
	}
	if v2 != v1+1 {
		t.Fatalf("版本应递增: v1=%d v2=%d", v1, v2)
	}

	_, segments, docSegs, err := s.SnapshotContent(t.Context(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docSegs) != 0 {
		t.Fatalf("音频快照不应返回文档段: %+v", docSegs)
	}
	if len(segments) != 1 || segments[0].Text != "第一版独有表述" {
		t.Fatalf("v1 快照应读 v1 内容，实际: %+v", segments)
	}
	// 同 ID 不同版本不是同一证据：v2 的 seg-0001 与 v1 的 seg-0001 分属不同版本内容
	cur, err := s.GetCurrentVersion(t.Context(), models.SourceEpisode, epID, KindTranscript)
	if err != nil || cur.Version != v2 {
		t.Fatalf("current 应为 v2: %v", err)
	}
}

// TestSourceSnapshot_AudioReplaced 原音更新不覆盖历史引用：替换后明确不可回听，不回放新文件。
func TestSourceSnapshot_AudioReplaced(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/a.mp3", "mp3", 100, "sha-old"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.SnapshotAudioIdentity(t.Context(), snap.ID)
	if err != nil || id.Status != models.AudioPlayable {
		t.Fatalf("冻结后应可回听: %+v %v", id, err)
	}
	// 重新处理覆盖原音（UpsertEvidenceAudio 语义）
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/a.mp3", "mp3", 120, "sha-new"); err != nil {
		t.Fatal(err)
	}
	id, err = s.SnapshotAudioIdentity(t.Context(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if id.Status != models.AudioReplaced {
		t.Fatalf("替换后应明确 audio_replaced: %+v", id)
	}
	if id.RelPath != "" {
		t.Fatalf("不可回听时不得返回回放路径: %+v", id)
	}
	if id.FrozenSHA != "sha-old" || id.CurrentSHA != "sha-new" {
		t.Fatalf("应保留冻结与当前哈希身份: %+v", id)
	}
}

// TestSourceSnapshot_LegacyWithoutAudio 无原音身份的旧数据标 legacy，可回听性未知。
func TestSourceSnapshot_LegacyWithoutAudio(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Legacy {
		t.Fatalf("缺原音身份应标 legacy: %+v", snap)
	}
	id, err := s.SnapshotAudioIdentity(t.Context(), snap.ID)
	if err != nil || id.Status != models.AudioIdentityUnknown {
		t.Fatalf("legacy 原音身份应未知: %+v %v", id, err)
	}
}

// TestSourceSnapshot_Document 文档快照绑定精确版本；位置不是音频秒数；新版不改变旧快照。
func TestSourceSnapshot_Document(t *testing.T) {
	s := newTestStore(t)
	doc, err := s.CreatePastedDocument(t.Context(), "设计文档", "第一版段落一。\n\n第一版段落二。")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceDocument, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != models.SnapshotKindDocument || snap.ContentVersion != 1 || snap.AudioSHA256 != "" {
		t.Fatalf("文档快照血缘不正确: %+v", snap)
	}
	if _, audioSegs, docSegs, err := s.SnapshotContent(t.Context(), snap.ID); err != nil || len(audioSegs) != 0 || len(docSegs) != 2 {
		t.Fatalf("文档快照应返回 2 个文档段: %d/%d %v", len(audioSegs), len(docSegs), err)
	}

	// v2 系列新版本：旧快照仍读 v1
	if _, err := s.CreateDocumentVersion(t.Context(), doc.ID, "设计文档", "第二版全部重写。"); err != nil {
		t.Fatal(err)
	}
	_, _, docSegs, err := s.SnapshotContent(t.Context(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docSegs) != 2 || docSegs[0].Text != "第一版段落一。" {
		t.Fatalf("v1 快照不应读到 v2 内容: %+v", docSegs)
	}
	if docSegs[0].Position != 1 || docSegs[1].Position != 2 {
		t.Fatalf("文档段应为段落位置: %+v", docSegs)
	}
}

// TestSourceSnapshot_PurgeInvalidates Purge 后快照明确失效，不静默解析。
func TestSourceSnapshot_PurgeInvalidates(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSourceSnapshotsPurged(t.Context(), models.SourceEpisode, epID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSourceSnapshot(t.Context(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.SnapshotPurged {
		t.Fatalf("Purge 后应标 purged: %+v", got)
	}
	if _, _, _, err := s.SnapshotContent(t.Context(), snap.ID); !errors.Is(err, ErrSnapshotInvalidated) {
		t.Fatalf("Purge 后读取应显式失效: %v", err)
	}
	if _, err := s.SnapshotAudioIdentity(t.Context(), snap.ID); !errors.Is(err, ErrSnapshotInvalidated) {
		t.Fatalf("Purge 后原音读取应显式失效: %v", err)
	}
}

// TestSourceSnapshot_FreezeRequiresVersions 无转录不入队同理：无可冻结版本报显式错误。
func TestSourceSnapshot_FreezeRequiresVersions(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	if _, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("无转录版本应显式拒绝: %v", err)
	}
	if _, err := s.FreezeSourceSnapshot(t.Context(), models.SourceDocument, "missing-doc"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("文档不存在应显式拒绝: %v", err)
	}
	if _, _, _, err := s.SnapshotContent(t.Context(), "no-such-snapshot"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知快照应 404 语义: %v", err)
	}
}

// TestSourceSnapshot_DeleteSourceRowsMarksPurged Purge 事务级联把快照标 purged。
func TestSourceSnapshot_DeleteSourceRowsMarksPurged(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceRows(t.Context(), models.SourceEpisode, epID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSourceSnapshot(t.Context(), snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.SnapshotPurged {
		t.Fatalf("DeleteSourceRows 应级联标 purged: %+v", got)
	}
	// 快照行保留（审计），但版本行已删除 → 读取显式失效而非静默
	if _, _, _, err := s.SnapshotContent(t.Context(), snap.ID); !errors.Is(err, ErrSnapshotInvalidated) {
		t.Fatalf("版本行删除后应显式失效: %v", err)
	}
}

// TestSourceSnapshot_BackupRestorePreservesIdentity 迁移与恢复保留快照身份：
// 备份库中的快照行（含状态、版本血缘、哈希）与源库一致，恢复后读取行为不变。
func TestSourceSnapshot_BackupRestorePreservesIdentity(t *testing.T) {
	s := newTestStore(t)
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	if err := s.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, epID, "evidence/b.mp3", "mp3", 10, "sha-backup"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(t.TempDir(), "snap-backup.db")
	if err := ConsistencyBackup(t.Context(), s.DB, dstPath); err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	dst, err := sql.Open("sqlite", dstPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dst.Close() })

	var id, kind, status, audioSHA, versionID string
	var version int
	if err := dst.QueryRow(`SELECT id, kind, status, audio_sha256, content_version, content_version_id FROM source_snapshots WHERE id=?`,
		snap.ID).Scan(&id, &kind, &status, &audioSHA, &version, &versionID); err != nil {
		t.Fatalf("备份库应保留快照行: %v", err)
	}
	if id != snap.ID || kind != string(models.SnapshotKindAudio) || status != models.SnapshotActive ||
		audioSHA != "sha-backup" || version != snap.ContentVersion || versionID != snap.ContentVersionID {
		t.Fatalf("备份库快照身份不一致: %s/%s/%s/%s/%d", id, kind, status, audioSHA, version)
	}
}

// TestSourceStageStatuses_B09 阶段推导：知识就绪、高光失败带原因、解说等待。
func TestSourceStageStatuses_B09(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	epID := seedSnapshotEpisode(t, s)
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "要点")
	// 卡片就绪
	cardPayload := `{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`
	job, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobAnalyze, IntentID: "an-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, epID, KindKnowledgeCard, "fake", "m", "1", job.ID, cardPayload); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, epID, KindKnowledgeCard, 1); err != nil {
		t.Fatal(err)
	}
	// 高光任务失败（预算原因）
	hlJob, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobHighlight,
		IntentID: "highlight:test:v1", InputSnapshotJSON: `{"transcript_version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, hlJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobFailed(ctx, hlJob.ID, "预算检查拒绝任务: monthly budget exhausted"); err != nil {
		t.Fatal(err)
	}

	stages, err := s.SourceStageStatuses(ctx, models.SourceEpisode, epID)
	if err != nil {
		t.Fatal(err)
	}
	byStage := map[string]*SourceStage{}
	for _, st := range stages {
		byStage[st.Stage] = st
	}
	if byStage["knowledge"] == nil || byStage["knowledge"].Status != "ready" {
		t.Fatalf("知识应就绪: %+v", byStage["knowledge"])
	}
	hl := byStage["highlight"]
	if hl == nil || hl.Status != "failed" || !strings.Contains(hl.Detail, "预算不足") {
		t.Fatalf("高光失败应带预算原因: %+v", hl)
	}
	if hl.LastJobID != hlJob.ID {
		t.Fatalf("失败阶段应能定位任务: %+v", hl)
	}
	na := byStage["narration"]
	if na == nil || na.Status != "waiting" {
		t.Fatalf("解说应等待高光: %+v", na)
	}
}

// TestRetryStageJob_ReusesFrozenInput 重试带回失败任务的冻结输入（不漂移到新 current）。
func TestRetryStageJob_ReusesFrozenInput(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	epID := seedSnapshotEpisode(t, s)
	v1 := seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "第一版")
	failed, _, err := s.EnqueueJobIdempotent(ctx, JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: epID, JobType: models.JobHighlight,
		IntentID: "highlight:retry:v1", InputSnapshotJSON: `{"transcript_version":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkJobRunning(ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobFailed(ctx, failed.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	// 转录前进到 v2，但重试仍用 v1 快照。
	seedSnapshotTranscript(t, s, models.SourceEpisode, epID, "第二版")

	retry, created, err := s.RetryStageJob(ctx, models.SourceEpisode, epID, "highlight")
	if err != nil || !created {
		t.Fatalf("重试应入队: %v %v", created, err)
	}
	exec, _ := s.GetJobExecution(ctx, retry.ID)
	if !strings.Contains(exec.InputSnapshotJSON, `"transcript_version":1`) {
		t.Fatalf("重试应带回冻结输入 v1: %s", exec.InputSnapshotJSON)
	}
	_ = v1
	// 双击重试：命中同一活跃任务，不重复入队。
	retry2, created2, err := s.RetryStageJob(ctx, models.SourceEpisode, epID, "highlight")
	if err != nil || created2 || retry2.ID != retry.ID {
		t.Fatalf("重复重试应幂等: %+v %v %v", retry2, created2, err)
	}
}
