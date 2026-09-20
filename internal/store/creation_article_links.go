package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

type CreationArticleLink struct {
	ID                   string
	CreationProposalID   string
	CreationBriefID      string
	ArticleProposalID    string
	ArticleBriefID       string
	ContractVersion      string
	CreationBriefVersion int
	CreatedAt            string
}

// EnsureCreationArticleLink bridges the current confirmed Brief revision using the legacy wrapper.
func (s *Store) EnsureCreationArticleLink(ctx context.Context, proposalID, briefID, contractVersion string) (*CreationArticleLink, error) {
	b, err := s.GetCreationBrief(ctx, briefID)
	if err != nil {
		return nil, err
	}
	if b.Status != "confirmed" || b.CurrentVersion == 0 || b.CurrentVersion != b.ConfirmedVersion {
		return nil, fmt.Errorf("%w: brief is not exactly confirmed", ErrInvalidEditorialState)
	}
	return s.EnsureCreationArticleLinkExact(ctx, proposalID, briefID, b.ConfirmedVersion, contractVersion)
}

// EnsureCreationArticleLinkExact bridges one exact immutable Brief revision transactionally.
func (s *Store) EnsureCreationArticleLinkExact(ctx context.Context, proposalID, briefID string, version int, contractVersion string) (*CreationArticleLink, error) {
	if contractVersion == "" {
		contractVersion = "v2"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	link, err := s.ensureCreationArticleLinkExactTx(ctx, tx, proposalID, briefID, version, contractVersion)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return link, nil
}
func (s *Store) ensureCreationArticleLinkExactTx(ctx context.Context, tx *sql.Tx, proposalID, briefID string, version int, contractVersion string) (*CreationArticleLink, error) {
	p, r, err := loadExactBridgeTx(ctx, tx, proposalID, briefID, version)
	if err != nil {
		return nil, err
	}
	sel, _, ok := parseMaterialPlan(r.MaterialPlanJSON)
	if !ok || len(sel) == 0 {
		return nil, fmt.Errorf("%w: selected materials required", ErrInvalidEditorialState)
	}
	data, _ := json.Marshal(sel)
	l := &CreationArticleLink{}
	err = tx.QueryRowContext(ctx, `SELECT id,creation_proposal_id,creation_brief_id,article_proposal_id,article_brief_id,contract_version,creation_brief_version,created_at FROM creation_article_links WHERE creation_proposal_id=?`, proposalID).Scan(&l.ID, &l.CreationProposalID, &l.CreationBriefID, &l.ArticleProposalID, &l.ArticleBriefID, &l.ContractVersion, &l.CreationBriefVersion, &l.CreatedAt)
	if err == nil {
		if l.CreationBriefID == briefID && l.CreationBriefVersion == version {
			return l, nil
		}
		if err := updateCompatibilityTx(ctx, tx, l.ArticleProposalID, l.ArticleBriefID, p, r, data); err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE creation_article_links SET creation_brief_id=?,creation_brief_version=?,contract_version=? WHERE id=?`, briefID, version, contractVersion, l.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil, ErrInvalidEditorialState
		}
		l.CreationBriefID, l.CreationBriefVersion, l.ContractVersion = briefID, version, contractVersion
		return l, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	apID, abID, linkID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	res, err := tx.ExecContext(ctx, `INSERT INTO article_proposals (id,editorial_profile_id,kind,status,title,thesis,audience,rationale,candidate_keypoints_json,provider,model,prompt_version,cost_cents) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, apID, p.EditorialProfileID, "deep_read", "accepted", p.WorkingTitle, r.OwnerClaim, p.Audience, p.Rationale, string(data), "", "", contractVersion, nil)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrInvalidEditorialState
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO article_briefs (id,proposal_id,status,thesis,audience,outline_markdown,material_plan_json,conflict_plan_json,style,target_length,confirmed_at) VALUES (?,?,?,?,?,?,?,?,?,?,datetime('now'))`, abID, apID, "confirmed", r.OwnerClaim, p.Audience, r.Outline, string(data), `[]`, r.Style, r.TargetLength)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrInvalidEditorialState
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO creation_article_links (id,creation_proposal_id,creation_brief_id,article_proposal_id,article_brief_id,contract_version,creation_brief_version) VALUES (?,?,?,?,?,?,?)`, linkID, proposalID, briefID, apID, abID, contractVersion, version)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrInvalidEditorialState
	}
	return &CreationArticleLink{ID: linkID, CreationProposalID: proposalID, CreationBriefID: briefID, ArticleProposalID: apID, ArticleBriefID: abID, ContractVersion: contractVersion, CreationBriefVersion: version}, nil
}

