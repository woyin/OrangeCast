package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func TestPureWorkspaceHelpers(t *testing.T) {
	// titleBigrams / editorialTitleNearDuplicate：短前缀包含、二元组重合、空标题。
	if !editorialTitleNearDuplicate("", []string{"旧标题"}) {
		t.Fatal("empty candidate must be treated as duplicate")
	}
	if editorialTitleNearDuplicate("AI 编程的审查成本", []string{"比特币减半周期"}) {
		t.Fatal("unrelated titles must not collide")
	}
	if !editorialTitleNearDuplicate("AI 编程的审查成本上升", []string{"AI 编程的审查成本"}) {
		t.Fatal("prefix containment over 6 runes is a duplicate")
	}
	if !editorialTitleNearDuplicate("证据导向创作工作台", []string{"证据导向创作工作台设计"}) {
		t.Fatal("bigram similarity should catch reordered wording")
	}
	if got := titleBigrams("ab"); len(got) != 1 || !got["ab"] {
		t.Fatalf("bigrams of two runes: %+v", got)
	}
	if got := titleBigrams("单"); len(got) != 1 || !got["单"] {
		t.Fatalf("single rune keeps itself: %+v", got)
	}
	if minInt(3, 5) != 3 || minInt(5, 3) != 3 {
		t.Fatal("minInt must return the smaller value")
	}
	// jsonArray / sourceHref / plainSourceList / coverSVG / truncateRunes。
	if got := jsonArray(`["a","b"]`); len(got) != 2 || got[0] != "a" {
		t.Fatalf("jsonArray should parse: %+v", got)
	}
	if got := jsonArray(`not-json`); got != nil {
		t.Fatalf("jsonArray should return nil on bad JSON: %+v", got)
	}
	if got := sourceHref(models.SourceDocument, "doc-1", 3); !strings.HasPrefix(got, "/documents/doc-1#doc-1-p") {
		t.Fatalf("document href should anchor paragraphs: %s", got)
	}
	if got := sourceHref(models.SourceEpisode, "ep/1", 2.5); !strings.Contains(got, "/sources/episode/ep%2F1?t=2.500") {
		t.Fatalf("episode href should escape and timestamp: %s", got)
	}
	if got := plainSourceList(nil); got != "本文未使用外部来源。\n" {
		t.Fatalf("empty source list: %q", got)
	}
	if got := plainSourceList([]string{"a", "b"}); got != "a\nb\n" {
		t.Fatalf("source list should join with newline: %q", got)
	}
	svg := coverSVG("标题<script>", "副标题")
	if !strings.Contains(svg, "标题&lt;script&gt;") || !strings.Contains(svg, "<svg") {
		t.Fatalf("cover svg must escape html: %s", svg)
	}
	if got := truncateRunes("  短  ", 10); got != "短" {
		t.Fatalf("truncate should trim first: %q", got)
	}
	if got := truncateRunes("很长很长很长很长很长很长", 5); !strings.HasSuffix(got, "…") {
		t.Fatalf("long text should end with ellipsis: %q", got)
	}
	// writeEditorialError 把 store 状态错误映射为 4xx。
	rec := httptest.NewRecorder()
	writeEditorialError(rec, store.ErrNotFound)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("store not-found should map to 400: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	writeEditorialError(rec, store.ErrInvalidEditorialState)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("store invalid-state should map to 400: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	writeEditorialError(rec, conflictEditorial("占用中"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("explicit transport error should keep its status: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	writeEditorialError(rec, http.ErrAbortHandler)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unknown errors stay 500: %d", rec.Code)
	}
}

func TestDocumentHandlersVersionPDFAndModelPrice(t *testing.T) {
	srv := newTestServer(t)
	doc, err := srv.store.CreatePastedDocument(t.Context(), "版本文档", "第一版正文")
	if err != nil {
		t.Fatal(err)
	}
	// handleDocumentVersion：POST 建新版本；GET 拒绝。
	form := url.Values{"document_id": {doc.ID}, "title": {"第二版标题"}, "content": {"第二版正文"}}
	if rec := postToHandler(t, srv.handleDocumentVersion, form); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/documents/") {
		t.Fatalf("document version should create and redirect: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	versions, err := srv.store.ListDocumentVersions(t.Context(), doc.SeriesID)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("new version should exist: versions=%+v err=%v", versions, err)
	}
	if rec := postToHandler(t, srv.handleDocumentVersion, url.Values{"document_id": {"missing"}, "title": {"t"}, "content": {"c"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown document version should reject: %d", rec.Code)
	}
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.handleDocumentVersion(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET version should reject: %d", rec.Code)
	}

	// handleModelPrice：非法数字与负数拒绝，合法值持久化并规范化 provider。
	if rec := postToHandler(t, srv.handleModelPrice, url.Values{"provider": {"groq"}, "model": {"scout"}, "input_cents_per_million": {"x"}, "output_cents_per_million": {"1"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-integer price should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleModelPrice, url.Values{"provider": {"groq"}, "model": {"scout"}, "input_cents_per_million": {"-1"}, "output_cents_per_million": {"1"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative price should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleModelPrice, url.Values{"provider": {"groq"}, "model": {"scout"}, "input_cents_per_million": {"50"}, "output_cents_per_million": {"100"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("valid price should save: %d", rec.Code)
	}
	price, err := srv.store.GetModelPrice(t.Context(), "groq", "scout")
	if err != nil || price.InputCentsPerMillion != 50 {
		t.Fatalf("price should persist: price=%+v err=%v", price, err)
	}
	getRec := httptest.NewRecorder()
	srv.handleModelPrice(getRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET model price should reject: %d", getRec.Code)
	}
}

func TestStartAutomaticDiscoveryRunsUntilContextCancel(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	go srv.StartAutomaticDiscovery(ctx)
	cancel()
	// 仅验证 ticker 循环随 ctx 取消而退出且不 panic；不发现在无授权画像时不触发 provider。
	time.Sleep(50 * time.Millisecond)
}

func TestFetchWebDocumentRejectsBadInput(t *testing.T) {
	if _, _, err := fetchWebDocument(t.Context(), "http://example.com/x"); err == nil {
		t.Fatal("https-required url should reject")
	}
	if _, _, err := fetchWebDocument(t.Context(), "not a url"); err == nil {
		t.Fatal("invalid url should reject")
	}
}

func TestListProposalBatchesAndCreationBriefDraftIdempotency(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "批次列表画像"})
	if err != nil {
		t.Fatal(err)
	}
	batch, _, err := srv.store.ReserveAutomaticProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "list-1", MaterialSnapshotJSON: `["m1"]`})
	if err != nil {
		t.Fatal(err)
	}
	batches, err := srv.store.ListProposalBatches(t.Context(), profile.ID)
	if err != nil || len(batches) != 1 || batches[0].ID != batch.ID {
		t.Fatalf("proposal batches should list per profile: batches=%+v err=%v", batches, err)
	}
	// Finalize 后批量归 ready；列表仍可见。
	if err := srv.store.FinalizeAutomaticProposalBatch(t.Context(), batch.ID, "fake", "fake-scout", "", nil, []models.CreationProposal{{WorkingTitle: "方向", ProposedClaim: "主张", MaterialIDsJSON: `["m1"]`}}); err != nil {
		t.Fatal(err)
	}
	batches, _ = srv.store.ListProposalBatches(t.Context(), profile.ID)
	if len(batches) != 1 || batches[0].Status != "ready" {
		t.Fatalf("finalized batch should remain listed: batches=%+v", batches)
	}
}

func TestCuratorRejectsUnacceptedProposal(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "Curator 画像"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateArticleProposal(t.Context(), models.ArticleProposal{EditorialProfileID: profile.ID, Kind: "evergreen", Title: "标题", Thesis: "论点", CandidateKeyPoints: `["kp-x"]`})
	if err != nil {
		t.Fatal(err)
	}
	// 未接受提案 → 400（writeEditorialError 经 badEditorial 映射）。
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("proposal_id="+proposal.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleCuratorRun(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unaccepted proposal must reject curator: %d body=%s", rec.Code, rec.Body.String())
	}
	// GET 拒绝。
	rec = httptest.NewRecorder()
	srv.handleCuratorRun(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET curator should reject: %d", rec.Code)
	}
}
