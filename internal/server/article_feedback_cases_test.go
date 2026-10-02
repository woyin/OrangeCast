package server

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestArticleQualityHTTPPrivacyAnchorsAndCAS(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "quality@example.com", "password123")
	if r := doWithCookie(srv, nil, "GET", "/quality-cases"); r.Code != 303 {
		t.Fatal(r.Code)
	}
	unsafe := httptest.NewRequest("POST", "/quality-cases/action", strings.NewReader("action=retire"))
	unsafe.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unsafe.AddCookie(session)
	blocked := httptest.NewRecorder()
	srv.Router().ServeHTTP(blocked, unsafe)
	if blocked.Code != 403 {
		t.Fatal("CSRF bypass", blocked.Code)
	}
	seedKnowledgeLearning(t, srv)
	profile, err := srv.store.CreateEditorialProfile(t.Context(), models.EditorialProfile{Name: "质量", TargetAudience: "Owner", Voice: "清楚", StyleGuide: "边界"})
	profiles := []*models.EditorialProfile{profile}
	if err != nil || len(profiles) == 0 {
		t.Fatal(err)
	}
	req, _, err := srv.store.BuildKnowledgeArticleRequest(t.Context(), profiles[0].ID, "pod")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := srv.store.ReserveKnowledgeArticle(t.Context(), profiles[0].ID, "pod", "writer", req, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(req)
	blocks := []provider.KnowledgeBlock{{Kind: "synthesis", Text: "自建文本的条件"}}
	body, _ := json.Marshal(blocks)
	_, err = srv.store.DB.Exec(`INSERT INTO knowledge_article_revisions(article_id,revision,title,input_json,blocks_json,content_hash,origin,provider,model,prompt_version) VALUES(?,1,'自建',?,?,'exact','owner','pod','writer','v4')`, a.ID, string(raw), string(body))
	if err != nil {
		t.Fatal(err)
	}
	page := doWithCookie(srv, session, "GET", "/quality-cases?article_id="+a.ID+"&revision=1")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "自建文本的条件") {
		t.Fatal(page.Code, page.Body.String())
	}
	f := url.Values{"action": {"feedback"}, "article_id": {a.ID}, "revision": {"1"}, "content_hash": {"exact"}, "paragraph_index": {"0"}, "paragraph_hash": {articleParagraphHash(blocks[0].Text)}, "category": {"shallow"}, "comment": {"补条件"}}
	response := postQualityJSON(t, srv, session, "/quality-cases/action", f.Encode())
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result struct {
		Result string `json:"result"`
	}
	json.Unmarshal(response.Body.Bytes(), &result)
	accept := url.Values{"action": {"accept"}, "feedback_id": {result.Result}, "request_key": {uuid.NewString()}, "version": {"0"}, "expected": {"补充具体条件"}}
	first := postQualityJSON(t, srv, session, "/quality-cases/action", accept.Encode())
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	again := postQualityJSON(t, srv, session, "/quality-cases/action", accept.Encode())
	if again.Code != 200 || first.Body.String() != again.Body.String() {
		t.Fatal("unsafe replay", again.Code)
	}
	accept.Set("expected", "修改期望")
	if conflict := postQualityJSON(t, srv, session, "/quality-cases/action", accept.Encode()); conflict.Code != 409 {
		t.Fatal(conflict.Code)
	}
	// Native browser submission has no JSON Accept header and needs no JavaScript.
	nativePage := doWithCookie(srv, session, "GET", "/quality-cases")
	keys := regexp.MustCompile(`name="request_key"[^>]*value="([^"]+)"`).FindAllStringSubmatch(nativePage.Body.String(), -1)
	if len(keys) != 3 {
		t.Fatalf("server-generated command identities: %d", len(keys))
	}
	for _, key := range keys {
		if _, e := uuid.Parse(key[1]); e != nil {
			t.Fatal(e)
		}
	}
	if !strings.Contains(nativePage.Body.String(), `value="`+result.Result+`">自建 · 原版本 1 · 太浅：补条件`) {
		t.Fatal("feedback selector must use real feedback with readable title/version/category")
	}
	accept.Set("request_key", keys[0][1])
	accept.Set("version", "1")
	accept.Set("expected", "原生表单的条件")
	native := postForm(t, srv, session, "/quality-cases/action", accept.Encode())
	if native.Code != 303 || native.Header().Get("Location") != "/quality-cases" {
		t.Fatal(native.Code, native.Body.String())
	}
	if replay := postForm(t, srv, session, "/quality-cases/action", accept.Encode()); replay.Code != 303 {
		t.Fatal("native replay", replay.Code)
	}
	accept.Set("expected", "冲突仍保留的原始期望")
	failed := postForm(t, srv, session, "/quality-cases/action", accept.Encode())
	for _, want := range []string{"失败操作的原输入", "冲突仍保留的原始期望", `name="request_key" value="` + keys[0][1] + `"`, `name="version" value="1"`, `name="feedback_id" value="` + result.Result + `"`} {
		if failed.Code != 409 || !strings.Contains(failed.Body.String(), want) {
			t.Fatalf("native recovery missing %q: %d %s", want, failed.Code, failed.Body.String())
		}
	}
	for _, action := range []string{"classify", "retire"} {
		page = doWithCookie(srv, session, "GET", "/quality-cases")
		pattern := regexp.MustCompile(`name="action" value="` + action + `"><input name="request_key" type="hidden" value="([^"]+)"><input type="hidden" name="case_id" value="([^"]+)"><input type="hidden" name="version" value="([^"]+)"`)
		match := pattern.FindStringSubmatch(page.Body.String())
		if len(match) != 4 {
			t.Fatal("missing native command key", action)
		}
		values := url.Values{"action": {action}, "case_id": {match[2]}, "version": {match[3]}, "request_key": {match[1]}, "classification": {"unknown"}, "evidence": {`{"owner":"条件尚待核对"}`}}
		response := postForm(t, srv, session, "/quality-cases/action", values.Encode())
		if response.Code != 303 {
			t.Fatal(action, response.Code, response.Body.String())
		}
		if replay := postForm(t, srv, session, "/quality-cases/action", values.Encode()); replay.Code != 303 {
			t.Fatal(action, "replay", replay.Code)
		}
	}

}

func postQualityJSON(t *testing.T, srv *Server, cookie *http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("GET", path, nil)
	request.AddCookie(cookie)
	csrfResponse := httptest.NewRecorder()
	srv.Router().ServeHTTP(csrfResponse, request)
	csrf := ""
	for _, c := range csrfResponse.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	request = httptest.NewRequest("POST", path, strings.NewReader("_csrf="+csrf+"&"+body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(cookie)
	request.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	response := httptest.NewRecorder()
	srv.Router().ServeHTTP(response, request)
	return response
}
