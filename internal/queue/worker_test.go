package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/filehash"
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
	w := NewWorker(s, sel, filepath.Join(dir, "tmp"), filepath.Join(dir, "evidence"), filepath.Join(dir, "narrations"))
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
	w2 := NewWorker(s, provider.NewSelector("g", "o"), w.tempDir, w.evidenceDir, w.narrationDir)
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
	sha, err := filehash.SHA256(path)
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

// TestEnsureEvidence_InvalidAudioDuration 验证原始音频无法读取时长时报错。
// 覆盖 ensureEvidence 中 "读取音频时长" 错误分支（原始文件不是有效音频）。
func TestEnsureEvidence_InvalidAudioDuration(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 不可用")
	}
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "bad.bin", "application/octet-stream", 100)
	if err != nil {
		t.Fatal(err)
	}
	// 原始文件是无效的二进制内容（非音频）→ ffprobe 读取时长失败
	os.MkdirAll(filepath.Join(w.tempDir, "uploads"), 0o755)
	rawPath := filepath.Join(w.tempDir, "uploads", up.ID)
	if err := os.WriteFile(rawPath, []byte("this is not audio data at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID, JobType: models.JobTranscribe}
	if _, err := w.ensureEvidence(ctx, job); err == nil {
		t.Fatal("无效音频应因时长读取失败而报错")
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

// TestEvidenceBitrateKbps_RejectsInvalidDuration 验证非正时长直接报错。
func TestEvidenceBitrateKbps_RejectsInvalidDuration(t *testing.T) {
	for _, d := range []float64{0, -1, -3.5} {
		if _, err := evidenceBitrateKbps(d); err == nil {
			t.Errorf("evidenceBitrateKbps(%.1f) 非正时长应报错", d)
		}
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

// TestRun_ProcessesAndStopsOnCancel 验证 Run 主循环：处理一个任务后，context 取消时干净退出。
func TestRun_ProcessesAndStopsOnCancel(t *testing.T) {
	s, w := newTestWorker(t)
	injectFakeProviders(t, w, nil, nil)
	ctx := context.Background()
	// upload 源 + 已就绪 EvidenceAudio（跳过转码）
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	if _, err := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe); err != nil {
		t.Fatalf("入队: %v", err)
	}

	// 用可取消 context 启动 Run，缩短轮询间隔以加速测试
	ctx, cancel := context.WithCancel(ctx)
	w.poll = 10 * time.Millisecond
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// 等待任务被处理（转录版本写入）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.GetCurrentVersion(context.Background(), models.SourceUpload, up.ID, store.KindTranscript); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done // 干净退出

	// 验证转录已写入
	if _, err := s.GetCurrentVersion(context.Background(), models.SourceUpload, up.ID, store.KindTranscript); err != nil {
		t.Fatalf("Run 应处理转录任务: %v", err)
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
	sha, err := filehash.SHA256(path)
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

type fakeHighlight struct{ err error }

func (f *fakeHighlight) GenerateHighlights(segments []provider.Segment) (*provider.HighlightSet, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &provider.HighlightSet{}, nil
}
func (f *fakeHighlight) Name() string { return "fake" }

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
			Highlight:     &fakeHighlight{},
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

// TestDoHighlight 验证 doHighlight 生成高光并写入 artifact 版本。
func TestDoHighlight(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)

	bundle := &provider.ProviderBundle{
		Highlight: &fakeHighlightOK{},
	}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀是物价上升"}}
	if err := w.doHighlight(ctx, job, bundle, segs); err != nil {
		t.Fatalf("doHighlight: %v", err)
	}
	// 高光版本已写入
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindHighlight); err != nil {
		t.Fatalf("高光版本未写入: %v", err)
	}
}

// TestDoHighlight_EmptySegmentsError 验证空 segments 时报错。
func TestDoHighlight_EmptySegmentsError(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	bundle := &provider.ProviderBundle{Highlight: &fakeHighlightOK{}}
	if err := w.doHighlight(ctx, job, bundle, nil); err == nil {
		t.Fatal("空 segments 应报错")
	}
}

// fakeHighlightOK 返回有效高光集合。
type fakeHighlightOK struct{}

func (f *fakeHighlightOK) GenerateHighlights(segments []provider.Segment) (*provider.HighlightSet, error) {
	return &provider.HighlightSet{
		Highlights: []provider.Highlight{
			{ID: "h1", Gist: "最值得听", Citations: []string{"seg-0001"}},
		},
	}, nil
}
func (f *fakeHighlightOK) Name() string { return "fake-highlight" }

// TestFetchRawAudio_Upload 验证 upload 源返回原始落盘文件路径。
func TestFetchRawAudio_Upload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	// 在 tempDir/uploads 落盘
	rawDir := filepath.Join(w.tempDir, "uploads")
	os.MkdirAll(rawDir, 0o755)
	os.WriteFile(filepath.Join(rawDir, up.ID), []byte("audio"), 0o644)

	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID}
	path, cleanup, err := w.fetchRawAudio(ctx, job)
	if err != nil {
		t.Fatalf("fetchRawAudio: %v", err)
	}
	if path != filepath.Join(rawDir, up.ID) {
		t.Errorf("路径错误: %q", path)
	}
	if cleanup == nil {
		t.Error("cleanup 不应为 nil")
	}
	cleanup() // 应安全调用
}

// TestFetchRawAudio_UploadMissing 验证 upload 文件不存在时返回错误。
func TestFetchRawAudio_UploadMissing(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID}
	if _, _, err := w.fetchRawAudio(ctx, job); err == nil {
		t.Fatal("upload 文件不存在应报错")
	}
}

// TestFetchRawAudio_EpisodeInvalidURL 验证 episode 音频 URL 非法时报错（无网络）。
func TestFetchRawAudio_EpisodeInvalidURL(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// episode 的 AudioURL 为非法协议（ftp://），ValidateURL 拒绝，无需网络
	p, _ := s.CreatePodcast(ctx, "https://f.xml", "P", "", "")
	s.MergeEpisodes(ctx, p.ID, []models.Episode{{GUID: "g1", Title: "Ep", AudioURL: "ftp://x.com/a.mp3"}})
	eps, _ := s.ListEpisodes(ctx, p.ID)
	sourceID := eps[0].ID
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: sourceID}
	if _, _, err := w.fetchRawAudio(ctx, job); err == nil {
		t.Fatal("非法音频 URL 应报错")
	}
}

