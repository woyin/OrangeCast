package server

import (
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestUnifiedSearchSSRLocalAndScoped(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "search-v2@example.com", "password123")
	doc, err := srv.store.CreatePastedDocument(t.Context(), "中文记忆", "主动回忆与记忆巩固的联系。")
	if err != nil {
		t.Fatal(err)
	}
	note, err := srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "document", SourceID: doc.ID, Kind: "owner_reflection", Content: "我想用主动回忆检查理解。<svg onload=alert(1)>"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &knowledgeStageFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	path := "/search?" + url.Values{"q": {"主动回忆"}, "source": {"document:" + doc.ID}, "kind": {"owner_reflection"}}.Encode()
	page := doWithCookie(srv, session, http.MethodGet, path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "我的理解") || !strings.Contains(page.Body.String(), "#note-"+note.ID) || !strings.Contains(page.Body.String(), "&lt;svg") {
		t.Fatal(page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "<svg onload") || fake.calls != 0 {
		t.Fatal("unsafe rendering or paid GET")
	}
	if bad := doWithCookie(srv, session, http.MethodGet, "/search?kind=unknown"); bad.Code != 400 {
		t.Fatal(bad.Code)
	}
	if err := srv.store.ArchiveSource(t.Context(), models.SourceDocument, doc.ID, true); err != nil {
		t.Fatal(err)
	}
	page = doWithCookie(srv, session, http.MethodGet, path)
	if page.Code != 200 || strings.Contains(page.Body.String(), "#note-"+note.ID) {
		t.Fatal("archived source results", page.Code)
	}
	srv.store.DB.Close()
	if failure := doWithCookie(srv, session, http.MethodGet, "/search?q=记忆"); failure.Code != 500 {
		t.Fatal("database failure hidden", failure.Code)
	}
}
