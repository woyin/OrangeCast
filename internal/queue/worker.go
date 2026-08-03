package queue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/safehttp"
	"github.com/woyin/orangecast/internal/store"
)

const (
	leaseDuration  = "60 seconds" // SQLite datetime modifier
	heartbeatEvery = 20 * time.Second
	pollInterval   = 3 * time.Second
	maxAudioSize   = 500 << 20 // 单集音频最大下载量（500MB）
	// Groq 的 25MB 上限包含 multipart 开销；EvidenceAudio 留出余量，避免边界 413。
	maxTranscriptionUploadBytes int64 = 22 << 20
	minEvidenceBitrateKbps            = 16
	maxEvidenceBitrateKbps            = 64
)

// Worker 处理转录与分析任务。
// SQLite 驱动（ADR-0006）：启动时回收 running 任务，周期领取 queued 任务，
// 领取时设置租约，处理中心跳续约；失败/中断后任务可被重新领取（至少一次执行）。
type Worker struct {
	store       *store.Store
	selector    *provider.Selector
	tempDir     string
	evidenceDir string
	client      *http.Client
	poll        time.Duration
	// bundleFor 选择本次任务的 provider bundle（ADR-0009 默认 Groq；测试可注入 fake）。
	bundleFor func(*models.ProcessingJob) (*provider.ProviderBundle, error)
}

func NewWorker(s *store.Store, sel *provider.Selector, tempDir, evidenceDir string) *Worker {
	client := safehttp.NewClient(5, maxAudioSize, 15*time.Minute)
	w := &Worker{
		store: s, selector: sel, tempDir: tempDir, evidenceDir: evidenceDir,
		client: client, poll: pollInterval,
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		// ADR-0009：Groq 是默认零成本 Provider；付费 Provider 按单次任务显式授权（Phase 4 落地）。
		return w.selector.Bundle("groq")
	}
	return w
}

// Run 启动 SQLite 驱动工作循环。
// 1) 启动恢复：所有 running 任务置回 queued（旧进程已死，至少一次执行）。
// 2) 周期领取并处理任务；无任务时等待下一个周期。
// 阻塞直到 ctx 取消。
func (w *Worker) Run(ctx context.Context) {
	if err := w.store.ResetRunningOnStartup(ctx); err != nil {
		log.Printf("启动恢复 running 任务失败: %v", err)
	}
	// 恢复中断的 Purge（文件删除 + DB 删除，ADR-0012）
	if err := w.ResumePurges(ctx); err != nil {
		log.Printf("启动恢复 Purge 失败: %v", err)
	}
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.ProcessOne(ctx); err != nil {
				log.Printf("worker 周期处理错误: %v", err)
			}
		}
	}
}

// ProcessOne 领取并同步处理一个任务（可测试）。无任务时返回 nil。
func (w *Worker) ProcessOne(ctx context.Context) error {
	job, err := w.store.ClaimNextJob(ctx, leaseDuration)
	if err != nil {
		return fmt.Errorf("领取任务: %w", err)
	}
	if job == nil {
		return nil
	}
	return w.processClaimed(ctx, job)
}

// processClaimed 处理已领取的任务：心跳续约 + 执行 + 终态。
func (w *Worker) processClaimed(ctx context.Context, job *models.ProcessingJob) error {
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go w.heartbeatLoop(hbCtx, job.ID)

	if err := w.processJob(hbCtx, job); err != nil {
		// 应用正常关闭会取消 worker context。此时保留 running 状态，
		// 让下一次启动的 ResetRunningOnStartup 将任务重新入队；不能把
		// 可恢复的中断伪装成业务失败。
		if errors.Is(err, context.Canceled) || errors.Is(hbCtx.Err(), context.Canceled) {
			log.Printf("任务 %s 因正常关闭中断，等待下次启动恢复", job.ID)
			return nil
		}
		log.Printf("任务 %s 处理失败: %v", job.ID, err)
		_ = w.store.MarkJobFailed(ctx, job.ID, err.Error())
		w.markSourceFailed(ctx, job)
		return nil // 已标记失败，不算周期错误
	}
	return w.store.MarkJobSucceeded(ctx, job.ID)
}

