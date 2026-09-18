package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// fakeDigestWriter 可脚本化的 DigestWriter fake。
type fakeDigestWriter struct {
	compose    *provider.DigestWritingResult
	composeErr error
	weave      *provider.DigestWeaveResult
	weaveErr   error
}

func (f *fakeDigestWriter) ComposeDigest(ctx context.Context, req provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	if f.composeErr != nil {
		return nil, f.composeErr
	}
	return f.compose, nil
}
func (f *fakeDigestWriter) WeaveDigestFacts(ctx context.Context, req provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	if f.weaveErr != nil {
		return nil, f.weaveErr
	}
	return f.weave, nil
}
func (f *fakeDigestWriter) Name() string { return "fake-digest" }

// fakeDigestSearch 可脚本化搜索 fake。
type fakeDigestSearch struct {
	results []provider.DigestSearchResult
	err     error
}

func (f *fakeDigestSearch) Search(ctx context.Context, query string) ([]provider.DigestSearchResult, error) {
	return f.results, f.err
}
func (f *fakeDigestSearch) Name() string { return "fake-search" }

// fakeDigestRewriter 渠道改写 fake。
type fakeDigestRewriter struct {
	text string
	err  error
}

func (f *fakeDigestRewriter) RewriteDigest(ctx context.Context, req provider.DigestRewriteRequest) (*provider.DigestRewriteResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &provider.DigestRewriteResult{Text: f.text}, nil
}
func (f *fakeDigestRewriter) Name() string { return "fake-rewriter" }

// seedDigestTranscript 写一份带 Segment 的转录版本（digest 的素材前提）。
func seedDigestTranscript(t *testing.T, s *store.Store, sourceType models.SourceType, sourceID string) {
	t.Helper()
	ctx := context.Background()
	seedJob, err := s.EnqueueJob(ctx, sourceType, sourceID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	// seedJob 仅用于满足 artifact_versions.job_id 外键；先置 running 再置终态，避免被 worker 领取。
	if _, err := s.MarkJobRunning(ctx, seedJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJobSucceeded(ctx, seedJob.ID); err != nil {
		t.Fatal(err)
	}
	// 释放 source 处理状态，使后续 digest job 可入队（EnqueueJob 乐观锁只认 unprocessed/failed）。
	if err := s.UpdateEpisodeStatus(ctx, sourceID, models.StatusUnprocessed); err != nil {
		t.Fatal(err)
	}
	tp := provider.TranscriptPayload{
		Language: "zh",
		Text:     "本期讨论主权基金的配置变化。利率环境影响估值。",
		Segments: []provider.Segment{
			{ID: "seg-0001", Start: 0, End: 30, Text: "本期讨论主权基金的配置变化"},
			{ID: "seg-0002", Start: 30, End: 60, Text: "利率环境影响估值"},
		},
	}
	payload, _ := json.Marshal(tp)
	if _, err := s.CreateArtifactVersion(ctx, sourceType, sourceID, store.KindTranscript, "fake", "m", "1", seedJob.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript, 1); err != nil {
		t.Fatal(err)
	}
}

func validDraftBlocks() []provider.DigestBlockDraft {
	return []provider.DigestBlockDraft{
		{Type: provider.DigestBlockParaphraseStr, Text: "本期讨论了主权基金的配置变化。", Citations: []string{"seg-0001"}},
		{Type: provider.DigestBlockAIExpansionStr, Text: "AI 展开：可以把配置变化理解成调整船帆。"},
	}
}

// TestDoDigest_HappyPath 无缺口：初稿过门禁 → 不可变修订落库 → 渠道改写落库。
func TestDoDigest_HappyPath(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter:   &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "读懂主权基金", Blocks: validDraftBlocks()}},
			DigestRewriter: &fakeDigestRewriter{text: "钩子\n正文短句\n#基金 #配置"},
		}, nil
	}
	if _, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("digest job 失败: %v", err)
	}

	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || d.Version != 1 || d.Degraded {
		t.Fatalf("精读文修订未落库：%v %+v", err, d)
	}
	blocks, _ := s.ListDigestBlocks(ctx, d.ID)
	if len(blocks) != 2 || blocks[0].Type != models.DigestBlockParaphrase {
		t.Fatalf("内容块不符：%+v", blocks)
	}
	rw, err := s.GetDigestRewrite(ctx, d.ID, models.DigestChannelXiaohongshu)
	if err != nil || rw.Text == "" {
		t.Fatalf("渠道改写未落库：%v %+v", err, rw)
	}
	// 无缺口 → 无搜索源记录
	rows, _ := s.ListDigestSearchSources(ctx, d.ID)
	if len(rows) != 0 {
		t.Fatalf("无缺口不应有落源行：%+v", rows)
	}
}

