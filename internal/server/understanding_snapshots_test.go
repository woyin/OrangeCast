package server

import (
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUnderstandingSnapshotSaveHTTPAuthCASAndOwnerConfirmation(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "understanding@example.com", "password123")
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "我的问题"})
	if e != nil {
		t.Fatal(e)
	}
	path := "/questions/" + q.ID + "/understandings"
	if rec := doWithCookie(srv, nil, "GET", path); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	page := doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "没有来源") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(page.Code, page.Body.String())
	}
	values := url.Values{"action": {"save"}, "question_id": {q.ID}, "question_revision": {"1"}, "head_revision": {"0"}, "request_key": {uuid.NewString()}, "answer": {"Owner自己的判断"}}
	req := httptest.NewRequest("POST", "/questions/understanding-action", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	rec = postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	values.Set("owner_confirmed", "yes")
	rec = postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 303 {
		t.Fatal("replay", rec.Code)
	}
	values.Set("request_key", uuid.NewString())
	values.Set("answer", "旧窗口覆盖")
	rec = postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	versions, e := srv.store.HistoryUnderstanding(t.Context(), q.ID, 0, 20)
	if e != nil || len(versions) != 1 {
		t.Fatal(versions, e)
	}
	choose := url.Values{"action": {"choose"}, "question_id": {q.ID}, "question_revision": {"1"}, "head_revision": {"1"}, "snapshot_id": {versions[0].ID}, "request_key": {uuid.NewString()}}
	rec = postForm(t, srv, cookie, "/questions/understanding-action", choose.Encode())
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	page = doWithCookie(srv, cookie, "GET", path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "当前 v1") {
		t.Fatal(page.Code, page.Body.String())
	}
	notes, e := srv.store.DB.Query(`SELECT count(*) FROM owner_notes`)
	if e != nil {
		t.Fatal(e)
	}
	notes.Close()
}

func TestUnderstandingSnapshotHistoryShowsChangedReferenceWithoutOverwritingAnswer(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "understanding-change@example.com", "password123")
	_, noteID := seedKnowledgeLearning(t, srv)
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "旧参考版本显示"})
	if e != nil {
		t.Fatal(e)
	}
	n, e := srv.store.GetOwnerNote(t.Context(), noteID)
	if e != nil {
		t.Fatal(e)
	}
	v, e := srv.store.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "Owner回答不自动重写", References: []store.UnderstandingReference{{Kind: "note", ObjectID: noteID, Version: n.Revision}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = srv.store.ChooseCurrentUnderstanding(t.Context(), q.ID, v.ID, q.Revision, 1, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.UpdateOwnerNote(t.Context(), n.ID, "参考笔记已经更新", n.CitationsJSON, n.ReferencesJSON, n.Revision); e != nil {
		t.Fatal(e)
	}
	page := doWithCookie(srv, cookie, "GET", "/questions/"+q.ID+"/understandings")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "参考对象已更新或移除") || !strings.Contains(page.Body.String(), "Owner回答不自动重写") {
		t.Fatal(page.Code, page.Body.String())
	}
}
