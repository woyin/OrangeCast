package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/safehttp"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// EpisodeDigest 生成编排（ADR-0023）。T1 手动触发 → job episode_digest：
//  1. 读本集当前 Transcript（Segment 素材）+ KnowledgeCard 摘要 + Owner 笔记；
//  2. DigestWriter.ComposeDigest 出长文初稿（转述/AI 展开/笔记块 + FactGap 清单）；
//  3. G2 门禁初审（引语截断后复检）；
//  4. FactGap 非空且搜索可用 → 每个 gap 一次 SourceSearch → fetchWebDocument 落源为 Document
//     → WeaveDigestFacts 补织 cited_fact 块 → 复跑门禁；
//  5. 搜索不可用/未命中 → C1 降级生成（degraded=1，缺口留档）；
//  6. 落库不可变修订（episode_digests + digest_blocks + search_sources + fact_gaps）；
//  7. 渠道改写（小红书）过同一门禁后存 digest_rewrites。
//
// 批量逐集隔离（D1）：每集一个独立 job，一集失败不影响其余。
func (w *Worker) doDigest(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.DigestWriter == nil {
		return fmt.Errorf("DigestWriter Provider 不可用")
	}

	// 1) 本集素材
	segments, summary, title, err := w.digestSourceMaterial(ctx, job)
	if err != nil {
		return err
	}
	notes := w.digestOwnerNotes(ctx, job)

	// 2) 长文初稿
	draft, err := bundle.DigestWriter.ComposeDigest(ctx, provider.DigestWritingRequest{
		SourceTitle:       title,
		SourceSummary:     summary,
		Segments:          segments,
		Notes:             notes,
		FactGapBudget:     digestFactGapBudget,
		TargetLengthChars: digestTargetLength,
	})
	if err != nil {
		return fmt.Errorf("精读文初稿: %w", err)
	}
	_ = w.recordDigestUsage(ctx, job, "digest_compose", digestProviderName(bundle.DigestWriter), provider.DigestWriterPromptVersion, draft.Usage)

	// 3) G2 初审：引语截断 → 复检；复检不过 = 生成失败（不静默降级正文不变量）
	blocks := digestTruncateQuotes(draft.Blocks)
	if issues := provider.ValidateDigestBlocks(provider.DigestValidationInput{
		Blocks: blocks, Segments: segMap(segments), Notes: noteMap(notes), QuoteMaxChars: digestQuoteMaxChars,
	}); len(issues) > 0 {
		return fmt.Errorf("初稿门禁失败：%v", issues)
	}

	// 4) FactGap 消解（SourceSearch 落源 → 补织）
	degraded := false
	var searchRows []models.DigestSearchSource
	var unresolvedGaps []models.DigestFactGap
	if len(draft.FactGaps) > 0 {
		woven, rows, gaps, searchOK := w.digestResolveGaps(ctx, job, bundle, draft, blocks)
		if searchOK {
			blocks = append(blocks, woven...)
			searchRows = rows
			unresolvedGaps = gaps
			// 补织块复检（含落源 Document Segment）
			if issues := provider.ValidateDigestBlocks(provider.DigestValidationInput{
				Blocks: blocks, Segments: segMap(segments),
				DocumentSegments: w.digestDocSegments(ctx, rows), Notes: noteMap(notes), QuoteMaxChars: digestQuoteMaxChars,
			}); len(issues) > 0 {
				return fmt.Errorf("补织后门禁失败：%v", issues)
			}
		} else {
			// C1 降级：正文已过初审，缺口留档，侧栏可见"联网补充不可用"
			degraded = true
			unresolvedGaps = gaps
			log.Printf("任务 %s SourceSearch 不可用，降级生成（缺口 %d 条留档）", job.ID, len(draft.FactGaps))
		}
	}
	digest, err := w.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: job.SourceType, SourceID: job.SourceID, Title: draft.Title, Degraded: degraded,
		Provider: digestProviderName(bundle.DigestWriter), Model: digestProviderName(bundle.DigestWriter), PromptVersion: provider.DigestWriterPromptVersion,
	}, digestModelsBlocks(blocks))
	if err != nil {
		return fmt.Errorf("保存精读文: %w", err)
	}
	if len(searchRows) > 0 {
		if err := w.store.AddDigestSearchSources(ctx, digest.ID, searchRows); err != nil {
			return err
		}
	}
	if len(unresolvedGaps) > 0 {
		if err := w.store.AddDigestFactGaps(ctx, digest.ID, unresolvedGaps); err != nil {
			return err
		}
	}

	// 6) 渠道改写（失败不阻塞长文版；Owner 可在页面重试）
	if bundle.DigestRewriter != nil {
		if err := w.digestRewrite(ctx, job, bundle, digest.ID, draft.Title, blocks); err != nil {
			log.Printf("任务 %s 渠道改写失败（不阻塞）: %v", job.ID, err)
		}
	}
	return nil
}