// TestNewWorker_DefaultBundleFor 验证默认 bundleFor 按 settings 选择 Provider/Model。
func TestNewWorker_DefaultBundleFor(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// 设置 Transcription Provider + Model
	tp := "groq"
	tm := "whisper-large-v3"
	s.UpdateSettings(ctx, &models.Settings{TranscriptionProvider: &tp, TranscriptionModel: &tm})

	bundle, err := w.bundleFor(&models.ProcessingJob{JobType: models.JobTranscribe})
	if err != nil {
		t.Fatalf("bundleFor: %v", err)
	}
	if bundle == nil || bundle.Transcription == nil {
		t.Fatal("应返回带转录 provider 的 bundle")
	}
}

// TestNewWorker_GroqMissingKeyFallback 验证 groq 无 key 时 bundleFor 返回错误。
func TestNewWorker_GroqMissingKeyFallback(t *testing.T) {
	_, w := newTestWorker(t)
	w.selector = provider.NewSelector("", "") // 无 key
	if _, err := w.bundleFor(&models.ProcessingJob{JobType: models.JobTranscribe}); err == nil {
		t.Fatal("groq 无 key 应报错")
	}
}

// TestProcessJob_UnknownType 验证未知 job_type 报错。
func TestProcessJob_UnknownType(t *testing.T) {
	_, w := newTestWorker(t)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{}, nil
	}
	job := &models.ProcessingJob{JobType: "bogus"}
	if err := w.processJob(context.Background(), job); err == nil {
		t.Fatal("未知 job_type 应报错")
	}
}

// TestSetSourceStatus_Episode 验证 episode source 状态更新。
func TestSetSourceStatus_Episode(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: sourceID}
	w.setSourceStatus(ctx, job, models.StatusTranscribing)
	// 验证已更新
	ep, err := s.GetEpisodeByID(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetEpisodeByID: %v", err)
	}
	if ep.ProcessingStatus != models.StatusTranscribing {
		t.Errorf("episode 状态应 Transcribing，实际 %q", ep.ProcessingStatus)
	}
}

