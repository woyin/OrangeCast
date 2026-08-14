// Package queue 实现 SQLite 驱动的可恢复处理队列（ADR-0006）。
//
// worker.go 是核心：启动时回收 running 任务、周期领取 queued 任务、租约 + 心跳续约，
// 失败/中断后可重新领取（至少一次执行）。流水线：doTranscribe（证据持久化 + 转录）→
// doAnalyze（知识卡片 + KeyPoint 索引 + 高光 + Narration）。audio.go 提供码率选择、
// 时长探测与 ffmpeg 转码工具。
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/woyin/orangecast/internal/filehash"
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
)

// Worker 处理转录与分析任务。
// SQLite 驱动（ADR-0006）：启动时回收 running 任务，周期领取 queued 任务，
// 领取时设置租约，处理中心跳续约；失败/中断后任务可被重新领取（至少一次执行）。
type Worker struct {
	store        *store.Store
	selector     *provider.Selector
	tempDir      string
	evidenceDir  string
	narrationDir string
	client       *http.Client
	poll         time.Duration
	// bundleFor 选择本次任务的 provider bundle（ADR-0009 默认 Groq；测试可注入 fake）。
	bundleFor func(*models.ProcessingJob) (*provider.ProviderBundle, error)
}

// NewWorker 构造一个 worker。tempDir 存放下载数据与转码中间产物；evidenceDir 持久保存
// 标准化 EvidenceAudio；narrationDir 保存 Highlight 的 TTS 解说音轨（独立于 evidence，不进备份）。
// HTTP 客户端复用 SSRF 防护（safehttp）：逐跳重定向校验 + 私网拦截 + 体积上限。
// bundleFor 默认读 SQLite settings 按任务类型选择 Provider/Model，读失败时降级到 Groq 默认。
func NewWorker(s *store.Store, sel *provider.Selector, tempDir, evidenceDir, narrationDir string) *Worker {
	client := safehttp.NewClient(10, maxAudioSize, 15*time.Minute)
	w := &Worker{
		store: s, selector: sel, tempDir: tempDir, evidenceDir: evidenceDir, narrationDir: narrationDir,
		client: client, poll: pollInterval,
	}
	w.bundleFor = func(job *models.ProcessingJob) (*provider.ProviderBundle, error) {
		// 读 settings 选每任务的 Provider + Model（ADR-0009 扩展）
		st, err := w.store.GetSettings(context.Background())
		if err != nil {
			return w.selector.Bundle("groq") // 降级默认
		}
		var tc provider.TaskConfig
		switch job.JobType {
		case models.JobTranscribe:
			tc = provider.TaskConfig{Provider: ptrStr(st.TranscriptionProvider), Model: ptrStr(st.TranscriptionModel)}
		case models.JobAnalyze:
			// analyze job 包含分析+高光，用 analysis 配置
			tc = provider.TaskConfig{Provider: ptrStr(st.AnalysisProvider), Model: ptrStr(st.AnalysisModel)}
		default:
			tc = provider.TaskConfig{Provider: "groq"}
		}
		if tc.Provider == "" {
			tc.Provider = "groq"
		}
		return w.selector.BundleForTask(tc)
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
	if _, err := w.store.EnqueueAnalyzeForIngestion(ctx, job.SourceType, job.SourceID, job.Automated); err != nil {
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

	// 刷新 KeyPoint 全局索引（ADR-0017）
	sourceTitle := ""
	if ep, err := w.store.GetEpisodeByID(ctx, job.SourceID); err == nil {
		sourceTitle = ep.Title
	} else if up, err := w.store.GetUploadByID(ctx, job.SourceID); err == nil {
		sourceTitle = up.OriginalFilename
	}
	if err := w.store.IndexKeyPoints(ctx, job.SourceType, job.SourceID, sourceTitle, version, validated, payload.Segments); err != nil {
		log.Printf("任务 %s KeyPoint 索引刷新失败（不阻塞）: %v", job.ID, err)
	}

	_ = w.store.RecordUsage(ctx, "analysis", bundle.Analysis.Name(), "", 0, 0, 0)

	if !job.Automated {
		// Owner 触发的处理才自动附带可选衍生产物；订阅自动采集止于 KeyPoint。
		if err := w.doHighlight(ctx, job, bundle, payload.Segments); err != nil {
			log.Printf("任务 %s 高光生成失败（不阻塞主流程）: %v", job.ID, err)
		}
		if err := w.doNarration(ctx, job, bundle); err != nil {
			log.Printf("任务 %s Narration 合成失败（不阻塞主流程）: %v", job.ID, err)
		}
	}
	return nil
}

// doHighlight 生成高光片段并存为独立 ArtifactVersion（ADR-0016）。
// 流程：调 HighlightProvider.GenerateHighlights → ValidateHighlightSet 校验
// （Citation 必须引用真实 Segment）→ CreateArtifactVersion（kind=highlight）
// → SetCurrentVersion 指向新版本。
// 失败不阻塞主流程（KnowledgeCard 已成功）；Highlight 是可选增强。
func (w *Worker) doHighlight(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle, segments []provider.Segment) error {
	raw, err := bundle.Highlight.GenerateHighlights(segments)
	if err != nil {
		return fmt.Errorf("生成高光: %w", err)
	}
	validated, err := provider.ValidateHighlightSet(raw, segments)
	if err != nil {
		return fmt.Errorf("高光校验: %w", err)
	}
	contentJSON, _ := json.Marshal(validated)
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindHighlight, bundle.Highlight.Name(), "llama-3.3-70b-versatile", "1", job.ID, string(contentJSON))
	if err != nil {
		return fmt.Errorf("创建高光版本: %w", err)
	}
	return w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight, version)
}