// digestSourceMaterial 读本集 Transcript Segments + 卡片摘要 + 标题。
func (w *Worker) digestSourceMaterial(ctx context.Context, job *models.ProcessingJob) ([]provider.Segment, string, string, error) {
	av, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindTranscript)
	if err != nil {
		return nil, "", "", fmt.Errorf("读取当前转录版本（需先完成转录）: %w", err)
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &payload); err != nil {
		return nil, "", "", fmt.Errorf("解析转录载荷: %w", err)
	}
	title, summary := "", ""
	if job.SourceType == models.SourceEpisode {
		if ep, err := w.store.GetEpisodeByID(ctx, job.SourceID); err == nil {
			title = ep.Title
		}
	} else if job.SourceType == models.SourceUpload {
		if up, err := w.store.GetUploadByID(ctx, job.SourceID); err == nil {
			title = up.OriginalFilename
		}
	}
	if cv, err := w.store.GetCurrentVersion(ctx, job.SourceType, job.SourceID, store.KindKnowledgeCard); err == nil {
		var card provider.KnowledgeCard
		if json.Unmarshal([]byte(cv.Payload), &card) == nil {
			summary = card.Summary.Text
			if title == "" {
				title = card.Title
			}
		}
	}
	return payload.Segments, summary, title, nil
}

// digestOwnerNotes 读本集 Owner 笔记（source_note + owner_reflection）。
func (w *Worker) digestOwnerNotes(ctx context.Context, job *models.ProcessingJob) []provider.DigestOwnerNote {
	rows, err := w.store.ListOwnerNotes(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return nil
	}
	out := make([]provider.DigestOwnerNote, 0, len(rows))
	for _, n := range rows {
		out = append(out, provider.DigestOwnerNote{NoteID: n.ID, Text: n.Content, Kind: n.Kind})
	}
	return out
}

// digestResolveGaps 消解 FactGap：逐条搜索 → 落源 → 汇总补织。
// 返回（补织块, 落源行, 未消解缺口, 搜索是否整体可用）。
// 搜索整体失败 → searchOK=false（C1 降级）；单条未命中只是该条留档，不算失败。
func (w *Worker) digestResolveGaps(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle, draft *provider.DigestWritingResult, blocks []provider.DigestBlockDraft) ([]provider.DigestBlockDraft, []models.DigestSearchSource, []models.DigestFactGap, bool) {
	if bundle.DigestSearch == nil {
		return nil, nil, gapModels(draft.FactGaps), false
	}
	var docs []provider.DigestDocument
	var rows []models.DigestSearchSource
	seen := map[string]bool{}
	searchFailed := false
	for _, gap := range draft.FactGaps {
		results, err := bundle.DigestSearch.Search(ctx, gap)
		if err != nil {
			log.Printf("任务 %s 搜索缺口失败（%s）: %v", job.ID, gap, err)
			searchFailed = true
			continue
		}
		resolved := false
		for _, res := range results {
			if seen[res.URL] || !strings.HasPrefix(res.URL, "http") {
				continue
			}
			doc, ok := w.digestIngestDocument(ctx, res.URL)
			if !ok {
				continue
			}
			seen[res.URL] = true
			docs = append(docs, doc)
			rows = append(rows, models.DigestSearchSource{Query: gap, URL: res.URL, Title: doc.Title, DocumentID: doc.DocumentID})
			resolved = true
			break // 每个缺口取首个可落源结果
		}
		if !resolved {
			log.Printf("任务 %s 缺口未命中可落源结果（%s）", job.ID, gap)
		}
	}
	if searchFailed && len(docs) == 0 {
		return nil, nil, gapModels(draft.FactGaps), false
	}
	var woven []provider.DigestBlockDraft
	if len(docs) > 0 {
		res, err := bundle.DigestWriter.WeaveDigestFacts(ctx, provider.DigestWeaveRequest{
			Title: draft.Title, Blocks: blocks, FactGaps: draft.FactGaps, Documents: docs,
		})
		if err != nil {
			log.Printf("任务 %s 补织事实块失败（降级为留档缺口）: %v", job.ID, err)
			return nil, rows, gapModels(draft.FactGaps), true
		}
		for _, b := range res.Blocks {
			// document_id → TargetSourceID 适配（模型字段名与领域字段名）
			if b.TargetSourceID == "" {
				b.TargetSourceID = b.NoteID // Weave 响应中误放时兜底；正常为空则被门禁拒绝
			}
			woven = append(woven, b)
		}
		_ = w.recordDigestUsage(ctx, job, "digest_weave", digestProviderName(bundle.DigestWriter), provider.DigestWriterPromptVersion, res.Usage)
	}
	unresolved := gapModels(draft.FactGaps)
	// 有落源的缺口视为已消解（rows[].Query 即缺口文本）
	resolved := map[string]bool{}
	for _, row := range rows {
		resolved[row.Query] = true
	}
	filtered := unresolved[:0]
	for _, g := range unresolved {
		if !resolved[g.Text] {
			filtered = append(filtered, g)
		}
	}
	return woven, rows, filtered, true
}

