package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
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
	if _, err := srv.store.CreateOwnerNote(r.Context(), models.OwnerNote{SourceType: string(sourceType), SourceID: sourceID, Kind: strings.TrimSpace(r.FormValue("kind")), Content: strings.TrimSpace(r.FormValue("content")), CitationsJSON: strings.TrimSpace(r.FormValue("citations_json"))}); err != nil {
		http.Error(w, "保存 OwnerNote 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}
