package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestWorkerResolversAndTaskConfiguration(t *testing.T) {
	_, w := newTestWorker(t)
	resolved := false
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		resolved = true
		return &provider.ProviderBundle{}, nil
	})
	if _, err := w.bundleFor(&models.ProcessingJob{}); err != nil || !resolved {
		t.Fatalf("自定义 Provider 解析器未生效: resolved=%v err=%v", resolved, err)
	}
	raw := func(context.Context, *models.ProcessingJob) (string, func(), error) {
		return "audio.wav", func() {}, nil
	}
	if got := w.WithRawAudioResolver(raw); got != w || w.rawAudioFor == nil {
		t.Fatal("外部音频边界注入必须返回同一 Worker 并保留解析器")
	}

	// 默认设置必须为每种持久任务选择一个可执行 Provider；未知任务回退 Groq。
	_, configuredWorker := newTestWorker(t)
	for _, kind := range []models.JobType{
		models.JobTranscribe, models.JobAnalyze, models.JobDigest, models.JobHighlight,
		models.JobNarration, models.JobKeypointQuality, models.JobDigestRewrite,
		models.JobCuratorBrief, models.JobClaimWriting, models.JobClaimRevision,
		models.JobClaimReview, models.JobStyleReview, models.JobType("unknown"),
	} {
		config, err := configuredWorker.taskConfigFor(&models.ProcessingJob{JobType: kind})
		if err != nil || config.Provider == "" {
			t.Errorf("任务 %s 应解析出 Provider: config=%+v err=%v", kind, config, err)
		}
	}
}

func TestReadableDocumentExtractionRejectsUnsafeOrEmptyContent(t *testing.T) {
	title, body, err := extractReadableHTML(`<html><head><title> Example Report </title><style>.hidden{}</style></head><body><script>alert(1)</script><h1>Heading</h1><p>Evidence text.</p></body></html>`)
	if err != nil || title != "example report" || !strings.Contains(body, "Evidence text.") {
		t.Fatalf("HTML 正文提取失败: title=%q body=%q err=%v", title, body, err)
	}
	if strings.Contains(body, "alert(1)") || strings.Contains(body, ".hidden") {
		t.Fatalf("脚本和样式不得进入证据正文: %q", body)
	}
	title, body, err = extractReadableHTML("<main>正文</main>")
	if err != nil || title != "检索落源文档" || body != "正文" {
		t.Fatalf("无标题页面应使用稳定标题: title=%q body=%q err=%v", title, body, err)
	}
	long := "<p>" + strings.Repeat("a", 21_000) + "</p>"
	_, body, err = extractReadableHTML(long)
	if err != nil || len(body) != 20_000 {
		t.Fatalf("正文必须截到 20000 字节: len=%d err=%v", len(body), err)
	}
	if _, _, err := extractReadableHTML("<html><script>x</script></html>"); err == nil || !strings.Contains(err.Error(), "正文为空") {
		t.Fatalf("空正文必须拒绝: %v", err)
	}
	if _, _, err := fetchReadableDocument(context.Background(), "http://127.0.0.1/private"); err == nil {
		t.Fatal("网页落源必须在请求前拒绝私网 URL")
	}
}

func TestReadableDocumentHTTPContract(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		content   string
		body      string
		wantTitle string
		wantBody  string
		wantError string
	}{
		{name: "plain text", status: http.StatusOK, content: "text/plain; charset=utf-8", body: "  evidence text  ", wantBody: "evidence text"},
		{name: "html", status: http.StatusOK, content: "text/html", body: "<title>Report</title><p>evidence</p>", wantTitle: "report", wantBody: "Report evidence"},
		{name: "status", status: http.StatusNotFound, content: "text/plain", body: "missing", wantError: "HTTP 404"},
		{name: "unsupported", status: http.StatusOK, content: "application/pdf", body: "pdf", wantError: "不支持的内容类型"},
		{name: "oversize", status: http.StatusOK, content: "text/plain", body: strings.Repeat("x", (4<<20)+1), wantError: "正文超上限"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.content)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			title, body, err := fetchReadableDocumentWithClient(context.Background(), srv.URL, srv.Client())
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("应返回含 %q 的错误，实际 title=%q body=%q err=%v", tt.wantError, title, body, err)
				}
				return
			}
			wantTitle := tt.wantTitle
			if tt.name == "plain text" {
				wantTitle = srv.URL
			}
			if err != nil || title != wantTitle || body != tt.wantBody {
				t.Fatalf("正文响应解析错误: title=%q body=%q err=%v", title, body, err)
			}
		})
	}
	if _, _, err := fetchReadableDocumentWithClient(context.Background(), "://bad", http.DefaultClient); err == nil {
		t.Fatal("非法请求 URL 必须失败")
	}
}

func TestDigestFailureAndRecoveryBranches(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.doDigest(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "DigestWriter") {
		t.Fatalf("缺少 writer 必须显式失败: %v", err)
	}
	composeFailure := errors.New("compose unavailable")
	err = w.doDigest(ctx, job, &provider.ProviderBundle{DigestWriter: &fakeDigestWriter{composeErr: composeFailure}})
	if err == nil || !strings.Contains(err.Error(), "精读文初稿") {
		t.Fatalf("初稿调用失败必须带阶段信息: %v", err)
	}

	draft := &provider.DigestWritingResult{Title: "T", FactGaps: []string{"gap-a", "gap-b"}}
	blocks, rows, gaps, ok := w.digestResolveGaps(ctx, job, &provider.ProviderBundle{}, draft, nil, &digestCheckpoint{})
	if ok || len(blocks) != 0 || len(rows) != 0 || len(gaps) != 2 {
		t.Fatalf("无搜索 Provider 应保留全部缺口并降级: blocks=%v rows=%v gaps=%v ok=%v", blocks, rows, gaps, ok)
	}
	if _, ok := w.digestIngestDocument(ctx, "http://127.0.0.1/private"); ok {
		t.Fatal("非法落源 URL 不得建档")
	}

	const docURL = "https://reports.example.com/existing"
	doc, err := s.CreateWebDocument(ctx, "落源", docURL, "事实正文")
	if err != nil {
		t.Fatal(err)
	}
	cp := digestCheckpoint{}
	_, rows, gaps, ok = w.digestResolveGaps(ctx, job, &provider.ProviderBundle{
		DigestSearch: &fakeDigestSearch{results: []provider.DigestSearchResult{{URL: "ftp://invalid"}, {URL: docURL}}},
		DigestWriter: &fakeDigestWriter{weaveErr: errors.New("weave unavailable")},
	}, draft, nil, &cp)
	if !ok || len(rows) != 1 || rows[0].DocumentID != doc.ID || len(gaps) != 2 {
		t.Fatalf("补织失败应保留落源审计与全部缺口: rows=%+v gaps=%+v ok=%v", rows, gaps, ok)
	}

	// 已落源且已补织的 checkpoint 必须直接复用，不再调用搜索/模型。
	cp = digestCheckpoint{
		LandedGaps: map[string]*digestGapLanding{"gap-a": {URL: docURL, Document: documentToDigest(doc)}},
		Woven:      []provider.DigestBlockDraft{{Type: provider.DigestBlockCitedFactStr, Text: "事实"}},
	}
	search := &countingDigestSearch{calls: new(int)}
	woven, rows, gaps, ok := w.digestResolveGaps(ctx, job, &provider.ProviderBundle{DigestSearch: search, DigestWriter: &fakeDigestWriter{}}, &provider.DigestWritingResult{Title: "T", FactGaps: []string{"gap-a"}}, nil, &cp)
	if !ok || len(woven) != 1 || len(rows) != 1 || len(gaps) != 0 || *search.calls != 0 {
		t.Fatalf("断点应完整复用: woven=%+v rows=%+v gaps=%+v calls=%d ok=%v", woven, rows, gaps, *search.calls, ok)
	}
}

