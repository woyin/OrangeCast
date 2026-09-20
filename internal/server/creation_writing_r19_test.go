package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// seedR19ConfirmedBrief 建立真实 episode/keypoint → accepted proposal → CAS revision →
// 生产确认+持久链接，返回精确确认的 CreationBrief 与材料身份。
func seedR19ConfirmedBrief(t *testing.T, srv *Server, guid string) (*models.CreationBrief, string) {
	t.Helper()
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	epID := seedDiscoveryEpisode(t, srv, guid, guid+" 讨论了学习方法的边界。", "seg-1")
	kps, _, err := srv.store.ListKeyPointsFiltered(ctx, store.KeyPointFilter{SourceID: epID}, 1, 10)
	if err != nil || len(kps) == 0 {
		t.Fatalf("keypoint fixture: %v %v", kps, err)
	}
	kpID := kps[0].ID
	if err := srv.store.SetKeyPointQualityStatus(ctx, kpID, models.KeyPointReady); err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal([]string{kpID})
	proposal, err := srv.store.CreateCreationProposal(ctx, models.CreationProposal{
		EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: guid + " 方向",
		ProposedClaim: "Owner", MaterialIDsJSON: string(ids),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AcceptCreationProposal(ctx, proposal.ID, "Owner"); err != nil {
		t.Fatal(err)
	}
	brief, err := srv.store.CreateCreationBriefDraftFromProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	kpRow, err := srv.store.GetKeyPoint(ctx, kpID)
	if err != nil {
		t.Fatal(err)
	}
	snapshotB, _ := json.Marshal(map[string]any{"provider": "test", "materials": []provider.ArticleMaterial{{
		SourceType: "episode", CardVersion: kpRow.CardVersion, KeyPointID: kpRow.ID, SourceID: kpRow.SourceID,
		SourceTitle: kpRow.SourceTitle, Content: kpRow.Content, Description: kpRow.Description,
		Citations: []string{"seg-1"},
	}}})
	snapshot := string(snapshotB)
	if _, err := srv.store.CreateCreationBriefRevisionCAS(ctx, brief.ID, brief.CurrentVersion, models.CreationBriefRevision{
		OwnerClaim: "Owner 主张", Outline: "# 提纲", MaterialPlanJSON: `{"selected":["` + kpID + `"]}`,
		CuratorPromptVersion: "curator-v1", CuratorInputSnapshotJSON: string(snapshot),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.ConfirmCreationBriefVersionAndEnsureLink(ctx, brief.ID, brief.CurrentVersion+1, "v2"); err != nil {
		t.Fatalf("confirm+link: %v", err)
	}
	fresh, err := srv.store.GetCreationBrief(ctx, brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fresh, kpID
}

// TestCreationBriefWriteRoute R19：真实 confirmed HTTP 卡片 → CSRF 生成按钮 →
// POST 生成动作幂等入队并重定向同一 draft 页面；未确认拒绝且零副作用。
func TestCreationBriefWriteRoute(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	confirmed, _ := seedR19ConfirmedBrief(t, srv, "r19a")
	draftBrief, _ := seedR19ConfirmedBrief(t, srv, "r19b")
	// 把第二个 Brief 编辑回 draft：确认授权失效，不允许生成。
	if _, err := srv.store.CreateCreationBriefRevisionCAS(ctx, draftBrief.ID, draftBrief.CurrentVersion, models.CreationBriefRevision{
		OwnerClaim: draftBrief.OwnerClaim, Outline: "# 新提纲", MaterialPlanJSON: draftBrief.MaterialPlanJSON,
	}); err != nil {
		t.Fatal(err)
	}

	session := claimOwnerAndLogin(t, srv, "r19@example.com", "password123")
	csrf := ""
	if rec := doWithCookie(srv, session, http.MethodGet, "/dashboard"); rec.Code == http.StatusOK {
		for _, c := range rec.Result().Cookies() {
			if c.Name == "cwp_csrf" {
				csrf = c.Value
			}
		}
	}
	if csrf == "" {
		t.Fatal("missing csrf cookie")
	}
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		form.Set("_csrf", csrf)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}

	// 未确认 Brief：拒绝 + 零副作用。
	if rec := post("/workbench/creation-briefs/write", url.Values{"creation_brief_id": {draftBrief.ID}, "expected_version": {strconv.Itoa(draftBrief.CurrentVersion)}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed write: %d", rec.Code)
	}
	var draftBriefJobs int
	srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing' AND source_id IN (SELECT id FROM article_drafts WHERE brief_id IN (SELECT article_brief_id FROM creation_article_links WHERE creation_brief_id=?))`, draftBrief.ID).Scan(&draftBriefJobs)
	if draftBriefJobs != 0 {
		t.Fatalf("未确认 Brief 不得入队: %d", draftBriefJobs)
	}

	// 已确认 Brief：POST 生成 → 303 重定向到 draft 页面；重复点击同一 job/页面。
	link, err := srv.store.GetCreationArticleLinkByCreationBrief(ctx, confirmed.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 生成前：确认卡片显示 CSRF 生成按钮。
	wb := doWithCookie(srv, session, http.MethodGet, "/workbench?profile="+profileIDFor(t, srv, confirmed))
	if wb.Code != http.StatusOK {
		t.Fatalf("workbench: %d", wb.Code)
	}
	body := wb.Body.String()
	if !strings.Contains(body, `action="/workbench/creation-briefs/write"`) || !strings.Contains(body, `name="_csrf"`) || !strings.Contains(body, "生成文章初稿") {
		t.Fatal("无草稿的确认卡片必须显示 CSRF 生成按钮")
	}

	// Stale expected_version：409 + 零副作用。
	stale := post("/workbench/creation-briefs/write", url.Values{"creation_brief_id": {confirmed.ID}, "expected_version": {strconv.Itoa(confirmed.CurrentVersion + 1)}})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale expected: %d want 409", stale.Code)
	}
	var preJobs int
	srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing'`).Scan(&preJobs)
	if preJobs != 0 {
		t.Fatalf("stale 入队不得有副作用: %d", preJobs)
	}
	rec := post("/workbench/creation-briefs/write", url.Values{"creation_brief_id": {confirmed.ID}, "expected_version": {strconv.Itoa(confirmed.CurrentVersion)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("write: %d", rec.Code)
	}
	beforeDraft, err := srv.store.GetArticleDraftByBrief(ctx, link.ArticleBriefID)
	if err != nil {
		t.Fatal(err)
	}
	wantLocation := "/workbench/drafts/" + beforeDraft.ID
	if rec.Header().Get("Location") != wantLocation {
		t.Fatalf("write Location=%s want %s", rec.Header().Get("Location"), wantLocation)
	}
	rec = post("/workbench/creation-briefs/write", url.Values{"creation_brief_id": {confirmed.ID}, "expected_version": {strconv.Itoa(confirmed.CurrentVersion)}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != wantLocation {
		t.Fatalf("repeat write: %d Location=%s want %s", rec.Code, rec.Header().Get("Location"), wantLocation)
	}
	var jobCount, intentCount int
	srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM processing_jobs WHERE job_type='claim_writing' AND source_id=?`, beforeDraft.ID).Scan(&jobCount)
	srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_writing_intents WHERE draft_id=?`, beforeDraft.ID).Scan(&intentCount)
	if jobCount != 1 || intentCount != 1 {
		t.Fatalf("重复点击必须复用同一 draft+job: jobs=%d intents=%d", jobCount, intentCount)
	}
	afterDraft, err := srv.store.GetArticleDraft(ctx, beforeDraft.ID)
	if err != nil || afterDraft.ID != beforeDraft.ID {
		t.Fatalf("draft 复用失败: %+v %v", afterDraft, err)
	}

	// 首次生成后：GET 显示可点击"打开文章"链接（指向 draft 页面），不再只有生成按钮。
	wbAfter := doWithCookie(srv, session, http.MethodGet, "/workbench?profile="+profileIDFor(t, srv, confirmed))
	if wbAfter.Code != http.StatusOK {
		t.Fatalf("workbench after: %d", wbAfter.Code)
	}
	bodyAfter := wbAfter.Body.String()
	if !strings.Contains(bodyAfter, "打开文章") || !strings.Contains(bodyAfter, wantLocation) {
		t.Fatal("已有草稿的确认卡片必须显示打开文章链接")
	}
	if strings.Contains(bodyAfter, `action="/workbench/creation-briefs/write"`) {
		t.Fatal("已有 Writer 草稿后不得再显示生成表单")
	}
}

func profileIDFor(t *testing.T, srv *Server, brief *models.CreationBrief) string {
	t.Helper()
	proposal, err := srv.store.GetCreationProposal(t.Context(), brief.CreationProposalID)
	if err != nil {
		t.Fatal(err)
	}
	return proposal.EditorialProfileID
}
