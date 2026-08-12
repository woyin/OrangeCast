package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// handleDocuments lists EvidenceDocument snapshots; their text is the immutable PrimarySource.
func (srv *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := srv.store.ListDocuments(r.Context())
	if err != nil { http.Error(w, "加载文档失败", http.StatusInternalServerError); return }
	srv.tmpl.Render(w, "documents.html", map[string]any{"Documents": docs})
}

func (srv *Server) handleDocumentNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet { srv.tmpl.Render(w, "document_new.html", map[string]any{"CSRF": auth.CSRFValue(r)}); return }
	doc, err := srv.store.CreatePastedDocument(r.Context(), r.FormValue("title"), r.FormValue("content"))
	if err != nil { srv.tmpl.Render(w, "document_new.html", map[string]any{"Error": "标题和正文均不能为空", "CSRF": auth.CSRFValue(r)}); return }
	http.Redirect(w, r, "/documents/"+doc.ID, http.StatusSeeOther)
}

func (srv *Server) handleDocumentDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/documents/"))
	if id == "" || strings.Contains(id, "/") { http.NotFound(w, r); return }
	doc, err := srv.store.GetDocument(r.Context(), id)
	if err == store.ErrNotFound { http.NotFound(w, r); return }
	if err != nil { http.Error(w, "加载文档失败", http.StatusInternalServerError); return }
	srv.tmpl.Render(w, "document_detail.html", map[string]any{"Document": doc})
}
