package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
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
	// R12：选中的素材选择（两集重点 + 个人笔记）绑定进同一构思意图。
	var selectionIDs []string
	for _, raw := range strings.FieldsFunc(r.FormValue("selection_ids"), func(c rune) bool { return c == '\n' || c == ',' }) {
		if id := strings.TrimSpace(raw); id != "" {
			selectionIDs = append(selectionIDs, id)
		}
	}
	selectionsJSON, err := json.Marshal(selectionIDs)
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	session, err := srv.store.CreateIdeationSession(r.Context(), models.IdeationSession{EditorialProfileID: strings.TrimSpace(r.FormValue("profile_id")), Intent: strings.TrimSpace(r.FormValue("intent")), ConstraintsJSON: strings.TrimSpace(r.FormValue("constraints_json")), SelectionsJSON: string(selectionsJSON)})
	if err != nil {
		writeEditorialError(w, err)
		return
	}
	http.Redirect(w, r, "/workbench/ideation/rounds?session_id="+session.ID, http.StatusSeeOther)
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

// handleIdeationRoundCreate 追加构思轮次（C02）：冻结用户输入、约束与材料快照；
// nonce 幂等（重复提交/刷新只产生一轮）。本端点只持久化真实输入，
// 不假装已产生 AI 诊断（C03 接入实际诊断）。
func (srv *Server) handleIdeationRoundCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	userInput := strings.TrimSpace(r.FormValue("input"))
	nonce := strings.TrimSpace(r.FormValue("nonce"))
	constraints := strings.TrimSpace(r.FormValue("constraints_json"))
	if constraints == "" {
		constraints = "{}"
	}
	if userInput == "" {
		http.Error(w, "补充问题不能为空", http.StatusBadRequest)
		return
	}
	if nonce == "" {
		nonce = fmt.Sprintf("auto:%d", time.Now().UnixNano())
	}
	// 冻结材料快照：优先使用会话绑定的素材选择（R12）；表单直传 material_ids
	// 保持兼容。所选材料的当前内容与版本在此冻结（只读，不付费）。
	var materialSnap []map[string]string
	session, sessErr := srv.store.GetIdeationSession(r.Context(), sessionID)
	if sessErr == nil && session.SelectionsJSON != "" && session.SelectionsJSON != "[]" {
		var selectionIDs []string
		if json.Unmarshal([]byte(session.SelectionsJSON), &selectionIDs) == nil && len(selectionIDs) > 0 {
			snaps, err := srv.store.MaterialSnapshotFromSelections(r.Context(), selectionIDs)
			if err != nil {
				http.Error(w, "冻结素材快照失败："+err.Error(), http.StatusInternalServerError)
				return
			}
			for _, m := range snaps {
				e := map[string]string{"id": m.ID, "kind": m.Kind, "content": m.Content, "citations": m.Citations}
				if m.Error != "" {
					e["error"] = m.Error
				}
				materialSnap = append(materialSnap, e)
			}
		}
	}
	if len(materialSnap) == 0 {
		for _, raw := range strings.FieldsFunc(r.FormValue("material_ids"), func(c rune) bool { return c == '\n' || c == ',' }) {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if kp, err := srv.store.GetKeyPoint(r.Context(), id); err == nil {
				materialSnap = append(materialSnap, map[string]string{
					"id": kp.ID, "content": kp.Content, "citations": kp.CitationsJSON,
				})
			} else {
				materialSnap = append(materialSnap, map[string]string{"id": id, "content": "", "error": "材料不存在"})
			}
		}
	}
	snapJSON, _ := json.Marshal(materialSnap)
	round, created, err := srv.store.AddIdeationRound(r.Context(), sessionID, nonce, userInput, constraints, string(snapJSON))
	if err != nil {
		http.Error(w, "追加构思轮次失败："+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"round_id": round.ID, "round_no": round.RoundNo, "created": created,
	})
}

