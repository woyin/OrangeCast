package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func (srv *Server) handleRightsConstraint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	if err := srv.store.UpsertRightsConstraint(r.Context(), sourceType, sourceID, strings.TrimSpace(r.FormValue("constraint_kind")), strings.TrimSpace(r.FormValue("details")), r.FormValue("active") == "on"); err != nil {
		http.Error(w, "保存 RightsConstraint 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}

// handleOwnerNote records either a cited source-faithful note or an explicitly
// personal reflection without conflating the two kinds of expression.
func (srv *Server) handleOwnerNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	redirectToSource := func() {
		if sourceType == models.SourceDocument {
			http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
	}
	// R23 补齐：删除入口（乐观并发：过期删除提示冲突；被精读引用的笔记拒绝删除）。
	if r.FormValue("action") == "delete" {
		noteID := strings.TrimSpace(r.FormValue("note_id"))
		expected, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("expected_revision")))
		if noteID == "" || convErr != nil || expected < 1 {
			http.Error(w, "缺少有效的笔记标识或版本号（请刷新页面后重试）", http.StatusBadRequest)
			return
		}
		if err := srv.store.DeleteOwnerNote(r.Context(), noteID, expected); err != nil {
			if errors.Is(err, store.ErrConflict) {
				http.Error(w, "笔记无法删除（版本冲突或已被精读引用），请刷新后重试", http.StatusConflict)
				return
			}
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "笔记不存在", http.StatusNotFound)
				return
			}
			http.Error(w, "删除 OwnerNote 失败："+err.Error(), http.StatusBadRequest)
			return
		}
		redirectToSource()
		return
	}
	// K04：带 note_id + expected_revision 的提交为编辑路径（乐观并发：过期编辑提示冲突）。
	if noteID := strings.TrimSpace(r.FormValue("note_id")); noteID != "" {
		expected, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("expected_revision")))
		if convErr != nil || expected < 1 {
			http.Error(w, "缺少有效的笔记版本号（请刷新页面后重试）", http.StatusBadRequest)
			return
		}
		if _, err := srv.store.UpdateOwnerNote(r.Context(), noteID, strings.TrimSpace(r.FormValue("content")), strings.TrimSpace(r.FormValue("citations_json")), strings.TrimSpace(r.FormValue("references_json")), expected); err != nil {
			if errors.Is(err, store.ErrConflict) {
				http.Error(w, "笔记已被其他编辑更新（版本冲突），请刷新后基于最新版本重试", http.StatusConflict)
				return
			}
			http.Error(w, "更新 OwnerNote 失败："+err.Error(), http.StatusBadRequest)
			return
		}
		if sourceType == models.SourceDocument {
			http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
		return
	}
	if _, err := srv.store.CreateOwnerNote(r.Context(), models.OwnerNote{SourceType: string(sourceType), SourceID: sourceID, Kind: strings.TrimSpace(r.FormValue("kind")), Content: strings.TrimSpace(r.FormValue("content")), CitationsJSON: strings.TrimSpace(r.FormValue("citations_json")), ReferencesJSON: strings.TrimSpace(r.FormValue("references_json"))}); err != nil {
		http.Error(w, "保存 OwnerNote 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}