// ConfirmCreationBriefVersionAndEnsureLink confirms R17 eligibility and bridges compatibility atomically.
func (s *Store) ConfirmCreationBriefVersionAndEnsureLink(ctx context.Context, briefID string, version int, contractVersion string) (*CreationArticleLink, error) {
	if contractVersion == "" {
		contractVersion = "v2"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	pid, err := s.confirmCreationBriefVersionTx(ctx, tx, briefID, version)
	if err != nil {
		return nil, err
	}
	link, err := s.ensureCreationArticleLinkExactTx(ctx, tx, pid, briefID, version, contractVersion)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return link, nil
}

// loadExactBridgeTx validates identity, Proposal accepted status, and the exact
// confirmed Brief revision. Legacy wrappers may tolerate an empty Outline; the
// confirmation transaction performs the stricter R17 outline check before calling it.
func loadExactBridgeTx(ctx context.Context, tx *sql.Tx, proposalID, briefID string, version int) (*models.CreationProposal, *models.CreationBriefRevision, error) {
	if strings.TrimSpace(proposalID) == "" || strings.TrimSpace(briefID) == "" || version <= 0 {
		return nil, nil, fmt.Errorf("%w: bridge IDs/version required", ErrInvalidEditorialState)
	}
	p := &models.CreationProposal{}
	var batch, session, round, owner sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT id,editorial_profile_id,proposal_batch_id,ideation_session_id,ideation_round_id,status,creation_form,working_title,proposed_claim,owner_claim,audience,rationale,material_ids_json,history_relationship,COALESCE(decision_note,''),created_at,updated_at FROM creation_proposals WHERE id=?`, proposalID).Scan(&p.ID, &p.EditorialProfileID, &batch, &session, &round, &p.Status, &p.CreationForm, &p.WorkingTitle, &p.ProposedClaim, &owner, &p.Audience, &p.Rationale, &p.MaterialIDsJSON, &p.HistoryRelationship, &p.DecisionNote, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, nil, err
	}
	p.OwnerClaim = strings.TrimSpace(owner.String)
	var briefProposalID, briefStatus string
	var currentVersion, confirmedVersion int
	if err := tx.QueryRowContext(ctx, `SELECT creation_proposal_id,current_version,confirmed_version,status FROM creation_briefs WHERE id=?`, briefID).Scan(&briefProposalID, &currentVersion, &confirmedVersion, &briefStatus); err != nil {
		return nil, nil, err
	}
	if p.Status != "accepted" || briefProposalID != proposalID || currentVersion != version || confirmedVersion != version || briefStatus != "confirmed" {
		return nil, nil, fmt.Errorf("%w: exact confirmed prerequisites failed", ErrInvalidEditorialState)
	}
	rv := &models.CreationBriefRevision{}
	if err := tx.QueryRowContext(ctx, `SELECT id,brief_id,version,origin_job_id,owner_claim,claim_plan_json,material_plan_json,research_need_ids_json,outline,style,target_length,claim_type,unresolved_questions_json,notes,curator_prompt_version,curator_input_snapshot_json,created_at FROM creation_brief_revisions WHERE brief_id=? AND version=?`, briefID, version).Scan(&rv.ID, &rv.BriefID, &rv.Version, &rv.OriginJobID, &rv.OwnerClaim, &rv.ClaimPlanJSON, &rv.MaterialPlanJSON, &rv.ResearchNeedIDsJSON, &rv.Outline, &rv.Style, &rv.TargetLength, &rv.ClaimType, &rv.UnresolvedQuestionsJSON, &rv.Notes, &rv.CuratorPromptVersion, &rv.CuratorInputSnapshotJSON, &rv.CreatedAt); err != nil {
		return nil, nil, err
	}
	if rv.OwnerClaim == "" {
		return nil, nil, fmt.Errorf("%w: revision incomplete", ErrInvalidEditorialState)
	}
	return p, rv, nil
}

func updateCompatibilityTx(ctx context.Context, tx *sql.Tx, ap, ab string, p *models.CreationProposal, r *models.CreationBriefRevision, data []byte) error {
	res, err := tx.ExecContext(ctx, `UPDATE article_proposals SET title=?,thesis=?,audience=?,rationale=?,candidate_keypoints_json=?,status='accepted' WHERE id=?`, p.WorkingTitle, r.OwnerClaim, p.Audience, p.Rationale, string(data), ap)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInvalidEditorialState
	}
	res, err = tx.ExecContext(ctx, `UPDATE article_briefs SET status='confirmed',thesis=?,audience=?,outline_markdown=?,material_plan_json=?,style=?,target_length=?,confirmed_at=datetime('now'),updated_at=datetime('now') WHERE id=?`, r.OwnerClaim, p.Audience, r.Outline, string(data), r.Style, r.TargetLength, ab)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInvalidEditorialState
	}
	return nil
}

// GetCreationArticleLinkByCreationProposal reads a link by CreationProposal.
func (s *Store) GetCreationArticleLinkByCreationProposal(ctx context.Context, id string) (*CreationArticleLink, error) {
	return s.getCreationLink(ctx, `creation_proposal_id=?`, id)
}

// GetCreationArticleLinkByArticleProposal reads a link by legacy ArticleProposal.
func (s *Store) GetCreationArticleLinkByArticleProposal(ctx context.Context, id string) (*CreationArticleLink, error) {
	return s.getCreationLink(ctx, `article_proposal_id=?`, id)
}

// GetCreationArticleLinkByArticleBrief reads a link by legacy ArticleBrief.
func (s *Store) GetCreationArticleLinkByArticleBrief(ctx context.Context, id string) (*CreationArticleLink, error) {
	return s.getCreationLink(ctx, `article_brief_id=?`, id)
}
func (s *Store) getCreationLink(ctx context.Context, w, a string) (*CreationArticleLink, error) {
	return scanCreationArticleLink(s.DB.QueryRowContext(ctx, `SELECT id,creation_proposal_id,creation_brief_id,article_proposal_id,article_brief_id,contract_version,creation_brief_version,created_at FROM creation_article_links WHERE `+w, a))
}
func scanCreationArticleLink(row interface{ Scan(...any) error }) (*CreationArticleLink, error) {
	l := &CreationArticleLink{}
	err := row.Scan(&l.ID, &l.CreationProposalID, &l.CreationBriefID, &l.ArticleProposalID, &l.ArticleBriefID, &l.ContractVersion, &l.CreationBriefVersion, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}