// TestDoDigest_GateReject 转述块缺 Citation：门禁失败，job 失败，无修订落库。
func TestDoDigest_GateReject(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	bad := validDraftBlocks()
	bad[0].Citations = nil
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "T", Blocks: bad}},
		}, nil
	}
	digestJob, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest)
	if err != nil || digestJob == nil {
		t.Fatalf("入队: %v %v", digestJob, err)
	}
	_ = w.ProcessOne(ctx) // 业务失败被标 failed，不算周期错误
	got, _ := s.GetJob(ctx, digestJob.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("门禁失败应使 job failed，实际 %s（%v）", got.Status, got.LastError)
	}
	if _, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("失败时不应落库：%v", err)
	}
}

// TestDoDigest_SearchUnavailableDegraded 搜索不可用 → C1 降级：仍成文，degraded=1，缺口留档。
func TestDoDigest_SearchUnavailableDegraded(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{
				Title: "T", Blocks: validDraftBlocks(), FactGaps: []string{"2024 行业规模"},
			}},
			DigestSearch: &fakeDigestSearch{err: errors.New("配额耗尽")},
		}, nil
	}
	if _, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("降级生成不应失败: %v", err)
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || !d.Degraded {
		t.Fatalf("应为降级修订：%v %+v", err, d)
	}
	gaps, _ := s.ListDigestFactGaps(ctx, d.ID)
	if len(gaps) != 1 || gaps[0].Text != "2024 行业规模" {
		t.Fatalf("缺口应留档：%+v", gaps)
	}
}

// TestDoDigest_SearchIngestsDocument 搜索命中 → 落源（幂等复用已存 Document）→ 补织块过门禁 → ⑥b 侧栏行。
// 网络路径用公网样式 URL + 预置同 origin_url 的 Document，验证幂等复用与补织链路；
// 真实抓取由 safehttp SSRF 防护保障（产品红线，不为测试放宽）。
func TestDoDigest_SearchIngestsDocument(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	// 预置落源 Document：正文一个段落 = 一个稳定 Segment（doc-seg-1 由 DocumentSegments 生成）
	const docURL = "https://reports.example.com/industry-2024"
	preset, err := s.CreateWebDocument(ctx, "行业报道", docURL, "2024 年行业规模约 1.2 万亿元，增长率为 8%。")
	if err != nil {
		t.Fatal(err)
	}
	segID := ""
	for _, seg := range store.DocumentSegments(preset) {
		segID = seg.ID
		break
	}
	if segID == "" {
		t.Fatal("预置文档应有 Segment")
	}

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &fakeDigestWriter{
				compose: &provider.DigestWritingResult{Title: "T", Blocks: validDraftBlocks(), FactGaps: []string{"2024 行业规模"}},
				weave: &provider.DigestWeaveResult{Blocks: []provider.DigestBlockDraft{
					{Type: provider.DigestBlockCitedFactStr, Text: "据行业报道，增长率 8%。", Citations: []string{segID}, TargetSourceID: preset.ID},
				}},
			},
			DigestSearch: &fakeDigestSearch{results: []provider.DigestSearchResult{{URL: docURL}}},
		}, nil
	}
	if _, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("落源 digest 失败: %v", err)
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListDigestSearchSources(ctx, d.ID)
	if len(rows) != 1 || rows[0].DocumentID != preset.ID || rows[0].Title != "行业报道" {
		t.Fatalf("落源行不符：%+v", rows)
	}
	// 幂等：不新建第二个 Document
	if again, err := s.GetDocumentByOriginURL(ctx, docURL); err != nil || again.ID != preset.ID {
		t.Fatalf("落源应幂等复用：%v %+v", err, again)
	}
	// 补织块入库且类型正确
	blocks, _ := s.ListDigestBlocks(ctx, d.ID)
	found := false
	for _, b := range blocks {
		if b.Type == models.DigestBlockCitedFact {
			found = true
		}
	}
	if !found {
		t.Fatalf("应含引用事实块：%+v", blocks)
	}
	// 已消解缺口不留档
	gaps, _ := s.ListDigestFactGaps(ctx, d.ID)
	if len(gaps) != 0 {
		t.Fatalf("已消解缺口不应留档：%+v", gaps)
	}
}

