// Package server：创作素材选择（C01 / ADR-0024 §1）。
// 从一个或多个来源选择 KeyPoint 与个人笔记，保存选择快照；
// 首次创作复用默认画像；被排除材料显式给出原因；全程无付费调用。
package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleCreationSelections(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		profileID := strings.TrimSpace(r.FormValue("profile_id"))
		var materialIDs, noteIDs []string
		// 每行一个 ID，也接受逗号分隔；支持多值字段。
		for _, raw := range r.Form["material_ids"] {
			for _, id := range strings.FieldsFunc(raw, func(c rune) bool { return c == '\n' || c == ',' || c == '\r' }) {
				if id = strings.TrimSpace(id); id != "" {
					materialIDs = append(materialIDs, id)
				}
			}
		}
		for _, raw := range r.Form["note_ids"] {
			for _, id := range strings.FieldsFunc(raw, func(c rune) bool { return c == '\n' || c == ',' || c == '\r' }) {
				if id = strings.TrimSpace(id); id != "" {
					noteIDs = append(noteIDs, id)
				}
			}
		}
		sel, err := srv.store.SaveCreationSelection(r.Context(), &models.CreationSelection{
			EditorialProfileID: profileID,
			Title:              strings.TrimSpace(r.FormValue("title")),
			MaterialIDs:        materialIDs,
			NoteIDs:            noteIDs,
			Status:             models.SelectionDraft,
		})
		if err != nil {
			http.Error(w, "保存素材选择失败："+err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("confirm") == "1" {
			if _, err := srv.store.SaveCreationSelection(r.Context(), &models.CreationSelection{
				ID: sel.ID, EditorialProfileID: sel.EditorialProfileID, Title: sel.Title,
				MaterialIDs: sel.MaterialIDs, NoteIDs: sel.NoteIDs,
				Scope: sel.Scope, Status: models.SelectionConfirmed,
			}); err != nil {
				http.Error(w, "确认素材选择失败："+err.Error(), http.StatusBadRequest)
				return
			}
		}
		http.Redirect(w, r, "/creation/selections", http.StatusSeeOther)
		return
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(r.Context())
	if err != nil {
		http.Error(w, "加载画像失败", http.StatusInternalServerError)
		return
	}
	selections, err := srv.store.ListCreationSelections(r.Context(), profile.ID)
	if err != nil {
		http.Error(w, "加载素材选择失败", http.StatusInternalServerError)
		return
	}
	srv.tmpl.Render(w, "creation_selections.html", map[string]any{
		"Profile":    profile,
		"Selections": selections,
		"CSRF":       auth.CSRFValue(r),
	})
}
