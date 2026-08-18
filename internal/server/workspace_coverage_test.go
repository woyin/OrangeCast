package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// seedEpisodeWithTranscript 建立带当前转录稿的单集，返回该单集 ID。
func seedEpisodeWithTranscript(t *testing.T, srv *Server, guid string) string {
	t.Helper()
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://feed.example.com/"+guid+".xml", guid+" Pod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: guid, Title: guid + " 单集", AudioURL: "https://cdn.example.com/" + guid + ".mp3"}}); err != nil {
		t.Fatal(err)
	}
	episodes, err := srv.store.ListEpisodes(t.Context(), podcast.ID)
	if err != nil || len(episodes) != 1 {
		t.Fatalf("episode setup failed: episodes=%+v err=%v", episodes, err)
	}
	job, err := srv.store.EnqueueJob(t.Context(), models.SourceEpisode, episodes[0].ID, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	version, err := srv.store.CreateArtifactVersion(t.Context(), models.SourceEpisode, episodes[0].ID, "transcript", "test", "test", "1", job.ID, `{"language":"zh","text":"素材","segments":[{"id":"seg-1","start":0,"end":1,"text":"素材"},{"id":"seg-2","start":1,"end":2,"text":"观点"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCurrentVersion(t.Context(), models.SourceEpisode, episodes[0].ID, "transcript", version); err != nil {
		t.Fatal(err)
	}
	return episodes[0].ID
}

// postToHandler 构造一个 POST 表单请求并执行到指定 handler（不经路由/中间件）。
func postToHandler(t *testing.T, handler http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestOwnerNoteAndRightsConstraintHandlers(t *testing.T) {
	srv := newTestServer(t)
	episodeID := seedEpisodeWithTranscript(t, srv, "notes-ep")

	// SourceNote 必须引用来源内片段；OwnerReflection 自由。
	if rec := postToHandler(t, srv.handleOwnerNote, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "kind": {"source_note"}, "content": {"来源笔记"}, "citations_json": {`["seg-1"]`}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("cited source note should save: %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := postToHandler(t, srv.handleOwnerNote, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "kind": {"owner_reflection"}, "content": {"个人反思"}, "citations_json": {`[]`}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("owner reflection should save: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleOwnerNote, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "kind": {"source_note"}, "content": {"坏引用"}, "citations_json": {`["missing-seg"]`}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("citation outside source should reject: %d", rec.Code)
	}
	notes, err := srv.store.ListOwnerNotes(t.Context(), models.SourceEpisode, episodeID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("owner notes should list: notes=%+v err=%v", notes, err)
	}

	// RightsConstraint upsert 幂等且可停用。
	form := url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "constraint_kind": {"expression_reuse"}, "details": {"不得复用表达"}, "active": {"on"}}
	if rec := postToHandler(t, srv.handleRightsConstraint, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("rights constraint should save: %d", rec.Code)
	}
	form.Set("details", "更新后的说明")
	if rec := postToHandler(t, srv.handleRightsConstraint, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("rights constraint upsert should succeed: %d", rec.Code)
	}
	constraints, err := srv.store.ListRightsConstraints(t.Context(), models.SourceEpisode, episodeID)
	if err != nil || len(constraints) != 1 || constraints[0].Details != "更新后的说明" {
		t.Fatalf("rights constraint should upsert in place: constraints=%+v err=%v", constraints, err)
	}
	if rec := postToHandler(t, srv.handleRightsConstraint, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "constraint_kind": {"expression_reuse"}, "details": {"停用"}, "active": {""}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("deactivating constraint should succeed: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleRightsConstraint, url.Values{"source_type": {"document"}, "source_id": {episodeID}, "constraint_kind": {"k"}, "details": {"d"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown document source should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleRightsConstraint, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "constraint_kind": {"k"}, "details": {"d"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("episode constraint without active checkbox should save inactive: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleRightsConstraint, url.Values{"source_type": {"episode"}, "source_id": {"episode"}, "constraint_kind": {"k"}, "details": {"d"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown episode should reject: %d", rec.Code)
	}
	// owner-note 走 document 分支的重定向。
	doc, err := srv.store.CreatePastedDocument(t.Context(), "笔记文档", "内容")
	if err != nil {
		t.Fatal(err)
	}
	if rec := postToHandler(t, srv.handleOwnerNote, url.Values{"source_type": {"document"}, "source_id": {doc.ID}, "kind": {"owner_reflection"}, "content": {"文档反思"}}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/documents/") {
		t.Fatalf("document note should redirect to document page: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := postToHandler(t, srv.handleRightsConstraint, url.Values{"source_type": {"document"}, "source_id": {doc.ID}, "constraint_kind": {"k"}, "details": {"d"}}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/documents/") {
		t.Fatalf("document constraint should redirect to document page: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestMaterialCandidateHandlersLifecycle(t *testing.T) {
	srv := newTestServer(t)
	episodeID := seedEpisodeWithTranscript(t, srv, "candidate-ep")

	create := url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "content": {"学习候选"}, "citations_json": {`["seg-1"]`}}
	if rec := postToHandler(t, srv.handleMaterialCandidateCreate, create); rec.Code != http.StatusSeeOther {
		t.Fatalf("material candidate should create: %d body=%s", rec.Code, rec.Body.String())
	}
	candidates, err := srv.store.ListMaterialCandidates(t.Context(), models.SourceEpisode, episodeID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidate should list: candidates=%+v err=%v", candidates, err)
	}
	candidate := candidates[0]
	if candidate.Status != "pending" {
		t.Fatalf("candidate starts pending: %+v", candidate)
	}
	// 未接受不能提升。
	if rec := postToHandler(t, srv.handleMaterialCandidatePromote, url.Values{"candidate_id": {candidate.ID}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("pending candidate must not promote: %d", rec.Code)
	}
	// 拒绝可带原因，且幂等决策不可重复为接受。
	if rec := postToHandler(t, srv.handleMaterialCandidateDecision, url.Values{"candidate_id": {candidate.ID}, "status": {"rejected"}, "reason": {"证据不足"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject decision should persist: %d", rec.Code)
	}
	decided, err := srv.store.GetMaterialCandidate(t.Context(), candidate.ID)
	if err != nil || decided.Status != "rejected" || decided.RejectionReason == nil || *decided.RejectionReason != "证据不足" {
		t.Fatalf("rejection should persist reason: candidate=%+v err=%v", decided, err)
	}
	// 重新创建并走接受→提升路径。
	if rec := postToHandler(t, srv.handleMaterialCandidateCreate, create); rec.Code != http.StatusSeeOther {
		t.Fatalf("second candidate should create: %d", rec.Code)
	}
	candidates, _ = srv.store.ListMaterialCandidates(t.Context(), models.SourceEpisode, episodeID)
	accepted := candidates[0]
	if rec := postToHandler(t, srv.handleMaterialCandidateDecision, url.Values{"candidate_id": {accepted.ID}, "status": {"accepted"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("accept decision should persist: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleMaterialCandidatePromote, url.Values{"candidate_id": {accepted.ID}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("accepted candidate should promote to KeyPoint: %d body=%s", rec.Code, rec.Body.String())
	}
	promoted, err := srv.store.GetMaterialCandidate(t.Context(), accepted.ID)
	if err != nil || promoted.Status != "promoted" {
		t.Fatalf("promoted candidate status: candidate=%+v err=%v", promoted, err)
	}
	// 无效引用直接拒绝创建。
	if rec := postToHandler(t, srv.handleMaterialCandidateCreate, url.Values{"source_type": {"episode"}, "source_id": {episodeID}, "content": {"坏引用"}, "citations_json": {`["nope"]`}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid citation should reject create: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleMaterialCandidateCreate, url.Values{"source_type": {"episode"}, "source_id": {"missing"}, "content": {"c"}, "citations_json": {`["seg-1"]`}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown source should reject create: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleMaterialCandidateDecision, url.Values{"candidate_id": {"missing"}, "status": {"accepted"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown candidate decision should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleMaterialCandidatePromote, url.Values{"candidate_id": {"missing"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown candidate promote should reject: %d", rec.Code)
	}
	// GET 不允许。
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.handleMaterialCandidateCreate(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should reject: %d", rec.Code)
	}
}

func TestCreationWorkspaceHandlersRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "工作台画像"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := seedEpisodeWithTranscript(t, srv, "workbench-ep")

	// 设置自动发现授权：非法整数/负预算拒绝，合法值持久化。
	if rec := postToHandler(t, srv.handleDiscoverySettings, url.Values{"profile_id": {profile.ID}, "daily_limit": {"x"}, "debounce_minutes": {"30"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-integer limit should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleDiscoverySettings, url.Values{"profile_id": {profile.ID}, "daily_limit": {"1"}, "debounce_minutes": {"30"}, "batch_budget_cents": {"-1"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative budget should reject: %d", rec.Code)
	}
	enable := url.Values{"profile_id": {profile.ID}, "enabled": {"on"}, "provider": {"groq"}, "model": {"scout"}, "daily_limit": {"1"}, "debounce_minutes": {"30"}, "batch_budget_cents": {"100"}}
	if rec := postToHandler(t, srv.handleDiscoverySettings, enable); rec.Code != http.StatusSeeOther {
		t.Fatalf("valid settings should save: %d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := srv.store.GetDiscoverySettings(t.Context(), profile.ID)
	if err != nil || !settings.Enabled || settings.BatchBudgetCents == nil || *settings.BatchBudgetCents != 100 {
		t.Fatalf("settings should persist: settings=%+v err=%v", settings, err)
	}

	// 定向构思会话。
	if rec := postToHandler(t, srv.handleIdeationSessionCreate, url.Values{"profile_id": {profile.ID}, "intent": {"探索证据边界"}, "constraints_json": {`{"长度":"短"}`}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("ideation session should create: %d body=%s", rec.Code, rec.Body.String())
	}
	sessions, err := srv.store.ListIdeationSessions(t.Context(), profile.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Status != "active" {
		t.Fatalf("ideation session should list active: sessions=%+v err=%v", sessions, err)
	}

	// 创作历史导入。
	if rec := postToHandler(t, srv.handleCreationHistoryCreate, url.Values{"profile_id": {profile.ID}, "status": {"published"}, "creation_form": {"article"}, "title": {"旧文标题"}, "core_claim": {"旧主张"}, "audience": {"读者"}, "content": {"旧正文"}, "source_url": {"https://example.com/old"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("creation history should create: %d body=%s", rec.Code, rec.Body.String())
	}
	history, err := srv.store.ListCreationHistory(t.Context(), profile.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("creation history should list: history=%+v err=%v", history, err)
	}

	// 研究缺口：建在自动提案上并标记阻断，验证 brief 确认时重查。
	batch, _, err := srv.store.ReserveAutomaticProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "ws-1", MaterialSnapshotJSON: `["m1"]`})
	if err != nil {
		t.Fatal(err)
	}
	proposals := []models.CreationProposal{{WorkingTitle: "方向", ProposedClaim: "主张", MaterialIDsJSON: `["m1"]`}}
	if err := srv.store.FinalizeAutomaticProposalBatch(t.Context(), batch.ID, "fake", "scout", "", nil, proposals); err != nil {
		t.Fatal(err)
	}
	created, err := srv.store.ListCreationProposals(t.Context(), profile.ID)
	if err != nil || len(created) != 1 {
		t.Fatalf("creation proposals should list: proposals=%+v err=%v", created, err)
	}
	proposal := created[0]
	// Owner 接受主张 → brief 草稿自动创建（此时还没有阻断缺口）。
	if rec := postToHandler(t, srv.handleCreationProposalAccept, url.Values{"creation_proposal_id": {proposal.ID}, "owner_claim": {"我承担的主张"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("accept should create brief draft: %d body=%s", rec.Code, rec.Body.String())
	}
	briefs, err := srv.store.ListCreationBriefs(t.Context(), profile.ID)
	if err != nil || len(briefs) != 1 || briefs[0].Status != "draft" {
		t.Fatalf("brief draft should exist after accept: briefs=%+v err=%v", briefs, err)
	}
	brief := briefs[0]
	// 阻断缺口在草稿建立后出现时，确认前重查必须拦下。
	if rec := postToHandler(t, srv.handleResearchNeedCreate, url.Values{"creation_proposal_id": {proposal.ID}, "severity": {"blocking"}, "question": {"还缺什么证据？"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("blocking research need should create: %d body=%s", rec.Code, rec.Body.String())
	}
	needs, err := srv.store.ListResearchNeeds(t.Context(), profile.ID)
	if err != nil || len(needs) != 1 || needs[0].Severity != "blocking" {
		t.Fatalf("blocking need should list first: needs=%+v err=%v", needs, err)
	}
	need := needs[0]
	// 有阻断缺口未解决时确认失败。
	if rec := postToHandler(t, srv.handleCreationBriefConfirm, url.Values{"creation_brief_id": {brief.ID}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blocking research need must stop confirmation: %d", rec.Code)
	}
	// 解决缺口（引用新 Source）后确认成功。
	newEpisode := seedEpisodeWithTranscript(t, srv, "resolve-ep")
	if rec := postToHandler(t, srv.handleResearchNeedResolve, url.Values{"research_need_id": {need.ID}, "resolution_source_id": {newEpisode}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resolve research need should redirect: %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := postToHandler(t, srv.handleCreationBriefConfirm, url.Values{"creation_brief_id": {brief.ID}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("resolved need should allow confirmation: %d body=%s", rec.Code, rec.Body.String())
	}
	confirmed, err := srv.store.GetCreationBrief(t.Context(), brief.ID)
	if err != nil || confirmed.Status != "confirmed" {
		t.Fatalf("brief should be confirmed: brief=%+v err=%v", confirmed, err)
	}
	// 错误分支：未知提案/缺口/草稿。
	if rec := postToHandler(t, srv.handleCreationProposalAccept, url.Values{"creation_proposal_id": {"missing"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown proposal accept should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleResearchNeedCreate, url.Values{"creation_proposal_id": {"missing"}, "severity": {"blocking"}, "question": {"q"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("need on unknown proposal should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleResearchNeedResolve, url.Values{"research_need_id": {"missing"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown need resolve should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleCreationBriefConfirm, url.Values{"creation_brief_id": {"missing"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown brief confirm should reject: %d", rec.Code)
	}
	_ = episodeID
}

func TestKeyPointSearchAndBatchHandlers(t *testing.T) {
	srv := newTestServer(t)
	episodeID := seedEpisodeWithTranscript(t, srv, "kp-ep")
	kp, err := srv.store.CreateManualKeyPoint(t.Context(), store.KeyPointRow{SourceType: models.SourceEpisode, SourceID: episodeID, SourceTitle: "kp 单集", Content: "混合搜索目标观点", CitationsJSON: `["seg-1"]`, TimeStart: 0, TimeEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 空查询返回空数组。
	req := httptest.NewRequest(http.MethodGet, "/api/keypoints/search?q=", nil)
	rec := httptest.NewRecorder()
	srv.handleKeyPointsSearch(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Fatalf("empty query should return empty results: %d %s", rec.Code, rec.Body.String())
	}
	// 命中返回质量状态。
	req = httptest.NewRequest(http.MethodGet, "/api/keypoints/search?q="+url.QueryEscape("混合搜索目标观点"), nil)
	rec = httptest.NewRecorder()
	srv.handleKeyPointsSearch(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), kp.ID) {
		t.Fatalf("search should find the keypoint: %d %s", rec.Code, rec.Body.String())
	}
	// 批量状态更新：csv 与 form 两种传法。
	if rec := postToHandler(t, srv.handleKeyPointBatchStatus, url.Values{"keypoint_ids_csv": {kp.ID}, "status": {"shortlisted"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("csv batch should update: %d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := srv.store.GetKeyPoint(t.Context(), kp.ID)
	if err != nil || updated.ProductionStatus != models.KeyPointShortlisted {
		t.Fatalf("batch should update status: kp=%+v err=%v", updated, err)
	}
	if rec := postToHandler(t, srv.handleKeyPointBatchStatus, url.Values{"keypoint_ids": {kp.ID}, "status": {"used"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("form batch should update: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleKeyPointBatchStatus, url.Values{"status": {"used"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty batch should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleKeyPointBatchStatus, url.Values{"keypoint_ids": {kp.ID}, "status": {"bogus"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status should reject: %d", rec.Code)
	}
	if rec := postToHandler(t, srv.handleKeyPointBatchStatus, url.Values{"keypoint_ids": {"missing"}, "status": {"used"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing keypoint should reject batch: %d", rec.Code)
	}
}
