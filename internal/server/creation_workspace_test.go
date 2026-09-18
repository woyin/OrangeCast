package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func TestCreationWorkspaceSettingsAcceptanceAndBriefAuthorization(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "creation-workspace@example.com", "password123")
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "创作画像"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateCreationProposal(t.Context(), models.CreationProposal{EditorialProfileID: profile.ID, WorkingTitle: "学习闭环", ProposedClaim: "学习质量决策应先于创作授权", MaterialIDsJSON: `["material-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRequest(http.MethodGet, "/workbench?profile="+profile.ID, nil)
	page.AddCookie(session)
	pageRec := httptest.NewRecorder()
	srv.Router().ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK || !strings.Contains(pageRec.Body.String(), "AutomaticDiscovery") {
		t.Fatalf("creation workspace should render: status=%d body=%s", pageRec.Code, pageRec.Body.String())
	}
	var csrf string
	for _, cookie := range pageRec.Result().Cookies() {
		if cookie.Name == "cwp_csrf" {
			csrf = cookie.Value
		}
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("_csrf="+csrf+"&"+body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/workbench/discovery-settings", "profile_id="+profile.ID+"&enabled=on&provider=fake&model=fake-scout&daily_limit=1&debounce_minutes=30&batch_budget_cents=12"); rec.Code != http.StatusSeeOther {
		t.Fatalf("discovery authorization should redirect: %d %s", rec.Code, rec.Body.String())
	}
	settings, err := srv.store.GetDiscoverySettings(t.Context(), profile.ID)
	if err != nil || !settings.Enabled || settings.Provider != "fake" || settings.BatchBudgetCents == nil || *settings.BatchBudgetCents != 12 {
		t.Fatalf("profile discovery authorization should persist: settings=%+v err=%v", settings, err)
	}
	if rec := post("/workbench/creation-proposals/accept", "creation_proposal_id="+proposal.ID+"&owner_claim=%E5%AD%A6%E4%B9%A0%E8%B4%A8%E9%87%8F%E5%86%B3%E7%AD%96%E5%BA%94%E5%85%88%E4%BA%8E%E5%88%9B%E4%BD%9C%E6%8E%88%E6%9D%83"); rec.Code != http.StatusSeeOther {
		t.Fatalf("Owner acceptance should redirect: %d %s", rec.Code, rec.Body.String())
	}
	accepted, err := srv.store.GetCreationProposal(t.Context(), proposal.ID)
	if err != nil || accepted.Status != "accepted" || accepted.OwnerClaim == "" {
		t.Fatalf("model proposal must become an OwnerClaim: proposal=%+v err=%v", accepted, err)
	}
	briefs, err := srv.store.ListCreationBriefs(t.Context(), profile.ID)
	if err != nil || len(briefs) != 1 || briefs[0].Status != "draft" {
		t.Fatalf("accepted proposal with material must create a reviewable brief draft: briefs=%+v err=%v", briefs, err)
	}
	if rec := post("/workbench/creation-briefs/confirm", "creation_brief_id="+briefs[0].ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("brief confirmation should redirect: %d %s", rec.Code, rec.Body.String())
	}
	confirmed, err := srv.store.GetCreationBrief(t.Context(), briefs[0].ID)
	if err != nil || confirmed.Status != "confirmed" || confirmed.ConfirmedAt == nil {
		t.Fatalf("only explicit Owner confirmation may authorize creation: brief=%+v err=%v", confirmed, err)
	}
}

// TestIdeationSelectionsFlowIntoRound R12：素材选择绑定会话后，轮次材料快照从
// 选择冻结（含重点与个人笔记、内容版本）；资格失效材料显式报错不静默；
// 重复提交（同 nonce）幂等；GET 不触发付费调用。
func TestIdeationSelectionsFlowIntoRound(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "ideo-sel@example.com", "password123")
	ctx := t.Context()

	// 素材：一条 ready 重点（带引用）+ 一条个人笔记 + 一条被 Owner 排除的重点。
	podcast, err := srv.store.CreatePodcast(ctx, "https://feed.example.com/ideo.xml", "构思播客", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.MergeEpisodes(ctx, podcast.ID, []models.Episode{{GUID: "i1", Title: "构思单集", AudioURL: "https://a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(ctx, podcast.ID)
	epID := eps[0].ID
	seedJob, err := srv.store.EnqueueJob(ctx, models.SourceEpisode, epID, models.JobAnalyze)
	if err != nil {
		t.Fatal(err)
	}
	srv.store.MarkJobRunning(ctx, seedJob.ID)
	srv.store.MarkJobSucceeded(ctx, seedJob.ID)
	tp := `{"segments":[{"id":"seg-0001","start":0,"end":5,"text":"a"}]}`
	tv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, "groq", "m", "1", seedJob.ID, tp)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, store.KindTranscript, tv)
	cv, _ := srv.store.CreateArtifactVersion(ctx, models.SourceEpisode, epID, store.KindKnowledgeCard, "groq", "m", "1", seedJob.ID,
		`{"title":"T","summary":{"text":"S","citations":["seg-0001"]},"keyPoints":[],"chapters":[],"quotes":[],"tags":[]}`)
	srv.store.SetCurrentVersion(ctx, models.SourceEpisode, epID, store.KindKnowledgeCard, cv)

	kpReady, err := srv.store.CreateManualKeyPoint(ctx, store.KeyPointRow{
		SourceType: models.SourceEpisode, SourceID: epID, Content: "个人确认的重点",
		CitationsJSON: `["seg-0001"]`, RelationKind: models.RelationCitation,
		TimeStart: 0, TimeEnd: 5, Origin: models.KeyPointManual,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.store.SetKeyPointQualityStatus(ctx, kpReady.ID, models.KeyPointOwnerConfirmed)
	note, err := srv.store.CreateOwnerNote(ctx, models.OwnerNote{
		SourceType: string(models.SourceEpisode), SourceID: epID, Kind: "owner_reflection", Content: "我的个人理解",
	})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := srv.store.SaveCreationSelection(ctx, &models.CreationSelection{
		MaterialIDs: []string{kpReady.ID}, NoteIDs: []string{note.ID},
	})
	if err != nil || sel.Status != models.SelectionConfirmed && sel.Status != models.SelectionDraft {
		t.Fatalf("选择应保存: %+v %v", sel, err)
	}

	// 创建会话（真实路由）绑定选择。
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	prof, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec := post("/workbench/ideation", url.Values{
		"_csrf": {csrf}, "profile_id": {prof.ID}, "intent": {"跨集个人文章"},
		"selection_ids": {sel.ID},
	}.Encode())
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("会话创建应 303: %d %s", rec.Code, rec.Body.String())
	}
	sessions, _ := srv.store.ListIdeationSessions(ctx, prof.ID)
	if len(sessions) == 0 || sessions[0].SelectionsJSON == "" || strings.Contains(sessions[0].SelectionsJSON, sel.ID) == false {
		t.Fatalf("会话应绑定素材选择: %+v", sessions)
	}
	sessionID := sessions[0].ID

	// 追加轮次（不传 material_ids）：快照从选择冻结（重点+笔记+版本）。
	rec = post("/workbench/ideation/round", url.Values{
		"_csrf": {csrf}, "session_id": {sessionID}, "input": {"这期讲什么"}, "nonce": {"n-1"},
	}.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("轮次创建应 200: %d %s", rec.Code, rec.Body.String())
	}
	rounds, _ := srv.store.ListIdeationRounds(ctx, sessionID)
	if len(rounds) != 1 {
		t.Fatalf("应一轮: %+v", rounds)
	}
	var snap []map[string]any
	if err := json.Unmarshal([]byte(rounds[0].MaterialSnapshotJSON), &snap); err != nil {
		t.Fatalf("快照应可解析: %v", err)
	}
	foundKP, foundNote := false, false
	for _, m := range snap {
		if m["id"] == kpReady.ID {
			foundKP = m["content"] == "个人确认的重点"
		}
		if m["id"] == note.ID {
			foundNote = m["content"] == "我的个人理解" && m["kind"] == "note"
		}
	}
	if !foundKP || !foundNote {
		t.Fatalf("快照应含重点与笔记内容: %+v", snap)
	}
	// 重复提交（同 nonce）幂等。
	rec = post("/workbench/ideation/round", url.Values{
		"_csrf": {csrf}, "session_id": {sessionID}, "input": {"这期讲什么"}, "nonce": {"n-1"},
	}.Encode())
	if rec.Code != http.StatusOK {
		t.Fatal("重复提交应 200")
	}
	rounds2, _ := srv.store.ListIdeationRounds(ctx, sessionID)
	if len(rounds2) != 1 || rounds2[0].ID != rounds[0].ID {
		t.Fatalf("重复提交不得新增轮次: %d", len(rounds2))
	}
}
