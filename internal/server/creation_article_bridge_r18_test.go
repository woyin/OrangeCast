package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// TestR18LegacyEntrypointsRejectLinkedArticles seeds one real compatibility link,
// then exercises the three old HTTP mutations and both Writer authorization paths.
func TestR18LegacyEntrypointsRejectLinkedArticles(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := srv.store.CreateCreationProposal(ctx, models.CreationProposal{EditorialProfileID: profile.ID, Status: "proposed", WorkingTitle: "R18", ProposedClaim: "Owner", OwnerClaim: "Owner", MaterialIDsJSON: `["legacy-material"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AcceptCreationProposal(ctx, proposal.ID, "Owner"); err != nil {
		t.Fatal(err)
	}
	brief, err := srv.store.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposal.ID, OwnerClaim: "Owner", Outline: "Outline", MaterialPlanJSON: `{"selected":["legacy-material"]}`})
	if err != nil {
		t.Fatal(err)
	}
	// Compatibility fixture is intentionally direct: this test isolates legacy-entry blocking.
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE creation_briefs SET status='confirmed',confirmed_version=current_version WHERE id=?`, brief.ID); err != nil {
		t.Fatal(err)
	}
	link, err := srv.store.EnsureCreationArticleLinkExact(ctx, proposal.ID, brief.ID, 1, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if link == nil {
		t.Fatal("missing link")
	}

	session := claimOwnerAndLogin(t, srv, "r18legacy@example.com", "password123")
	csrf := ""
	if rec := doWithCookie(srv, session, http.MethodGet, "/dashboard"); rec.Code == http.StatusOK {
		for _, c := range rec.Result().Cookies() {
			if c.Name == "cwp_csrf" {
				csrf = c.Value
			}
		}
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
	var briefCount, draftCount int
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_briefs`).Scan(&briefCount); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts`).Scan(&draftCount); err != nil {
		t.Fatal(err)
	}
	if rec := post("/workbench/briefs", url.Values{"profile_id": {profile.ID}, "proposal_id": {link.ArticleProposalID}, "thesis": {"x"}, "outline": {"o"}, "material_plan": {`[]`}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("linked brief create: %d", rec.Code)
	}
	if rec := post("/workbench/briefs/confirm", url.Values{"profile_id": {profile.ID}, "brief_id": {link.ArticleBriefID}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("linked brief confirm: %d", rec.Code)
	}
	if rec := post("/workbench/drafts", url.Values{"brief_id": {link.ArticleBriefID}, "title": {"x"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("linked draft create: %d", rec.Code)
	}
	var briefCountAfter, draftCountAfter int
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_briefs`).Scan(&briefCountAfter); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts`).Scan(&draftCountAfter); err != nil {
		t.Fatal(err)
	}
	if briefCountAfter != briefCount || draftCountAfter != draftCount {
		t.Fatalf("linked handlers must not mutate: brief %d→%d draft %d→%d", briefCount, briefCountAfter, draftCount, draftCountAfter)
	}

	factoryCalls := 0
	writer := &fakeArticleWriter{}
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		factoryCalls++
		return &provider.ProviderBundle{Writer: writer}, nil
	}
	if _, err := srv.loadInitialWriterAuthorization(ctx, link.ArticleBriefID); err == nil {
		t.Fatal("linked initial Writer must reject")
	}
	if factoryCalls != 0 || len(writer.requests) != 0 {
		t.Fatalf("initial Writer side effects: factory=%d calls=%d", factoryCalls, len(writer.requests))
	}
	draft, err := srv.store.CreateArticleDraft(ctx, link.ArticleBriefID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := srv.store.CreateArticleRevision(ctx, models.ArticleRevision{DraftID: draft.ID, Title: "legacy", Markdown: "# old", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.loadRevisionWriterAuthorization(ctx, rev.ID); err == nil {
		t.Fatal("linked revision Writer must reject")
	}
	if factoryCalls != 0 || len(writer.requests) != 0 {
		t.Fatalf("revision Writer side effects: factory=%d calls=%d", factoryCalls, len(writer.requests))
	}
}
