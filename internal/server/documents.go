package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
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

func (srv *Server) handleDocumentPDF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+(1<<20))
	file, header, err := r.FormFile("pdf")
	if err != nil {
		http.Error(w, "请选择不超过 4MB 的 PDF", http.StatusBadRequest)
		return
	}
	defer file.Close()
	temporary, err := os.CreateTemp("", "cloudwisepod-document-*.pdf")
	if err != nil {
		http.Error(w, "创建 PDF 临时文件失败", http.StatusInternalServerError)
		return
	}
	path := temporary.Name()
	defer os.Remove(path)
	written, copyErr := io.Copy(temporary, io.LimitReader(file, maxDocumentSize+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		http.Error(w, "读取 PDF 失败", http.StatusBadRequest)
		return
	}
	if written > maxDocumentSize {
		http.Error(w, "PDF 超过 4MB 上限", http.StatusRequestEntityTooLarge)
		return
	}
	pdfFile, reader, err := pdf.Open(path)
	if err != nil {
		http.Error(w, "PDF 格式无效："+err.Error(), http.StatusBadRequest)
		return
	}
	defer pdfFile.Close()
	plainReader, err := reader.GetPlainText()
	if err != nil {
		http.Error(w, "提取 PDF 文本失败："+err.Error(), http.StatusBadRequest)
		return
	}
	content, err := io.ReadAll(io.LimitReader(plainReader, maxDocumentSize+1))
	if err != nil || len(content) == 0 {
		http.Error(w, "PDF 没有可提取文本", http.StatusBadRequest)
		return
	}
	if len(content) > maxDocumentSize {
		http.Error(w, "PDF 提取文本超过 4MB 上限", http.StatusRequestEntityTooLarge)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	document, err := srv.store.CreatePDFDocument(r.Context(), title, header.Filename, string(content))
	if err != nil {
		http.Error(w, fmt.Sprintf("保存 PDF 证据失败：%v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/documents/"+document.ID, http.StatusSeeOther)
}

func (srv *Server) handleDocumentNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "document_new.html", map[string]any{"CSRF": auth.CSRFValue(r)})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+(64<<10))
	if err := r.ParseForm(); err != nil {
		http.Error(w, "文档请求超过大小上限", http.StatusRequestEntityTooLarge)
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
	policy, err := srv.store.GetSourcePolicy(r.Context(), models.SourceDocument, doc.ID)
	if err != nil {
		http.Error(w, "加载文档策略失败", http.StatusInternalServerError)
		return
	}
	versions, err := srv.store.ListDocumentVersions(r.Context(), doc.SeriesID)
	if err != nil {
		http.Error(w, "加载文档版本失败", http.StatusInternalServerError)
		return
	}
	card, err := srv.store.GetDocumentKnowledgeCard(r.Context(), doc.ID)
	if err != nil {
		http.Error(w, "加载文档知识卡片失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "document_detail.html", map[string]any{"Document": doc, "Segments": store.DocumentSegments(doc), "SourcePolicy": policy, "Versions": versions, "Card": card, "CSRF": auth.CSRFValue(r)})
}

func (srv *Server) handleDocumentVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSize+(64<<10))
	if err := r.ParseForm(); err != nil {
		http.Error(w, "文档请求超过大小上限", http.StatusRequestEntityTooLarge)
		return
	}
	document, err := srv.store.CreateDocumentVersion(r.Context(), strings.TrimSpace(r.FormValue("document_id")), strings.TrimSpace(r.FormValue("title")), r.FormValue("content"))
	if err != nil {
		http.Error(w, "创建文档版本失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/documents/"+document.ID, http.StatusSeeOther)
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