func (w *Worker) heartbeatLoop(ctx context.Context, jobID string) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.store.HeartbeatJob(ctx, jobID, leaseDuration); err != nil {
				log.Printf("任务 %s 心跳失败: %v", jobID, err)
			}
		}
	}
}

// processJob 执行一个已领取任务（不处理终态写回）。
func (w *Worker) processJob(ctx context.Context, job *models.ProcessingJob) error {
	bundle, err := w.bundleFor(job)
	if err != nil {
		return err
	}
	switch job.JobType {
	case models.JobTranscribe:
		return w.doTranscribe(ctx, job, bundle)
	case models.JobAnalyze:
		return w.doAnalyze(ctx, job, bundle)
	default:
		return fmt.Errorf("未知 job_type: %s", job.JobType)
	}
}

// doTranscribe：确保 EvidenceAudio 持久化 → 转录 → 存 transcript → 入队 analyze。
func (w *Worker) doTranscribe(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	w.setSourceStatus(ctx, job, models.StatusTranscribing)

	// 1) 持久化标准化 EvidenceAudio（幂等：已存在且校验通过则复用）
	evidencePath, err := w.ensureEvidence(ctx, job)
	if err != nil {
		return fmt.Errorf("持久化证据音频: %w", err)
	}

	// 2) 从 EvidenceAudio 转录（播放/引用只依赖它，ADR-0005）
	result, err := bundle.Transcription.Transcribe(evidencePath)
	if err != nil {
		return fmt.Errorf("转录: %w", err)
	}

	// 3) 创建不可变 Transcript ArtifactVersion（ADR-0011），并指向当前版本
	payload, _ := json.Marshal(provider.TranscriptPayload{
		Language: result.Language,
		Text:     result.Text,
		Segments: result.Segments,
	})
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindTranscript, bundle.Transcription.Name(), "whisper-large-v3", "1", job.ID, string(payload))
	if err != nil {
		return fmt.Errorf("创建转录版本: %w", err)
	}
	if err := w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, version); err != nil {
		return fmt.Errorf("设置当前转录版本: %w", err)
	}
	w.setSourceStatus(ctx, job, models.StatusTranscribed)

	_ = w.store.RecordUsage(ctx, "transcription", bundle.Transcription.Name(), "", 0, 0, 0)

	// 4) 入队分析任务（已有进行中 analyze 则不重复创建）
	if _, err := w.store.EnqueueAnalyze(ctx, job.SourceType, job.SourceID); err != nil {
		return err
	}
	return nil
}

// doAnalyze：读当前 Transcript 版本 → 调 provider 分析（模型返回 Segment ID）→
// 证据校验（Citation 存在性 + 金句逐字）→ 创建不可变 KnowledgeCard ArtifactVersion。
func (w *Worker) doAnalyze(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	w.setSourceStatus(ctx, job, models.StatusAnalyzing)

	av, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript)
	if err != nil {
		return fmt.Errorf("读取当前转录版本: %w", err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return fmt.Errorf("解析转录载荷: %w", err)
	}

	// 模型只引用 Segment.ID；程序负责时间范围解析与证据校验（ADR-0008）
	card, err := bundle.Analysis.Analyze(payload.Text, payload.Segments)
	if err != nil {
		return fmt.Errorf("分析: %w", err)
	}
	validated, err := provider.ValidateCard(card, payload.Segments)
	if err != nil {
		return fmt.Errorf("证据校验: %w", err)
	}

	contentJSON, _ := json.Marshal(validated)
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindKnowledgeCard, bundle.Analysis.Name(), "llama-3.3-70b-versatile", "1", job.ID, string(contentJSON))
	if err != nil {
		return fmt.Errorf("创建卡片版本: %w", err)
	}
	if err := w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindKnowledgeCard, version); err != nil {
		return fmt.Errorf("设置当前卡片版本: %w", err)
	}
	w.setSourceStatus(ctx, job, models.StatusProcessed)

	// 更新分段级搜索索引（幂等：先删后插，Roadmap Phase 5）
	_ = w.store.IndexSearch(ctx, job.SourceType, job.SourceID, validated.Title, validated.Summary.Text, payload.Segments)

	_ = w.store.RecordUsage(ctx, "analysis", bundle.Analysis.Name(), "", 0, 0, 0)
	return nil
}

