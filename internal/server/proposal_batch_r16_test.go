package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func seedProposalBatchR16(t *testing.T, srv *Server, key string, n int) (string, []string) {
	t.Helper()
	ctx := t.Context()
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, created, err := srv.store.ReserveAutomaticProposalBatch(ctx, models.ProposalBatch{
		EditorialProfileID: profile.ID, IdempotencyKey: key,
	})
	if err != nil || !created {
		t.Fatalf("reserve batch: %v created=%v", err, created)
	}
	proposals := make([]models.CreationProposal, 0, n)
	for i := 0; i < n; i++ {
		proposals = append(proposals, models.CreationProposal{
			WorkingTitle: "候选" + string(rune('A'+i)), ProposedClaim: "主张" + string(rune('A'+i)), CreationForm: "article",
			MaterialIDsJSON: `["mat-a","mat-b"]`, HistoryRelationship: "possible_duplicate:hist-1",
		})
	}
	if err := srv.store.FinalizeAutomaticProposalBatch(ctx, batch.ID, "test", "m", "", nil, proposals); err != nil {
		t.Fatal(err)
	}
	list, err := srv.store.ListCreationProposals(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, n)
	for _, p := range list {
		if p.ProposalBatchID == batch.ID {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) != n {
		t.Fatalf("batch proposals: got %d want %d", len(ids), n)
	}
	return batch.ID, ids
}

func postProposalDecisionR16(t *testing.T, srv *Server, session *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	form.Set("_csrf", csrf)
	req := httptest.NewRequest(http.MethodPost, "/workbench/proposal-decision", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func mustProfileID(t *testing.T, srv *Server) string {
	t.Helper()
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return profile.ID
}

func TestProposalBatch_ReleasesOnLastDecision_R16(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "r16@example.com", "password123")
	batchID, ids := seedProposalBatchR16(t, srv, "r16-release", 2)
	if rec := postProposalDecisionR16(t, srv, session, url.Values{"proposal_id": {ids[0]}, "decision": {"accept"}, "owner_claim": {"Owner 主张一"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	accepted, err := srv.store.GetCreationProposal(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if accepted.MaterialIDsJSON != `["mat-a","mat-b"]` || accepted.HistoryRelationship != "possible_duplicate:hist-1" {
		t.Fatalf("accept must preserve material/history provenance: %+v", accepted)
	}
	// 同 owner_claim 重复通过 HTTP 应 303 幂等；不同 claim 应 400 冲突。
	if rec := postProposalDecisionR16(t, srv, session, url.Values{"proposal_id": {ids[0]}, "decision": {"accept"}, "owner_claim": {"Owner 主张一"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("same owner_claim accept should be idempotent: %d", rec.Code)
	}
	if rec := postProposalDecisionR16(t, srv, session, url.Values{"proposal_id": {ids[0]}, "decision": {"accept"}, "owner_claim": {"不同主张"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("different owner_claim should conflict: %d", rec.Code)
	}
	open, err := srv.store.HasOpenProposalBatch(t.Context(), mustProfileID(t, srv))
	if err != nil || !open {
		t.Fatalf("one remaining proposal must keep backpressure: open=%v err=%v", open, err)
	}
	if rec := postProposalDecisionR16(t, srv, session, url.Values{"proposal_id": {ids[1]}, "decision": {"reject"}, "feedback": {"NotNow"}, "reason": {"重复"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body.String())
	}
	batch, err := srv.store.GetProposalBatchByIdempotencyKey(t.Context(), "r16-release")
	if err != nil || batch.Status != "completed" {
		t.Fatalf("last decision must complete batch %q: %+v %v", batchID, batch, err)
	}
	p, err := srv.store.GetCreationProposal(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	var note map[string]string
	if err := json.Unmarshal([]byte(p.DecisionNote), &note); err != nil || note["feedbackKind"] != "NotNow" || note["reason"] != "重复" {
		t.Fatalf("decision_note: %q %v", p.DecisionNote, err)
	}
	firstNote := p.DecisionNote
	if rec := postProposalDecisionR16(t, srv, session, url.Values{"proposal_id": {ids[1]}, "decision": {"reject"}, "feedback": {"Different"}, "reason": {"不得覆盖"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("same reject should be idempotent: %d", rec.Code)
	}
	pAgain, err := srv.store.GetCreationProposal(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if pAgain.DecisionNote != firstNote {
		t.Fatalf("repeated reject must preserve first decision_note: %q -> %q", firstNote, pAgain.DecisionNote)
	}
}

func TestProposalBatch_ConcurrentLastTwoDecisions_R16(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "r16c@example.com", "password123")
	batchID, ids := seedProposalBatchR16(t, srv, "r16-conc", 2)
	rec0 := doWithCookie(srv, session, http.MethodGet, "/dashboard")
	csrf := ""
	for _, c := range rec0.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	var wg sync.WaitGroup
	decide := func(id, decision, claim string) {
		defer wg.Done()
		form := url.Values{"proposal_id": {id}, "decision": {decision}, "_csrf": {csrf}, "owner_claim": {claim}, "feedback": {"NotNow"}, "reason": {"并发"}}
		req := httptest.NewRequest(http.MethodPost, "/workbench/proposal-decision", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("decision: %d %s", rec.Code, rec.Body.String())
		}
	}
	wg.Add(2)
	go decide(ids[0], "accept", "Owner A")
	go decide(ids[1], "reject", "")
	wg.Wait()
	batch, err := srv.store.GetProposalBatchByIdempotencyKey(t.Context(), "r16-conc")
	if err != nil || batch.Status != "completed" {
		t.Fatalf("concurrent batch status: %+v %v", batch, err)
	}
	remaining, err := srv.store.CountOpenProposalsForBatch(t.Context(), batchID)
	if err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}

func TestProposalBatch_ZeroCandidateAutoComplete_R16(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch, created, err := srv.store.ReserveAutomaticProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "r16-zero"})
	if err != nil || !created {
		t.Fatalf("reserve: %v %v", err, created)
	}
	if err := srv.store.FinalizeAutomaticProposalBatch(t.Context(), batch.ID, "test", "m", "无足够素材", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := srv.store.GetProposalBatchByIdempotencyKey(t.Context(), "r16-zero")
	if err != nil || got.Status != "completed" {
		t.Fatalf("zero candidate batch: %+v %v", got, err)
	}
	open, err := srv.store.HasOpenProposalBatch(t.Context(), profile.ID)
	if err != nil || open {
		t.Fatalf("zero candidate batch must not create backpressure: open=%v err=%v", open, err)
	}
}

func TestProposalBatch_SameSnapshotIdempotent_R16(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch, created, err := srv.store.ReserveAutomaticProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "r16-old-snapshot"})
	if err != nil || !created {
		t.Fatalf("reserve: %v %v", err, created)
	}
	if err := srv.store.FinalizeAutomaticProposalBatch(t.Context(), batch.ID, "test", "m", "", nil, []models.CreationProposal{{WorkingTitle: "T", ProposedClaim: "C", CreationForm: "article"}}); err != nil {
		t.Fatal(err)
	}
	again, createdAgain, err := srv.store.ReserveAutomaticProposalBatch(t.Context(), models.ProposalBatch{EditorialProfileID: profile.ID, IdempotencyKey: "r16-old-snapshot"})
	if err != nil || createdAgain || again.ID != batch.ID {
		t.Fatalf("same snapshot must not restock: %+v created=%v err=%v", again, createdAgain, err)
	}
}

// TestSavedProposalPreservesDedupForDiscovery_R16：save 不丢 material/history 来历，
// 后续 automaticCreationProposals 仍把 saved 候选参与去重。
func TestSavedProposalPreservesDedupForDiscovery_R16(t *testing.T) {
	srv := newTestServer(t)
	profile, err := srv.store.EnsureDefaultEditorialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p, err := srv.store.CreateCreationProposal(t.Context(), models.CreationProposal{
		EditorialProfileID: profile.ID, WorkingTitle: "已暂存标题", ProposedClaim: "已暂存主张", CreationForm: "article",
		MaterialIDsJSON: `["mat-a","mat-b"]`, HistoryRelationship: "possible_duplicate:hist-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveProposalForLater(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := srv.store.GetCreationProposal(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "saved" || stored.MaterialIDsJSON != `["mat-a","mat-b"]` || stored.HistoryRelationship != "possible_duplicate:hist-1" {
		t.Fatalf("save must preserve provenance: %+v", stored)
	}
	result := &provider.ScoutResult{Proposals: []provider.ScoutProposal{{
		Title: "已暂存标题", Thesis: "已暂存主张", CandidateKeyPointIDs: []string{"mat-a", "mat-b"},
	}}}
	materials := []provider.ArticleMaterial{{KeyPointID: "mat-a", SourceID: "ep-a"}, {KeyPointID: "mat-b", SourceID: "ep-b"}}
	out, err := srv.automaticCreationProposals(t.Context(), profile.ID, "r16-dedup", result, materials, materials)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("saved proposal must participate in dedup: %+v", out)
	}
}