// TestSetSourceStatus_Upload 验证 upload source 状态更新。
func TestSetSourceStatus_Upload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID}
	w.setSourceStatus(ctx, job, models.StatusTranscribing)
	got, err := s.GetUploadByID(ctx, up.ID)
	if err != nil {
		t.Fatalf("GetUploadByID: %v", err)
	}
	if got.ProcessingStatus != models.StatusTranscribing {
		t.Errorf("upload 状态应 Transcribing，实际 %q", got.ProcessingStatus)
	}
}

// TestHighlightName 验证 HighlightName 返回高光 Provider 名。
func TestHighlightName(t *testing.T) {
	_, w := newTestWorker(t)
	if got := w.HighlightName(); got != "groq" {
		t.Errorf("HighlightName() = %q want groq", got)
	}
}

// TestHeartbeatLoop_StopsOnCancel 验证 heartbeatLoop 随 context 取消干净退出。
func TestHeartbeatLoop_StopsOnCancel(t *testing.T) {
	_, w := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.heartbeatLoop(ctx, "job-1")
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeatLoop 应在取消后退出")
	}
}

// TestNewWorker_DefaultBundleFor_AnalyzeCase 验证默认 bundleFor 对 analyze job
// 读取 Analysis Provider/Model 配置（覆盖 JobAnalyze 分支）。
func TestNewWorker_DefaultBundleFor_AnalyzeCase(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// 设置 Analysis Provider + Model
	ap := "groq"
	am := "llama-3.3-70b-versatile"
	s.UpdateSettings(ctx, &models.Settings{AnalysisProvider: &ap, AnalysisModel: &am})

	bundle, err := w.bundleFor(&models.ProcessingJob{JobType: models.JobAnalyze})
	if err != nil {
		t.Fatalf("bundleFor(analyze): %v", err)
	}
	if bundle == nil {
		t.Fatal("应返回非空 bundle")
	}
}

// TestNewWorker_DefaultBundleFor_UnknownJobType 验证默认 bundleFor 对未知 job_type
// 回退到 groq（覆盖 default 分支）。
func TestNewWorker_DefaultBundleFor_UnknownJobType(t *testing.T) {
	s, w := newTestWorker(t)
	_ = s
	// 未知 job_type → default 分支 → Provider="groq"
	bundle, err := w.bundleFor(&models.ProcessingJob{JobType: "unknown-type"})
	if err != nil {
		t.Fatalf("未知 job_type 不应报错，实际 %v", err)
	}
	if bundle == nil {
		t.Fatal("default 分支应返回 groq bundle")
	}
}

// TestNewWorker_DefaultBundleFor_EmptyProviderFallsBack 验证 settings 中 Provider 为空时
// 回退到 groq（覆盖 tc.Provider == "" 分支）。
func TestNewWorker_DefaultBundleFor_EmptyProviderFallsBack(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// TranscriptionProvider 为空字符串指针 → tc.Provider 空 → 回退 groq
	empty := ""
	s.UpdateSettings(ctx, &models.Settings{TranscriptionProvider: &empty, TranscriptionModel: &empty})

	bundle, err := w.bundleFor(&models.ProcessingJob{JobType: models.JobTranscribe})
	if err != nil {
		t.Fatalf("空 Provider 回退不应报错，实际 %v", err)
	}
	if bundle == nil {
		t.Fatal("应回退到 groq bundle")
	}
}