// ensureEvidence 确保 Source 的标准化 EvidenceAudio 已持久化并校验，返回文件路径。
// 幂等：evidence_audio 已记录且文件存在（按 sha256 校验）时直接复用。
func (w *Worker) ensureEvidence(ctx context.Context, job *models.ProcessingJob) (string, error) {
	rel := fmt.Sprintf("%s_%s.mp3", job.SourceType, job.SourceID)
	path := filepath.Join(w.evidenceDir, rel)

	// 已存在且哈希一致 → 直接复用（避免重复转码/下载，且保证幂等）
	if ev, err := w.store.GetEvidenceAudio(ctx, job.SourceType, job.SourceID); err == nil && ev.Status == "ready" {
		if fi, serr := os.Stat(path); serr == nil && fi.Size() > 0 {
			if h, herr := fileSHA256(path); herr == nil && h == ev.SHA256 && fi.Size() <= maxTranscriptionUploadBytes {
				return path, nil
			}
		}
	}

	// 获取原始音频：episode 临时下载；upload 读取已落盘的原始文件
	rawPath, cleanup, err := w.fetchRawAudio(ctx, job)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// 转码到证据路径（原子写：临时文件 + rename，崩溃不留下半成品证据）。
	// 为满足 Groq 单文件上传上限，码率按时长自适应，而非固定 64kbps。
	tmpPath := path + ".part"
	duration, err := audioDuration(rawPath)
	if err != nil {
		return "", fmt.Errorf("读取音频时长: %w", err)
	}
	bitrate, err := evidenceBitrateKbps(duration)
	if err != nil {
		return "", err
	}
	if err := transcodeAudio(rawPath, tmpPath, bitrate); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("音频转码失败: %w", err)
	}
	sha, err := fileSHA256(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	fi, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("落盘证据音频: %w", err)
	}
	if err := w.store.UpsertEvidenceAudio(ctx, job.SourceType, job.SourceID, rel, "mp3", fi.Size(), sha); err != nil {
		return "", err
	}

	// 证据已持久化：upload 的原始输入可以删除（ADR-0005：先落盘校验，后删原始输入）
	if job.SourceType == models.SourceUpload {
		if rawPath != path {
			_ = os.Remove(rawPath)
		}
	}
	return path, nil
}

// fetchRawAudio 获取原始音频并返回路径与清理函数。
// episode：从外链下载到临时目录（清理删除）；upload：读取已落盘原始文件（无清理）。
func (w *Worker) fetchRawAudio(ctx context.Context, job *models.ProcessingJob) (string, func(), error) {
	if job.SourceType == models.SourceEpisode {
		ep, err := w.store.GetEpisodeByID(ctx, job.SourceID)
		if err != nil {
			return "", nil, err
		}
		path, err := w.downloadAudio(ctx, ep.AudioURL)
		if err != nil {
			return "", nil, fmt.Errorf("下载音频: %w", err)
		}
		return path, func() { os.Remove(path) }, nil
	}
	// upload：原始文件在 tempDir/uploads/<id>（handlers 落盘；证据持久化后会被删除）
	rawPath := filepath.Join(w.tempDir, "uploads", job.SourceID)
	if _, err := os.Stat(rawPath); err != nil {
		return "", nil, fmt.Errorf("上传音频文件不存在: %w", err)
	}
	return rawPath, func() {}, nil
}