// TestDoDigest_NoTranscript 未转录的 Source 直接入队 digest：失败且报可读错误。
func TestDoDigest_NoTranscript(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "T", Blocks: validDraftBlocks()}}}, nil
	}
	noTrJob, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest)
	if err != nil || noTrJob == nil {
		t.Fatalf("入队: %v %v", noTrJob, err)
	}
	_ = w.ProcessOne(ctx)
	got, _ := s.GetJob(ctx, noTrJob.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("无转录版本应 job failed，实际 %s", got.Status)
	}
}

// TestDoDigest_DocumentSource G01：Document Source 从入队跑到正文——
// 素材来自文档段落（不再误查 Transcript），块引用文档段落 ID。
func TestDoDigest_DocumentSource(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	doc, err := s.CreatePastedDocument(ctx, "架构文档", "第一段讲架构分层。\n\n第二段讲数据一致性。")
	if err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{
				Title: "读懂架构文档",
				Blocks: []provider.DigestBlockDraft{
					{Type: provider.DigestBlockParaphraseStr, Text: "文档先讲分层。", Citations: []string{doc.ID + "-p0001"}},
				},
			}},
			DigestRewriter: &fakeDigestRewriter{text: "钩子"},
		}, nil
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceDocument, doc.ID)
	if err != nil || job == nil {
		t.Fatalf("文档入队失败: %v %v", job, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("文档精读失败: %v", err)
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceDocument, doc.ID)
	if err != nil || d.Title != "读懂架构文档" {
		t.Fatalf("文档精读应落库: %v %+v", err, d)
	}
	blocks, _ := s.ListDigestBlocks(ctx, d.ID)
	if len(blocks) != 1 || len(blocks[0].Citations) == 0 || blocks[0].Citations[0] != doc.ID+"-p0001" {
		t.Fatalf("块应引用文档段落: %+v", blocks)
	}
}

// TestDoDigest_FrozenInput G01：入队后改笔记/重分析不改变本次输入。
func TestDoDigest_FrozenInput(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	// 入队前的一条笔记 → 属于冻结输入
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "source_note",
		Content: "入队前笔记", CitationsJSON: `["seg-0001"]`,
	}); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	// 入队后再写一条新笔记 → 不属于本次输入
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "owner_reflection",
		Content: "入队后笔记",
	}); err != nil {
		t.Fatal(err)
	}
	var captured []provider.DigestOwnerNote
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "冻结输入", Blocks: validDraftBlocks()}},
		}, nil
	}
	// 通过包装 writer 捕获请求里的笔记
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &capturingWriter{inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "冻结输入", Blocks: validDraftBlocks()}}, captured: &captured},
		}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("精读失败: %v", err)
	}
	found := false
	for _, n := range captured {
		if n.Text == "入队前笔记" {
			found = true
		}
		if n.Text == "入队后笔记" {
			t.Fatal("入队后的笔记不应进入本次输入")
		}
	}
	if !found {
		t.Fatalf("入队前笔记应在冻结输入内: %+v", captured)
	}
}

// capturingWriter 捕获 Compose 请求中的笔记（G01 冻结断言用）。
type capturingWriter struct {
	inner    provider.DigestWriterProvider
	captured *[]provider.DigestOwnerNote
}

func (c *capturingWriter) ComposeDigest(ctx context.Context, req provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	*c.captured = req.Notes
	return c.inner.ComposeDigest(ctx, req)
}
func (c *capturingWriter) WeaveDigestFacts(ctx context.Context, req provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	return c.inner.WeaveDigestFacts(ctx, req)
}
func (c *capturingWriter) Name() string { return "capturing" }

// TestDoDigest_DocumentTooLongExplicit G01：过长文档显式失败，不静默截尾。
func TestDoDigest_DocumentTooLongExplicit(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	content := ""
	for i := 0; i < 601; i++ {
		content += fmt.Sprintf("段落%d。\n\n", i)
	}
	doc, err := s.CreatePastedDocument(ctx, "超长文档", content)
	if err != nil {
		t.Fatal(err)
	}
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{DigestWriter: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "x", Blocks: validDraftBlocks()}}}, nil
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceDocument, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusFailed || got.LastError == nil || !strings.Contains(*got.LastError, "文档过长") {
		t.Fatalf("过长文档应显式失败: %+v", got)
	}
}