// HighlightName 返回高光 Provider 名（供 worker 记录）。
func (w *Worker) HighlightName() string { return "groq" }

// ensureEvidence 确保 Source 的标准化 EvidenceAudio 已持久化并校验，返回文件路径。
// 幂等：evidence_audio 已记录且文件存在（按 sha256 校验）时直接复用。
func (w *Worker) ensureEvidence(ctx context.Context, job *models.ProcessingJob) (string, error) {
	rel := fmt.Sprintf("%s_%s.mp3", job.SourceType, job.SourceID)
	path := filepath.Join(w.evidenceDir, rel)

	// 已存在且哈希一致 → 直接复用（避免重复转码/下载，且保证幂等）
	if ev, err := w.store.GetEvidenceAudio(ctx, job.SourceType, job.SourceID); err == nil && ev.Status == "ready" {
		if fi, serr := os.Stat(path); serr == nil && fi.Size() > 0 {
			if h, herr := filehash.SHA256(path); herr == nil && h == ev.SHA256 && fi.Size() <= maxTranscriptionUploadBytes {
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
	sha, err := filehash.SHA256(tmpPath)
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
		for _, path := range []string{
			filepath.Join(w.evidenceDir, fmt.Sprintf("%s_%s.mp3", p.SourceType, p.SourceID)),
			filepath.Join(w.tempDir, "uploads", p.SourceID),
		} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("purge 删除文件 %s: %w", path, err)
			}
		}
		// 2) 删除 KeyPoint 索引 + 标注/收藏/集合成员（ADR-0017）
		if err := w.store.InvalidateAndDeleteKeyPointsForSource(ctx, p.SourceType, p.SourceID); err != nil {
			return fmt.Errorf("purge 原子撤销文章证据并删除 KeyPoint（%s/%s）: %w", p.SourceType, p.SourceID, err)
		}
		for _, statement := range []string{
			`DELETE FROM annotations WHERE source_type=? AND source_id=?`,
			`DELETE FROM pins WHERE source_type=? AND source_id=?`,
			`DELETE FROM collection_items WHERE source_type=? AND source_id=?`,
		} {
			if _, err := w.store.DB.ExecContext(ctx, statement, string(p.SourceType), p.SourceID); err != nil {
				return fmt.Errorf("purge 删除素材关系（%s/%s）: %w", p.SourceType, p.SourceID, err)
			}
		}
		// ADR-0018：删除 GeneratedDerivative 产物（Paraphrase / StudySession），
		// 使 PersonalKnowledgeBase 中指向该 Source 的 Citation 与 Reference 一并失效。
		if err := w.store.DeleteParaphrasesForSource(ctx, p.SourceType, p.SourceID); err != nil {
			return err
		}
		if err := w.store.DeleteStudySessionsForSource(ctx, p.SourceType, p.SourceID); err != nil {
			return err
		}
		if err := w.store.DeleteNarrationsForSource(ctx, p.SourceType, p.SourceID); err != nil {
			return err
		}
		// 3) 事务性删除 DB 行
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

// ptrStr 安全解引用 *string，nil 返回空串。
func ptrStr(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// doNarration 为当前 HighlightSet 的每个 Gist 合成一段 Narration（解说音轨，ADR-0019）。
//
// 触发：紧接 doHighlight 成功后（analyze 流水线末尾），失败不阻塞主流程。
// 依赖：读当前 Highlight 版本（取已校验的 Gist 与 Highlight.ID）。
// 容错：
//   - Narration Provider 不可用（如 Kokoro 未安装）→ 跳过、记 log、不阻塞。
//   - 单段合成失败 → 跳过该段、继续其他段、记 log。
//   - 已存在该 (highlight_id, voice, model) 的 Narration → 跳过（幂等，避免重复合成）。
//
// 存储位置：w.narrationDir/{sourceType}_{sourceID}_{highlightID}_{version}.wav，独立于 evidence 目录。
func (w *Worker) doNarration(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	// Provider 不可用 → 优雅跳过（ADR-0019 R1）。
	if bundle.Narration == nil || !bundle.Narration.Available() {
		log.Printf("任务 %s Narration Provider 不可用，跳过合成（不阻塞）", job.ID)
		return nil
	}

	// 读当前 Highlight 版本（取已校验的 HighlightSet，含稳定 Highlight.ID）。
	hv, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight)
	if err != nil {
		return fmt.Errorf("读取当前高光版本失败: %w", err)
	}
	var hs provider.HighlightSet
	if err := json.Unmarshal([]byte(hv.Payload), &hs); err != nil {
		return fmt.Errorf("解析高光载荷失败: %w", err)
	}
	if len(hs.Highlights) == 0 {
		return nil // 无高光，无需合成
	}

	// 取已存在的 Narration，做幂等（同 voice+model 已合成则跳过）。
	existing, err := w.store.ListCurrentNarrationsForSource(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return fmt.Errorf("读取已有 Narration 失败: %w", err)
	}

	np := bundle.Narration
	voice := "" // 用 Provider 默认音色
	for _, h := range hs.Highlights {
		if h.ID == "" || h.Gist == "" {
			continue
		}
		// 幂等：同 highlight_id 已有该 provider 的 Narration → 跳过（避免重复合成）。
		if cur, ok := existing[h.ID]; ok && cur.Provider == np.Name() {
			continue
		}
		// 合成到 narrations 目录（文件名含 source + highlight_id + version placeholder）。
		// version 在 CreateNarration 后才确定；这里用临时名 + 重命名，或预查 version。
		nextVer := w.nextNarrationVersion(ctx, job.SourceType, job.SourceID, h.ID)
		relPath := fmt.Sprintf("%s_%s_%s_%d.wav", job.SourceType, job.SourceID, h.ID, nextVer)
		outPath := filepath.Join(w.narrationDir, relPath)
		if err := os.MkdirAll(w.narrationDir, 0o755); err != nil {
			return fmt.Errorf("创建 narrations 目录: %w", err)
		}
		res, err := np.Synthesize(h.Gist, voice, outPath)
		if err != nil {
			log.Printf("任务 %s Highlight %s 的 Narration 合成失败（跳过该段）: %v", job.ID, h.ID, err)
			continue
		}
		// 探测时长（复用 audio.go 的 audioDuration）。
		dur, _ := audioDuration(outPath)
		if dur <= 0 {
			dur = 0 // 探测失败记 0，不阻塞
		}
		if _, err := w.store.CreateNarration(ctx, job.SourceType, job.SourceID, h.ID, res.Voice, res.Model, relPath, dur, res.CharCount, np.Name()); err != nil {
			log.Printf("任务 %s Highlight %s 的 Narration 写库失败（音频已合成）: %v", job.ID, h.ID, err)
			continue
		}
	}
	return nil
}

// nextNarrationVersion 返回某 highlight_id 下一个版本号（用于预生成文件名）。
// 与 CreateNarration 的版本号计算独立，并发下 CreateNarration 的 UNIQUE 会兜底；
// 文件名版本号与 DB version 偶尔不一致（并发重生成）可接受——relpath 仅是文件名，真理在 DB。
func (w *Worker) nextNarrationVersion(ctx context.Context, sourceType models.SourceType, sourceID, highlightID string) int {
	// 简化：直接查当前 MAX(version)+1；与 CreateNarration 内部逻辑重复但可接受（文件名不要求严格一致）。
	cur, err := w.store.GetCurrentNarration(ctx, sourceType, sourceID, highlightID)
	if err != nil {
		return 1
	}
	return cur.Version + 1
}
