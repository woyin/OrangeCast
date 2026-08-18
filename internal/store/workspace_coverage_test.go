package store

import (
	"context"
	"errors"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// seedWorkspaceProfile 建立一个工作空间测试所需的画像。
func seedWorkspaceProfile(t *testing.T, s *Store, name string) *models.EditorialProfile {
	t.Helper()
	profile, err := s.CreateEditorialProfile(context.Background(), models.EditorialProfile{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

// seedTranscriptEpisode 建立带当前转录稿的单集，返回该单集 ID。
func seedTranscriptEpisode(t *testing.T, s *Store, guid string) string {
	t.Helper()
	ctx := context.Background()
	podcast, err := s.CreatePodcast(ctx, "https://feed.example.com/"+guid+".xml", guid+" Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: guid, Title: guid + " 单集", AudioURL: "https://cdn.example.com/" + guid + ".mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := s.ListEpisodes(ctx, podcast.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatalf("episode setup failed: episodes=%+v err=%v", episodes, err)
	}
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, episodes[0].ID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, "test", "test", "1", job.ID, `{"language":"zh","text":"素材","segments":[{"id":"seg-1","start":0,"end":1,"text":"素材"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentVersion(ctx, models.SourceEpisode, episodes[0].ID, KindTranscript, version); err != nil {
		t.Fatal(err)
	}
	return episodes[0].ID
}

func TestDocumentOriginsVersionsAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pasted, err := s.CreatePastedDocument(ctx, "粘贴文档", "第一段内容\n\n第二段内容")
	if err != nil || pasted.OriginKind != "pasted" {
		t.Fatalf("pasted document should persist: doc=%+v err=%v", pasted, err)
	}
	web, err := s.CreateWebDocument(ctx, "网页文档", "https://example.com/article", "网页正文")
	if err != nil || web.OriginKind != "url" || web.OriginURL != "https://example.com/article" {
		t.Fatalf("web document should persist origin URL: doc=%+v err=%v", web, err)
	}
	pdf, err := s.CreatePDFDocument(ctx, "PDF 文档", "report.pdf", "PDF 提取文本")
	if err != nil || pdf.OriginKind != "pdf" {
		t.Fatalf("PDF document should persist: doc=%+v err=%v", pdf, err)
	}
	all, err := s.ListDocuments(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("documents should list all origins: docs=%+v err=%v", all, err)
	}
	version, err := s.CreateDocumentVersion(ctx, pasted.ID, "修订标题", "全新正文")
	if err != nil || version.Version != 2 || version.SeriesID != pasted.SeriesID {
		t.Fatalf("document version should extend series: version=%+v err=%v", version, err)
	}
	versions, err := s.ListDocumentVersions(ctx, pasted.SeriesID)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("version history should be newest first: versions=%+v err=%v", versions, err)
	}
	if _, err := s.CreateDocumentVersion(ctx, "missing", "t", "c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("versioning unknown document should fail: %v", err)
	}
	card, err := s.GetDocumentKnowledgeCard(ctx, pasted.ID)
	if err != nil || card.Title != pasted.Title || len(card.KeyPoints) == 0 {
		t.Fatalf("document knowledge card should derive from segments: card=%+v err=%v", card, err)
	}
	if _, err := s.GetDocumentKnowledgeCard(ctx, "missing"); err == nil {
		t.Fatal("missing document card should fail")
	}
	hits, err := s.SearchDocuments(ctx, "网页正文", 20)
	if err != nil || len(hits) != 1 || hits[0].ID != web.ID {
		t.Fatalf("document search should find web document: hits=%+v err=%v", hits, err)
	}
	if clamped, err := s.SearchDocuments(ctx, "网页正文", 0); err != nil || len(clamped) != 1 {
		t.Fatalf("zero limit should clamp to default and still search: hits=%+v err=%v", clamped, err)
	}
}

func TestSourceOptionsAndApprovedProviders(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "options@example.com")
	episodeID := seedTranscriptEpisode(t, s, "options-ep")
	doc, err := s.CreatePastedDocument(ctx, "选项文档", "内容")
	if err != nil {
		t.Fatal(err)
	}
	options, err := s.ListSourceOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, option := range options {
		found[option.SourceID] = true
	}
	if !found[episodeID] || !found[doc.ID] {
		t.Fatalf("source options should include episode and document: options=%+v", options)
	}
	if err := s.SetSourceApprovedProviders(ctx, models.SourceEpisode, episodeID, []string{" Groq ", "", "groq", "openai"}); err != nil {
		t.Fatal(err)
	}
	policy, err := s.GetSourcePolicy(ctx, models.SourceEpisode, episodeID)
	if err != nil || len(policy.ApprovedProviders) != 2 {
		t.Fatalf("approved providers should dedupe and trim: policy=%+v err=%v", policy, err)
	}
	if err := s.SetSourceApprovedProviders(ctx, models.SourceType("unknown"), "x", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("unknown source type should reject: %v", err)
	}
	if err := s.UpdateSourcePolicy(ctx, models.SourceEpisode, episodeID, models.SourcePolicy{ProductionUse: "bogus"}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid production use should reject: %v", err)
	}
	if err := s.UpdateSourcePolicy(ctx, models.SourceEpisode, episodeID, models.SourcePolicy{ProductionUse: "internal", ModelDataPolicy: models.ModelDataApprovedProvidersOnly, ApprovedProviders: []string{"openai"}, Archived: true}); err != nil {
		t.Fatal(err)
	}
	policy, err = s.GetSourcePolicy(ctx, models.SourceEpisode, episodeID)
	if err != nil || !policy.Archived || policy.ModelDataPolicy != models.ModelDataApprovedProvidersOnly {
		t.Fatalf("source policy update should persist archive + policy: policy=%+v err=%v", policy, err)
	}
}

func TestEditorialTaskClaimLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.ClaimEditorialTask(ctx, "", "key"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty task kind should reject: %v", err)
	}
	if _, err := s.ClaimEditorialTask(ctx, "writer", ""); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty idempotency key should reject: %v", err)
	}
	claimed, err := s.ClaimEditorialTask(ctx, "writer_initial", "brief-1")
	if err != nil || !claimed {
		t.Fatalf("first claim should succeed: claimed=%v err=%v", claimed, err)
	}
	again, err := s.ClaimEditorialTask(ctx, "writer_initial", "brief-1")
	if err != nil || again {
		t.Fatalf("running claim must not be double-acquired: again=%v err=%v", again, err)
	}
	if _, err := s.GetEditorialTaskResult(ctx, "writer_initial", "brief-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfinished task result should be ErrNotFound: %v", err)
	}
	if err := s.SaveEditorialTaskResult(ctx, "writer_initial", "brief-1", "not-json"); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("non-JSON result should reject: %v", err)
	}
	if err := s.SaveEditorialTaskResult(ctx, "writer_initial", "brief-1", `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEditorialTaskResult(ctx, "unknown", "key", `{"ok":true}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("saving result on unknown task should fail: %v", err)
	}
	payload, err := s.GetEditorialTaskResult(ctx, "writer_initial", "brief-1")
	if err != nil || payload != `{"ok":true}` {
		t.Fatalf("saved result should be readable: payload=%q err=%v", payload, err)
	}
	if err := s.FinishEditorialTask(ctx, "writer_initial", "brief-1", nil); err != nil {
		t.Fatal(err)
	}
	// 已完成的任务不可重跑，但失败的任务可以重试。
	if reclaimed, err := s.ClaimEditorialTask(ctx, "writer_initial", "brief-1"); err != nil || reclaimed {
		t.Fatalf("completed task must not re-run: reclaimed=%v err=%v", reclaimed, err)
	}
	claimed, err = s.ClaimEditorialTask(ctx, "curator", "proposal-1")
	if err != nil || !claimed {
		t.Fatal(err)
	}
	if err := s.FinishEditorialTask(ctx, "curator", "proposal-1", errors.New("provider 不可用")); err != nil {
		t.Fatal(err)
	}
	retried, err := s.ClaimEditorialTask(ctx, "curator", "proposal-1")
	if err != nil || !retried {
		t.Fatalf("failed task should be retryable: retried=%v err=%v", retried, err)
	}
}

func TestBriefAndDraftLookupsPlusReviewIndex(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "lookups@example.com")
	episodeID := seedTranscriptEpisode(t, s, "lookup-ep")
	profile, err := s.CreateEditorialProfile(ctx, models.EditorialProfile{Name: "查询画像"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.CreateArticleProposal(ctx, models.ArticleProposal{EditorialProfileID: profile.ID, Kind: "evergreen", Title: "标题", Thesis: "论点", CandidateKeyPoints: `["kp-x"]`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetArticleBriefByProposal(ctx, proposal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proposal without brief should be ErrNotFound: %v", err)
	}
	if err := s.SetArticleProposalStatus(ctx, proposal.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	brief, err := s.CreateArticleBrief(ctx, models.ArticleBrief{ProposalID: proposal.ID, Thesis: proposal.Thesis, Outline: "# 大纲", MaterialPlan: `["kp-x"]`})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetArticleBriefByProposal(ctx, proposal.ID)
	if err != nil || got.ID != brief.ID {
		t.Fatalf("brief by proposal should match: got=%+v err=%v", got, err)
	}
	if _, err := s.GetArticleDraftByBrief(ctx, brief.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("brief without draft should be ErrNotFound: %v", err)
	}
	if err := s.ConfirmArticleBrief(ctx, brief.ID); err != nil {
		t.Fatal(err)
	}
	draft, err := s.CreateArticleDraft(ctx, brief.ID, "草稿标题")
	if err != nil {
		t.Fatal(err)
	}
	gotDraft, err := s.GetArticleDraftByBrief(ctx, brief.ID)
	if err != nil || gotDraft.ID != draft.ID {
		t.Fatalf("draft by brief should match: got=%+v err=%v", gotDraft, err)
	}
	revision, err := s.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: draft.Title, Markdown: "正文", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, reviewerModel := "test-reviewer", "test-model"
	if _, err := s.CreateArticleReview(ctx, models.ArticleReview{RevisionID: revision.ID, Kind: "evidence", Status: "passed", Provider: &reviewer, Model: &reviewerModel}); err != nil {
		t.Fatal(err)
	}
	byDraft, err := s.ListArticleReviewsForDraft(ctx, draft.ID)
	if err != nil || len(byDraft[revision.ID]) != 1 {
		t.Fatalf("reviews indexed by revision should be readable per draft: byDraft=%+v err=%v", byDraft, err)
	}
	if byEmpty, err := s.ListArticleReviewsForDraft(ctx, "missing"); err != nil || len(byEmpty) != 0 {
		t.Fatalf("unknown draft should return empty map: byDraft=%+v err=%v", byEmpty, err)
	}
	// InvalidateRevisionsForSource: 引用该 KeyPoint 的修订在素材失效后不可发布。
	kp, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodeID, SourceTitle: "Lookup 单集", Content: "观点", CitationsJSON: `["seg-1"]`, TimeStart: 0, TimeEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEvidenceMap(ctx, models.EvidenceMap{RevisionID: revision.ID, Kind: models.EvidenceParaphrased, Excerpt: "观点", KeyPointIDs: `["` + kp.ID + `"]`}); err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateRevisionsForSource(ctx, models.SourceEpisode, episodeID); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.IsRevisionReadyForPublication(ctx, revision.ID); err != nil || ready {
		t.Fatalf("invalidated revision must not be publishable: ready=%v err=%v", ready, err)
	}
	// InvalidateAndDeleteKeyPointsForSource 是原子清退：KeyPoint 消失且可重试。
	if err := s.InvalidateAndDeleteKeyPointsForSource(ctx, models.SourceEpisode, episodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKeyPoint(ctx, kp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged source's keypoints must be gone: %v", err)
	}
	if err := s.InvalidateAndDeleteKeyPointsForSource(ctx, models.SourceEpisode, episodeID); err != nil {
		t.Fatalf("purge retry must be safe no-op: %v", err)
	}
}

func TestModelPricesAndRoleFallbacks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: " Groq ", Model: "scout-large", InputCentsPerMillion: 50, OutputCentsPerMillion: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelPrice(ctx, models.ModelPrice{Provider: "", Model: "m", InputCentsPerMillion: -1}); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("invalid price should reject: %v", err)
	}
	price, err := s.GetModelPrice(ctx, "groq", "scout-large")
	if err != nil || price.InputCentsPerMillion != 50 {
		t.Fatalf("price should round-trip normalized: price=%+v err=%v", price, err)
	}
	prices, err := s.ListModelPrices(ctx)
	if err != nil || len(prices) != 1 {
		t.Fatalf("model prices should list: prices=%+v err=%v", prices, err)
	}
	if _, err := s.GetModelPrice(ctx, "groq", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing price should be ErrNotFound: %v", err)
	}
	if err := s.SetEditorialRoleFallback(ctx, models.EditorialRoleFallback{Role: "scout", Provider: "OpenAI", Model: "gpt-test"}); err != nil {
		t.Fatal(err)
	}
	fallback, err := s.GetEditorialRoleFallback(ctx, "scout")
	if err != nil || fallback.Provider != "openai" {
		t.Fatalf("role fallback should round-trip: fallback=%+v err=%v", fallback, err)
	}
	if _, err := s.GetEditorialRoleFallback(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing fallback should be ErrNotFound: %v", err)
	}
	if err := s.SetEditorialRoleFallback(ctx, models.EditorialRoleFallback{Role: "scout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEditorialRoleFallback(ctx, "scout"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clearing a fallback should remove it: %v", err)
	}
}

func TestDiscoveryEnabledWrapperAndEnabledList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := seedWorkspaceProfile(t, s, "调度画像")
	if err := s.SetDiscoveryEnabled(ctx, profile.ID, true, "groq", "scout-large", nil); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.ListEnabledDiscoverySettings(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].EditorialProfileID != profile.ID {
		t.Fatalf("enabled settings should list authorized profile: settings=%+v err=%v", enabled, err)
	}
	if err := s.SetDiscoveryEnabled(ctx, profile.ID, false, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if enabled, err := s.ListEnabledDiscoverySettings(ctx); err != nil || len(enabled) != 0 {
		t.Fatalf("disabled profile should leave enabled list: settings=%+v err=%v", enabled, err)
	}
}

func TestKeyPointBatchStatusAndAttentionQueueLanes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "attention@example.com")
	episodeID := seedTranscriptEpisode(t, s, "attn-ep")
	kp, err := s.CreateManualKeyPoint(ctx, KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodeID, SourceTitle: "Attention 单集", Content: "批注观点", CitationsJSON: `["seg-1"]`, TimeStart: 0, TimeEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyPointProductionStatuses(ctx, nil, models.KeyPointShortlisted); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("empty batch should reject: %v", err)
	}
	if err := s.SetKeyPointProductionStatuses(ctx, []string{kp.ID}, models.KeyPointShortlisted); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyPointProductionStatuses(ctx, []string{"missing"}, models.KeyPointUsed); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing keypoint in batch should fail: %v", err)
	}
	profile := seedWorkspaceProfile(t, s, "注意力画像")
	batch, _, err := s.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "attn-1", MaterialSnapshotJSON: `["kp"]`})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.AttentionQueue(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, item := range items {
		kinds[item.Kind]++
	}
	if kinds["material_review"] == 0 && kinds["proposal_batch"] == 0 {
		t.Fatalf("attention queue should surface actionable items: items=%+v", items)
	}
	for _, item := range items {
		if item.Href == "" {
			t.Fatalf("attention items must link to their workspace: %+v", item)
		}
	}
	// 失败批次也要保持可见，而不是被静默清除。
	if err := s.FailAutomaticProposalBatch(ctx, batch.ID, "groq", "scout", "provider 不可用", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FailAutomaticProposalBatch(ctx, batch.ID, "groq", "scout", "重复失败", nil); !errors.Is(err, ErrInvalidEditorialState) {
		t.Fatalf("failing a non-reviewing batch should reject: %v", err)
	}
	items, err = s.AttentionQueue(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedVisible := false
	for _, item := range items {
		if item.Kind == "proposal_batch" && item.Detail == "自动发现失败" {
			failedVisible = true
		}
	}
	if !failedVisible {
		t.Fatalf("failed batch must remain visible in attention queue: items=%+v", items)
	}
}

func TestCreationFlowListAndHistoryLookups(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := seedWorkspaceProfile(t, s, "流程画像")
	session, err := s.CreateIdeationSession(ctx, models.IdeationSession{EditorialProfileID: profile.ID, Intent: "探索反脆弱", ConstraintsJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := s.ListIdeationSessions(ctx, profile.ID)
	if err != nil || len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("ideation sessions should list per profile: sessions=%+v err=%v", sessions, err)
	}
	work, err := s.CreateCreationHistory(ctx, models.CreationHistory{EditorialProfileID: profile.ID, Status: "published", CreationForm: "article", Title: "已发布", CoreClaim: "主张", Audience: "读者", Content: "正文", SourceURL: "https://example.com/post"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ListCreationHistory(ctx, profile.ID)
	if err != nil || len(history) != 1 || history[0].ID != work.ID {
		t.Fatalf("creation history should list per profile: history=%+v err=%v", history, err)
	}
	if _, err := s.CreateResearchNeed(ctx, models.ResearchNeed{CreationProposalID: "missing", Severity: "blocking", Question: "缺证据"}); err == nil {
		t.Fatal("research need on missing proposal should fail")
	}
	if _, err := s.GetResearchNeed(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing research need should be ErrNotFound: %v", err)
	}
	if needs, err := s.ListResearchNeeds(ctx, profile.ID); err != nil || len(needs) != 0 {
		t.Fatalf("profile without research needs should list empty: needs=%+v err=%v", needs, err)
	}
	if briefs, err := s.ListCreationBriefs(ctx, profile.ID); err != nil || len(briefs) != 0 {
		t.Fatalf("profile without briefs should list empty: briefs=%+v err=%v", briefs, err)
	}
}