func TestDigestDocumentIngestionWithValidatedFetcher(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	const rawURL = "https://example.com/new-source"
	doc, ok := w.digestIngestDocumentWithFetcher(ctx, rawURL, func(context.Context, string) (string, string, error) {
		return "研究报告", "第一段事实。\n\n第二段事实。", nil
	})
	if !ok || doc.DocumentID == "" || doc.Title != "研究报告" || len(doc.Segments) != 2 {
		t.Fatalf("验证后的网页必须沉淀为可引用文档: %+v ok=%v", doc, ok)
	}
	stored, err := s.GetDocumentByOriginURL(ctx, rawURL)
	if err != nil || stored.ID != doc.DocumentID {
		t.Fatalf("落源文档未持久化: stored=%+v err=%v", stored, err)
	}

	_ = s.Close()
	if _, ok := w.digestIngestDocumentWithFetcher(ctx, "https://example.com/db-error", func(context.Context, string) (string, string, error) {
		return "报告", "正文", nil
	}); ok {
		t.Fatal("建档失败不得返回可用落源文档")
	}
}

func TestDigestSourceMaterialUsesFrozenCardAndUploadTitle(t *testing.T) {
	ctx := context.Background()
	t.Run("frozen card summary", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
		var originJobID string
		if err := s.DB.QueryRowContext(ctx, `SELECT job_id FROM artifact_versions WHERE source_id=? AND kind=? LIMIT 1`, sourceID, store.KindTranscript).Scan(&originJobID); err != nil {
			t.Fatal(err)
		}
		card, _ := json.Marshal(provider.KnowledgeCard{Title: "卡片标题", Summary: provider.CitedText{Text: "冻结摘要", Citations: []string{"seg-0001"}}})
		version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, "fake", "m", "1", originJobID, string(card))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetCurrentVersion(ctx, models.SourceEpisode, sourceID, store.KindKnowledgeCard, version); err != nil {
			t.Fatal(err)
		}
		job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		segments, summary, title, err := w.digestSourceMaterial(ctx, job)
		if err != nil || len(segments) != 2 || summary != "冻结摘要" || title == "" {
			t.Fatalf("冻结卡片/转录读取错误: segments=%d summary=%q title=%q err=%v", len(segments), summary, title, err)
		}
	})

	t.Run("legacy upload title", func(t *testing.T) {
		s, w := newTestWorker(t)
		up, err := s.CreateUpload(ctx, "owner-recording.wav", "audio/wav", 10)
		if err != nil {
			t.Fatal(err)
		}
		seedJob, err := s.EnqueueJob(ctx, models.SourceUpload, up.ID, models.JobTranscribe)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(provider.TranscriptPayload{Text: "原文", Segments: []provider.Segment{{ID: "seg-1", Text: "原文"}}})
		version, err := s.CreateArtifactVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript, "fake", "m", "1", seedJob.ID, string(payload))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetCurrentVersion(ctx, models.SourceUpload, up.ID, store.KindTranscript, version); err != nil {
			t.Fatal(err)
		}
		segments, _, title, err := w.digestSourceMaterial(ctx, &models.ProcessingJob{ID: "legacy", SourceType: models.SourceUpload, SourceID: up.ID})
		if err != nil || len(segments) != 1 || title != "owner-recording.wav" {
			t.Fatalf("旧上传任务应回退当前转录并使用文件名: segments=%+v title=%q err=%v", segments, title, err)
		}
	})
}

func TestDigestRecoveryRejectsMissingPublishedRevisionAndBadRewrite(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
	job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := json.Marshal(digestCheckpoint{
		Stage: "published", PublishedDigestID: "missing-digest",
		Draft: &provider.DigestWritingResult{Title: "冻结初稿", Blocks: validDraftBlocks()},
	})
	if err := s.SaveJobCheckpoint(ctx, job.ID, string(cp)); err != nil {
		t.Fatal(err)
	}
	err = w.doDigest(ctx, job, &provider.ProviderBundle{DigestWriter: &fakeDigestWriter{}})
	if err == nil || !strings.Contains(err.Error(), "读取已发布修订") {
		t.Fatalf("断点引用不存在的已发布修订必须失败: %v", err)
	}

	rewriteJob := &models.ProcessingJob{ID: job.ID, SourceType: job.SourceType, SourceID: job.SourceID}
	err = w.digestRewrite(ctx, rewriteJob, &provider.ProviderBundle{DigestRewriter: &fakeDigestRewriter{err: errors.New("rewrite failed")}}, "digest", "标题", validDraftBlocks())
	if err == nil || !strings.Contains(err.Error(), "rewrite failed") {
		t.Fatalf("渠道改写错误必须上浮: %v", err)
	}
	err = w.digestRewrite(ctx, rewriteJob, &provider.ProviderBundle{DigestRewriter: &fakeDigestRewriter{text: strings.Repeat("长", digestXHSMaxChars+1)}}, "digest", "标题", validDraftBlocks())
	if err == nil || !strings.Contains(err.Error(), "渠道格式门禁") {
		t.Fatalf("超长渠道稿必须被格式门禁拒绝: %v", err)
	}

	segments := w.digestDocSegments(ctx, []models.DigestSearchSource{{DocumentID: "missing"}})
	if len(segments) != 0 {
		t.Fatalf("缺失落源文档不得产生伪 Segment: %+v", segments)
	}
}

