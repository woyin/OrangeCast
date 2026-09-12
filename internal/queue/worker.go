// Package queue 实现 SQLite 驱动的可恢复处理队列（ADR-0006）。
//
// worker.go 是核心：启动时回收 running 任务、周期领取 queued 任务、租约 + 心跳续约，
// 失败/中断后可重新领取（至少一次执行）。流水线：doTranscribe（证据持久化 + 转录）→
// doAnalyze（知识卡片 + KeyPoint 索引 + 高光 + Narration）。audio.go 提供码率选择、
// 时长探测与 ffmpeg 转码工具。
package queue

import (
	"context"
	"database/sql"
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
	// taskConfigFor 解析任务的 Provider/Model 配置（B04 预算检查使用）。
	taskConfigFor func(*models.ProcessingJob) (provider.TaskConfig, error)
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
	w.taskConfigFor = func(job *models.ProcessingJob) (provider.TaskConfig, error) {
		// 读 settings 选每任务的 Provider + Model（ADR-0009 扩展）
		st, err := w.store.GetSettings(context.Background())
		if err != nil {
			return provider.TaskConfig{Provider: "groq"}, nil // 降级默认
		}
		var tc provider.TaskConfig
		switch job.JobType {
		case models.JobTranscribe:
			tc = provider.TaskConfig{Provider: ptrStr(st.TranscriptionProvider), Model: ptrStr(st.TranscriptionModel)}
		case models.JobAnalyze:
			// analyze job 包含分析+高光，用 analysis 配置
			tc = provider.TaskConfig{Provider: ptrStr(st.AnalysisProvider), Model: ptrStr(st.AnalysisModel)}
		case models.JobDigest:
			// 精读文写作复用 Writer 角色配置（ADR-0023：与文章写作同池计费治理）
			tc = provider.TaskConfig{Provider: ptrStr(st.WriterProvider), Model: ptrStr(st.WriterModel)}
		case models.JobHighlight:
			// 高光独立任务（B06）使用高光角色配置
			tc = provider.TaskConfig{Provider: ptrStr(st.HighlightProvider), Model: ptrStr(st.HighlightModel)}
		case models.JobNarration:
			// 解说任务（B07）：本地 TTS 零成本，Provider 路由保持默认
			tc = provider.TaskConfig{Provider: ptrStr(st.HighlightProvider), Model: ptrStr(st.HighlightModel)}
		case models.JobKeypointQuality:
			// 重点质量判定（K02）使用分析角色配置（判定与生成分开提示词）
			tc = provider.TaskConfig{Provider: ptrStr(st.AnalysisProvider), Model: ptrStr(st.AnalysisModel)}
		case models.JobDigestRewrite:
			// 渠道改写（G06）复用 Writer 角色配置（与文章同池计费）
			tc = provider.TaskConfig{Provider: ptrStr(st.WriterProvider), Model: ptrStr(st.WriterModel)}
		default:
			tc = provider.TaskConfig{Provider: "groq"}
		}
		if tc.Provider == "" {
			tc.Provider = "groq"
		}
		return tc, nil
	}
	w.bundleFor = func(job *models.ProcessingJob) (*provider.ProviderBundle, error) {
		tc, err := w.taskConfigFor(job)
		if err != nil {
			return w.selector.Bundle("groq")
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
	// B02：结果已持久化的任务（进程在结果落库后、终态写入前中断）恢复时直接复用，
	// 不重放远端模型调用；"结果写入后中断再执行不新增版本"由此保证。
	if exec, err := w.store.GetJobExecution(ctx, job.ID); err == nil && exec.ResultState == models.JobResultComplete {
		log.Printf("任务 %s 命中已持久化结果，恢复时直接复用", job.ID)
		return w.store.MarkJobSucceeded(ctx, job.ID)
	}
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
		w.releaseJobBudget(ctx, job) // B04：失败释放预占，区分是否已发生远端调用
		return nil                   // 已标记失败，不算周期错误
	}
	w.settleJobBudget(ctx, job) // B04：成功后以实际费用结算预占
	return w.store.MarkJobSucceeded(ctx, job.ID)
}

// budgetEstimateUnits 返回预算预估计量单位（B04）。转录按音频计费、单位未知，
// 预估 0（价格缺失时由配置缺口路径显式阻塞）；chat 类操作用保守默认值。
func budgetEstimateUnits(operation string) (int, int) {
	switch operation {
	case "analyze":
		return 100_000, 20_000
	case "episode_digest":
		return 120_000, 30_000
	case "highlight":
		return 60_000, 10_000
	case "keypoint_quality":
		return 40_000, 8_000
	case "claim_review":
		return 50_000, 10_000
	case "digest_rewrite":
		return 30_000, 6_000
	default:
		return 0, 0
	}
}

// holdJobBudget 调用前预算预占（B04）。非付费任务类型直接放行。
func (w *Worker) holdJobBudget(ctx context.Context, job *models.ProcessingJob) error {
	switch job.JobType {
	case models.JobTranscribe, models.JobAnalyze, models.JobDigest, models.JobHighlight, models.JobKeypointQuality, models.JobDigestRewrite, models.JobIdeationDiagnosis, models.JobClaimReview:
	default:
		return nil
	}
	tc, err := w.taskConfigFor(job)
	if err != nil {
		return err
	}
	// 预算针对将要实际调用的生效模型（配置为空时用 Provider 官方默认）。
	model := provider.EffectiveModel(tc.Provider, tc.Model, string(job.JobType))
	in, out := budgetEstimateUnits(string(job.JobType))
	if _, err := w.store.HoldBudget(ctx, job.ID, string(job.JobType), job.Automated, tc.Provider, model, in, out); err != nil {
		return fmt.Errorf("预算检查拒绝任务: %w", err)
	}
	return nil
}

// jobReceiptUsage 返回任务本次执行已落账的 receipt 数与已知费用合计。
func (w *Worker) jobReceiptUsage(ctx context.Context, jobID string) (int, int64) {
	var n int64
	var cost sql.NullFloat64
	if err := w.store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE receipt_id LIKE ? AND estimated_cost IS NOT NULL`,
		jobID+":%").Scan(&n, &cost); err != nil {
		return 0, 0
	}
	return int(n), int64(cost.Float64)
}

// settleJobBudget 成功结算：实际费用 = 已落账 receipt 的已知费用合计。
func (w *Worker) settleJobBudget(ctx context.Context, job *models.ProcessingJob) {
	_, actual := w.jobReceiptUsage(ctx, job.ID)
	if err := w.store.SettleBudget(ctx, job.ID, actual); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("任务 %s 预算结算失败: %v", job.ID, err)
	}
}

// releaseJobBudget 失败释放：已发生远端调用（有 receipt）时标记结果未知，否则直接解除。
func (w *Worker) releaseJobBudget(ctx context.Context, job *models.ProcessingJob) {
	receipts, _ := w.jobReceiptUsage(ctx, job.ID)
	if err := w.store.ReleaseBudget(ctx, job.ID, receipts > 0); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("任务 %s 预算释放失败: %v", job.ID, err)
	}
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
	// B04：付费任务在构建 Provider 之前做全局预算检查并预占在途预估；
	// 预算不足/未配价格/日限额超限以显式错误失败（可见原因，不无限重试）。
	if err := w.holdJobBudget(ctx, job); err != nil {
		return err
	}
	bundle, err := w.bundleFor(job)
	if err != nil {
		return err
	}
	switch job.JobType {
	case models.JobTranscribe:
		return w.doTranscribe(ctx, job, bundle)
	case models.JobAnalyze:
		return w.doAnalyze(ctx, job, bundle)
	case models.JobDigest:
		return w.doDigest(ctx, job, bundle)
	case models.JobHighlight:
		return w.doHighlightJob(ctx, job, bundle)
	case models.JobNarration:
		return w.doNarrationJob(ctx, job, bundle)
	case models.JobKeypointQuality:
		return w.doKeypointQualityJob(ctx, job, bundle)
	case models.JobDigestRewrite:
		return w.doDigestRewriteJob(ctx, job, bundle)
	case models.JobDJPlan:
		return w.doDJPlanJob(ctx, job, bundle)
	case models.JobIdeationDiagnosis:
		return w.doIdeationDiagnosisJob(ctx, job, bundle)
	case models.JobClaimReview:
		return w.doClaimReviewJob(ctx, job, bundle)
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
	// B03：产物血缘记录响应报告的实际模型；未报告时不冒充配置值，记 unknown。
	transcribeModel := result.Model
	if transcribeModel == "" {
		transcribeModel = "unknown"
	}
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindTranscript, bundle.Transcription.Name(), transcribeModel, "1", job.ID, string(payload))
	if err != nil {
		return fmt.Errorf("创建转录版本: %w", err)
	}
	if err := w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript, version); err != nil {
		return fmt.Errorf("设置当前转录版本: %w", err)
	}
	w.setSourceStatus(ctx, job, models.StatusTranscribed)

	w.recordCallUsage(ctx, job, "transcription", bundle.Transcription.Name(), transcribeModel, result.Usage)

	// 4) 入队分析任务（已有进行中 analyze 则不重复创建），并继承处理深度快照（B08）。
	analyzeJob, err := w.store.EnqueueAnalyzeForIngestion(ctx, job.SourceType, job.SourceID, job.Automated)
	if err != nil {
		return err
	}
	if analyzeJob != nil {
		if err := w.store.InheritJobInputSnapshot(ctx, job.ID, analyzeJob.ID); err != nil {
			return fmt.Errorf("继承处理深度快照: %w", err)
		}
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
	analysis, err := bundle.Analysis.Analyze(payload.Text, payload.Segments)
	if err != nil {
		return fmt.Errorf("分析: %w", err)
	}
	validated, err := provider.ValidateCard(analysis.Card, payload.Segments)
	if err != nil {
		return fmt.Errorf("证据校验: %w", err)
	}

	contentJSON, _ := json.Marshal(validated)
	analysisModel := analysis.Model
	if analysisModel == "" {
		analysisModel = "unknown"
	}
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindKnowledgeCard, bundle.Analysis.Name(), analysisModel, "1", job.ID, string(contentJSON))
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
	if stats, err := w.store.IndexKeyPoints(ctx, job.SourceType, job.SourceID, sourceTitle, version, validated, payload.Segments); err != nil {
		log.Printf("任务 %s KeyPoint 索引刷新失败（不阻塞）: %v", job.ID, err)
	} else if stats != nil && (stats.Updated > 0 || stats.New > 0 || stats.Staled > 0) {
		log.Printf("任务 %s 重点协调: kept=%d updated=%d new=%d staled=%d removed=%d changes=%d",
			job.ID, stats.Kept, stats.Updated, stats.New, stats.Staled, stats.Removed, stats.MaterialChanges)
	}

	w.recordCallUsage(ctx, job, "analysis", bundle.Analysis.Name(), analysisModel, analysis.Usage)

	// B08：统一编排——分析完成后按深度/来源衔接高光任务（高光成功后再衔接解说）：
	//   手动处理：始终衔接（替代旧内联高光/解说）；
	//   自动处理：knowledge_dj 深度衔接；knowledge 深度与旧无快照自动任务止于 KeyPoint。
	// 衔接失败必须可见（B09 进度页展示），但不回抹已完成的卡片。
	if !job.Automated || jobSnapshotDepth(ctx, w, job) == string(models.DepthKnowledgeDJ) {
		if _, err := w.store.EnqueueHighlightJobWithChain(ctx, job.SourceType, job.SourceID, true); err != nil {
			log.Printf("任务 %s 衔接高光任务失败: %v", job.ID, err)
		}
	}
	return nil
}

// jobSnapshotDepth 读取任务冻结快照中的处理深度；无快照（旧任务）返回 knowledge 语义的空串。
func jobSnapshotDepth(ctx context.Context, w *Worker, job *models.ProcessingJob) string {
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return ""
	}
	var snapshot struct {
		ProcessingDepth string `json:"processing_depth"`
	}
	_ = json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot)
	return snapshot.ProcessingDepth
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
	highlightModel := raw.Model
	if highlightModel == "" {
		highlightModel = "unknown"
	}
	contentJSON, _ := json.Marshal(validated)
	version, err := w.store.CreateArtifactVersion(ctx, job.SourceType, job.SourceID,
		store.KindHighlight, bundle.Highlight.Name(), highlightModel, "1", job.ID, string(contentJSON))
	if err != nil {
		return fmt.Errorf("创建高光版本: %w", err)
	}
	w.recordCallUsage(ctx, job, "highlight", bundle.Highlight.Name(), highlightModel, raw.Usage)
	return w.store.SetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight, version)
}

// HighlightName 返回高光 Provider 名（供 worker 记录）。
func (w *Worker) HighlightName() string { return "groq" }

// ensureEvidence 确保 Source 的标准化 EvidenceAudio 已持久化并校验，返回文件路径。
// 幂等：evidence_audio 已记录且文件存在（按 sha256 校验）时直接复用。
func (w *Worker) ensureEvidence(ctx context.Context, job *models.ProcessingJob) (string, error) {
	rel := fmt.Sprintf("%s_%s.mp3", job.SourceType, job.SourceID)
	path := filepath.Join(w.evidenceDir, rel)
	if reusable, ok := w.reusableEvidencePath(ctx, job, path); ok {
		return reusable, nil
	}
	rawPath, cleanup, err := w.fetchRawAudio(ctx, job)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return w.transcodeEvidence(ctx, job, rawPath, path, rel)
}

func (w *Worker) reusableEvidencePath(ctx context.Context, job *models.ProcessingJob, path string) (string, bool) {
	ev, err := w.store.GetEvidenceAudio(ctx, job.SourceType, job.SourceID)
	if err != nil || ev.Status != "ready" {
		return "", false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= 0 || fi.Size() > maxTranscriptionUploadBytes {
		return "", false
	}
	hash, err := filehash.SHA256(path)
	if err != nil || hash != ev.SHA256 {
		return "", false
	}
	return path, true
}

func (w *Worker) transcodeEvidence(ctx context.Context, job *models.ProcessingJob, rawPath, path, rel string) (string, error) {
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
		if err := w.store.DeleteEpisodeDigestsForSource(ctx, p.SourceType, p.SourceID); err != nil {
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
	if bundle.Narration == nil || !bundle.Narration.Available() {
		log.Printf("任务 %s Narration Provider 不可用，跳过合成（不阻塞）", job.ID)
		return nil
	}
	hs, err := w.currentHighlightSet(ctx, job)
	if err != nil {
		return err
	}
	if len(hs.Highlights) == 0 {
		return nil
	}
	existing, err := w.store.ListCurrentNarrationsForSource(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return fmt.Errorf("读取已有 Narration 失败: %w", err)
	}
	providerName := bundle.Narration.Name()
	for _, h := range hs.Highlights {
		if err := w.narrateHighlight(ctx, job, bundle.Narration, providerName, h, existing); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) currentHighlightSet(ctx context.Context, job *models.ProcessingJob) (provider.HighlightSet, error) {
	version, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight)
	if err != nil {
		return provider.HighlightSet{}, fmt.Errorf("读取当前高光版本失败: %w", err)
	}
	var highlights provider.HighlightSet
	if err := json.Unmarshal([]byte(version.Payload), &highlights); err != nil {
		return provider.HighlightSet{}, fmt.Errorf("解析高光载荷失败: %w", err)
	}
	return highlights, nil
}

func (w *Worker) narrateHighlight(ctx context.Context, job *models.ProcessingJob, narration provider.NarrationProvider, providerName string, highlight provider.Highlight, existing map[string]*store.NarrationRow) error {
	if highlight.ID == "" || highlight.Gist == "" {
		return nil
	}
	if current, ok := existing[highlight.ID]; ok && current.Provider == providerName {
		return nil
	}
	nextVersion := w.nextNarrationVersion(ctx, job.SourceType, job.SourceID, highlight.ID)
	relPath := fmt.Sprintf("%s_%s_%s_%d.wav", job.SourceType, job.SourceID, highlight.ID, nextVersion)
	outPath := filepath.Join(w.narrationDir, relPath)
	if err := os.MkdirAll(w.narrationDir, 0o755); err != nil {
		return fmt.Errorf("创建 narrations 目录: %w", err)
	}
	result, err := narration.Synthesize(highlight.Gist, "", outPath)
	if err != nil {
		log.Printf("任务 %s Highlight %s 的 Narration 合成失败（跳过该段）: %v", job.ID, highlight.ID, err)
		return nil
	}
	duration, _ := audioDuration(outPath)
	if duration < 0 {
		duration = 0
	}
	if _, err := w.store.CreateNarration(ctx, job.SourceType, job.SourceID, highlight.ID, result.Voice, result.Model, relPath, duration, result.CharCount, providerName); err != nil {
		log.Printf("任务 %s Highlight %s 的 Narration 写库失败（音频已合成）: %v", job.ID, highlight.ID, err)
	}
	// B03：本地 TTS 是确定零成本调用（无远端计费单位），按合成字符计量、费用记 0。
	_ = w.store.RecordUsageReceipt(ctx, models.UsageReceipt{
		ReceiptID:   fmt.Sprintf("%s:narration:%s", job.ID, highlight.ID),
		AttemptID:   fmt.Sprintf("%s:%d", job.ID, job.AttemptCount),
		Operation:   "narration",
		Provider:    providerName,
		Model:       result.Model,
		InputUnits:  result.CharCount,
		OutputUnits: int(duration),
		CostKnown:   true,
	})
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

// recordCallUsage 以任务 attempt + 远端调用身份记账（B03）。
// receipt 唯一：同一任务同一阶段的重复记账（重放/恢复）不重复累计；
// 价格未知时记 NULL（不显示为免费），本地零成本调用记确定 0。
func (w *Worker) recordCallUsage(ctx context.Context, job *models.ProcessingJob, operation, providerName, model string, usage provider.TaskUsage) {
	receipt := fmt.Sprintf("%s:%s", job.ID, operation)
	receiptErr := w.store.RecordUsageReceipt(ctx, models.UsageReceipt{
		ReceiptID:   receipt,
		AttemptID:   fmt.Sprintf("%s:%d", job.ID, job.AttemptCount),
		Operation:   operation,
		Provider:    providerName,
		Model:       model,
		InputUnits:  usage.InputUnits,
		OutputUnits: usage.OutputUnits,
	})
	if receiptErr == nil && (usage.InputUnits > 0 || usage.OutputUnits > 0) {
		costCents, known, err := w.store.ResolveUsageCost(ctx, providerName, model, usage.InputUnits, usage.OutputUnits)
		if err == nil && known {
			// 已知价格：补写费用到同一 receipt 行（幂等更新，非第二次调用）。
			_ = w.store.UpdateUsageCost(ctx, receipt, costCents)
		}
	}
	if receiptErr != nil {
		log.Printf("任务 %s 记账失败（operation=%s）: %v", job.ID, operation, receiptErr)
	}
}
