package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
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