func TestDigestFrozenSnapshotAndNoteReadFailuresAreVisible(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"snapshot_id":"missing"}`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.digestSourceMaterial(ctx, job); err == nil || !strings.Contains(err.Error(), "读取冻结来源快照") {
		t.Fatalf("失效来源快照必须明确失败: %v", err)
	}

	_ = s.Close()
	if _, _, err := w.digestOwnerNotes(ctx, job, digestInputSnapshot{}); err == nil || !strings.Contains(err.Error(), "读取 Owner 笔记") {
		t.Fatalf("笔记读取错误必须上浮: %v", err)
	}
}

func TestDigestAdditionalRecoveryFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("compose checkpoint persistence", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
		job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		writer := &closingDigestWriter{store: s, draft: &provider.DigestWritingResult{Title: "初稿", Blocks: validDraftBlocks()}}
		err = w.doDigest(ctx, job, &provider.ProviderBundle{DigestWriter: writer})
		if err == nil {
			t.Fatal("compose 后 checkpoint 持久化失败必须上浮")
		}
	})

	t.Run("woven blocks are revalidated", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
		job, err := s.EnqueueDigestJob(ctx, models.SourceEpisode, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		const docURL = "https://example.com/woven-source"
		doc, err := s.CreateWebDocument(ctx, "来源", docURL, "落源事实")
		if err != nil {
			t.Fatal(err)
		}
		writer := &fakeDigestWriter{
			compose: &provider.DigestWritingResult{Title: "初稿", Blocks: validDraftBlocks(), FactGaps: []string{"事实缺口"}},
			weave:   &provider.DigestWeaveResult{Blocks: []provider.DigestBlockDraft{{Type: provider.DigestBlockCitedFactStr, Text: "无引用事实", TargetSourceID: doc.ID}}},
		}
		err = w.doDigest(ctx, job, &provider.ProviderBundle{DigestWriter: writer, DigestSearch: &fakeDigestSearch{results: []provider.DigestSearchResult{{URL: docURL}}}})
		if err == nil || !strings.Contains(err.Error(), "补织后门禁失败") {
			t.Fatalf("补织块必须重新通过引用门禁: %v", err)
		}
	})

	t.Run("document and frozen note failures", func(t *testing.T) {
		s, w := newTestWorker(t)
		if _, _, _, err := w.digestDocumentMaterial(ctx, "missing", ""); err == nil || !strings.Contains(err.Error(), "读取文档证据") {
			t.Fatalf("缺失文档必须失败: %v", err)
		}
		doc, err := s.CreatePastedDocument(ctx, "文档", "正文")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := w.digestDocumentMaterial(ctx, doc.ID, "missing-snapshot"); err == nil || !strings.Contains(err.Error(), "读取冻结文档快照") {
			t.Fatalf("失效文档快照必须失败: %v", err)
		}

		sourceID := seedEpisode(t, s)
		keep, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "owner_reflection", Content: "保留", CitationsJSON: "[]", ReferencesJSON: "[]"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateOwnerNote(ctx, models.OwnerNote{SourceType: string(models.SourceEpisode), SourceID: sourceID, Kind: "owner_reflection", Content: "排除", CitationsJSON: "[]", ReferencesJSON: "[]"}); err != nil {
			t.Fatal(err)
		}
		notes, _, err := w.digestOwnerNotes(ctx, &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: sourceID}, digestInputSnapshot{NoteIDs: []string{keep.ID}})
		if err != nil || len(notes) != 1 || notes[0].NoteID != keep.ID {
			t.Fatalf("旧快照必须只解析冻结 NoteID: notes=%+v err=%v", notes, err)
		}
		_ = s.Close()
		_, _, err = w.digestOwnerNotes(ctx, &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: sourceID}, digestInputSnapshot{NotesRecorded: true, Notes: []store.DigestFrozenNote{{ID: keep.ID, Content: "保留", Kind: "source_note"}}})
		if err == nil || !strings.Contains(err.Error(), "存在性检查") {
			t.Fatalf("冻结笔记存在性检查错误必须上浮: %v", err)
		}
	})

	t.Run("gap fallback identities", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		job, _ := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobDigest)
		doc, _ := s.CreateWebDocument(ctx, "来源", "https://example.com/gap", "事实")
		cp := digestCheckpoint{LandedGaps: map[string]*digestGapLanding{"gap": {URL: doc.OriginURL, Document: documentToDigest(doc)}}}
		writer := &fakeDigestWriter{weave: &provider.DigestWeaveResult{Blocks: []provider.DigestBlockDraft{{Type: provider.DigestBlockCitedFactStr, Text: "事实", Citations: []string{doc.ID + "-p0001"}, NoteID: doc.ID}}}}
		woven, _, _, ok := w.digestResolveGaps(ctx, job, &provider.ProviderBundle{DigestSearch: &fakeDigestSearch{}, DigestWriter: writer}, &provider.DigestWritingResult{Title: "T", FactGaps: []string{"gap"}}, nil, &cp)
		if !ok || len(woven) != 1 || woven[0].TargetSourceID != doc.ID {
			t.Fatalf("补织 document_id 兼容身份未恢复: woven=%+v ok=%v", woven, ok)
		}
		gaps := w.unresolvedGaps(&provider.DigestWritingResult{FactGaps: []string{"resolved", "open"}}, []models.DigestSearchSource{{Query: "resolved"}})
		if len(gaps) != 1 || gaps[0].Text != "open" {
			t.Fatalf("仅未落源缺口应保留: %+v", gaps)
		}
		_, _, _, ok = w.digestResolveGaps(ctx, job, &provider.ProviderBundle{DigestSearch: &fakeDigestSearch{results: []provider.DigestSearchResult{{URL: "http://127.0.0.1/private"}}}, DigestWriter: writer}, &provider.DigestWritingResult{FactGaps: []string{"unsafe"}}, nil, &digestCheckpoint{})
		if !ok {
			t.Fatal("单条不可落源应留档，但搜索能力本身仍可用")
		}
	})
}

type closingDigestWriter struct {
	store *store.Store
	draft *provider.DigestWritingResult
}

func (f *closingDigestWriter) ComposeDigest(context.Context, provider.DigestWritingRequest) (*provider.DigestWritingResult, error) {
	_ = f.store.Close()
	return f.draft, nil
}
func (*closingDigestWriter) WeaveDigestFacts(context.Context, provider.DigestWeaveRequest) (*provider.DigestWeaveResult, error) {
	return &provider.DigestWeaveResult{}, nil
}
func (*closingDigestWriter) Name() string { return "closing-digest" }

type countingDigestSearch struct {
	calls *int
}

func (f *countingDigestSearch) Search(context.Context, string) ([]provider.DigestSearchResult, error) {
	*f.calls++
	return nil, nil
}
func (*countingDigestSearch) Name() string { return "counting-search" }

func TestNarrationJobRejectsBrokenContracts(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	if err := w.doNarrationJob(ctx, &models.ProcessingJob{ID: "missing"}, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "Narration Provider") {
		t.Fatalf("缺少解说 Provider 必须显式失败: %v", err)
	}
	if err := w.doNarrationJob(ctx, &models.ProcessingJob{ID: "missing"}, &provider.ProviderBundle{Narration: &taskNarration{available: true}}); err == nil {
		t.Fatal("不存在的任务契约必须失败")
	}

	sourceID := seedEpisode(t, s)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, sourceID, models.JobNarration)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.doNarrationJob(ctx, job, &provider.ProviderBundle{Narration: &taskNarration{available: true}}); err == nil || !strings.Contains(err.Error(), "冻结的高光版本") {
		t.Fatalf("无冻结快照必须失败: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"highlight_version":99}`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.doNarrationJob(ctx, job, &provider.ProviderBundle{Narration: &taskNarration{available: true}}); err == nil || !strings.Contains(err.Error(), "读取冻结高光版本") {
		t.Fatalf("不存在的冻结版本必须失败: %v", err)
	}
}

func queueContractJob(t *testing.T, kind models.JobType, snapshot string) (*Worker, *models.ProcessingJob) {
	t.Helper()
	s, w := newTestWorker(t)
	sourceID := seedEpisode(t, s)
	job, err := s.EnqueueJob(context.Background(), models.SourceEpisode, sourceID, kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, snapshot, job.ID); err != nil {
		t.Fatal(err)
	}
	return w, job
}

