package queue

import (
	"context"
	"encoding/json"
	"errors"
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