// handleIdeationRoundsDetail 会话轮次详情（C02：刷新后可继续，历史可回看）。
func (srv *Server) handleIdeationRoundsDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "缺少会话 ID", http.StatusBadRequest)
		return
	}
	session, err := srv.store.GetIdeationSession(r.Context(), sessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rounds, err := srv.store.ListIdeationRounds(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "读取轮次失败", http.StatusInternalServerError)
		return
	}
	// R13：每轮诊断（支持/反驳/缺口/建议主张）随轮次渲染；过期结果只在其原轮次可见。
	diagnoses := map[string]*models.MaterialDiagnosis{}
	claimsByRound := map[string][]provider.ProposedClaimItem{}
	for _, rd := range rounds {
		if rd.OutputDiagnosisID == "" {
			continue
		}
		md, err := srv.store.GetMaterialDiagnosis(r.Context(), rd.OutputDiagnosisID)
		if err != nil {
			continue
		}
		diagnoses[rd.ID] = md
		var d provider.IdeationDiagnosis
		if json.Unmarshal([]byte(md.DiagnosisJSON), &d) == nil {
			claimsByRound[rd.ID] = d.ProposedClaims
		}
	}
	srv.tmpl.Render(w, "ideation_rounds.html", map[string]any{
		"Session":       session,
		"Rounds":        rounds,
		"Diagnoses":     diagnoses,
		"ClaimsByRound": claimsByRound,
		"CSRF":          auth.CSRFValue(r),
	})
}

// handleIdeationDiagnose 为轮次入队诊断任务（C03）：HTTP 只保存意图，
// 实际诊断由后台 worker 执行；同轮重复提交由意图幂等去重。
func (srv *Server) handleIdeationDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	roundID := strings.TrimSpace(r.FormValue("round_id"))
	round, err := srv.store.GetIdeationRoundByID(r.Context(), roundID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if round.SessionID != sessionID {
		http.Error(w, "轮次不属于该会话", http.StatusBadRequest)
		return
	}
	snapshot, _ := json.Marshal(map[string]string{"session_id": sessionID, "round_id": roundID})
	if _, _, err := srv.store.EnqueueJobIdempotent(r.Context(), store.JobIntentSpec{
		SourceType: models.SourceEpisode, SourceID: sessionID, JobType: models.JobIdeationDiagnosis,
		IntentID:          "ideation_diag:" + sessionID + ":" + roundID,
		InputSnapshotJSON: string(snapshot),
	}); err != nil {
		http.Error(w, "入队诊断失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/workbench/ideation/rounds?session_id="+sessionID, http.StatusSeeOther)
}

// handleIdeationClaimPromote R13：Owner 选中诊断候选 → 幂等创建关联轮次的提案
// （proposed_claim，尚不承担 OwnerClaim；重复动作一个提案）。
func (srv *Server) handleIdeationClaimPromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	roundID := strings.TrimSpace(r.FormValue("round_id"))
	claimIndex, err := strconv.Atoi(strings.TrimSpace(r.FormValue("claim_index")))
	if err != nil {
		http.Error(w, "候选序号非法", http.StatusBadRequest)
		return
	}
	proposal, err := srv.store.PromoteDiagnosisClaim(r.Context(), sessionID, roundID, claimIndex)
	if err != nil {
		http.Error(w, "候选提升失败："+err.Error(), http.StatusBadRequest)
		return
	}
	profile, err := srv.store.GetEditorialProfile(r.Context(), proposal.EditorialProfileID)
	if err != nil {
		http.Redirect(w, r, "/workbench/ideation/rounds?session_id="+sessionID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/workbench?profile="+profile.Name, http.StatusSeeOther)
}

// handleProposalDecision C06：候选决策闭环（接受/暂存/拒绝）。
// 接受走 AcceptCreationProposal（已有）；暂存/拒绝经 SaveProposalForLater /
// RejectProposal 幂等处理。全部处理完后批次可完成（释放背压）。
func (srv *Server) handleProposalDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	proposalID := strings.TrimSpace(r.FormValue("proposal_id"))
	decision := strings.TrimSpace(r.FormValue("decision"))
	var err error
	switch decision {
	case "accept":
		claim := strings.TrimSpace(r.FormValue("owner_claim"))
		if claim == "" {
			http.Error(w, "接受主张必须填写 OwnerClaim", http.StatusBadRequest)
			return
		}
		err = srv.store.AcceptCreationProposal(r.Context(), proposalID, claim)
	case "save":
		err = srv.store.SaveProposalForLater(r.Context(), proposalID)
	case "reject":
		err = srv.store.RejectProposal(r.Context(), proposalID, strings.TrimSpace(r.FormValue("feedback")), strings.TrimSpace(r.FormValue("reason")))
	default:
		http.Error(w, "未知决策", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "决策失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench", http.StatusSeeOther)
}