func TestDurableJobsRejectMissingOrBrokenSnapshots(t *testing.T) {
	ctx := context.Background()
	t.Run("claim writing provider", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobClaimWriting, `{}`)
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "不支持 v2 写作") {
			t.Fatalf("不支持 claim-aware 写作必须失败: %v", err)
		}
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: &fakeClaimWriter{}}); err == nil || !strings.Contains(err.Error(), "缺少冻结输入") {
			t.Fatalf("空写作快照必须失败: %v", err)
		}
	})
	t.Run("claim writing malformed", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobClaimWriting, `{broken`)
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: &fakeClaimWriter{}}); err == nil || !strings.Contains(err.Error(), "解析冻结写作输入") {
			t.Fatalf("畸形写作快照必须失败: %v", err)
		}
	})
	t.Run("claim revision", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobClaimRevision, `{broken`)
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "不支持 claim-aware AI 修订") {
			t.Fatalf("缺少修订能力必须失败: %v", err)
		}
		writer := &fakeDualWriter{}
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: writer}); err == nil || !strings.Contains(err.Error(), "解析冻结 AI 修订输入") {
			t.Fatalf("畸形修订快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json='{}' WHERE id=?`, job.ID)
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: writer}); err == nil || !strings.Contains(err.Error(), "缺少冻结输入") {
			t.Fatalf("空修订快照必须失败: %v", err)
		}
	})
	t.Run("claim review", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobClaimReview, `{broken`)
		if err := w.doClaimReviewJob(ctx, job, nil); err == nil || !strings.Contains(err.Error(), "ClaimReviewer") {
			t.Fatalf("缺少审校 Provider 必须失败: %v", err)
		}
		if err := w.doClaimReviewJob(ctx, job, &provider.ProviderBundle{ClaimReviewer: &fakeClaimReviewer{}}); err == nil || !strings.Contains(err.Error(), "解析冻结审校输入") {
			t.Fatalf("畸形审校快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json='{}' WHERE id=?`, job.ID)
		if err := w.doClaimReviewJob(ctx, job, &provider.ProviderBundle{ClaimReviewer: &fakeClaimReviewer{}}); err == nil || !strings.Contains(err.Error(), "缺少冻结输入") {
			t.Fatalf("空审校快照必须失败: %v", err)
		}
		input := store.ReviewTaskInput{Kind: store.ReviewKindClaim, RevisionID: "revision", Markdown: "body"}
		snapshot, _ := json.Marshal(input)
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(snapshot), job.ID)
		if err := w.doClaimReviewJob(ctx, job, &provider.ProviderBundle{ClaimReviewer: &fakeClaimReviewer{}}); err == nil || !strings.Contains(err.Error(), "缺少 ClaimMap") {
			t.Fatalf("无 ClaimMap 或授权材料必须失败: %v", err)
		}
	})
	t.Run("style review", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobStyleReview, `{}`)
		if err := w.doStyleReviewJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "StyleEditor") {
			t.Fatalf("缺少风格审校 Provider 必须失败: %v", err)
		}
		if err := w.doStyleReviewJob(ctx, job, &provider.ProviderBundle{StyleEditor: &fakeStyleEditor{}}); err == nil || !strings.Contains(err.Error(), "缺少冻结输入") {
			t.Fatalf("空风格审校快照必须失败: %v", err)
		}
	})
	t.Run("style review malformed", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobStyleReview, `{broken`)
		if err := w.doStyleReviewJob(ctx, job, &provider.ProviderBundle{StyleEditor: &fakeStyleEditor{}}); err == nil || !strings.Contains(err.Error(), "解析冻结风格审校输入") {
			t.Fatalf("畸形风格审校快照必须失败: %v", err)
		}
	})
	t.Run("ideation", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobIdeationDiagnosis, `{}`)
		if err := w.doIdeationDiagnosisJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "不支持构思诊断") {
			t.Fatalf("缺少诊断能力必须失败: %v", err)
		}
		if err := w.doIdeationDiagnosisJob(ctx, job, &provider.ProviderBundle{Analysis: &qualityAnalyzer{}}); err == nil || !strings.Contains(err.Error(), "缺少轮次快照") {
			t.Fatalf("空轮次快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"round_id":"missing"}`, job.ID)
		if err := w.doIdeationDiagnosisJob(ctx, job, &provider.ProviderBundle{Analysis: &qualityAnalyzer{}}); err == nil || !strings.Contains(err.Error(), "读取轮次") {
			t.Fatalf("不存在的轮次必须失败: %v", err)
		}
	})
	t.Run("highlight", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobHighlight, `{}`)
		if err := w.doHighlightJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "Highlight Provider") {
			t.Fatalf("缺少高光 Provider 必须失败: %v", err)
		}
		hl := &countingHighlight{}
		if err := w.doHighlightJob(ctx, job, &provider.ProviderBundle{Highlight: hl}); err == nil || !strings.Contains(err.Error(), "冻结的转录版本") {
			t.Fatalf("空高光快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"transcript_version":99}`, job.ID)
		if err := w.doHighlightJob(ctx, job, &provider.ProviderBundle{Highlight: hl}); err == nil || !strings.Contains(err.Error(), "读取冻结转录版本") {
			t.Fatalf("不存在的转录版本必须失败: %v", err)
		}
	})
	t.Run("keypoint quality", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobKeypointQuality, `{}`)
		if err := w.doKeypointQualityJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "不支持重点质量判定") {
			t.Fatalf("缺少质量判定能力必须失败: %v", err)
		}
		qa := &qualityAnalyzer{}
		if err := w.doKeypointQualityJob(ctx, job, &provider.ProviderBundle{Analysis: qa}); err == nil || !strings.Contains(err.Error(), "冻结的卡片版本") {
			t.Fatalf("空质量快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"card_version":1,"transcript_version":99}`, job.ID)
		if err := w.doKeypointQualityJob(ctx, job, &provider.ProviderBundle{Analysis: qa}); err == nil || !strings.Contains(err.Error(), "读取冻结转录版本") {
			t.Fatalf("不存在的转录版本必须失败: %v", err)
		}
	})
	t.Run("curator", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobCuratorBrief, `{}`)
		if err := w.doCuratorBriefJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "Curator Provider") {
			t.Fatalf("缺少 Curator 必须失败: %v", err)
		}
		curator := &curatorBriefFake{}
		if err := w.doCuratorBriefJob(ctx, job, &provider.ProviderBundle{Curator: curator}); err == nil || !strings.Contains(err.Error(), "缺少冻结方案快照") {
			t.Fatalf("空 Curator 快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"proposal_id":"p","brief_id":"b","owner_claim":"owner"}`, job.ID)
		if err := w.doCuratorBriefJob(ctx, job, &provider.ProviderBundle{Curator: curator}); err == nil || !strings.Contains(err.Error(), "返回空结果") {
			t.Fatalf("Curator 空结果必须失败: %v", err)
		}
	})
	t.Run("digest rewrite", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobDigestRewrite, `{}`)
		if err := w.doDigestRewriteJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "DigestRewriter") {
			t.Fatalf("缺少改写 Provider 必须失败: %v", err)
		}
		rewriter := &fakeDigestRewriter{text: "正文"}
		if err := w.doDigestRewriteJob(ctx, job, &provider.ProviderBundle{DigestRewriter: rewriter}); err == nil || !strings.Contains(err.Error(), "缺少修订与渠道快照") {
			t.Fatalf("空改写快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, fmt.Sprintf(`{"digest_id":%q,"channel":"x"}`, "missing"), job.ID)
		if err := w.doDigestRewriteJob(ctx, job, &provider.ProviderBundle{DigestRewriter: rewriter}); err == nil || !strings.Contains(err.Error(), "读取精读修订") {
			t.Fatalf("不存在的精读修订必须失败: %v", err)
		}
	})
	t.Run("dj plan", func(t *testing.T) {
		w, job := queueContractJob(t, models.JobDJPlan, `{}`)
		if err := w.doDJPlanJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "缺少高光版本快照") {
			t.Fatalf("空 DJ 快照必须失败: %v", err)
		}
		_, _ = w.store.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, `{"highlight_version":99}`, job.ID)
		if err := w.doDJPlanJob(ctx, job, &provider.ProviderBundle{}); err == nil || !strings.Contains(err.Error(), "读取冻结高光版本") {
			t.Fatalf("不存在的高光版本必须失败: %v", err)
		}
	})
}

