package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// newTestWorker 构造临时 store + worker（evidence/tmp 独立目录）。
func newTestWorker(t *testing.T) (*store.Store, *Worker) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	sel := provider.NewSelector("fake-groq", "fake-openai")
	w := NewWorker(s, sel, filepath.Join(dir, "tmp"), filepath.Join(dir, "evidence"))
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)
	os.MkdirAll(filepath.Join(dir, "evidence"), 0o755)
	return s, w
}

func seedEpisode(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	p, err := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	if err != nil {
		t.Fatalf("创建播客: %v", err)
	}
	if _, err := s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "e", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatalf("合并单集: %v", err)
	}
	eps, err := s.ListEpisodes(ctx, p.ID)
	if err != nil || len(eps) == 0 {
		t.Fatalf("列出单集: %v %v", eps, err)
	}
	return eps[0].ID
}

func TestStartup_RecoversStuckRunningJob(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

	// 模拟崩溃：job 处于 running 且租约过期（旧进程已死）
	s.MarkJobRunning(ctx, job.ID)
	// 制造过期租约
	s.DB.Exec(`UPDATE processing_jobs SET lease_until=datetime('now','-10 minutes') WHERE id=?`, job.ID)

	// 启动恢复：running → queued
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusQueued {
		t.Fatalf("启动恢复后应 queued，实际 %s", got.Status)
	}
}

func TestProcessClaimed_ShutdownLeavesJobForStartupRecovery(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	job, err := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextJob(ctx, leaseDuration)
	if err != nil || claimed == nil {
		t.Fatalf("领取任务: %v %v", claimed, err)
	}

	// 模拟应用关闭：处理被 context cancellation 打断，而不是业务失败。
	injectFakeProviders(t, w, context.Canceled, nil)
	shutdownCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := w.processClaimed(shutdownCtx, claimed); err != nil {
		t.Fatalf("正常关闭不应把中断作为 worker 错误返回: %v", err)
	}

	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusRunning {
		t.Fatalf("关闭中断后 job 应保留 running 供重启恢复，实际 %s", got.Status)
	}
	if got.AttemptCount != 0 {
		t.Fatalf("关闭中断不应计为失败尝试，实际 %d", got.AttemptCount)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusQueued {
		t.Errorf("重启恢复后 job 应 queued，实际 %s", got.Status)
	}
}

func TestClaimNextJob_OnlyOneWorkerWins(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)

	// 两个 worker 并发领取同一 job → 只有一个成功
	w2 := NewWorker(s, provider.NewSelector("g", "o"), w.tempDir, w.evidenceDir)
	done := make(chan *models.ProcessingJob, 2)
	run := func(ww *Worker) {
		job, err := ww.store.ClaimNextJob(ctx, leaseDuration)
		if err != nil {
			done <- nil
			return
		}
		done <- job
	}
	go run(w)
	go run(w2)
	var claimed int
	for i := 0; i < 2; i++ {
		if j := <-done; j != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Errorf("并发领取应恰好 1 个成功，实际 %d", claimed)
	}
}

func TestClaimNextJob_ReclaimsExpiredLease(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)
	s.DB.Exec(`UPDATE processing_jobs SET lease_until=datetime('now','-10 minutes') WHERE id=?`, job.ID)

	claimed, err := w.store.ClaimNextJob(ctx, leaseDuration)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("租约过期的 running 任务应被回收领取")
	}
	if claimed.ID != job.ID {
		t.Errorf("应领取同一 job，实际 %s != %s", claimed.ID, job.ID)
	}
	// 已被领取（running + 新租约）
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusRunning {
		t.Errorf("领取后应 running，实际 %s", got.Status)
	}
}

