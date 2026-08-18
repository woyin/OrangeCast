package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
)

// handleDiscoverySettings records the one explicit, profile-scoped authorization
// required before the background scheduler may call a paid Scout provider.
func (srv *Server) handleDiscoverySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.FormValue("profile_id"))
	dailyLimit, dailyErr := strconv.Atoi(strings.TrimSpace(r.FormValue("daily_limit")))
	debounce, debounceErr := strconv.Atoi(strings.TrimSpace(r.FormValue("debounce_minutes")))
	if dailyErr != nil || debounceErr != nil {
		http.Error(w, "发现频率必须是整数", http.StatusBadRequest)
		return
	}
	var budget *int64
	if raw := strings.TrimSpace(r.FormValue("batch_budget_cents")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			http.Error(w, "单批预算必须是非负整数", http.StatusBadRequest)
			return
		}
		budget = &value
	}
	settings := models.DiscoverySettings{EditorialProfileID: profileID, Enabled: r.FormValue("enabled") == "on", Provider: strings.TrimSpace(r.FormValue("provider")), Model: strings.TrimSpace(r.FormValue("model")), DailyLimit: dailyLimit, DebounceMinutes: debounce, BatchBudgetCents: budget}
	if err := srv.store.SetDiscoverySettings(r.Context(), settings); err != nil {
		http.Error(w, "保存自动发现设置失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profileID, http.StatusSeeOther)
}

// handleCreationProposalAccept is the boundary where a model-suggested claim
// becomes the Owner's own claim. It then creates the reviewable brief draft
// when the accepted direction already has sufficient material.
func (srv *Server) handleCreationProposalAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	proposalID := strings.TrimSpace(r.FormValue("creation_proposal_id"))
	proposal, err := srv.store.GetCreationProposal(r.Context(), proposalID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	ownerClaim := strings.TrimSpace(r.FormValue("owner_claim"))
	if ownerClaim == "" {
		ownerClaim = proposal.ProposedClaim
	}
	if err := srv.store.AcceptCreationProposal(r.Context(), proposal.ID, ownerClaim); err != nil {
		writeEditorialError(w, err)
		return
	}
	if _, err := srv.store.CreateCreationBriefDraftFromProposal(r.Context(), proposal.ID); err != nil && !strings.Contains(err.Error(), "needs material") && !strings.Contains(err.Error(), "blocking") {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+proposal.EditorialProfileID, http.StatusSeeOther)
}

func (srv *Server) handleCreationHistoryCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	work, err := srv.store.CreateCreationHistory(r.Context(), models.CreationHistory{EditorialProfileID: strings.TrimSpace(r.FormValue("profile_id")), Status: strings.TrimSpace(r.FormValue("status")), CreationForm: strings.TrimSpace(r.FormValue("creation_form")), Title: strings.TrimSpace(r.FormValue("title")), CoreClaim: strings.TrimSpace(r.FormValue("core_claim")), Audience: strings.TrimSpace(r.FormValue("audience")), Content: strings.TrimSpace(r.FormValue("content")), SourceURL: strings.TrimSpace(r.FormValue("source_url"))})
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+work.EditorialProfileID, http.StatusSeeOther)
}

func (srv *Server) handleIdeationSessionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	session, err := srv.store.CreateIdeationSession(r.Context(), models.IdeationSession{EditorialProfileID: strings.TrimSpace(r.FormValue("profile_id")), Intent: strings.TrimSpace(r.FormValue("intent")), ConstraintsJSON: strings.TrimSpace(r.FormValue("constraints_json"))})
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+session.EditorialProfileID, http.StatusSeeOther)
}

func (srv *Server) handleResearchNeedCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	need, err := srv.store.CreateResearchNeed(r.Context(), models.ResearchNeed{CreationProposalID: strings.TrimSpace(r.FormValue("creation_proposal_id")), Severity: strings.TrimSpace(r.FormValue("severity")), Question: strings.TrimSpace(r.FormValue("question"))})
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	proposal, err := srv.store.GetCreationProposal(r.Context(), need.CreationProposalID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+proposal.EditorialProfileID, http.StatusSeeOther)
}

func (srv *Server) handleResearchNeedResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	need, err := srv.store.GetResearchNeed(r.Context(), strings.TrimSpace(r.FormValue("research_need_id")))
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	proposal, err := srv.store.GetCreationProposal(r.Context(), need.CreationProposalID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	if err := srv.store.ResolveResearchNeed(r.Context(), need.ID, strings.TrimSpace(r.FormValue("resolution_source_id"))); err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+proposal.EditorialProfileID, http.StatusSeeOther)
}

func (srv *Server) handleCreationBriefConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	briefID := strings.TrimSpace(r.FormValue("creation_brief_id"))
	brief, err := srv.store.GetCreationBrief(r.Context(), briefID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	proposal, err := srv.store.GetCreationProposal(r.Context(), brief.CreationProposalID)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	if err := srv.store.ConfirmCreationBrief(r.Context(), brief.ID); err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+proposal.EditorialProfileID, http.StatusSeeOther)
}