func TestStyleReviewReusesBoundCheckpointBeforePersistenceRetry(t *testing.T) {
	input := store.ReviewTaskInput{
		Kind: store.ReviewKindStyle, RevisionID: "missing-revision", Markdown: "body",
		Model: "model", PromptVersion: "prompt",
	}
	snapshot, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	w, job := queueContractJob(t, models.JobStyleReview, string(snapshot))
	req := provider.StyleReviewRequest{Markdown: input.Markdown}
	cp, err := json.Marshal(styleReviewCheckpoint{
		Stage: "reviewed", Result: &provider.StyleReviewResult{Status: "passed"},
		Provider: "fake-styler", Model: input.Model, PromptVersion: input.PromptVersion,
		RevisionID: input.RevisionID, RequestHash: reviewRequestHash(req),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.store.SaveJobCheckpoint(context.Background(), job.ID, string(cp)); err != nil {
		t.Fatal(err)
	}
	style := &fakeStyleEditor{err: errors.New("checkpoint must avoid provider call")}
	err = w.doStyleReviewJob(context.Background(), job, &provider.ProviderBundle{StyleEditor: style})
	if err == nil || !strings.Contains(err.Error(), "保存风格审校") || style.calls != 0 {
		t.Fatalf("匹配断点必须跳过 Provider，并在业务落库失败处停下: calls=%d err=%v", style.calls, err)
	}
}

func TestDJPlanFrozenSegmentsAndDocumentTitleContracts(t *testing.T) {
	ctx := context.Background()
	t.Run("frozen transcript and empty fallback", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
		job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: sourceID}
		segments := w.frozenSegments(ctx, job, &models.ProcessingJobExecution{InputSnapshotJSON: `{"transcript_version":1}`})
		if len(segments) == 0 {
			t.Fatal("冻结转录版本必须解析出 Segment")
		}
		if got := w.frozenSegments(ctx, &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: "missing"}, nil); got != nil {
			t.Fatalf("无冻结版本且无当前转录时应返回 nil: %+v", got)
		}
	})

	t.Run("document source title", func(t *testing.T) {
		s, w := newTestWorker(t)
		doc, err := s.CreatePastedDocument(ctx, "文档标题", "正文")
		if err != nil {
			t.Fatal(err)
		}
		origin, _, err := s.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
			SourceType: models.SourceDocument, SourceID: doc.ID, JobType: models.JobAnalyze, IntentID: "document-dj-seed",
		})
		if err != nil {
			t.Fatal(err)
		}
		transcript, _ := json.Marshal(provider.TranscriptPayload{Segments: []provider.Segment{{ID: "seg-1", Start: 0, End: 3, Text: "正文"}}})
		transcriptVersion, err := s.CreateArtifactVersion(ctx, models.SourceDocument, doc.ID, store.KindTranscript, "fake", "m", "1", origin.ID, string(transcript))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetCurrentVersion(ctx, models.SourceDocument, doc.ID, store.KindTranscript, transcriptVersion); err != nil {
			t.Fatal(err)
		}
		highlights, _ := json.Marshal(provider.HighlightSet{Highlights: []provider.Highlight{{ID: "h1", Gist: "重点", Citations: []string{"seg-1"}}}})
		highlightVersion, err := s.CreateArtifactVersion(ctx, models.SourceDocument, doc.ID, store.KindHighlight, "fake", "m", "1", origin.ID, string(highlights))
		if err != nil {
			t.Fatal(err)
		}
		job, err := s.EnqueueDJPlanJob(ctx, models.SourceDocument, doc.ID, highlightVersion, 60)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.doDJPlanJob(ctx, job, &provider.ProviderBundle{}); err != nil {
			t.Fatal(err)
		}
		plan, err := s.GetCurrentDJPlan(ctx, models.SourceDocument, doc.ID, highlightVersion)
		if err != nil || len(plan.Items) == 0 || !strings.Contains(plan.Items[0].ScriptText, doc.Title) {
			t.Fatalf("文档标题必须进入 DJ 开场: plan=%+v err=%v", plan, err)
		}
	})
}

func TestResumePurgesSurfacesEachInterruptedStage(t *testing.T) {
	ctx := context.Background()
	t.Run("evidence file removal", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(w.evidenceDir, "episode_"+sourceID+".mp3")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := w.ResumePurges(ctx); err == nil || !strings.Contains(err.Error(), "purge 删除文件") {
			t.Fatalf("不可删除的证据路径必须中止 purge: %v", err)
		}
	})

	for _, tc := range []struct {
		name, table, want string
	}{
		{"keypoints", "keypoint_index", "原子撤销"},
		{"annotations", "annotations", "素材关系"},
		{"research resolutions", "research_needs", "失效研究缺口"},
		{"paraphrases", "paraphrases", "no such table"},
		{"study sessions", "study_sessions", "no such table"},
		{"narrations", "narrations", "no such table"},
		{"digests", "episode_digests", "no such table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			sourceID := seedEpisode(t, s)
			if err := s.CreatePurgeIntent(ctx, models.SourceEpisode, sourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.ExecContext(ctx, `DROP TABLE `+tc.table); err != nil {
				t.Fatal(err)
			}
			if err := w.ResumePurges(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s 阶段存储错误必须中止 purge: %v", tc.name, err)
			}
		})
	}
}

func TestDurableJobsRejectMissingExecution(t *testing.T) {
	_, w := newTestWorker(t)
	ctx := context.Background()
	job := &models.ProcessingJob{ID: "missing", SourceType: models.SourceEpisode, SourceID: "missing"}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "claim writing", run: func() error {
			return w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: &fakeClaimWriter{}})
		}},
		{name: "claim revision", run: func() error {
			return w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: &fakeDualWriter{}})
		}},
		{name: "claim review", run: func() error {
			return w.doClaimReviewJob(ctx, job, &provider.ProviderBundle{ClaimReviewer: &fakeClaimReviewer{}})
		}},
		{name: "style review", run: func() error {
			return w.doStyleReviewJob(ctx, job, &provider.ProviderBundle{StyleEditor: &fakeStyleEditor{}})
		}},
		{name: "ideation", run: func() error {
			return w.doIdeationDiagnosisJob(ctx, job, &provider.ProviderBundle{Analysis: &qualityAnalyzer{}})
		}},
		{name: "highlight", run: func() error {
			return w.doHighlightJob(ctx, job, &provider.ProviderBundle{Highlight: &countingHighlight{}})
		}},
		{name: "quality", run: func() error {
			return w.doKeypointQualityJob(ctx, job, &provider.ProviderBundle{Analysis: &qualityAnalyzer{}})
		}},
		{name: "curator", run: func() error {
			return w.doCuratorBriefJob(ctx, job, &provider.ProviderBundle{Curator: &curatorBriefFake{}})
		}},
		{name: "digest rewrite", run: func() error {
			return w.doDigestRewriteJob(ctx, job, &provider.ProviderBundle{DigestRewriter: &fakeDigestRewriter{}})
		}},
		{name: "dj plan", run: func() error { return w.doDJPlanJob(ctx, job, &provider.ProviderBundle{}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("不存在的持久任务必须失败")
			}
		})
	}
}