// TestNewWorker_DefaultBundleFor_GetSettingsError 验证 GetSettings 出错时降级到 groq 默认 bundle。
// 覆盖 bundleFor 中 err != nil → return w.selector.Bundle("groq") 分支。
func TestNewWorker_DefaultBundleFor_GetSettingsError(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// 先正常迁移，然后删除 settings 表制造 GetSettings 错误
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE settings`); err != nil {
		t.Fatalf("drop settings: %v", err)
	}
	// GetSettings 报错 → 应降级到 groq bundle（不报错）
	bundle, err := w.bundleFor(&models.ProcessingJob{JobType: models.JobTranscribe})
	if err != nil {
		t.Fatalf("GetSettings 出错时应降级、不报错，实际 %v", err)
	}
	if bundle == nil {
		t.Fatal("应返回降级 groq bundle")
	}
}

// TestDoAnalyze_NoTranscriptVersion 验证无当前转录版本时 doAnalyze 报错。
// 覆盖 doAnalyze 中 "读取当前转录版本" 错误分支。
func TestDoAnalyze_NoTranscriptVersion(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)

	bundle := &provider.ProviderBundle{
		Analysis:  &fakeAnalyzer{},
		Highlight: &fakeHighlight{},
	}
	err := w.doAnalyze(ctx, job, bundle)
	if err == nil {
		t.Fatal("无转录版本应报错")
	}
	if !strings.Contains(err.Error(), "读取当前转录版本") {
		t.Errorf("错误应含 '读取当前转录版本'，实际 %v", err)
	}
}

// TestDoAnalyze_CorruptTranscriptPayload 验证转录载荷 JSON 损坏时 doAnalyze 报错。
// 覆盖 doAnalyze 中 "解析转录载荷" 错误分支。
func TestDoAnalyze_CorruptTranscriptPayload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)
	// 写入一个 payload 非法 JSON 的转录版本
	tv, err := s.CreateArtifactVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript,
		"fake", "m", "1", job.ID, "{not valid json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript, tv); err != nil {
		t.Fatal(err)
	}

	bundle := &provider.ProviderBundle{
		Analysis:  &fakeAnalyzer{},
		Highlight: &fakeHighlight{},
	}
	err = w.doAnalyze(ctx, job, bundle)
	if err == nil {
		t.Fatal("损坏载荷应报错")
	}
	if !strings.Contains(err.Error(), "解析转录载荷") {
		t.Errorf("错误应含 '解析转录载荷'，实际 %v", err)
	}
}

// TestDoAnalyze_ValidationFails 验证分析产物通不过证据校验时 doAnalyze 报错。
// 覆盖 doAnalyze 中 "证据校验" 错误分支（分析返回的卡片引用不存在 Segment）。
func TestDoAnalyze_ValidationFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)
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

	// 分析器返回引用不存在 Segment 的卡片 → ValidateCard 拒绝
	bundle := &provider.ProviderBundle{
		Analysis:  &fakeAnalyzerBadCitations{},
		Highlight: &fakeHighlight{},
	}
	err = w.doAnalyze(ctx, job, bundle)
	if err == nil {
		t.Fatal("证据校验失败应报错")
	}
	if !strings.Contains(err.Error(), "证据校验") {
		t.Errorf("错误应含 '证据校验'，实际 %v", err)
	}
}

// fakeAnalyzerBadCitations 返回引用不存在 Segment 的卡片（触发 ValidateCard 拒绝）。
type fakeAnalyzerBadCitations struct{}

func (f *fakeAnalyzerBadCitations) Analyze(transcript string, segments []provider.Segment) (*provider.KnowledgeCard, error) {
	return &provider.KnowledgeCard{
		Title:     "T",
		Summary:   provider.CitedText{Text: "S", Citations: []string{"seg-9999"}}, // 不存在
		KeyPoints: []provider.KeyPoint{{Content: "KP", Citations: []string{"seg-9999"}}},
		Chapters:  []provider.Chapter{{Title: "CH", Citations: []string{"seg-9999"}}},
	}, nil
}
func (f *fakeAnalyzerBadCitations) Name() string { return "fake-bad" }

// TestDoHighlight_GenerationFails 验证 HighlightProvider 报错时 doHighlight 包装错误返回。
// 覆盖 doHighlight 中 "生成高光" 错误分支。
func TestDoHighlight_GenerationFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)

	bundle := &provider.ProviderBundle{Highlight: &fakeHighlight{err: errFakeAnalyze}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	err := w.doHighlight(ctx, job, bundle, segs)
	if err == nil {
		t.Fatal("Highlight 生成失败应报错")
	}
	if !strings.Contains(err.Error(), "生成高光") {
		t.Errorf("错误应含 '生成高光'，实际 %v", err)
	}
}

// TestDoHighlight_ValidationFails 验证高光校验失败（全部 Citation 无效）时 doHighlight 报错。
// 覆盖 doHighlight 中 "高光校验" 错误分支。
func TestDoHighlight_ValidationFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)

	// fakeHighlightBadCite 返回的 Citation 都不存在 → ValidateHighlightSet 拒绝
	bundle := &provider.ProviderBundle{Highlight: &fakeHighlightBadCite{}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	err := w.doHighlight(ctx, job, bundle, segs)
	if err == nil {
		t.Fatal("高光校验失败应报错")
	}
	if !strings.Contains(err.Error(), "高光校验") {
		t.Errorf("错误应含 '高光校验'，实际 %v", err)
	}
}

// fakeHighlightBadCite 返回的高光引用不存在的 Segment（触发 ValidateHighlightSet 拒绝）。
type fakeHighlightBadCite struct{}

func (f *fakeHighlightBadCite) GenerateHighlights(segments []provider.Segment) (*provider.HighlightSet, error) {
	return &provider.HighlightSet{
		Highlights: []provider.Highlight{
			{ID: "h1", Gist: "g", Citations: []string{"seg-9999"}}, // 不存在
		},
	}, nil
}
func (f *fakeHighlightBadCite) Name() string { return "fake-bad-cite" }

// TestPurgeSource_CreateIntentFails 验证 PurgeSource 在记录 purge 意图失败时报错。
// 覆盖 PurgeSource 中 CreatePurgeIntent err != nil 分支。
func TestPurgeSource_CreateIntentFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	// 删除 purges 表 → CreatePurgeIntent 报错
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE purges`); err != nil {
		t.Fatalf("drop purges: %v", err)
	}
	if err := w.PurgeSource(ctx, models.SourceEpisode, "ep-x"); err == nil {
		t.Fatal("purges 表缺失时 PurgeSource 应报错")
	}
}