// TestDoDigest_ComposeCheckpointReuse G02：compose 断点复用——重试零模型调用，
// 修订原子落库（含落源与缺口）。
func TestDoDigest_ComposeCheckpointReuse(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	calls := 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &countingDigestWriter{
				inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "断点复用", Blocks: validDraftBlocks()}},
				count: &calls,
			},
			DigestRewriter: &fakeDigestRewriter{text: "渠道"},
		}, nil
	}
	job, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobDigest,
		IntentID: "digest:ckpt", InputSnapshotJSON: `{"snapshot_id":""}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 注入"已 compose"断点，模拟 compose 成功后、发布前进程中断。
	draft := &provider.DigestWritingResult{Title: "断点复用", Blocks: validDraftBlocks()}
	cpData, _ := json.Marshal(digestCheckpoint{Stage: "composed", Draft: draft})
	if _, err := s.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cpData)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetRunningOnStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("断点重试失败: %v", err)
	}
	if calls != 0 {
		t.Fatalf("断点复用不得重复 compose: %d", calls)
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || d.Title != "断点复用" {
		t.Fatalf("修订应原子落库: %v %+v", err, d)
	}
}

// countingDigestWriter 统计 compose 调用次数。
type countingDigestWriter struct {
	inner provider.DigestWriterProvider
	count *int
}

func (c *countingDigestWriter) ComposeDigest(ctx context.Context, req provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	*c.count++
	return c.inner.ComposeDigest(ctx, req)
}
func (c *countingDigestWriter) WeaveDigestFacts(ctx context.Context, req provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	return c.inner.WeaveDigestFacts(ctx, req)
}
func (c *countingDigestWriter) Name() string { return "counting" }

// TestPublishEpisodeDigest_AtomicWithSourcesAndGaps 原子发布：检索落源与缺口随修订同事务可见。
func TestPublishEpisodeDigest_AtomicWithSourcesAndGaps(t *testing.T) {
	s, _ := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	d, err := s.PublishEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: sourceID, Title: "原子", Provider: "p", Model: "m", PromptVersion: "v",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "正文", Citations: []string{"seg-0001"}},
	}, []models.DigestSearchSource{
		{Query: "q", URL: "https://ex.com/a", Title: "来源A", DocumentID: "doc-a", Status: "pending"},
	}, []models.DigestFactGap{
		{Text: "缺口1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListDigestSearchSources(ctx, d.ID)
	gaps, _ := s.ListDigestFactGaps(ctx, d.ID)
	if len(rows) != 1 || len(gaps) != 1 {
		t.Fatalf("落源与缺口应随修订原子可见: %+v %+v", rows, gaps)
	}
	// current 可立即读取完整修订
	cur, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || cur.ID != d.ID {
		t.Fatalf("current 应为该修订: %v", err)
	}
}

// TestDigestRewriteJob_OnlyRewrites G06：重试只执行改写——compose/search 均为零；
// 同输入复用不重复改写；不同修订不串结果。
func TestDigestRewriteJob_OnlyRewrites(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	composeCalls, rewriteCalls := 0, 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &countingDigestWriter{
				inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "主文", Blocks: validDraftBlocks()}},
				count: &composeCalls,
			},
			DigestRewriter: &countingRewriter{inner: &fakeDigestRewriter{text: "渠道文案"}, count: &rewriteCalls},
		}, nil
	}
	// 先产出主文修订
	if _, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	composeCalls, rewriteCalls = 0, 0

	// 渠道改写独立任务：compose 必须为零。
	rw, err := s.EnqueueDigestRewriteJob(ctx, d.ID, string(models.DigestChannelXiaohongshu))
	if err != nil || rw == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("改写失败: %v", err)
	}
	if composeCalls != 0 {
		t.Fatalf("渠道重写不得触发 compose: %d", composeCalls)
	}
	if rewriteCalls != 1 {
		t.Fatalf("改写应恰好一次: %d", rewriteCalls)
	}
	// 相同输入再次入队 → 输入指纹一致，复用产物（零改写调用）。
	rw2, err := s.EnqueueDigestRewriteJob(ctx, d.ID, string(models.DigestChannelXiaohongshu))
	if err != nil || rw2 == nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if rewriteCalls != 1 {
		t.Fatalf("同输入应复用既有改写: %d", rewriteCalls)
	}
	// 新修订 → 渠道产物按修订独立存储，不沿用旧渠道结果。
	if _, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if d2.ID == d.ID || d2.Version != 2 {
		t.Fatalf("新修订应独立: %+v", d2)
	}
	// v1 的渠道行仍指向 v1（不被改写到 v2）
	v1rw, err := s.GetDigestRewrite(ctx, d.ID, models.DigestChannelXiaohongshu)
	if err != nil || v1rw.DigestID != d.ID {
		t.Fatalf("旧渠道产物应归属旧修订: %+v %v", v1rw, err)
	}
}

// countingRewriter 统计改写调用次数。
type countingRewriter struct {
	inner provider.DigestRewriteProvider
	count *int
}

func (c *countingRewriter) RewriteDigest(ctx context.Context, req provider.DigestRewriteRequest) (*provider.DigestRewriteResult, error) {
	*c.count++
	return c.inner.RewriteDigest(ctx, req)
}

// TestDoDigest_FrozenNoteContents R06：入队冻结笔记确切文本/类型/锚点；
// 排队后编辑、删除、新增笔记均不改变本次输入；空选择保持为空；
// 旧数据（无冻结文本）按 NoteIDs 兼容解析，不伪造冻结文本。
func TestDoDigest_FrozenNoteContents(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "source_note",
		Content: "原始文本", CitationsJSON: `["seg-0001"]`,
	}); err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	// 快照应含冻结文本与锚点。
	exec, err := s.GetJobExecution(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var snap digestInputSnapshot
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.NotesRecorded || len(snap.Notes) != 1 || snap.Notes[0].Content != "原始文本" ||
		snap.Notes[0].Kind != "source_note" || len(snap.Notes[0].Anchors) != 1 || snap.Notes[0].Anchors[0] != "seg-0001" {
		t.Fatalf("入队应冻结确切笔记输入: %+v", snap)
	}

	// 排队后：编辑内容、删除笔记、再新增一条 → 均不改变本次输入。
	noteID := snap.Notes[0].ID
	if _, err := s.UpdateOwnerNote(ctx, noteID, "编辑后的文本", `["seg-0001"]`, "", snap.Notes[0].Revision); err != nil {
		t.Fatal(err)
	}
	// 笔记无 Owner 删除入口（存储层无 API）；以 SQL 删除模拟唯一现实的失效场景。
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM owner_notes WHERE id = ?`, noteID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "owner_reflection",
		Content: "入队后新增",
	}); err != nil {
		t.Fatal(err)
	}

	var captured []provider.DigestOwnerNote
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &capturingWriter{inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "冻结", Blocks: validDraftBlocks()}}, captured: &captured},
		}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if len(captured) != 1 || captured[0].NoteID != noteID || captured[0].Text != "原始文本" || captured[0].Kind != "source_note" {
		t.Fatalf("应按冻结文本生成，不受编辑/删除/新增影响: %+v", captured)
	}
}

