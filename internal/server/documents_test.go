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
	if page.Code != http.StatusOK {
		t.Fatalf("new document page: %d", page.Code)
	}
	csrf := ""
	for _, c := range page.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c.Value
		}
	}
	post := postForm(t, srv, cookie, "/documents/new", "_csrf="+csrf+"&title=%E7%A0%94%E7%A9%B6&content=%E5%8E%9F%E5%A7%8B%E8%AF%81%E6%8D%AE")
	if post.Code != http.StatusSeeOther || !strings.HasPrefix(post.Header().Get("Location"), "/documents/") {
		t.Fatalf("document save: %d %q", post.Code, post.Header().Get("Location"))
	}
	list := doWithCookie(srv, cookie, http.MethodGet, "/documents")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "研究") {
		t.Fatalf("document list: %d %s", list.Code, list.Body.String())
	}
	detail := doWithCookie(srv, cookie, http.MethodGet, post.Header().Get("Location"))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "原始证据") || !strings.Contains(detail.Body.String(), "SHA-256") {
		t.Fatalf("document detail: %d %s", detail.Code, detail.Body.String())
	}
	docs, err := srv.store.ListDocuments(t.Context())
	if err != nil || len(docs) != 1 {
		t.Fatalf("saved document: %+v %v", docs, err)
	}
	kp := postForm(t, srv, cookie, "/documents/keypoints", "document_id="+docs[0].ID+"&segment_id="+docs[0].ID+"-p0001&content=%E5%8F%AF%E5%A4%8D%E7%94%A8%E8%A7%82%E7%82%B9")
	if kp.Code != http.StatusSeeOther {
		t.Fatalf("document KeyPoint: %d %s", kp.Code, kp.Body.String())
	}
	keyPoints, _, err := srv.store.ListKeyPoints(t.Context(), 1, 10)
	if err != nil || len(keyPoints) != 1 || keyPoints[0].SourceType != "document" || !strings.Contains(keyPoints[0].CitationsJSON, "-p0001") {
		t.Fatalf("document KeyPoint must retain paragraph citation: %+v %v", keyPoints, err)
	}
	bad := postForm(t, srv, cookie, "/documents/new", "_csrf="+csrf+"&title=&content=")
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), "不能为空") {
		t.Fatalf("invalid document should rerender: %d %s", bad.Code, bad.Body.String())
	}
}