// TestResumePurges_EmptyNoop 验证无 pending purge 时 ResumePurges 直接返回 nil。
// 覆盖 ResumePurges 中无待恢复 purge 的循环不执行路径。
func TestResumePurges_EmptyNoop(t *testing.T) {
	_, w := newTestWorker(t)
	if err := w.ResumePurges(context.Background()); err != nil {
		t.Fatalf("无 pending purge 时 ResumePurges 不应报错，实际 %v", err)
	}
}

// TestRun_StartupRecoveryFailure 验证启动恢复失败时 Run 不 panic、继续运行。
// 覆盖 Run 中 ResetRunningOnStartup/ResumePurges 失败仅记 log 的分支。
func TestRun_StartupRecoveryFailure(t *testing.T) {
	s, w := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 删除 jobs 与 purges 表 → ResetRunningOnStartup/ResumePurges 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE processing_jobs`); err != nil {
		t.Fatalf("DROP TABLE processing_jobs: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE purges`); err != nil {
		t.Fatalf("DROP TABLE purges: %v", err)
	}
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	// 短暂运行后取消，确认 Run 不 panic
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run 取消后应退出")
	}
}

// TestProcessOne_ClaimFails 验证领取任务失败时 ProcessOne 报错。
// 覆盖 ProcessOne 中 ClaimNextJob err != nil 分支（删除 jobs 表）。
func TestProcessOne_ClaimFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE processing_jobs`); err != nil {
		t.Fatalf("DROP TABLE processing_jobs: %v", err)
	}
	if err := w.ProcessOne(ctx); err == nil {
		t.Fatal("jobs 表缺失时 ProcessOne 应报错")
	}
}

// TestDoTranscribe_CreateVersionFails 验证转录版本创建失败时 doTranscribe 报错。
// 覆盖 doTranscribe 中 "创建转录版本" 错误分支（删除 artifact_versions 表）。
func TestDoTranscribe_CreateVersionFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID) // 已就绪证据，跳过转码
	job, _ := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
	// 删除 artifact_versions 表 → CreateArtifactVersion 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE artifact_versions`); err != nil {
		t.Fatalf("DROP TABLE artifact_versions: %v", err)
	}

	bundle := &provider.ProviderBundle{Transcription: &fakeTranscriber{}}
	err := w.doTranscribe(ctx, job, bundle)
	if err == nil {
		t.Fatal("artifact_versions 表缺失时 doTranscribe 应报错")
	}
	if !strings.Contains(err.Error(), "创建转录版本") {
		t.Errorf("错误应含 '创建转录版本'，实际 %v", err)
	}
}

