package server

import (
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
	"html"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUnderstandingSSRConflictRetainsExactSubmittedDraftIdentity(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "understanding-recovery@example.com", "password123")
	q, e := srv.store.CreateLearningQuestion(t.Context(), store.LearningQuestion{Body: "冲突恢复"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = srv.store.SaveUnderstanding(t.Context(), store.SaveUnderstandingCommand{QuestionID: q.ID, QuestionRevision: q.Revision, RequestKey: uuid.NewString(), Answer: "另一窗口已保存"}); e != nil {
		t.Fatal(e)
	}
	ref := `{"Kind":"note","ObjectID":"unavailable-note","Version":1}`
	values := url.Values{"action": {"save"}, "question_id": {q.ID}, "question_revision": {"1"}, "head_revision": {"0"}, "parent_id": {"old-parent"}, "request_key": {uuid.NewString()}, "answer": {"失败后保留的理解草稿 <script>danger</script>"}, "uncertainty": {"仍不确定的条件"}, "next_step": {"下一步核对"}, "model_data_policy": {"approved_providers_only"}, "approved_providers": {"pod other"}, "owner_confirmed": {"yes"}, "reference": {ref}}
	rec := postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "data-understanding-recovery") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, field := range []string{"answer", "uncertainty", "next_step", "head_revision", "parent_id", "request_key", "model_data_policy", "approved_providers", "reference"} {
		if !strings.Contains(rec.Body.String(), html.EscapeString(values.Get(field))) {
			t.Fatalf("lost %s: %s", field, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "<script>danger</script>") || strings.Contains(rec.Body.String(), `name="owner_confirmed" value="yes" required checked`) {
		t.Fatal("unsafe or silently confirmed recovery")
	}
	h, e := srv.store.UnderstandingHead(t.Context(), q.ID)
	if e != nil || h.Revision != 1 {
		t.Fatal(h, e)
	}
	// JSON clients retain the existing short conflict response and never receive reflected draft HTML.
	req := httptest.NewRequest("POST", "/questions/understanding-action", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec = httptest.NewRecorder()
	srv.handleUnderstandingAction(rec, req)
	if rec.Code != 409 || strings.Contains(rec.Body.String(), values.Get("answer")) || strings.Contains(rec.Body.String(), "data-understanding-recovery") {
		t.Fatal(rec.Code, rec.Body.String())
	}

	values.Set("answer", strings.Repeat("x", 40001))
	values.Set("reference", "{")
	oversized := postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if oversized.Code != 400 || !strings.Contains(oversized.Body.String(), "超过字段限制") || !strings.Contains(oversized.Body.String(), values.Get("uncertainty")) || strings.Contains(oversized.Body.String(), strings.Repeat("x", 40001)) {
		t.Fatal(oversized.Code, oversized.Body.String())
	}
	values.Set("answer", "失败后保留的理解草稿 <script>danger</script>")
	values.Set("reference", ref)
	values.Set("question_revision", "invalid")
	rec = postForm(t, srv, cookie, "/questions/understanding-action", values.Encode())
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), html.EscapeString(values.Get("answer"))) {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
