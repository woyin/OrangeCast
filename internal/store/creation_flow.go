package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// CreateIdeationSession starts a durable Owner-directed exploration.
func (s *Store) CreateIdeationSession(ctx context.Context, session models.IdeationSession) (*models.IdeationSession, error) {
	session.ID = uuid.NewString()
	session.Intent = strings.TrimSpace(session.Intent)
	if session.ConstraintsJSON == "" {
		session.ConstraintsJSON = "{}"
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.Intent == "" || session.Status != "active" {
		return nil, fmt.Errorf("%w: invalid ideation session", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, session.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO ideation_sessions (id,editorial_profile_id,intent,constraints_json,status) VALUES (?,?,?,?,?)`, session.ID, session.EditorialProfileID, session.Intent, session.ConstraintsJSON, session.Status)
	if err != nil {
		return nil, err
	}
	return s.GetIdeationSession(ctx, session.ID)
}

// GetIdeationSession retrieves a durable Owner-directed exploration.
func (s *Store) GetIdeationSession(ctx context.Context, id string) (*models.IdeationSession, error) {
	v := &models.IdeationSession{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,intent,constraints_json,status,created_at,updated_at FROM ideation_sessions WHERE id=?`, id).Scan(&v.ID, &v.EditorialProfileID, &v.Intent, &v.ConstraintsJSON, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ListIdeationSessions returns a profile's directed exploration history.
func (s *Store) ListIdeationSessions(ctx context.Context, profileID string) ([]*models.IdeationSession, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,intent,constraints_json,status,created_at,updated_at FROM ideation_sessions WHERE editorial_profile_id=? ORDER BY updated_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.IdeationSession
	for rows.Next() {
		v := &models.IdeationSession{}
		if err := rows.Scan(&v.ID, &v.EditorialProfileID, &v.Intent, &v.ConstraintsJSON, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateMaterialDiagnosis stores a reproducible material diagnosis for an ideation session.
func (s *Store) CreateMaterialDiagnosis(ctx context.Context, v models.MaterialDiagnosis) (*models.MaterialDiagnosis, error) {
	v.ID = uuid.NewString()
	if v.DiagnosisJSON == "" {
		return nil, fmt.Errorf("%w: empty material diagnosis", ErrInvalidEditorialState)
	}
	if v.MaterialSnapshotJSON == "" {
		v.MaterialSnapshotJSON = "[]"
	}
	if _, err := s.GetIdeationSession(ctx, v.IdeationSessionID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO material_diagnoses (id,ideation_session_id,diagnosis_json,material_snapshot_json) VALUES (?,?,?,?)`, v.ID, v.IdeationSessionID, v.DiagnosisJSON, v.MaterialSnapshotJSON)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM material_diagnoses WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// CreateCreationProposal persists an AI-proposed or Owner-directed claim-led direction.
func (s *Store) CreateCreationProposal(ctx context.Context, v models.CreationProposal) (*models.CreationProposal, error) {
	v.ID = uuid.NewString()
	v.WorkingTitle = strings.TrimSpace(v.WorkingTitle)
	v.ProposedClaim = strings.TrimSpace(v.ProposedClaim)
	if v.Status == "" {
		v.Status = "proposed"
	}
	if v.CreationForm == "" {
		v.CreationForm = "article"
	}
	if v.MaterialIDsJSON == "" {
		v.MaterialIDsJSON = "[]"
	}
	if v.WorkingTitle == "" || v.ProposedClaim == "" || v.Status != "proposed" {
		return nil, fmt.Errorf("%w: invalid creation proposal", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, v.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO creation_proposals (id,editorial_profile_id,proposal_batch_id,ideation_session_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.EditorialProfileID, optionalWorkspaceString(v.ProposalBatchID), optionalWorkspaceString(v.IdeationSessionID), v.Status, v.CreationForm, v.WorkingTitle, v.ProposedClaim, optionalWorkspaceString(v.OwnerClaim), v.Audience, v.Rationale, v.MaterialIDsJSON, v.HistoryRelationship)
	if err != nil {
		return nil, err
	}
	return s.GetCreationProposal(ctx, v.ID)
}

// GetCreationProposal retrieves a proposed or Owner-accepted direction.
func (s *Store) GetCreationProposal(ctx context.Context, id string) (*models.CreationProposal, error) {
	v := &models.CreationProposal{}
	var batch, session, owner sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,editorial_profile_id,proposal_batch_id,ideation_session_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship,created_at,updated_at FROM creation_proposals WHERE id=?`, id).Scan(&v.ID, &v.EditorialProfileID, &batch, &session, &v.Status, &v.CreationForm, &v.WorkingTitle, &v.ProposedClaim, &owner, &v.Audience, &v.Rationale, &v.MaterialIDsJSON, &v.HistoryRelationship, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.ProposalBatchID = batch.String
	v.IdeationSessionID = session.String
	v.OwnerClaim = owner.String
	return v, nil
}

// ListCreationProposals lists all claim-led directions for one profile, newest first.
func (s *Store) ListCreationProposals(ctx context.Context, profileID string) ([]*models.CreationProposal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,editorial_profile_id,proposal_batch_id,ideation_session_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship,created_at,updated_at FROM creation_proposals WHERE editorial_profile_id=? ORDER BY created_at DESC,id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CreationProposal
	for rows.Next() {
		v := &models.CreationProposal{}
		var batch, session, owner sql.NullString
		if err := rows.Scan(&v.ID, &v.EditorialProfileID, &batch, &session, &v.Status, &v.CreationForm, &v.WorkingTitle, &v.ProposedClaim, &owner, &v.Audience, &v.Rationale, &v.MaterialIDsJSON, &v.HistoryRelationship, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.ProposalBatchID, v.IdeationSessionID, v.OwnerClaim = batch.String, session.String, owner.String
		out = append(out, v)
	}
	return out, rows.Err()
}

// AcceptCreationProposal records the moment an AI proposed claim becomes an OwnerClaim.
func (s *Store) AcceptCreationProposal(ctx context.Context, id, ownerClaim string) error {
	ownerClaim = strings.TrimSpace(ownerClaim)
	if ownerClaim == "" {
		return fmt.Errorf("%w: owner claim required", ErrInvalidEditorialState)
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE creation_proposals SET status='accepted',owner_claim=?,updated_at=datetime('now') WHERE id=? AND status='proposed'`, ownerClaim, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// CreateResearchNeed records a blocking or enhancement learning gap.
func (s *Store) CreateResearchNeed(ctx context.Context, v models.ResearchNeed) (*models.ResearchNeed, error) {
	v.ID = uuid.NewString()
	v.Question = strings.TrimSpace(v.Question)
	if v.Status == "" {
		v.Status = "open"
	}
	if (v.Severity != "blocking" && v.Severity != "enhancement") || v.Question == "" || v.Status != "open" {
		return nil, fmt.Errorf("%w: invalid research need", ErrInvalidEditorialState)
	}
	if _, err := s.GetCreationProposal(ctx, v.CreationProposalID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO research_needs (id,creation_proposal_id,severity,question,status) VALUES (?,?,?,?,?)`, v.ID, v.CreationProposalID, v.Severity, v.Question, v.Status)
	if err != nil {
		return nil, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM research_needs WHERE id=?`, v.ID).Scan(&v.CreatedAt)
	return &v, err
}

// GetResearchNeed retrieves one research gap by stable identifier.
func (s *Store) GetResearchNeed(ctx context.Context, id string) (*models.ResearchNeed, error) {
	v := &models.ResearchNeed{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,severity,question,status,COALESCE(resolution_source_id,''),created_at,COALESCE(resolved_at,'') FROM research_needs WHERE id=?`, id).Scan(&v.ID, &v.CreationProposalID, &v.Severity, &v.Question, &v.Status, &v.ResolutionSourceID, &v.CreatedAt, &v.ResolvedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return v, err
}

// ListResearchNeeds returns unresolved and resolved research gaps for a profile.
func (s *Store) ListResearchNeeds(ctx context.Context, profileID string) ([]*models.ResearchNeed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,r.creation_proposal_id,r.severity,r.question,r.status,COALESCE(r.resolution_source_id,''),r.created_at,COALESCE(r.resolved_at,'') FROM research_needs r JOIN creation_proposals p ON p.id=r.creation_proposal_id WHERE p.editorial_profile_id=? ORDER BY CASE r.status WHEN 'open' THEN 0 ELSE 1 END,r.created_at DESC,r.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ResearchNeed
	for rows.Next() {
		v := &models.ResearchNeed{}
		if err := rows.Scan(&v.ID, &v.CreationProposalID, &v.Severity, &v.Question, &v.Status, &v.ResolutionSourceID, &v.CreatedAt, &v.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ResolveResearchNeed records the source that supplied the missing learning.
// Research execution itself remains out of scope for V1; an Owner must first
// create a ResearchPlan and later add the resulting Source to this workspace.
func (s *Store) ResolveResearchNeed(ctx context.Context, id, sourceID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return fmt.Errorf("%w: resolution source required", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE research_needs SET status='resolved',resolution_source_id=?,resolved_at=datetime('now') WHERE id=? AND status='open'`, sourceID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// CreateResearchPlan records a future, Owner-reviewable research contract and
// intentionally does not perform external research.
func (s *Store) CreateResearchPlan(ctx context.Context, plan models.ResearchPlan) (*models.ResearchPlan, error) {
	plan.ID, plan.Question, plan.Scope = uuid.NewString(), strings.TrimSpace(plan.Question), strings.TrimSpace(plan.Scope)
	if plan.Status == "" {
		plan.Status = "draft"
	}
	if plan.Question == "" || plan.Status != "draft" || plan.BudgetCents != nil && *plan.BudgetCents < 0 {
		return nil, fmt.Errorf("%w: invalid research plan", ErrInvalidEditorialState)
	}
	var status string
	if err := s.DB.QueryRowContext(ctx, `SELECT status FROM research_needs WHERE id=?`, plan.ResearchNeedID).Scan(&status); err == sql.ErrNoRows {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "open" {
		return nil, fmt.Errorf("%w: research need is not open", ErrInvalidEditorialState)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO research_plans (id,research_need_id,question,scope,budget_cents,status) VALUES (?,?,?,?,?,?)`, plan.ID, plan.ResearchNeedID, plan.Question, plan.Scope, plan.BudgetCents, plan.Status); err != nil {
		return nil, err
	}
	return s.GetResearchPlan(ctx, plan.ID)
}

// GetResearchPlan retrieves one Owner-authorized research contract.
func (s *Store) GetResearchPlan(ctx context.Context, id string) (*models.ResearchPlan, error) {
	plan := &models.ResearchPlan{}
	var budget sql.NullInt64
	var confirmed sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,research_need_id,question,scope,budget_cents,status,owner_confirmed_at,created_at FROM research_plans WHERE id=?`, id).Scan(&plan.ID, &plan.ResearchNeedID, &plan.Question, &plan.Scope, &budget, &plan.Status, &confirmed, &plan.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if budget.Valid {
		value := budget.Int64
		plan.BudgetCents = &value
	}
	if confirmed.Valid {
		value := confirmed.String
		plan.OwnerConfirmedAt = &value
	}
	return plan, nil
}

// ConfirmResearchPlan records the Owner's explicit authorization to perform future research.
func (s *Store) ConfirmResearchPlan(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE research_plans SET status='confirmed',owner_confirmed_at=datetime('now') WHERE id=? AND status='draft'`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}

// HasBlockingResearchNeed reports whether a proposal remains blocked from confirmation.
func (s *Store) HasBlockingResearchNeed(ctx context.Context, proposalID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM research_needs WHERE creation_proposal_id=? AND severity='blocking' AND status!='resolved'`, proposalID).Scan(&n)
	return n > 0, err
}

// CreateCreationBrief creates a reviewable contract only after blocking needs are resolved.
func (s *Store) CreateCreationBrief(ctx context.Context, v models.CreationBrief) (*models.CreationBrief, error) {
	v.ID = uuid.NewString()
	if v.Status == "" {
		v.Status = "draft"
	}
	if v.ClaimPlanJSON == "" {
		v.ClaimPlanJSON = "[]"
	}
	if v.MaterialPlanJSON == "" {
		v.MaterialPlanJSON = "[]"
	}
	if v.ResearchNeedIDsJSON == "" {
		v.ResearchNeedIDsJSON = "[]"
	}
	proposal, err := s.GetCreationProposal(ctx, v.CreationProposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != "accepted" || v.OwnerClaim == "" {
		return nil, fmt.Errorf("%w: accepted owner claim required", ErrInvalidEditorialState)
	}
	blocked, err := s.HasBlockingResearchNeed(ctx, v.CreationProposalID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("%w: blocking research need unresolved", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO creation_briefs (id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length) VALUES (?,?,?,?,?,?,?,?,?,?)`, v.ID, v.CreationProposalID, v.Status, v.OwnerClaim, v.ClaimPlanJSON, v.MaterialPlanJSON, v.ResearchNeedIDsJSON, v.Outline, v.Style, v.TargetLength)
	if err != nil {
		return nil, err
	}
	return s.GetCreationBrief(ctx, v.ID)
}

// GetCreationBrief retrieves one Owner-reviewable creation contract.
func (s *Store) GetCreationBrief(ctx context.Context, id string) (*models.CreationBrief, error) {
	v := &models.CreationBrief{}
	var confirmed sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,status,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,confirmed_at,created_at,updated_at FROM creation_briefs WHERE id=?`, id).Scan(&v.ID, &v.CreationProposalID, &v.Status, &v.OwnerClaim, &v.ClaimPlanJSON, &v.MaterialPlanJSON, &v.ResearchNeedIDsJSON, &v.Outline, &v.Style, &v.TargetLength, &confirmed, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if confirmed.Valid {
		value := confirmed.String
		v.ConfirmedAt = &value
	}
	return v, err
}

// ListCreationBriefs lists a profile's current and historical creation contracts.
func (s *Store) ListCreationBriefs(ctx context.Context, profileID string) ([]*models.CreationBrief, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id,b.creation_proposal_id,b.status,b.owner_claim,b.claim_plan_json,b.material_plan_json,b.research_need_ids_json,b.outline,b.style,b.target_length,b.confirmed_at,b.created_at,b.updated_at FROM creation_briefs b JOIN creation_proposals p ON p.id=b.creation_proposal_id WHERE p.editorial_profile_id=? ORDER BY b.updated_at DESC,b.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CreationBrief
	for rows.Next() {
		v := &models.CreationBrief{}
		var confirmed sql.NullString
		if err := rows.Scan(&v.ID, &v.CreationProposalID, &v.Status, &v.OwnerClaim, &v.ClaimPlanJSON, &v.MaterialPlanJSON, &v.ResearchNeedIDsJSON, &v.Outline, &v.Style, &v.TargetLength, &confirmed, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if confirmed.Valid {
			value := confirmed.String
			v.ConfirmedAt = &value
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateCreationBriefDraftFromProposal creates the minimum reviewable contract
// immediately after an Owner accepts a sufficiently evidenced direction.
func (s *Store) CreateCreationBriefDraftFromProposal(ctx context.Context, proposalID string) (*models.CreationBrief, error) {
	proposal, err := s.GetCreationProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != "accepted" || proposal.OwnerClaim == "" {
		return nil, fmt.Errorf("%w: accepted owner claim required", ErrInvalidEditorialState)
	}
	var materialIDs []string
	if err := json.Unmarshal([]byte(proposal.MaterialIDsJSON), &materialIDs); err != nil || len(materialIDs) == 0 {
		return nil, fmt.Errorf("%w: accepted proposal needs material", ErrInvalidEditorialState)
	}
	var existing string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM creation_briefs WHERE creation_proposal_id=? AND status IN ('draft','confirmed') ORDER BY created_at DESC LIMIT 1`, proposalID).Scan(&existing)
	if err == nil {
		return s.GetCreationBrief(ctx, existing)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	claimPlan, _ := json.Marshal([]map[string]string{{"kind": string(models.ClaimOwner), "claim": proposal.OwnerClaim}})
	return s.CreateCreationBrief(ctx, models.CreationBrief{CreationProposalID: proposalID, OwnerClaim: proposal.OwnerClaim, ClaimPlanJSON: string(claimPlan), MaterialPlanJSON: proposal.MaterialIDsJSON})
}

// ConfirmCreationBrief records the explicit work-generation authorization.
func (s *Store) ConfirmCreationBrief(ctx context.Context, id string) error {
	var proposalID string
	if err := s.DB.QueryRowContext(ctx, `SELECT creation_proposal_id FROM creation_briefs WHERE id=?`, id).Scan(&proposalID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	blocked, err := s.HasBlockingResearchNeed(ctx, proposalID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("%w: blocking research need unresolved", ErrInvalidEditorialState)
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE creation_briefs SET status='confirmed',confirmed_at=datetime('now'),updated_at=datetime('now') WHERE id=? AND status='draft'`, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrInvalidEditorialState
	}
	return nil
}