// TestResumePurges_DeleteSourceRowsError 验证 DeleteSourceRows 失败时 ResumePurges 报错。
// 覆盖 ResumePurges 中 "purge 删除 DB 行" 错误分支（删除 episodes 表）。
func TestResumePurges_DeleteSourceRowsError(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	// 删除 episodes 表 → DeleteSourceRows 中的 DELETE episodes 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE episodes`); err != nil {
		t.Fatalf("DROP TABLE episodes: %v", err)
	}
	if err := w.ResumePurges(ctx); err == nil {
		t.Fatal("episodes 表缺失时 ResumePurges 应报错")
	}
}

// TestResumePurges_MarkDoneError 验证 MarkPurgeDone 失败时 ResumePurges 报错。
// 覆盖 ResumePurges 中 MarkPurgeDone 错误分支（删除 purges 表）。
func TestResumePurges_MarkDoneError(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	// 删除 purges 表 → MarkPurgeDone 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE purges`); err != nil {
		t.Fatalf("DROP TABLE purges: %v", err)
	}
	if err := w.ResumePurges(ctx); err == nil {
		t.Fatal("purges 表缺失时 ResumePurges 应报错")
	}
}

// TestProcessJob_BundleForError 验证 bundleFor 失败时 processJob 返回错误。
// 覆盖 processJob 中 bundleFor err != nil 分支。
func TestProcessJob_BundleForError(t *testing.T) {
	_, w := newTestWorker(t)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return nil, errors.New("bundle 解析失败")
	}
	job := &models.ProcessingJob{JobType: models.JobTranscribe}
	if err := w.processJob(context.Background(), job); err == nil {
		t.Fatal("bundleFor 失败应报错")
	}
}

// TestRun_PeriodicError 验证 Run 周期处理错误时仅记 log、不 panic。
// 覆盖 Run 中 ProcessOne err → log 分支（删除 jobs 表）。
func TestRun_PeriodicError(t *testing.T) {
	s, w := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	// 删除 jobs 表 → ProcessOne 领取失败 → log
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE processing_jobs`); err != nil {
		t.Fatalf("DROP TABLE processing_jobs: %v", err)
	}
	w.poll = 5 * time.Millisecond
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	// 等待至少一个周期
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run 取消后应退出")
	}
}

// TestHeartbeatLoop_HeartbeatError 验证心跳失败时仅记 log、不 panic。
// 覆盖 heartbeatLoop 中 HeartbeatJob err → log 分支（删除 jobs 表后等待心跳周期）。
func TestHeartbeatLoop_HeartbeatError(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode 跳过长时心跳测试")
	}
	s, w := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	// 删除 jobs 表 → HeartbeatJob 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE processing_jobs`); err != nil {
		t.Fatalf("DROP TABLE processing_jobs: %v", err)
	}
	done := make(chan struct{})
	go func() {
		w.heartbeatLoop(ctx, "job-1")
		close(done)
	}()
	// 等待一个心跳周期（heartbeatEvery=20s）让失败分支执行
	select {
	case <-done:
		t.Fatal("heartbeatLoop 不应提前退出")
	case <-time.After(heartbeatEvery + 5*time.Second):
	}
	cancel()
	<-done
}

// TestDoTranscribe_SetCurrentVersionFails 验证设置当前转录版本失败时报错。
// 覆盖 doTranscribe 中 "设置当前转录版本" 分支（删除 episodes 表）。
func TestDoTranscribe_SetCurrentVersionFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	seedEvidence(t, s, w, models.SourceUpload, up.ID)
	job, _ := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
	// 删除 uploads 表 → SetCurrentVersion 的 UPDATE uploads 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE uploads`); err != nil {
		t.Fatalf("DROP TABLE uploads: %v", err)
	}
	bundle := &provider.ProviderBundle{Transcription: &fakeTranscriber{}}
	err := w.doTranscribe(ctx, job, bundle)
	if err == nil {
		t.Fatal("SetCurrentVersion 失败应报错")
	}
	if !strings.Contains(err.Error(), "设置当前转录版本") {
		t.Errorf("错误应含 '设置当前转录版本'，实际 %v", err)
	}
}

