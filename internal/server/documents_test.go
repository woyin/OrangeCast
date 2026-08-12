package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestPastedDocumentWorkflow(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "docs@example.com", "password123")
	page := doWithCookie(srv, cookie, http.MethodGet, "/documents/new")
	if page.Code != http.StatusOK { t.Fatalf("new document page: %d", page.Code) }
	csrf := ""
	for _, c := range page.Result().Cookies() { if c.Name == "cwp_csrf" { csrf = c.Value } }
	post := postForm(t, srv, cookie, "/documents/new", "_csrf="+csrf+"&title=%E7%A0%94%E7%A9%B6&content=%E5%8E%9F%E5%A7%8B%E8%AF%81%E6%8D%AE")
	if post.Code != http.StatusSeeOther || !strings.HasPrefix(post.Header().Get("Location"), "/documents/") { t.Fatalf("document save: %d %q", post.Code, post.Header().Get("Location")) }
	list := doWithCookie(srv, cookie, http.MethodGet, "/documents")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "研究") { t.Fatalf("document list: %d %s", list.Code, list.Body.String()) }
	detail := doWithCookie(srv, cookie, http.MethodGet, post.Header().Get("Location"))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "原始证据") || !strings.Contains(detail.Body.String(), "SHA-256") { t.Fatalf("document detail: %d %s", detail.Code, detail.Body.String()) }
	bad := postForm(t, srv, cookie, "/documents/new", "_csrf="+csrf+"&title=&content=")
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), "不能为空") { t.Fatalf("invalid document should rerender: %d %s", bad.Code, bad.Body.String()) }
}