func TestFrozenMaterialPoliciesSurfaceRevocationAndStorageFailures(t *testing.T) {
	ctx := context.Background()
	makeFixture := func(t *testing.T) (*Worker, string, provider.ClaimOwnerNote, provider.ArticleMaterial) {
		t.Helper()
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		note, err := s.CreateOwnerNote(ctx, models.OwnerNote{
			SourceType: string(models.SourceEpisode), SourceID: sourceID,
			Kind: "owner_reflection", Content: "个人判断", CitationsJSON: "[]", ReferencesJSON: "[]",
		})
		if err != nil {
			t.Fatal(err)
		}
		frozen := provider.ClaimOwnerNote{ID: note.ID, Content: note.Content, Kind: note.Kind, SourceType: note.SourceType, SourceID: note.SourceID, Revision: note.Revision}
		material := provider.ArticleMaterial{KeyPointID: "kp-1", SourceType: string(models.SourceEpisode), SourceID: sourceID, Content: "材料"}
		return w, sourceID, frozen, material
	}

	t.Run("deleted note", func(t *testing.T) {
		w, _, note, _ := makeFixture(t)
		if _, err := w.store.DB.ExecContext(ctx, `DELETE FROM owner_notes WHERE id=?`, note.ID); err != nil {
			t.Fatal(err)
		}
		if err := frozenNotesPolicy(ctx, w, "", []provider.ClaimOwnerNote{note}, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "已删除") {
			t.Fatalf("删除笔记必须阻止复用冻结文本: %v", err)
		}
	})
	t.Run("archived note source", func(t *testing.T) {
		w, sourceID, note, _ := makeFixture(t)
		if _, err := w.store.DB.ExecContext(ctx, `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, sourceID); err != nil {
			t.Fatal(err)
		}
		if err := frozenNotesPolicy(ctx, w, "", []provider.ClaimOwnerNote{note}, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "已归档") {
			t.Fatalf("归档来源必须使冻结笔记失效: %v", err)
		}
	})
	t.Run("local only note source", func(t *testing.T) {
		w, sourceID, note, _ := makeFixture(t)
		if err := w.store.SetSourceProductionPolicy(ctx, models.SourceEpisode, sourceID, "internal", models.ModelDataLocalOnly); err != nil {
			t.Fatal(err)
		}
		if err := frozenNotesPolicy(ctx, w, "", []provider.ClaimOwnerNote{note}, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "禁止外发") {
			t.Fatalf("LocalOnly 来源必须阻止笔记外发: %v", err)
		}
	})
	t.Run("note query failure", func(t *testing.T) {
		w, _, note, _ := makeFixture(t)
		_ = w.store.Close()
		if err := frozenNotesPolicy(ctx, w, "", []provider.ClaimOwnerNote{note}, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "笔记查询失败") {
			t.Fatalf("笔记查询错误必须上浮: %v", err)
		}
	})
	t.Run("material revocation", func(t *testing.T) {
		w, sourceID, _, material := makeFixture(t)
		input := store.ReviewTaskInput{}
		if err := reviewMaterialPolicy(ctx, w, input, material, "groq", "审校"); err != nil {
			t.Fatalf("默认可用材料被拒绝: %v", err)
		}
		if _, err := w.store.DB.ExecContext(ctx, `UPDATE episodes SET archived_at=datetime('now') WHERE id=?`, sourceID); err != nil {
			t.Fatal(err)
		}
		if err := reviewMaterialPolicy(ctx, w, input, material, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "已归档") {
			t.Fatalf("归档材料必须拒绝: %v", err)
		}
	})
	t.Run("missing material", func(t *testing.T) {
		w, _, _, material := makeFixture(t)
		material.SourceID = "missing"
		if err := reviewMaterialPolicy(ctx, w, store.ReviewTaskInput{}, material, "groq", "审校"); err == nil || !strings.Contains(err.Error(), "可用性查询失败") {
			t.Fatalf("缺失材料查询错误必须上浮: %v", err)
		}
	})
	if got := reviewRequestHash(make(chan int)); got != "" {
		t.Fatalf("不可序列化请求不得产生伪 hash: %q", got)
	}
}

func TestWorkerBudgetAndUsageFailureBoundaries(t *testing.T) {
	ctx := context.Background()
	s, w := newTestWorker(t)
	w.taskConfigFor = func(*models.ProcessingJob) (provider.TaskConfig, error) {
		return provider.TaskConfig{}, errors.New("config unavailable")
	}
	job := &models.ProcessingJob{ID: "job-boundary", SourceType: models.SourceEpisode, SourceID: "missing", JobType: models.JobDigest}
	if _, err := w.bundleFor(job); err != nil {
		t.Fatalf("bundle 解析应在配置读取失败时回退 Groq: %v", err)
	}
	if err := w.enforceSourceSendPolicy(ctx, job); err == nil || !strings.Contains(err.Error(), "config unavailable") {
		t.Fatalf("动态策略必须上浮任务配置错误: %v", err)
	}
	if err := w.holdJobBudget(ctx, job); err == nil || !strings.Contains(err.Error(), "config unavailable") {
		t.Fatalf("预算预占必须上浮任务配置错误: %v", err)
	}

	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "local", Model: "priced", InputCentsPerMillion: 1_000_000, OutputCentsPerMillion: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	w.recordCallUsage(ctx, &models.ProcessingJob{ID: "priced-job", AttemptCount: 1}, "operation", "local", "priced", provider.TaskUsage{InputUnits: 1, OutputUnits: 2})
	var cost float64
	if err := s.DB.QueryRowContext(ctx, `SELECT estimated_cost FROM usage_records WHERE receipt_id=?`, "priced-job:operation").Scan(&cost); err != nil || cost != 3 {
		t.Fatalf("已知价格必须回填同一 receipt: cost=%v err=%v", cost, err)
	}

	_ = s.Close()
	if receipts, cost := w.jobReceiptUsage(ctx, "missing"); receipts != 0 || cost != 0 {
		t.Fatalf("账本查询失败应保守返回零值: receipts=%d cost=%d", receipts, cost)
	}
	w.settleJobBudget(ctx, job)
	w.finalizeJobBudgetOnFailure(ctx, job)
	w.markRemoteCallStarted(ctx, job)
	w.recordCallUsage(ctx, job, "failed-record", "local", "priced", provider.TaskUsage{InputUnits: 1})
}

func TestHighlightJobRejectsCorruptTranscriptAndInvalidGeneration(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		corrupt    bool
		generator  *countingHighlight
		wantSubstr string
	}{
		{name: "corrupt transcript", corrupt: true, generator: &countingHighlight{}, wantSubstr: "解析转录载荷"},
		{name: "provider failure", generator: &countingHighlight{err: errors.New("generation unavailable")}, wantSubstr: "生成高光"},
		{
			name: "invalid citation",
			generator: &countingHighlight{set: provider.HighlightSet{Highlights: []provider.Highlight{
				{ID: "hl", Gist: "gist", Citations: []string{"missing"}},
			}}},
			wantSubstr: "高光校验",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			sourceID := seedEpisode(t, s)
			seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
			job, err := s.EnqueueHighlightJob(ctx, models.SourceEpisode, sourceID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				if _, err := s.DB.ExecContext(ctx, `UPDATE artifact_versions SET payload='{broken' WHERE source_id=? AND kind=?`, sourceID, store.KindTranscript); err != nil {
					t.Fatal(err)
				}
			}
			err = w.doHighlightJob(ctx, job, &provider.ProviderBundle{Highlight: tc.generator})
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("应返回含 %q 的错误，实际 %v", tc.wantSubstr, err)
			}
		})
	}
}

func TestIdeationAndQualityEnqueueContracts(t *testing.T) {
	ctx := context.Background()
	t.Run("ideation missing and active duplicate", func(t *testing.T) {
		s, w := newTestWorker(t)
		if _, err := w.EnqueueIdeationDiagnosisJob(ctx, "session", "missing"); err == nil {
			t.Fatal("不存在的轮次不得入队")
		}
		profile, _ := s.EnsureDefaultEditorialProfile(ctx)
		session, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "验证幂等"})
		if err != nil {
			t.Fatal(err)
		}
		round, _, err := s.AddIdeationRound(ctx, session.ID, "nonce", "问题", "{}", "[]")
		if err != nil {
			t.Fatal(err)
		}
		first, err := w.EnqueueIdeationDiagnosisJob(ctx, session.ID, round.ID)
		if err != nil || first == nil {
			t.Fatalf("首次诊断入队失败: job=%+v err=%v", first, err)
		}
		second, err := w.EnqueueIdeationDiagnosisJob(ctx, session.ID, round.ID)
		if err != nil || second != nil {
			t.Fatalf("活跃诊断意图必须幂等: job=%+v err=%v", second, err)
		}
	})
	t.Run("quality requires transcript and deduplicates", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		if _, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1); err == nil || !strings.Contains(err.Error(), "尚未完成转录") {
			t.Fatalf("无转录不得入队质量任务: %v", err)
		}
		seedDigestTranscript(t, s, models.SourceEpisode, sourceID)
		first, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1)
		if err != nil || first == nil {
			t.Fatalf("首次质量任务入队失败: job=%+v err=%v", first, err)
		}
		second, err := w.EnqueueKeypointQualityJob(ctx, models.SourceEpisode, sourceID, 1)
		if err != nil || second != nil {
			t.Fatalf("活跃质量意图必须幂等: job=%+v err=%v", second, err)
		}
	})
}

func TestDigestRewriteJobSurfacesProviderAndFormatFailures(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		rewriter   *fakeDigestRewriter
		wantSubstr string
	}{
		{name: "provider", rewriter: &fakeDigestRewriter{err: errors.New("rewrite unavailable")}, wantSubstr: "渠道改写"},
		{name: "format", rewriter: &fakeDigestRewriter{text: strings.Repeat("长", digestXHSMaxChars+1)}, wantSubstr: "渠道格式门禁"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			sourceID := seedEpisode(t, s)
			digest, err := s.PublishEpisodeDigest(ctx, &models.EpisodeDigest{
				SourceType: models.SourceEpisode, SourceID: sourceID, Title: "精读", Provider: "fake", Model: "m", PromptVersion: "v",
			}, []models.DigestBlock{{Type: models.DigestBlockParaphrase, Text: "正文", Citations: []string{"seg-1"}}}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.EnqueueDigestRewriteJob(ctx, digest.ID, string(models.DigestChannelXiaohongshu))
			if err != nil {
				t.Fatal(err)
			}
			err = w.doDigestRewriteJob(ctx, job, &provider.ProviderBundle{DigestRewriter: tc.rewriter})
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("应返回含 %q 的错误，实际 %v", tc.wantSubstr, err)
			}
		})
	}
}

func TestDJPlanBoundaryCalculations(t *testing.T) {
	segments := []provider.Segment{
		{ID: "s1", Start: 10, End: 20},
		{ID: "s2", Start: 2, End: 8},
		{ID: "s3", Start: 18, End: 30},
	}
	end, start, ok := segmentSpanBounds([]string{"missing", "s1", "s2", "s3"}, segments)
	if !ok || start != 2 || end != 30 || segmentSpan([]string{"s1", "s3"}, segments) != 20 {
		t.Fatalf("跨段边界计算错误: start=%v end=%v ok=%v", start, end, ok)
	}
	if _, _, ok := segmentSpanBounds([]string{"missing"}, segments); ok {
		t.Fatal("全是未知引用时不得伪造区间")
	}
	if got := djScriptEstSeconds("短"); got != 4 {
		t.Fatalf("短脚本应使用 4 秒下限: %v", got)
	}
	if got := djScriptEstSeconds(strings.Repeat("长", 40)); got != 10 {
		t.Fatalf("长脚本应按每秒 4 字估算: %v", got)
	}
	if got := firstIndex([]string{"missing", "s3", "s2"}, map[string]int{"s2": 1, "s3": 2}); got != 1 {
		t.Fatalf("应选择最早引用位置: %d", got)
	}
}

func TestNarrationPlanScriptFailuresRemainPartial(t *testing.T) {
	for _, tc := range []struct {
		name        string
		providerFn  func(*store.Store) provider.NarrationProvider
		storeClosed bool
	}{
		{name: "synthesis failure", providerFn: func(*store.Store) provider.NarrationProvider {
			return &taskNarration{available: true, synthErr: errors.New("tts failed")}
		}},
		{name: "invalid audio", providerFn: func(*store.Store) provider.NarrationProvider { return &taskNarration{available: true, garbage: true} }},
		{name: "rename failure", providerFn: func(*store.Store) provider.NarrationProvider { return &renameFailNarration{} }},
		{name: "database failure", providerFn: func(s *store.Store) provider.NarrationProvider { return &closingNarration{store: s} }, storeClosed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := context.Background()
			sourceID := seedEpisode(t, s)
			plan, err := s.CreateDJPlan(ctx, &models.DJPlan{
				SourceType: models.SourceEpisode, SourceID: sourceID, HighlightVersion: 1,
				Items: []models.DJPlanItem{
					{Kind: models.DJItemEvidence, HighlightID: "hl", SegmentIDs: []string{"seg-1"}},
					{Kind: models.DJItemNarration, ScriptKind: "outro", ScriptText: "感谢收听"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			narrated := 0
			var firstErr error
			w.synthesizePlanScripts(ctx, &models.ProcessingJob{ID: "narration-job", SourceType: models.SourceEpisode, SourceID: sourceID}, &provider.ProviderBundle{Narration: tc.providerFn(s)}, narrationTaskSnapshot{HighlightVersion: plan.HighlightVersion}, &narrated, &firstErr)
			if firstErr == nil || narrated != 0 {
				t.Fatalf("脚本失败必须作为部分失败保留: narrated=%d err=%v", narrated, firstErr)
			}
			if tc.storeClosed {
				return
			}
			rows, err := s.ListCurrentNarrationsForSource(ctx, models.SourceEpisode, sourceID)
			if err != nil || len(rows) != 0 {
				t.Fatalf("失败脚本不得发布解说行: rows=%+v err=%v", rows, err)
			}
		})
	}
}

func TestNarrationJobRejectsCorruptFrozenHighlight(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := context.Background()
	sourceID := seedEpisode(t, s)
	seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, &provider.HighlightSet{Highlights: []provider.Highlight{{ID: "hl", Gist: "gist"}}})
	completeSeedJobs(t, s)
	if _, err := s.DB.ExecContext(ctx, `UPDATE artifact_versions SET payload='{broken' WHERE source_id=? AND kind=?`, sourceID, store.KindHighlight); err != nil {
		t.Fatal(err)
	}
	job, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
	if err != nil {
		t.Fatal(err)
	}
	err = w.doNarrationJob(ctx, job, &provider.ProviderBundle{Narration: &taskNarration{available: true}})
	if err == nil || !strings.Contains(err.Error(), "解析高光载荷") {
		t.Fatalf("损坏的冻结高光必须失败: %v", err)
	}
}

func TestNarrationEnqueueAndSegmentFailureContracts(t *testing.T) {
	ctx := context.Background()
	t.Run("enqueue requires highlight and deduplicates active intent", func(t *testing.T) {
		s, w := newTestWorker(t)
		sourceID := seedEpisode(t, s)
		if _, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh"); err == nil || !strings.Contains(err.Error(), "尚未生成高光") {
			t.Fatalf("无高光不得入队解说: %v", err)
		}
		seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, twoHighlights())
		completeSeedJobs(t, s)
		first, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
		if err != nil || first == nil {
			t.Fatalf("首次解说入队失败: job=%+v err=%v", first, err)
		}
		second, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
		if err != nil || second != nil {
			t.Fatalf("活跃解说意图必须幂等: job=%+v err=%v", second, err)
		}
		_ = s.Close()
		if _, err := w.EnqueueNarrationJobForVersion(ctx, models.SourceEpisode, sourceID, 2, "hv", "", "zh"); err == nil {
			t.Fatal("队列存储错误必须上浮")
		}
	})

	for _, tc := range []struct {
		name       string
		providerFn func(*store.Store) provider.NarrationProvider
		wantError  bool
	}{
		{name: "synthesis error", providerFn: func(*store.Store) provider.NarrationProvider {
			return &taskNarration{available: true, synthErr: errors.New("tts failed")}
		}},
		{name: "rename error", providerFn: func(*store.Store) provider.NarrationProvider { return &renameFailNarration{} }},
		{name: "database error", providerFn: func(s *store.Store) provider.NarrationProvider { return &closingNarration{store: s} }, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := newTestWorker(t)
			sourceID := seedEpisode(t, s)
			seedCurrentHighlight(t, s, models.SourceEpisode, sourceID, &provider.HighlightSet{Highlights: []provider.Highlight{
				{ID: "", Gist: "invalid"},
				{ID: "hl", Gist: "valid gist", Citations: []string{"seg-1"}},
			}})
			completeSeedJobs(t, s)
			job, err := w.EnqueueNarrationJob(ctx, models.SourceEpisode, sourceID, "", "zh")
			if err != nil {
				t.Fatal(err)
			}
			err = w.doNarrationJob(ctx, job, &provider.ProviderBundle{Narration: tc.providerFn(s)})
			if tc.wantError && err == nil {
				t.Fatal("存储关闭后结果持久化必须失败")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("单段失败应以 partial failure 完成其余任务: %v", err)
			}
		})
	}
}

func TestEditorialRemoteFailuresDoNotAdvanceDurableJobs(t *testing.T) {
	ctx := context.Background()
	t.Run("claim writing", func(t *testing.T) {
		s, w := newTestWorker(t)
		_, kpid, _, job := claimWritingFixture(t, s)
		fake := &fakeClaimWriter{err: errors.New("writer unavailable")}
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "v2 写作") {
			t.Fatalf("Writer 错误必须带阶段上浮: %v", err)
		}
		fake.err = nil
		fake.result = &provider.ClaimAwareWritingResult{}
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "返回空标题") {
			t.Fatalf("空 Writer 结果必须拒绝: %v", err)
		}
		fake.result = goodClaimWritingResult("# R19\n\n正文不含映射片段", kpid)
		if err := w.doClaimWritingJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "ClaimMap 校验失败") {
			t.Fatalf("越界 ClaimMap 必须拒绝: %v", err)
		}
	})

	t.Run("claim revision", func(t *testing.T) {
		s, w := newTestWorker(t)
		draftID, baseID := revisionFixture(t, s, w)
		job, err := s.EnqueueClaimRevisionForRevision(ctx, baseID)
		if err != nil {
			t.Fatal(err)
		}
		fake := &fakeDualWriter{err: errors.New("revision unavailable")}
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "claim-aware AI 修订") {
			t.Fatalf("修订调用错误必须上浮: %v", err)
		}
		fake.err = nil
		fake.result = &provider.ClaimAwareWritingResult{}
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "返回空标题") {
			t.Fatalf("空修订结果必须拒绝: %v", err)
		}
		fake.result = &provider.ClaimAwareWritingResult{Title: "坏修订", Markdown: "# 坏修订\n\n越界内容", ClaimMap: []provider.ClaimMapEntry{{Excerpt: "越界内容", ClaimKind: provider.ClaimSource, MaterialIDs: []string{"invented"}}}}
		if err := w.doClaimRevisionJob(ctx, job, &provider.ProviderBundle{Writer: fake}); err == nil || !strings.Contains(err.Error(), "AI 修订校验失败") {
			t.Fatalf("越权修订必须拒绝: draft=%s err=%v", draftID, err)
		}
	})

	t.Run("claim and style review", func(t *testing.T) {
		s, w := newTestWorker(t)
		_, kpID, _, writeJob := claimWritingFixture(t, s)
		w.bundleFor = bundleWithWriter(&fakeClaimWriter{result: goodClaimWritingResult(r20Markdown, kpID)})
		if err := w.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
		var revisionID string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_revisions WHERE origin_job_id=?`, writeJob.ID).Scan(&revisionID); err != nil {
			t.Fatal(err)
		}
		claimJob, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindClaim)
		if err != nil {
			t.Fatal(err)
		}
		claim := &fakeClaimReviewer{err: errors.New("review unavailable")}
		if err := w.doClaimReviewJob(ctx, claimJob, &provider.ProviderBundle{ClaimReviewer: claim}); err == nil || !strings.Contains(err.Error(), "独立主张审校") {
			t.Fatalf("主张审校错误必须上浮: %v", err)
		}
		claim.err = nil
		claim.result = &provider.ClaimReviewResult{Status: provider.ClaimReviewFailed, Findings: []provider.ClaimReviewFinding{{Excerpt: "不在正文", IssueKind: "misattributed", Detail: "越界"}}}
		if err := w.doClaimReviewJob(ctx, claimJob, &provider.ProviderBundle{ClaimReviewer: claim}); err == nil || !strings.Contains(err.Error(), "结果校验失败") {
			t.Fatalf("越界审校结论必须拒绝: %v", err)
		}

		styleJob, err := s.EnqueueRevisionReview(ctx, revisionID, store.ReviewKindStyle)
		if err != nil {
			t.Fatal(err)
		}
		style := &fakeStyleEditor{err: errors.New("style unavailable")}
		if err := w.doStyleReviewJob(ctx, styleJob, &provider.ProviderBundle{StyleEditor: style}); err == nil || !strings.Contains(err.Error(), "独立风格审校") {
			t.Fatalf("风格审校错误必须上浮: %v", err)
		}
	})
}