// TestDoDigest_EmptyNoteSelectionStaysEmpty R06：空选择显式记录且保持为空；
// 入队后新增笔记不进入本次输入。
func TestDoDigest_EmptyNoteSelectionStaysEmpty(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	exec, _ := s.GetJobExecution(ctx, job.ID)
	var snap digestInputSnapshot
	_ = json.Unmarshal([]byte(exec.InputSnapshotJSON), &snap)
	if !snap.NotesRecorded || len(snap.Notes) != 0 {
		t.Fatalf("空选择应显式记录为空: %+v", snap)
	}
	if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "owner_reflection",
		Content: "入队后笔记",
	}); err != nil {
		t.Fatal(err)
	}
	var captured []provider.DigestOwnerNote
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &capturingWriter{inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "空选择", Blocks: validDraftBlocks()}}, captured: &captured},
		}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("空选择应保持为空: %+v", captured)
	}
}

// TestDoDigest_LegacySnapshotResolvesNoteIDs R06：旧任务（无冻结文本）按 NoteIDs
// 从当前库解析——不伪造冻结文本；读取行为与旧版本一致。
func TestDoDigest_LegacySnapshotResolvesNoteIDs(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "source_note",
		Content: "旧任务笔记", CitationsJSON: `["seg-0001"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	// 模拟旧快照：仅 NoteIDs，无 notes_recorded/notes。
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET input_snapshot_json = json_remove(input_snapshot_json, '$.notes', '$.notes_recorded') WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	var captured []provider.DigestOwnerNote
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &capturingWriter{inner: &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "旧任务", Blocks: validDraftBlocks()}}, captured: &captured},
		}, nil
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].NoteID != note.ID || captured[0].Text != "旧任务笔记" {
		t.Fatalf("旧任务应按 NoteIDs 从当前库解析: %+v", captured)
	}
}

// ---- R08：精读阶段恢复与渠道改写 ----

// resumeCountingSearch / resumeCountingWriter 带调用计数的 fake（恢复断点断言用）。
type resumeCountingSearch struct {
	inner fakeDigestSearch
	calls *int
}

func (f *resumeCountingSearch) Search(ctx context.Context, query string) ([]provider.DigestSearchResult, error) {
	*f.calls++
	return f.inner.Search(ctx, query)
}
func (f *resumeCountingSearch) Name() string { return "resume-search" }

type resumeCountingWriter struct {
	inner *fakeDigestWriter
	calls *int
}

func (f *resumeCountingWriter) ComposeDigest(ctx context.Context, req provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	*f.calls++
	return f.inner.ComposeDigest(ctx, req)
}
func (f *resumeCountingWriter) WeaveDigestFacts(ctx context.Context, req provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	*f.calls++
	return f.inner.WeaveDigestFacts(ctx, req)
}
func (f *resumeCountingWriter) Name() string { return "resume-writer" }

// TestDigestResolveGaps_ResumeFromCheckpoint R08：逐项检索落源与补织的断点恢复——
// 同一 checkpoint 下重启重试，不重复 Search、不重复落源、不重复补织。
func TestDigestResolveGaps_ResumeFromCheckpoint(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	const docURL = "https://reports.example.com/resume-2024"
	preset, err := s.CreateWebDocument(ctx, "行业报道", docURL, "2024 年行业规模约 1.2 万亿元。")
	if err != nil {
		t.Fatal(err)
	}
	var segID string
	for _, seg := range store.DocumentSegments(preset) {
		segID = seg.ID
		break
	}

	searchCalls, writerCalls := 0, 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter: &resumeCountingWriter{inner: &fakeDigestWriter{weave: &provider.DigestWeaveResult{Blocks: []provider.DigestBlockDraft{
				{Type: provider.DigestBlockCitedFactStr, Text: "据行业报道，规模约 1.2 万亿元。", Citations: []string{segID}, TargetSourceID: preset.ID},
			}}}, calls: &writerCalls},
			DigestSearch: &resumeCountingSearch{inner: fakeDigestSearch{results: []provider.DigestSearchResult{{URL: docURL}}}, calls: &searchCalls},
		}, nil
	}
	job := &models.ProcessingJob{ID: "job-resume", SourceType: models.SourceEpisode, SourceID: sourceID, JobType: models.JobDigest}
	draft := &provider.DigestWritingResult{Title: "T", Blocks: validDraftBlocks(), FactGaps: []string{"2024 行业规模"}}
	blocks := digestTruncateQuotes(draft.Blocks)
	bundle, err := w.bundleFor(job)
	if err != nil {
		t.Fatal(err)
	}

	cp := digestCheckpoint{Stage: "composed_validated", Draft: draft}
	woven1, rows1, gaps1, ok1 := w.digestResolveGaps(ctx, job, bundle, draft, blocks, &cp)
	if !ok1 || len(rows1) != 1 || len(woven1) != 1 || len(gaps1) != 0 {
		t.Fatalf("首次消解应落源并补织: ok=%v rows=%d woven=%d gaps=%d", ok1, len(rows1), len(woven1), len(gaps1))
	}
	if searchCalls != 1 || writerCalls != 1 {
		t.Fatalf("首次应恰好一次搜索与补织: search=%d writer=%d", searchCalls, writerCalls)
	}
	// 模拟重启：同一持久化断点下重试——不重复 Search / 落源 / 补织。
	woven2, rows2, gaps2, ok2 := w.digestResolveGaps(ctx, job, bundle, draft, blocks, &cp)
	if !ok2 || len(rows2) != 1 || len(woven2) != 1 || len(gaps2) != 0 {
		t.Fatalf("恢复消解结果应一致: %+v", rows2)
	}
	if searchCalls != 1 || writerCalls != 1 {
		t.Fatalf("恢复时不得重复调用 Provider: search=%d writer=%d", searchCalls, writerCalls)
	}
	if rows2[0].DocumentID != rows1[0].DocumentID {
		t.Fatalf("落源 Document 应复用: %s vs %s", rows1[0].DocumentID, rows2[0].DocumentID)
	}
}

// TestDoDigest_PublishCrashResume R08：发布后、终态写入前中断——恢复时复用
// 已发布修订，不产生重复修订、不重调 Provider；渠道改写已完成的不重做。
func TestDoDigest_PublishCrashResume(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestWriter:   &fakeDigestWriter{compose: &provider.DigestWritingResult{Title: "发布恢复", Blocks: validDraftBlocks()}},
			DigestRewriter: &fakeDigestRewriter{text: "渠道版本"},
		}, nil
	}
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil || job == nil {
		t.Fatalf("入队失败: %v %v", job, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("首次执行失败: %v", err)
	}
	d1, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	rw1, err := s.GetDigestRewrite(ctx, d1.ID, "xiaohongshu")
	if err != nil {
		t.Fatalf("内联渠道改写应已落库: %v", err)
	}

	// 模拟"发布后、终态前"中断：清终态，保留 checkpoint。
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE processing_jobs SET status='queued', result_state='', result_json='', lease_until=NULL WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("恢复执行失败: %v", err)
	}
	d2, err := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if err != nil || d2.ID != d1.ID || d2.Version != 1 {
		t.Fatalf("恢复不得产生重复修订: %+v vs %+v", d2, d1)
	}
	rw2, _ := s.GetDigestRewrite(ctx, d1.ID, "xiaohongshu")
	if rw2 == nil || rw2.Text != rw1.Text {
		t.Fatalf("渠道产物应保持: %+v vs %+v", rw2, rw1)
	}
	got, _ := s.GetJob(ctx, job.ID)
	if got.Status != models.StatusSucceeded {
		t.Fatalf("任务应成功终结: %+v", got)
	}
}

// TestDigestRewriteJob_RetryKeepsMainText R08：渠道改写重试不重做主文或其他渠道；
// 同修订同输入复用既有产物，不重复调用 Provider。
func TestDigestRewriteJob_RetryKeepsMainText(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)

	base, err := s.PublishEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: sourceID, Title: "主文",
		Provider: "p", Model: "m", PromptVersion: "v",
	}, []models.DigestBlock{{Type: models.DigestBlockParaphrase, Text: "转述", Citations: []string{"seg-0001"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rewriteCalls := 0
	w.bundleFor = func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{
			DigestRewriter: &countingRewriter{inner: &fakeDigestRewriter{text: "渠道文本"}, count: &rewriteCalls},
		}, nil
	}
	jobA, err := w.EnqueueDigestRewriteJob(ctx, base.ID, "xiaohongshu")
	if err != nil || jobA == nil {
		t.Fatalf("改写入队失败: %v %v", jobA, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("改写失败: %v", err)
	}
	if rewriteCalls != 1 {
		t.Fatalf("首次改写应恰好一次调用: %d", rewriteCalls)
	}
	// 重复重试同修订同渠道：输入指纹复用，不重新调用 Provider。
	jobRetry, err := w.EnqueueDigestRewriteJob(ctx, base.ID, "xiaohongshu")
	if err != nil || jobRetry == nil {
		t.Fatalf("重试入队失败: %v %v", jobRetry, err)
	}
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatalf("重试失败: %v", err)
	}
	if rewriteCalls != 1 {
		t.Fatalf("同输入重试不得重复调用 Provider: %d", rewriteCalls)
	}
	// 其他渠道不受波及。
	if _, err := s.GetDigestRewrite(ctx, base.ID, "video"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("其他渠道不应被波及: %v", err)
	}
	// 渠道改写不改变当前主文修订。
	cur, _ := s.GetCurrentEpisodeDigest(ctx, models.SourceEpisode, sourceID)
	if cur.ID != base.ID {
		t.Fatalf("渠道改写不应改变当前主文修订: %+v", cur)
	}
}
