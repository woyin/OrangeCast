package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/breestealth/wisepod/internal/provider"
	"github.com/breestealth/wisepod/internal/store"
)

// Worker 处理转录与分析任务。
// 任务以 goroutine 异步执行；状态写回 SQLite。音频临时落盘，转录后删除。
type Worker struct {
	store    *store.Store
	selector *provider.Selector
	tempDir  string
	client   *http.Client // 用于下载 episode 音频
}

func NewWorker(s *store.Store, sel *provider.Selector, tempDir string) *Worker {
	return &Worker{
		store: s, selector: sel, tempDir: tempDir,
		client: &http.Client{},
	}
}

// Process 异步处理一个任务。在 goroutine 中执行，立即返回。
// 调用方（路由 handler）创建 job 并入队后调用此方法。
func (w *Worker) Process(job *models.ProcessingJob) {
	go func() {
		ctx := context.Background()
		if err := w.processSync(ctx, job); err != nil {
			log.Printf("任务 %s 处理失败: %v", job.ID, err)
			_ = w.store.MarkJobFailed(ctx, job.ID, err.Error())
			w.markSourceFailed(ctx, job)
		}
	}()
}

func (w *Worker) processSync(ctx context.Context, job *models.ProcessingJob) error {
	// 原子 claim：queued→running，防重复处理
	ok, err := w.store.MarkJobRunning(ctx, job.ID)
	if err != nil {
		return err
	}
	if !ok {
		return nil // 已被处理或状态不符，跳过
	}

	// 运行时实时读取 active_provider（第 9 题）
	settings, err := w.store.GetOrCreateSettings(ctx, job.UserID)
	if err != nil {
		return err
	}
	bundle, err := w.selector.Bundle(settings.ActiveProvider)
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

// doTranscribe：下载音频（临时落盘）→ 调 provider 转录 → 存 transcript → 入队 analyze。
func (w *Worker) doTranscribe(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	w.setSourceStatus(ctx, job, models.StatusTranscribing)

	// 获取音频来源 URL（episode 从 audio_url；upload 需要外部提供文件路径，此处简化为 episode 路径）
	audioPath, err := w.fetchAudio(ctx, job)
	if err != nil {
		return fmt.Errorf("获取音频: %w", err)
	}
	defer os.Remove(audioPath) // 转录后删除临时文件

	result, err := bundle.Transcription.Transcribe(audioPath)
	if err != nil {
		return fmt.Errorf("转录: %w", err)
	}

	segmentsJSON, _ := json.Marshal(result.Segments)
	if err := w.store.UpsertTranscript(ctx, job.UserID, job.SourceType, job.SourceID, result.Language, result.Text, string(segmentsJSON)); err != nil {
		return err
	}
	w.setSourceStatus(ctx, job, models.StatusTranscribed)

	// 记录用量
	_ = w.store.RecordUsage(ctx, job.UserID, "transcription", bundle.Transcription.Name(), "", 0, 0, 0)

	// 入队分析任务
	analyzeJob, err := w.store.EnqueueAnalyze(ctx, job.UserID, job.SourceType, job.SourceID)
	if err != nil {
		return err
	}
	if analyzeJob != nil {
		w.Process(analyzeJob)
	}

	return w.store.MarkJobSucceeded(ctx, job.ID)
}

// doAnalyze：读 transcript → 调 provider 分析 → 存 KnowledgeCard。
func (w *Worker) doAnalyze(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	w.setSourceStatus(ctx, job, models.StatusAnalyzing)

	t, err := w.store.GetTranscript(ctx, job.UserID, job.SourceType, job.SourceID)
	if err != nil {
		return fmt.Errorf("读取 transcript: %w", err)
	}

	card, err := bundle.Analysis.Analyze(t.PlainText)
	if err != nil {
		return fmt.Errorf("分析: %w", err)
	}

	contentJSON, _ := json.Marshal(card)
	if err := w.store.UpsertAnalysis(ctx, job.UserID, job.SourceType, job.SourceID, card.Title, card.Summary, string(contentJSON)); err != nil {
		return err
	}
	w.setSourceStatus(ctx, job, models.StatusProcessed)

	// 更新搜索索引
	_ = w.store.IndexSearch(ctx, job.UserID, job.SourceType, job.SourceID, card.Title, card.Summary+" "+t.PlainText)

	_ = w.store.RecordUsage(ctx, job.UserID, "analysis", bundle.Analysis.Name(), "", 0, 0, 0)
	return w.store.MarkJobSucceeded(ctx, job.ID)
}

// fetchAudio 获取音频到临时文件，并转码为 64kbps 单声道 mp3。
// 转码目的：完整播客单集常达 30-50MB，超过 Groq Whisper ~25MB 上传限制。
// 降码率后体积砍半，音质对语音转录足够。返回转码后文件路径。
func (w *Worker) fetchAudio(ctx context.Context, job *models.ProcessingJob) (string, error) {
	var rawPath string
	var isTemp bool // episode 下载的临时文件需清理；upload 的持久化原文件必须保留（播放端点与重试依赖它）
	if job.SourceType == models.SourceEpisode {
		ep, gerr := w.store.GetEpisodeByID(ctx, job.SourceID)
		if gerr != nil {
			return "", gerr
		}
		rawPath, gerr = w.downloadAudio(ctx, ep.AudioURL)
		if gerr != nil {
			return "", fmt.Errorf("下载音频: %w", gerr)
		}
		isTemp = true
	} else {
		// upload：从持久化的上传文件读取
		rawPath = w.uploadPath(job.SourceID)
		if _, serr := os.Stat(rawPath); serr != nil {
			return "", fmt.Errorf("上传音频文件不存在: %w", serr)
		}
	}
	if isTemp {
		defer os.Remove(rawPath) // 仅清理 episode 下载的临时文件
	}

	// 转码为 64kbps 单声道 mp3
	transcoded, err := os.CreateTemp(w.tempDir, "cwp-tc-*.mp3")
	if err != nil {
		return "", err
	}
	transcoded.Close()
	if err := transcodeAudio(rawPath, transcoded.Name()); err != nil {
		os.Remove(transcoded.Name())
		return "", fmt.Errorf("音频转码失败: %w", err)
	}
	return transcoded.Name(), nil
}

// downloadAudio 下载 URL 到临时文件。
func (w *Worker) downloadAudio(ctx context.Context, audioURL string) (string, error) {
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
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", err
	}
	tmpFile.Close()
	return tmpFile.Name(), nil
}

// transcodeAudio 用 ffmpeg 转码为 64kbps 单声道 mp3。
// 音质降级对语音转录影响极小，但大幅缩小体积以适配 Groq 上传限制。
func transcodeAudio(in, out string) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", in,
		"-ac", "1",        // 单声道
		"-ar", "16000",    // 16kHz（语音足够，Whisper 内部也用 16kHz）
		"-b:a", "64k",     // 64kbps
		"-format", "mp3",
		out,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", stderr.String(), err)
	}
	return nil
}

func (w *Worker) uploadPath(uploadID string) string {
	return filepath.Join(w.tempDir, "uploads", uploadID)
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