// downloadAudio 下载 URL 到临时文件。复用 SSRF 防护客户端（逐跳重定向校验 + 私网拦截）。
func (w *Worker) downloadAudio(ctx context.Context, audioURL string) (string, error) {
	if err := safehttp.ValidateURL(audioURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载音频 HTTP %d", resp.StatusCode)
	}
	ext := guessAudioExt(audioURL)
	tmpFile, err := os.CreateTemp(w.tempDir, "cwp-audio-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmpFile, safehttp.LimitBody(resp.Body, maxAudioSize)); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", err
	}
	tmpFile.Close()
	return tmpFile.Name(), nil
}

// evidenceBitrateKbps 按时长选择可让转录请求（含 multipart 开销）保持在 25MB
// Groq 上限以内的最高标准 MP3 码率。长到 16kbps 仍无法容纳时，需要后续分段策略。
func evidenceBitrateKbps(durationSeconds float64) (int, error) {
	if durationSeconds <= 0 {
		return 0, fmt.Errorf("音频时长无效: %.3f", durationSeconds)
	}
	maxKbps := int(math.Floor(float64(maxTranscriptionUploadBytes*8) / durationSeconds / 1000))
	for _, bitrate := range []int{maxEvidenceBitrateKbps, 56, 48, 40, 32, 24, minEvidenceBitrateKbps} {
		if bitrate <= maxKbps {
			return bitrate, nil
		}
	}
	return 0, fmt.Errorf("音频时长 %.0f 秒即使以 %dkbps 转码仍超过 Groq 单文件上传上限；需要分段转录", durationSeconds, minEvidenceBitrateKbps)
}

// audioDuration 用 ffprobe 读取原始输入时长，以便选择 EvidenceAudio 的码率。
func audioDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

// transcodeAudio 用给定码率转码为 16kHz 单声道 MP3。
func transcodeAudio(in, out string, bitrateKbps int) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", in,
		"-ac", "1",
		"-ar", "16000",
		"-b:a", fmt.Sprintf("%dk", bitrateKbps),
		"-f", "mp3",
		out,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", stderr.String(), err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- 可恢复 Purge（ADR-0012）----

// ResumePurges 恢复所有 pending 的 purge：先删文件（幂等），再事务性删 DB 行。
// 任一步崩溃后重启可继续，不会只删一半。
func (w *Worker) ResumePurges(ctx context.Context) error {
	purges, err := w.store.ListPendingPurges(ctx)
	if err != nil {
		return err
	}
	for _, p := range purges {
		// 1) 删除文件（EvidenceAudio + upload 原始文件；不存在视为已删，幂等）
		_ = os.Remove(filepath.Join(w.evidenceDir, fmt.Sprintf("%s_%s.mp3", p.SourceType, p.SourceID)))
		_ = os.Remove(filepath.Join(w.tempDir, "uploads", p.SourceID))
		// 2) 事务性删除 DB 行
		if err := w.store.DeleteSourceRows(ctx, p.SourceType, p.SourceID); err != nil {
			return fmt.Errorf("purge 删除 DB 行（%s/%s）: %w", p.SourceType, p.SourceID, err)
		}
		// 3) 标记完成
		if err := w.store.MarkPurgeDone(ctx, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// PurgeSource 发起并立即执行一次 Purge（Owner 显式发起，ADR-0012）。
func (w *Worker) PurgeSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	if err := w.store.CreatePurgeIntent(ctx, sourceType, sourceID); err != nil {
		return err
	}
	return w.ResumePurges(ctx)
}

func guessAudioExt(url string) string {
	low := strings.ToLower(url)
	for _, e := range []string{".mp3", ".m4a", ".wav", ".aac", ".ogg"} {
		if strings.HasSuffix(low, e) {
			return e
		}
	}
	return ".mp3"
}

func (w *Worker) setSourceStatus(ctx context.Context, job *models.ProcessingJob, status models.EpisodeProcessingStatus) {
	if job.SourceType == models.SourceEpisode {
		_ = w.store.UpdateEpisodeStatus(ctx, job.SourceID, status)
	} else {
		_ = w.store.UpdateUploadStatus(ctx, job.SourceID, status)
	}
}

func (w *Worker) markSourceFailed(ctx context.Context, job *models.ProcessingJob) {
	w.setSourceStatus(ctx, job, models.StatusFailedEp)
}