func TestEvidenceAudio_TranscodeAndIdempotentReuse(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 不可用")
	}
	s, w := newTestWorker(t)
	ctx := context.Background()
	// 用 upload 源（不需要外网下载）
	up, err := s.CreateUpload(ctx, "input.wav", "audio/wav", 100)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := up.ID

	// 构造一个最小的真实音频输入（ffmpeg 生成 0.2s 静音）作为 upload 原始文件
	raw := filepath.Join(w.tempDir, "input.wav")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.2", "-f", "wav", raw)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 无法生成测试音频: %v %s", err, out)
	}
	os.MkdirAll(filepath.Join(w.tempDir, "uploads"), 0o755)
	if err := os.Rename(raw, filepath.Join(w.tempDir, "uploads", sourceID)); err != nil {
		t.Fatal(err)
	}

	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: sourceID, JobType: models.JobTranscribe}

	// 第一次：生成证据
	path1, err := w.ensureEvidence(ctx, job)
	if err != nil {
		t.Fatalf("ensureEvidence: %v", err)
	}
	ev, err := s.GetEvidenceAudio(ctx, models.SourceUpload, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != "ready" || ev.SHA256 == "" || ev.SizeBytes == 0 {
		t.Errorf("evidence 记录不完整: %+v", ev)
	}
	fi1, _ := os.Stat(path1)
	if fi1.Size() == 0 {
		t.Error("证据文件不应为空")
	}
	// 原始输入在证据落盘后应被删除（ADR-0005）
	if _, err := os.Stat(filepath.Join(w.tempDir, "uploads", sourceID)); !os.IsNotExist(err) {
		t.Error("证据持久化后原始上传文件应被删除")
	}

	// 第二次：应复用（幂等，不重复生成）
	path2, err := w.ensureEvidence(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if path1 != path2 {
		t.Error("幂等调用应返回同一证据文件")
	}
	ev2, _ := s.GetEvidenceAudio(ctx, models.SourceUpload, sourceID)
	if ev2.SHA256 != ev.SHA256 {
		t.Error("重复 ensureEvidence 不应改变证据哈希")
	}
}

func TestEvidenceAudio_OversizeExistingFileIsNotReused(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 不可用")
	}
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "input.mp3", "audio/mpeg", 10)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(w.evidenceDir, 0o755)
	path := filepath.Join(w.evidenceDir, "upload_"+up.ID+".mp3")
	if err := os.WriteFile(path, []byte("old evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxTranscriptionUploadBytes+1); err != nil {
		t.Fatal(err)
	}
	sha, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(ctx, models.SourceUpload, up.ID, filepath.Base(path), "mp3", maxTranscriptionUploadBytes+1, sha); err != nil {
		t.Fatal(err)
	}

	// 提供一个真实且很短的原始输入。若 oversize 文件被错误复用，结果仍会大于预算。
	rawDir := filepath.Join(w.tempDir, "uploads")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(rawDir, up.ID)
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.2", "-f", "wav", raw)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成原始音频: %v %s", err, out)
	}
	if _, err := w.ensureEvidence(ctx, &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > maxTranscriptionUploadBytes {
		t.Fatal("超预算的旧 EvidenceAudio 应重新转码")
	}
}

func TestEvidenceBitrateKbps_StaysWithinGroqUploadBudget(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		want     int
	}{
		{name: "one hour preserves 48kbps speech quality", duration: 60 * 60, want: 48},
		{name: "65 minute episode avoids 413", duration: 64*60 + 55, want: 40},
		{name: "104 minute episode adapts further", duration: 104 * 60, want: 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evidenceBitrateKbps(tt.duration)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("evidenceBitrateKbps(%.0f)=%d, want %d", tt.duration, got, tt.want)
			}
			predictedBytes := int64(float64(got*1000) * tt.duration / 8)
			if predictedBytes > maxTranscriptionUploadBytes {
				t.Errorf("预估 %d bytes 超过上传预算 %d", predictedBytes, maxTranscriptionUploadBytes)
			}
		})
	}
}

func TestEvidenceBitrateKbps_RejectsAudioNeedingSegmentation(t *testing.T) {
	if _, err := evidenceBitrateKbps(float64(maxTranscriptionUploadBytes*8) / float64(minEvidenceBitrateKbps*1000) * 1.01); err == nil {
		t.Fatal("16kbps 仍超上传预算的超长音频应要求分段转录")
	}
}

