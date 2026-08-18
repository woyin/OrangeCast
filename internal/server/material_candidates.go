package server

import (
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

func (srv *Server) handleMaterialCandidateCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(strings.TrimSpace(r.FormValue("source_type")))
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	if _, err := srv.store.CreateMaterialCandidate(r.Context(), models.MaterialCandidate{SourceType: string(sourceType), SourceID: sourceID, OriginKind: "owner_note", Content: strings.TrimSpace(r.FormValue("content")), CitationsJSON: strings.TrimSpace(r.FormValue("citations_json"))}); err != nil {
		http.Error(w, "创建学习候选失败："+err.Error(), http.StatusBadRequest)
		return
	}
	srv.redirectSource(w, r, sourceType, sourceID)
}

func (srv *Server) handleMaterialCandidateDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	candidate, err := srv.store.GetMaterialCandidate(r.Context(), strings.TrimSpace(r.FormValue("candidate_id")))
	if err != nil {
		http.Error(w, "读取学习候选失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.store.SetMaterialCandidateStatus(r.Context(), candidate.ID, strings.TrimSpace(r.FormValue("status")), strings.TrimSpace(r.FormValue("reason"))); err != nil {
		http.Error(w, "更新学习候选失败："+err.Error(), http.StatusBadRequest)
		return
	}
	srv.redirectSource(w, r, models.SourceType(candidate.SourceType), candidate.SourceID)
}

func (srv *Server) handleMaterialCandidatePromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	candidate, err := srv.store.GetMaterialCandidate(r.Context(), strings.TrimSpace(r.FormValue("candidate_id")))
	if err != nil {
		http.Error(w, "读取学习候选失败："+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := srv.store.PromoteMaterialCandidate(r.Context(), candidate.ID); err != nil {
		http.Error(w, "提升为 KeyPoint 失败："+err.Error(), http.StatusBadRequest)
		return
	}
	srv.redirectSource(w, r, models.SourceType(candidate.SourceType), candidate.SourceID)
}

func (srv *Server) redirectSource(w http.ResponseWriter, r *http.Request, sourceType models.SourceType, sourceID string) {
	if sourceType == models.SourceDocument {
		http.Redirect(w, r, "/documents/"+sourceID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources/"+string(sourceType)+"/"+sourceID, http.StatusSeeOther)
}