type renameFailNarration struct{}

func (*renameFailNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	if err := writeValidTinyWAV(outPath); err != nil {
		return nil, err
	}
	if err := os.Mkdir(strings.TrimSuffix(outPath, ".tmp"), 0o755); err != nil {
		return nil, err
	}
	return &provider.NarrationResult{AudioPath: outPath, CharCount: len([]rune(text)), Voice: voice, Model: "test"}, nil
}
func (*renameFailNarration) Available() bool { return true }
func (*renameFailNarration) Name() string    { return "rename-fail" }

type closingNarration struct {
	store *store.Store
}

func (f *closingNarration) Synthesize(text, voice, outPath string) (*provider.NarrationResult, error) {
	err := writeValidTinyWAV(outPath)
	if err == nil {
		_ = f.store.Close()
	}
	return &provider.NarrationResult{AudioPath: outPath, CharCount: len([]rune(text)), Voice: voice, Model: "test"}, err
}
func (*closingNarration) Available() bool { return true }
func (*closingNarration) Name() string    { return "closing" }

func writeValidTinyWAV(path string) error {
	dataSize := uint32(800)
	buf := make([]byte, 0, 44+int(dataSize))
	buf = append(buf, "RIFF"...)
	buf = append(buf, byte(36+dataSize), 0, 0, 0)
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = append(buf, 16, 0, 0, 0, 1, 0, 1, 0)
	buf = append(buf, 0x40, 0x1f, 0, 0, 0, 0x7d, 0, 0)
	buf = append(buf, 2, 0, 16, 0)
	buf = append(buf, "data"...)
	buf = append(buf, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	buf = append(buf, make([]byte, dataSize)...)
	return os.WriteFile(path, buf, 0o644)
}