func TestPurgeSource_RemovesFilesAndRows(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	// 造一些证据与关联数据（现行模型：artifact_versions + evidence）
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "groq", "m", "1", job.ID, `{"text":"hello"}`)
	s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "groq", "m", "1", job.ID, `{"title":"T"}`)
	s.UpsertEvidenceAudio(ctx, models.SourceEpisode, sourceID, "episode_"+sourceID+".mp3", "mp3", 123, "abc")
	os.MkdirAll(w.evidenceDir, 0o755)
	os.WriteFile(filepath.Join(w.evidenceDir, "episode_"+sourceID+".mp3"), []byte("audio"), 0o644)

	if err := w.PurgeSource(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatalf("PurgeSource: %v", err)
	}
	// 文件删除
	if _, err := os.Stat(filepath.Join(w.evidenceDir, "episode_"+sourceID+".mp3")); !os.IsNotExist(err) {
		t.Error("证据文件应被删除")
	}
	// DB 行删除
	if _, err := s.GetEpisodeByID(ctx, sourceID); err != store.ErrNotFound {
		t.Error("episode 应被删除")
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE source_id=?`, sourceID).Scan(&n)
	if n != 0 {
		t.Errorf("artifact_versions 应被删除，剩余 %d", n)
	}
	if _, err := s.GetEvidenceAudio(ctx, models.SourceEpisode, sourceID); err != store.ErrNotFound {
		t.Error("evidence_audio 应被删除")
	}
	// purge 记录完成
	purges, _ := s.ListPendingPurges(ctx)
	if len(purges) != 0 {
		t.Errorf("pending purge 应为空，实际 %d", len(purges))
	}
}

func TestPurgeSource_ResumeAfterCrash(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)

	// 只记录 intent（模拟崩溃在"删除文件前"）
	s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID)
	os.MkdirAll(w.evidenceDir, 0o755)
	os.WriteFile(filepath.Join(w.evidenceDir, "episode_"+sourceID+".mp3"), []byte("audio"), 0o644)

	// 重启恢复 → 完成删除
	if err := w.ResumePurges(ctx); err != nil {
		t.Fatalf("ResumePurges: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.evidenceDir, "episode_"+sourceID+".mp3")); !os.IsNotExist(err) {
		t.Error("崩溃恢复后证据文件应被删除")
	}
	if _, err := s.GetEpisodeByID(ctx, sourceID); err != store.ErrNotFound {
		t.Error("崩溃恢复后 episode 应被删除")
	}
}

func TestProcessOne_NoJob_Noop(t *testing.T) {
	_, w := newTestWorker(t)
	if err := w.ProcessOne(context.Background()); err != nil {
		t.Fatalf("空队列 ProcessOne 不应报错: %v", err)
	}
}

func TestWorkerHeartbeat_ExtendsLease(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobTranscribe)
	s.MarkJobRunning(ctx, job.ID)

	// 给一个即将过期的租约，心跳后应延长
	s.DB.Exec(`UPDATE processing_jobs SET lease_until=datetime('now','+5 seconds') WHERE id=?`, job.ID)
	if err := s.HeartbeatJob(ctx, job.ID, "60 seconds"); err != nil {
		t.Fatal(err)
	}
	var until string
	s.DB.QueryRow(`SELECT lease_until FROM processing_jobs WHERE id=?`, job.ID).Scan(&until)
	parsed, _ := time.Parse("2006-01-02 15:04:05", until)
	if parsed.Before(time.Now().Add(30 * time.Second)) {
		t.Errorf("心跳后租约应延长到未来，实际 %s", until)
	}
}

// seedEvidence 生成真实微小 mp3 并登记 evidence_audio，让 pipeline 测试跳过 ffmpeg 转码。
func seedEvidence(t *testing.T, s *store.Store, w *Worker, sourceType models.SourceType, sourceID string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 不可用")
	}
	os.MkdirAll(w.evidenceDir, 0o755)
	rel := fmt.Sprintf("%s_%s.mp3", sourceType, sourceID)
	path := filepath.Join(w.evidenceDir, rel)
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.2", "-f", "mp3", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 生成证据失败: %v %s", err, out)
	}
	fi, _ := os.Stat(path)
	sha, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvidenceAudio(context.Background(), sourceType, sourceID, rel, "mp3", fi.Size(), sha); err != nil {
		t.Fatal(err)
	}
}

// ---- fake providers（避免真实网络调用，验证完整处理管道）----

type fakeTranscriber struct{ err error }

func (f *fakeTranscriber) Transcribe(filePath string) (*provider.TranscriptResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &provider.TranscriptResult{
		Language: "en",
		Text:     "hello world",
		Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 1, Text: "hello world"}},
	}, nil
}
func (f *fakeTranscriber) Name() string { return "fake" }

type fakeAnalyzer struct{ err error }

func (f *fakeAnalyzer) Analyze(transcript string, segments []provider.Segment) (*provider.KnowledgeCard, error) {
	if f.err != nil {
		return nil, f.err
	}
	cites := []string{}
	if len(segments) > 0 {
		cites = []string{segments[0].ID}
	}
	// 金句逐字摘自首个片段，确保通过证据校验
	quoteText := ""
	if len(segments) > 0 {
		quoteText = segments[0].Text
	}
	return &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: cites},
		KeyPoints: []provider.KeyPoint{{Content: "KP", Description: "D", Citations: cites}},
		Chapters:  []provider.Chapter{{Title: "CH", Gist: "G", Citations: cites}},
		Quotes:    []provider.Quote{{Text: quoteText, Citations: cites}},
		Tags:      []string{"t"},
	}, nil
}
func (f *fakeAnalyzer) Name() string { return "fake" }

type fakeQA struct{}

func (f *fakeQA) Answer(question string, segments []provider.Segment) (*provider.QAResult, error) {
	return &provider.QAResult{Answer: "A"}, nil
}
func (f *fakeQA) Name() string { return "fake" }

func injectFakeProviders(t *testing.T, w *Worker, tc, an error) {
	t.Helper()
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Transcription: &fakeTranscriber{err: tc},
			Analysis:      &fakeAnalyzer{err: an},
			QA:            &fakeQA{},
		}, nil
	}
}

// TestPipeline_TranscribeThenAnalyze 验证完整管道：转录成功 → 自动入队分析 → 分析成功。
// 同时验证不产生重复产物（transcript/analysis 各一行）。
func TestPipeline_TranscribeThenAnalyze(t *testing.T) {
	s, w := newTestWorker(t)
	injectFakeProviders(t, w, nil, nil)
	ctx := context.Background()
	// upload 源 + 已就绪的 EvidenceAudio（跳过转码，专注管道）
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID)

	job, err := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
	if err != nil || job == nil {
		t.Fatalf("入队: %v %v", job, err)
	}

	// worker 循环处理转录
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne 转录: %v", err)
	}
	// 转录 ArtifactVersion 已写入，且 analyze job 已入队
	tr, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript)
	if err != nil {
		t.Fatalf("转录版本未写入: %v", err)
	}
	var trPayload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(tr.Payload), &trPayload); err != nil {
		t.Fatal(err)
	}
	if trPayload.Text != "hello world" {
		t.Errorf("转录文本错误: %q", trPayload.Text)
	}
	if len(trPayload.Segments) == 0 || trPayload.Segments[0].ID == "" {
		t.Error("转录段应带稳定 Segment ID")
	}
	analyzeJob, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)
	if analyzeJob != nil {
		t.Fatal("转录后应已有 analyze job（EnqueueAnalyze 应去重返回 nil）")
	}

	// worker 循环处理分析
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne 分析: %v", err)
	}
	av2, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindKnowledgeCard)
	if err != nil {
		t.Fatalf("卡片版本未写入: %v", err)
	}
	var card provider.KnowledgeCard
	if err := json.Unmarshal([]byte(av2.Payload), &card); err != nil {
		t.Fatal(err)
	}
	if card.Title != "T" {
		t.Errorf("卡片标题错误: %q", card.Title)
	}
	up2, _ := s.GetUploadByID(ctx, up.ID)
	if up2.ProcessingStatus != models.StatusProcessed {
		t.Errorf("upload 应 processed，实际 %s", up2.ProcessingStatus)
	}
	// 无重复产物
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE source_id=? AND kind='transcript'`, up.ID).Scan(&n)
	if n != 1 {
		t.Errorf("transcript 版本应恰好 1 行，实际 %d", n)
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM artifact_versions WHERE source_id=? AND kind='knowledge_card'`, up.ID).Scan(&n)
	if n != 1 {
		t.Errorf("knowledge_card 版本应恰好 1 行，实际 %d", n)
	}
}

// TestPipeline_TranscribeFailure 验证转录失败 → job failed + source failed。
func TestPipeline_TranscribeFailure(t *testing.T) {
	s, w := newTestWorker(t)
	injectFakeProviders(t, w, errFakeTranscribe, nil)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	job, _ := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)

	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed {
		t.Errorf("转录失败后 job 应 failed，实际 %s", got.Status)
	}
	if got.AttemptCount < 1 {
		t.Errorf("attempt_count 应 >=1，实际 %d", got.AttemptCount)
	}
	up2, _ := s.GetUploadByID(ctx, up.ID)
	if up2.ProcessingStatus != models.StatusFailedEp {
		t.Errorf("upload 应 failed，实际 %s", up2.ProcessingStatus)
	}
}

// TestPipeline_AnalyzeFailure 验证分析失败 → job failed（转录保留，可重试分析）。
func TestPipeline_AnalyzeFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID)

	// 先入队 analyze job（需要真实 job id 供 FK）
	job, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)
	if job == nil {
		t.Fatal("应创建 analyze job")
	}
	// 创建转录 ArtifactVersion（分析需要读取当前转录版本）
	tp, _ := json.Marshal(provider.TranscriptPayload{
		Language: "en", Text: "hello world",
		Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 1, Text: "hello world"}},
	})
	tv, err := s.CreateArtifactVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript, "fake", "m", "1", job.ID, string(tp))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript, tv); err != nil {
		t.Fatal(err)
	}

	// 转录 fake 成功（不需要），分析 fake 失败
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			Transcription: &fakeTranscriber{},
			Analysis:      &fakeAnalyzer{err: errFakeAnalyze},
			QA:            &fakeQA{},
		}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed {
		t.Errorf("分析失败后 job 应 failed，实际 %s", got.Status)
	}
	// 转录版本保留（证据不丢）
	if _, err := s.GetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript); err != nil {
		t.Error("分析失败不应删除转录版本")
	}
}

var errFakeTranscribe = &fakeError{"transcribe boom"}
var errFakeAnalyze = &fakeError{"analyze boom"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }
