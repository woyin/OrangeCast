package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

// handleDocuments lists EvidenceDocument snapshots; their text is the immutable PrimarySource.
func (srv *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := srv.store.ListDocuments(r.Context())
	if err != nil {
		http.Error(w, "加载文档失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "documents.html", map[string]any{"Documents": docs, "CSRF": auth.CSRFValue(r)})
}

func (srv *Server) handleDocumentNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "document_new.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	doc, err := srv.store.CreatePastedDocument(r.Context(), r.FormValue("title"), r.FormValue("content"))
	if err != nil {
		srv.tmpl.Render(w, "document_new.html", map[string]any{"Error": "标题和正文均不能为空", "CSRF": auth.CSRFValue(r)})
		return
	}
	http.Redirect(w, r, "/documents/"+doc.ID, http.StatusSeeOther)
}

func (srv *Server) handleDocumentImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	url := strings.TrimSpace(r.FormValue("url"))
	fetchedTitle, content, err := srv.fetchDocument(r.Context(), url)
	if err != nil {
		srv.tmpl.Render(w, "documents.html", map[string]any{"Error": "抓取失败：" + err.Error(), "CSRF": auth.CSRFValue(r)})
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = fetchedTitle
	}
	if title == "" {
		title = url
	}
	doc, err := srv.store.CreateWebDocument(r.Context(), title, url, content)
	if err != nil {
		http.Error(w, "保存网页证据失败", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/documents/"+doc.ID, http.StatusSeeOther)
}

func (srv *Server) handleDocumentDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/documents/"))
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	doc, err := srv.store.GetDocument(r.Context(), id)
	if err == store.ErrNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "加载文档失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "document_detail.html", map[string]any{"Document": doc, "Segments": store.DocumentSegments(doc), "CSRF": auth.CSRFValue(r)})
}

// handleDocumentKeyPoint creates a curator-owned KeyPoint anchored to one exact paragraph.
func (srv *Server) handleDocumentKeyPoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	doc, err := srv.store.GetDocument(r.Context(), strings.TrimSpace(r.FormValue("document_id")))
	if err != nil {
		http.Error(w, "读取文档失败", http.StatusBadRequest)
		return
	}
	segmentID := strings.TrimSpace(r.FormValue("segment_id"))
	position := 0
	for _, segment := range store.DocumentSegments(doc) {
		if segment.ID == segmentID {
			position = segment.Position
			break
		}
	}
	if position == 0 {
		http.Error(w, "段落引用无效", http.StatusBadRequest)
		return
	}
	citations, _ := json.Marshal([]string{segmentID})
	_, err = srv.store.CreateManualKeyPoint(r.Context(), store.KeyPointRow{SourceType: models.SourceDocument, SourceID: doc.ID, SourceTitle: doc.Title, Content: r.FormValue("content"), Description: r.FormValue("description"), CitationsJSON: string(citations), TimeStart: float64(position), TimeEnd: float64(position) + .5})
	if err != nil {
		http.Error(w, "创建 KeyPoint 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/documents/"+doc.ID, http.StatusSeeOther)
}