// digestIngestDocument 把一个 URL 经 SSRF 防护管道落源为 Document Source（幂等：同 URL 已存在则复用）。
func (w *Worker) digestIngestDocument(ctx context.Context, rawURL string) (provider.DigestDocument, bool) {
	if existing, err := w.store.GetDocumentByOriginURL(ctx, rawURL); err == nil {
		return documentToDigest(existing), true
	}
	title, content, err := fetchReadableDocument(ctx, rawURL)
	if err != nil {
		log.Printf("落源抓取失败（%s）: %v", rawURL, err)
		return provider.DigestDocument{}, false
	}
	doc, err := w.store.CreateWebDocument(ctx, title, rawURL, content)
	if err != nil {
		log.Printf("落源建档失败（%s）: %v", rawURL, err)
		return provider.DigestDocument{}, false
	}
	return documentToDigest(doc), true
}

// documentToDigest 把 Document 投影为 DigestDocument（含稳定 Segment）。
func documentToDigest(doc *models.Document) provider.DigestDocument {
	segs := store.DocumentSegments(doc)
	out := make([]provider.Segment, 0, len(segs))
	for _, s := range segs {
		out = append(out, provider.Segment{ID: s.ID, Text: s.Text})
	}
	return provider.DigestDocument{DocumentID: doc.ID, Title: doc.Title, URL: doc.OriginURL, Segments: out}
}

// digestRewrite 渠道语气版本（F3）：输入只有已过门禁的长文块，产物过格式约束后落库。
func (w *Worker) digestRewrite(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle, digestID, title string, blocks []provider.DigestBlockDraft) error {
	res, err := bundle.DigestRewriter.RewriteDigest(ctx, provider.DigestRewriteRequest{
		Channel: models.DigestChannelXiaohongshu, Blocks: blocks, MaxChars: digestXHSMaxChars, MaxHashtags: digestXHSMaxTags,
	})
	if err != nil {
		return err
	}
	if err := provider.ValidateDigestRewrite(res.Text, digestXHSMaxChars, digestXHSMaxTags); err != nil {
		return fmt.Errorf("渠道格式门禁: %w", err)
	}
	_, err = w.store.UpsertDigestRewrite(ctx, &models.DigestRewrite{
		DigestID: digestID, Channel: models.DigestChannelXiaohongshu, Text: res.Text,
		Provider: digestProviderName(bundle.DigestRewriter), Model: digestProviderName(bundle.DigestRewriter),
	})
	if err != nil {
		return err
	}
	_ = w.recordDigestUsage(ctx, job, "digest_rewrite", digestProviderName(bundle.DigestRewriter), provider.DigestWriterPromptVersion, res.Usage)
	return nil
}

// digestDocSegments 汇总落源行的 Document Segment（补织门禁输入）。
func (w *Worker) digestDocSegments(ctx context.Context, rows []models.DigestSearchSource) map[string][]provider.Segment {
	out := make(map[string][]provider.Segment, len(rows))
	for _, row := range rows {
		doc, err := w.store.GetDocument(ctx, row.DocumentID)
		if err != nil {
			continue
		}
		dd := documentToDigest(doc)
		out[dd.DocumentID] = dd.Segments
	}
	return out
}

