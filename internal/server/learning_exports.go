package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

type learningExportChoice struct{ ID, Label string }

// registerLearningExportRoutes is mounted behind the Owner authentication and CSRF boundary.
func (srv *Server) registerLearningExportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/learning-exports", srv.handleLearningExports)
	mux.HandleFunc("/learning-exports/preview", srv.handleLearningExportPreview)
	mux.HandleFunc("/learning-exports/create", srv.handleLearningExportCreate)
	mux.HandleFunc("/learning-exports/", srv.handleLearningExportItem)
}

func learningExportScope(r *http.Request) store.LearningExportScope {
	kind, id := r.FormValue("kind"), r.FormValue("id")
	if selection := r.FormValue("selection"); selection != "" {
		var ok bool
		kind, id, ok = strings.Cut(selection, ":")
		if !ok || id == "" {
			kind, id = "", ""
		}
	}
	return store.LearningExportScope{Kind: kind, ID: id, IncludeHistory: r.FormValue("include_history") == "yes", IncludeDrafts: r.FormValue("include_drafts") == "yes", IncludeExcerpts: r.FormValue("include_excerpts") == "yes"}
}

func learningExportError(w http.ResponseWriter, err error) {
	code, message := http.StatusInternalServerError, "导出操作失败，请稍后重试。"
	switch {
	case errors.Is(err, store.ErrConflict):
		code, message = 409, "预览已过期或范围已改变，请重新预览并确认。"
	case errors.Is(err, store.ErrNotFound):
		code, message = 404, "导出或范围不存在，或当前不可下载。"
	case errors.Is(err, store.ErrInvalidEditorialState):
		code, message = 400, "导出范围或选项无效，或者超出导出限制。"
	}
	http.Error(w, message, code)
}

func (srv *Server) handleLearningExports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	var preview *store.LearningExportPreview
	if id := r.URL.Query().Get("preview_id"); id != "" {
		var err error
		preview, err = srv.store.GetLearningExportPreview(r.Context(), id)
		if err != nil {
			learningExportError(w, err)
			return
		}
	}
	srv.renderLearningExports(w, r, preview)
}

func (srv *Server) renderLearningExports(w http.ResponseWriter, r *http.Request, preview *store.LearningExportPreview) {
	exports, err := srv.store.ListLearningExports(r.Context())
	if err != nil {
		learningExportError(w, err)
		return
	}
	questions, err := srv.store.ListLearningQuestions(r.Context(), "", "")
	if err != nil {
		learningExportError(w, err)
		return
	}
	profiles, err := srv.store.ListEditorialProfiles(r.Context())
	if err != nil {
		learningExportError(w, err)
		return
	}
	var themes []learningExportChoice
	for _, profile := range profiles {
		items, e := srv.store.ListThemes(r.Context(), profile.ID)
		if e != nil {
			learningExportError(w, e)
			return
		}
		for _, item := range items {
			themes = append(themes, learningExportChoice{ID: item.ID, Label: item.Name})
		}
	}
	scope := learningExportScope(r)
	if preview != nil {
		scope = preview.Scope
	}
	labels := map[string]string{}
	var choices []learningExportChoice
	for _, q := range questions {
		key := "question:" + q.ID
		label := "学习问题：" + q.Body
		labels[key] = label
		choices = append(choices, learningExportChoice{ID: key, Label: label})
	}
	for _, theme := range themes {
		key := "theme:" + theme.ID
		label := "主题：" + theme.Label
		labels[key] = label
		choices = append(choices, learningExportChoice{ID: key, Label: label})
	}
	resolve := func(kind, id string) string {
		key := kind + ":" + id
		if label := labels[key]; label != "" {
			return label
		}
		label := "材料已删除，请重新选择范围"
		if kind == "question" {
			if q, e := srv.store.GetLearningQuestion(r.Context(), id); e == nil {
				label = "学习问题：" + q.Body
			}
		} else if kind == "theme" {
			if theme, e := srv.store.GetTheme(r.Context(), id); e == nil {
				label = "主题：" + theme.Name
			}
		}
		labels[key] = label
		return label
	}
	selected := scope.Kind + ":" + scope.ID
	if scope.ID != "" && labels[selected] == "" {
		label := resolve(scope.Kind, scope.ID)
		if label != "材料已删除，请重新选择范围" {
			choices = append(choices, learningExportChoice{ID: selected, Label: label})
		}
	}
	for _, v := range exports {
		resolve(v.Preview.Scope.Kind, v.Preview.Scope.ID)
	}
	scopeLabel := resolve(scope.Kind, scope.ID)
	w.Header().Set("Cache-Control", "no-store")
	if err = srv.tmpl.Render(w, "learning_exports.html", map[string]any{"CSRF": auth.CSRFValue(r), "Scope": scope, "Preview": preview, "Exports": exports, "Questions": questions, "Themes": themes, "Choices": choices, "Selection": selected, "ScopeLabel": scopeLabel, "ScopeLabels": labels}); err != nil {
		http.Error(w, "渲染导出页面失败", 500)
	}
}

func (srv *Server) handleLearningExportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	scope := learningExportScope(r)
	scope.PublicURL = srv.cfg.PublicURL
	preview, err := srv.store.PreviewLearningExport(r.Context(), scope)
	if err != nil {
		learningExportError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeLearningExportNavigation(w, "/learning-exports?preview_id="+preview.ID)
		return
	}
	srv.renderLearningExports(w, r, preview)
}

func writeLearningExportNavigation(w http.ResponseWriter, href string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"state": "saved", "href": href})
}

func (srv *Server) handleLearningExportCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	if r.FormValue("confirmed") != "yes" {
		http.Error(w, "请明确确认本次预览范围。", 400)
		return
	}
	ownerID, _ := auth.UserIDFromContext(r.Context())
	item, err := srv.store.CreateLearningExport(r.Context(), r.FormValue("preview_id"), r.FormValue("preview_hash"), r.FormValue("request_key"), ownerID)
	if err != nil {
		learningExportError(w, err)
		return
	}
	href := "/learning-exports#export-" + item.ID
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeLearningExportNavigation(w, href)
		return
	}
	http.Redirect(w, r, href, http.StatusSeeOther)
}

func (srv *Server) handleLearningExportItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/learning-exports/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch parts[1] {
	case "status":
		item, err := srv.store.GetLearningExport(r.Context(), parts[0])
		if err != nil {
			learningExportError(w, err)
			return
		}
		if item.Status == "ready" {
			if _, err := srv.store.ValidLearningExportDownload(r.Context(), item.ID); err != nil {
				if !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrNotFound) {
					learningExportError(w, err)
					return
				}
				item.Status = "expired"
			}
		}
		// Never expose the absolute archive path, frozen material, or provider metadata.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": item.ID, "status": item.Status, "expires_at": item.ExpiresAt, "download_url": "/learning-exports/" + item.ID + "/download"})
	case "download":
		item, err := srv.store.ValidLearningExportDownload(r.Context(), parts[0])
		if err != nil {
			learningExportError(w, err)
			return
		}
		root, err := filepath.Abs(filepath.Join(srv.cfg.DataDir, "learning-exports"))
		if err != nil {
			learningExportError(w, err)
			return
		}
		path, err := filepath.Abs(item.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			http.NotFound(w, r)
			return
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		directory, err := os.OpenRoot(root)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer directory.Close()
		file, err := directory.Open(rel)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="learning-export.zip"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "learning-export.zip", info.ModTime(), file)
	default:
		http.NotFound(w, r)
	}
}
