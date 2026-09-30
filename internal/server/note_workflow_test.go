package server

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNotesAJAXHistoryAndFrozenEvidence(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "notes-v2@example.com", "password123")
	doc, err := srv.store.CreatePastedDocument(t.Context(), "文档", "原始依据内容")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceDocument, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	vals := url.Values{"_csrf": {session.Value}, "source_type": {"document"}, "source_id": {doc.ID}, "kind": {"owner_reflection"}, "content": {"我的理解"}, "anchor_json": {`{"snapshot_id":"` + snap.ID + `","version":1,"position":1}`}}
	csrf := journeyCSRF(t, srv, session)
	vals.Set("_csrf", csrf)
	req := httptest.NewRequest(http.MethodPost, "/api/owner-notes", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save %d: %s", rec.Code, rec.Body.String())
	}
	var data struct {
		Note models.OwnerNote `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Note.ID == "" {
		t.Fatal("missing note identity")
	}
	hist := doWithCookie(srv, session, http.MethodGet, "/notes/"+data.Note.ID+"/history")
	if hist.Code != 200 || !strings.Contains(hist.Body.String(), "我的理解") {
		t.Fatal(hist.Code, hist.Body.String())
	}
	frozen := doWithCookie(srv, session, http.MethodGet, "/evidence/"+snap.ID+"?position=1")
	if frozen.Code != 200 || !strings.Contains(frozen.Body.String(), "原始依据内容") {
		t.Fatal(frozen.Code)
	}
}
