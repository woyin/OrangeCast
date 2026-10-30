package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSettingsModels_ListsFromUpstream(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"},{"id":"a-model"}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "test@example.com", "password123")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/settings/models", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "t")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "t"})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"provider":"openai","api_key":"sk-form","base_url":"` + upstream.URL + `/v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct{ Models []string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Models, ",") != "a-model,b-model" {
		t.Errorf("应排序去重，实际 %v", out.Models)
	}
	if gotAuth != "Bearer sk-form" {
		t.Errorf("应使用表单中的 key，实际 %q", gotAuth)
	}

	if rec := post(`{"provider":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("未知 provider 应 400，实际 %d", rec.Code)
	}
	if rec := post(`{"provider":"groq"}`); rec.Code != http.StatusBadGateway {
		t.Errorf("未配置 key 应 502，实际 %d", rec.Code)
	}
}