// recordDigestUsage 记费用审计（digest_ 前缀任务允许无画像；失败只 log，不阻塞成文）。
func (w *Worker) recordDigestUsage(ctx context.Context, job *models.ProcessingJob, taskKind, providerName, promptVersion string, usage provider.TaskUsage) error {
	model := providerName
	cost, err := w.store.CalculateEditorialCost(ctx, providerName, model, usage.InputUnits, usage.OutputUnits)
	if err != nil {
		return err
	}
	_, err = w.store.RecordEditorialUsage(ctx, models.EditorialUsageRecord{
		EditorialProfileID: "", TaskKind: taskKind, EntityType: "episode_digest", EntityID: job.SourceID,
		Provider: providerName, Model: model, PromptVersion: promptVersion,
		InputUnits: usage.InputUnits, OutputUnits: usage.OutputUnits, CostCents: cost, RetryCount: usage.RetryCount,
	})
	if err != nil {
		log.Printf("任务 %s 费用记录失败（不阻塞）: %v", job.ID, err)
	}
	return nil
}

// ---- 纯辅助 ----

const (
	digestFactGapBudget = 5 // 单次生成最多触发检索的缺口数
	digestTargetLength  = 1800
	digestQuoteMaxChars = 40  // 直接引语上限（RightsConstraint 默认投影）
	digestXHSMaxChars   = 800 // 小红书字数上限
	digestXHSMaxTags    = 5
)

func segMap(segs []provider.Segment) map[string]provider.Segment {
	m := make(map[string]provider.Segment, len(segs))
	for _, s := range segs {
		m[s.ID] = s
	}
	return m
}

func noteMap(notes []provider.DigestOwnerNote) map[string]string {
	m := make(map[string]string, len(notes))
	for _, n := range notes {
		m[n.NoteID] = n.Text
	}
	return m
}

// digestTruncateQuotes 对全部块执行引语截断（超限自动转述化，ADR-0023 G2 规则 3）。
func digestTruncateQuotes(blocks []provider.DigestBlockDraft) []provider.DigestBlockDraft {
	out := make([]provider.DigestBlockDraft, len(blocks))
	copy(out, blocks)
	for i := range out {
		out[i].Text = provider.TruncateQuotedSpans(out[i].Text, digestQuoteMaxChars)
	}
	return out
}

// digestModelsBlocks provider 草稿块 → 领域块。
func digestModelsBlocks(blocks []provider.DigestBlockDraft) []models.DigestBlock {
	out := make([]models.DigestBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, models.DigestBlock{
			Type: models.DigestBlockType(b.Type), Text: b.Text, Citations: b.Citations,
			TargetSourceID: b.TargetSourceID, NoteID: b.NoteID,
		})
	}
	return out
}

// gapModels 缺口字符串 → 领域对象。
func gapModels(gaps []string) []models.DigestFactGap {
	out := make([]models.DigestFactGap, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, models.DigestFactGap{Text: g})
	}
	return out
}

// digestProviderName 从 ConfigurableProvider 或命名接口取 provider 标识。
func digestProviderName(p any) string {
	if c, ok := p.(interface{ Name() string }); ok {
		return c.Name()
	}
	return "unknown"
}

// fetchReadableDocument queue 侧网页落源抓取（与 server.fetchWebDocument 同构：
// safehttp SSRF 防护 + 4MB 上限 + 正文提取）。不 import server，避免包环。
func fetchReadableDocument(ctx context.Context, rawURL string) (string, string, error) {
	if err := safehttp.ValidateURL(rawURL); err != nil {
		return "", "", err
	}
	client := safehttp.NewClient(5, 4<<20, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("抓取网页: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("网页返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(body)) > 4<<20 {
		return "", "", fmt.Errorf("网页正文超上限")
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/plain") {
		return rawURL, strings.TrimSpace(string(body)), nil
	}
	if !strings.Contains(ct, "text/html") {
		return "", "", fmt.Errorf("不支持的内容类型: %s", ct)
	}
	return extractReadableHTML(string(body))
}

// extractReadableHTML 极简正文提取：取 <title> 与去标签文本（落源只需可引用文本）。
func extractReadableHTML(raw string) (string, string, error) {
	title := ""
	if m := regexp.MustCompile(`<title[^>]*>(.*?)</title>`).FindStringSubmatch(strings.ToLower(raw)); len(m) == 2 {
		title = strings.TrimSpace(m[1])
	}
	if title == "" {
		title = "检索落源文档"
	}
	text := htmlRe.ReplaceAllString(raw, "")
	text = scripts.ReplaceAllString(text, "")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 20000 {
		text = text[:20000]
	}
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("正文为空")
	}
	return title, text, nil
}

var (
	htmlRe  = regexp.MustCompile(`<[^>]+>`)
	scripts = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)
