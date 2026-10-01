package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAutomationReadControlsAndPrivateProjection(t *testing.T) {
	srv, cookie, fake, _ := dailyHTTPFixture(t)
	ctx := t.Context()
	runs, _, err := srv.store.ListRuns(ctx, "", "", 0)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	id := runs[0].ID
	for _, path := range []string{"/automation", "/api/automation", "/automation/" + id} {
		page := doWithCookie(srv, cookie, "GET", path)
		if page.Code != 200 {
			t.Fatal(path, page.Code, page.Body.String())
		}
	}
	// Raw endpoint, authentication and checkpoint data never enter the typed detail.
	_, err = srv.store.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='{"secret":"private-token"}',last_error='remote https://user:secret@example.test/v1?api_key=token failed Bearer secret-token' WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	page := doWithCookie(srv, cookie, "GET", "/automation/"+id)
	if page.Code != 200 || strings.Contains(page.Body.String(), "private-token") || strings.Contains(page.Body.String(), "api_key=token") || strings.Contains(page.Body.String(), "secret-token") {
		t.Fatal(page.Body.String())
	}
	if fake.calls != 1 {
		t.Fatal("GET called provider")
	}
	fields := url.Values{"kind": {"lane"}, "target": {"review"}, "action": {"pause"}, "reason": {"暂不生成"}, "expected_revision": {"1"}, "request_key": {uuid.NewString()}}
	response := postForm(t, srv, cookie, "/automation/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = postForm(t, srv, cookie, "/automation/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	fields.Set("request_key", uuid.NewString())
	fields.Set("reason", "另一窗口的输入")
	response = postForm(t, srv, cookie, "/automation/action", fields.Encode())
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	control, _ := srv.store.GetRunControl(ctx, "lane", "review")
	if !control.Paused || control.Revision != 2 {
		t.Fatal(control)
	}
	fields.Set("expected_revision", strconv.Itoa(control.Revision))
	fields.Set("action", "resume")
	fields.Set("request_key", uuid.NewString())
	response = postForm(t, srv, cookie, "/automation/action", fields.Encode())
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	if p := doWithCookie(srv, cookie, "GET", "/automation?status=bad"); p.Code != 400 {
		t.Fatal(p.Code)
	}
	if p := doWithCookie(srv, cookie, "GET", "/automation/missing"); p.Code != 404 {
		t.Fatal(p.Code)
	}
	if p := doWithCookie(srv, cookie, "GET", "/automation?offset=bad"); p.Code != 400 {
		t.Fatal(p.Code)
	}
	if p := doWithCookie(srv, cookie, "GET", "/automation/"+id+"?actions_offset=-1"); p.Code != 400 {
		t.Fatal(p.Code)
	}
}
func TestAutomationAuthCSRFMethodAndJSONResponse(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "automation@example.com", "password123")
	for _, path := range []string{"/automation", "/api/automation", "/automation/action"} {
		req := httptest.NewRequest("POST", path, strings.NewReader("kind=lane&target=review&action=pause"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatal(path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest("GET", "/api/automation", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code)
	}
	page := doWithCookie(srv, cookie, "GET", "/automation/action")
	if page.Code != 405 {
		t.Fatal(page.Code)
	}
	req := httptest.NewRequest("POST", "/automation/action", strings.NewReader("kind=lane&target=voice&action=pause&reason=explicit&expected_revision=1&request_key="+uuid.NewString()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	csrf := journeyCSRF(t, srv, cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	req.Header.Set("X-CSRF-Token", csrf)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"href":"/automation"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