// TestDoAnalyze_IndexKeyPointsFails 验证 KeyPoint 索引刷新失败不阻塞主流程。
// 覆盖 doAnalyze 中 IndexKeyPoints err → log 分支（删除 keypoint_index 表）。
func TestDoAnalyze_IndexKeyPointsFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, _ := s.CreateUpload(ctx, "a.wav", "audio/wav", 10)
	job, _ := s.EnqueueAnalyze(ctx, models.SourceUpload, up.ID)
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
	// 删除 keypoint_index 表 → IndexKeyPoints 失败（不阻塞 doAnalyze）
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE keypoint_index`); err != nil {
		t.Fatalf("DROP TABLE keypoint_index: %v", err)
	}
	bundle := &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}
	err = w.doAnalyze(ctx, job, bundle)
	if err != nil {
		t.Fatalf("IndexKeyPoints 失败不应阻塞 doAnalyze，实际 %v", err)
	}
}

// TestDoAnalyze_EpisodeSource 验证 doAnalyze 用 episode 源时获取 episode 标题。
// 覆盖 doAnalyze 中 GetEpisodeByID 成功分支（sourceTitle = ep.Title）。
func TestDoAnalyze_EpisodeSource(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueAnalyze(ctx, models.SourceEpisode, sourceID)
	tp, _ := json.Marshal(provider.TranscriptPayload{
		Language: "en", Text: "hello world",
		Segments: []provider.Segment{{ID: "seg-0001", Start: 0, End: 1, Text: "hello world"}},
	})
	tv, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, "fake", "m", "1", job.ID, string(tp))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindTranscript, tv); err != nil {
		t.Fatal(err)
	}
	bundle := &provider.ProviderBundle{Analysis: &fakeAnalyzer{}, Highlight: &fakeHighlight{}}
	if err := w.doAnalyze(ctx, job, bundle); err != nil {
		t.Fatalf("doAnalyze(episode): %v", err)
	}
	// 验证卡片版本已创建（episode 路径走通）
	if _, err := s.GetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard); err != nil {
		t.Errorf("episode 源分析应创建卡片版本: %v", err)
	}
}

// TestDoHighlight_CreateVersionFails 验证高光版本创建失败时报错。
// 覆盖 doHighlight 中 "创建高光版本" 分支（删除 artifact_versions 表）。
func TestDoHighlight_CreateVersionFails(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobAnalyze)
	// 删除 artifact_versions 表 → CreateArtifactVersion 失败
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE artifact_versions`); err != nil {
		t.Fatalf("DROP TABLE artifact_versions: %v", err)
	}
	bundle := &provider.ProviderBundle{Highlight: &fakeHighlightOK{}}
	segs := []provider.Segment{{ID: "seg-0001", Start: 0, End: 5, Text: "通胀"}}
	err := w.doHighlight(ctx, job, bundle, segs)
	if err == nil {
		t.Fatal("artifact_versions 表缺失时 doHighlight 应报错")
	}
	if !strings.Contains(err.Error(), "创建高光版本") {
		t.Errorf("错误应含 '创建高光版本'，实际 %v", err)
	}
}

// TestEnsureEvidence_BitrateTooLow 验证时长过长导致码率不足时报错。
// 覆盖 ensureEvidence 中 evidenceBitrateKbps 失败分支（用 fake ffprobe 输出超长时长）。
func TestEnsureEvidence_BitrateTooLow(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, "long.wav", "audio/wav", 100)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(w.tempDir, "uploads"), 0o755)
	rawPath := filepath.Join(w.tempDir, "uploads", up.ID)
	os.WriteFile(rawPath, []byte("audio"), 0o644)
	// 用 fake ffprobe 输出超长时长（如 999999 秒）→ evidenceBitrateKbps 返回错误
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "ffprobe")
	os.WriteFile(fake, []byte("#!/bin/sh\necho '999999'\n"), 0o755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: up.ID, JobType: models.JobTranscribe}
	if _, err := w.ensureEvidence(ctx, job); err == nil {
		t.Fatal("超长时长应因码率不足报错")
	}
}
