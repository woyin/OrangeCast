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
