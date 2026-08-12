// Content production persistence (ADR-0021 / roadmap Phase 8).
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

// ErrInvalidEditorialState indicates an invalid content production transition or input.
var ErrInvalidEditorialState = errors.New("invalid editorial state")

// CreateEditorialProfile creates a long-lived content brand profile.
func (s *Store) CreateEditorialProfile(ctx context.Context, profile models.EditorialProfile) (*models.EditorialProfile, error) {
	profile.ID = uuid.NewString()
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return nil, fmt.Errorf("%w: profile name is required", ErrInvalidEditorialState)
	}
	if profile.SourceAttribution == "" {
		profile.SourceAttribution = "standard"
	}
	if !validAttribution(profile.SourceAttribution) {
		return nil, fmt.Errorf("%w: unknown source attribution %q", ErrInvalidEditorialState, profile.SourceAttribution)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO editorial_profiles (id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		profile.ID, profile.Name, profile.TargetAudience, profile.Voice, profile.StyleGuide, profile.SourceAttribution, profile.MonthlyBudgetCents)
	if err != nil {
		return nil, fmt.Errorf("create editorial profile: %w", err)
	}
	return s.GetEditorialProfile(ctx, profile.ID)
}

// GetEditorialProfile returns an editorial profile by stable ID.
func (s *Store) GetEditorialProfile(ctx context.Context, id string) (*models.EditorialProfile, error) {
	p := &models.EditorialProfile{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents, created_at, updated_at
		 FROM editorial_profiles WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.TargetAudience, &p.Voice, &p.StyleGuide, &p.SourceAttribution, &p.MonthlyBudgetCents, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListEditorialProfiles returns profiles in stable display order.
func (s *Store) ListEditorialProfiles(ctx context.Context) ([]*models.EditorialProfile, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents, created_at, updated_at
		 FROM editorial_profiles ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EditorialProfile
	for rows.Next() {
		p := &models.EditorialProfile{}
		if err := rows.Scan(&p.ID, &p.Name, &p.TargetAudience, &p.Voice, &p.StyleGuide, &p.SourceAttribution, &p.MonthlyBudgetCents, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GrantSourceScope explicitly permits one Source to be used by an editorial profile.
func (s *Store) GrantSourceScope(ctx context.Context, profileID string, sourceType models.SourceType, sourceID string) error {
	if !validSourceType(sourceType) || strings.TrimSpace(sourceID) == "" {
		return fmt.Errorf("%w: invalid source scope", ErrInvalidEditorialState)
	}
	exists, err := s.sourceExists(ctx, sourceType, sourceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO editorial_source_scopes (editorial_profile_id, source_type, source_id) VALUES (?, ?, ?)`,
		profileID, string(sourceType), sourceID)
	return err
}

// CanUseSourceForPublication checks both explicit SourceScope and the source's publication state.
// Archived, internal-only, and disabled Sources cannot enter a new public ArticleBrief.
func (s *Store) CanUseSourceForPublication(ctx context.Context, profileID string, sourceType models.SourceType, sourceID string) (bool, error) {
	inScope, err := s.IsSourceInScope(ctx, profileID, sourceType, sourceID)
	if err != nil || !inScope {
		return inScope, err
	}
	if !validSourceType(sourceType) {
		return false, fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	var productionUse string
	var archivedAt *string
	err = s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT production_use, archived_at FROM %s WHERE id=?`, sourceTable(sourceType)), sourceID).Scan(&productionUse, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return productionUse == "public" && archivedAt == nil, nil
}

// CanSendSourceToExternalProvider checks the model data policy before task enqueueing.
// ApprovedProvidersOnly needs a provider allowlist introduced with role routing, so it is
// deliberately rejected here rather than silently treated as external_allowed.
func (s *Store) CanSendSourceToExternalProvider(ctx context.Context, sourceType models.SourceType, sourceID string) (bool, error) {
	if !validSourceType(sourceType) {
		return false, fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	var policy models.ModelDataPolicy
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT model_data_policy FROM %s WHERE id=?`, sourceTable(sourceType)), sourceID).Scan(&policy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return policy == models.ModelDataExternalAllowed, nil
}

// RevokeSourceScope removes a profile's authorization to use one Source in future production.
func (s *Store) RevokeSourceScope(ctx context.Context, profileID string, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM editorial_source_scopes WHERE editorial_profile_id=? AND source_type=? AND source_id=?`,
		profileID, string(sourceType), sourceID)
	return err
}

// IsSourceInScope reports whether a profile has explicitly authorized one Source.
func (s *Store) IsSourceInScope(ctx context.Context, profileID string, sourceType models.SourceType, sourceID string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM editorial_source_scopes WHERE editorial_profile_id=? AND source_type=? AND source_id=?`,
		profileID, string(sourceType), sourceID).Scan(&count)
	return count > 0, err
}

// ListScopedSources returns the explicit SourceScope entries for an editorial profile.
func (s *Store) ListScopedSources(ctx context.Context, profileID string) ([]SourceScopeEntry, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT source_type, source_id, created_at FROM editorial_source_scopes WHERE editorial_profile_id=? ORDER BY created_at DESC, source_id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceScopeEntry
	for rows.Next() {
		entry := SourceScopeEntry{EditorialProfileID: profileID}
		if err := rows.Scan(&entry.SourceType, &entry.SourceID, &entry.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// SourceScopeEntry is one explicit source authorization for a profile.
type SourceScopeEntry struct {
	EditorialProfileID string
	SourceType         models.SourceType
	SourceID           string
	CreatedAt          string
}

// SetSourceProductionPolicy updates the source's public-use and model-data restrictions.
func (s *Store) SetSourceProductionPolicy(ctx context.Context, sourceType models.SourceType, sourceID, productionUse string, dataPolicy models.ModelDataPolicy) error {
	if !validSourceType(sourceType) || !validProductionUse(productionUse) || !validModelDataPolicy(dataPolicy) {
		return fmt.Errorf("%w: invalid source production policy", ErrInvalidEditorialState)
	}
	table := sourceTable(sourceType)
	result, err := s.DB.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET production_use=?, model_data_policy=? WHERE id=?`, table), productionUse, string(dataPolicy), sourceID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ArchiveSource stops a Source from being used in future production without deleting evidence.
func (s *Store) ArchiveSource(ctx context.Context, sourceType models.SourceType, sourceID string, archived bool) error {
	if !validSourceType(sourceType) {
		return fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	table := sourceTable(sourceType)
	value := "NULL"
	if archived {
		value = "datetime('now')"
	}
	result, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET archived_at=%s WHERE id=?`, table, value), sourceID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateArticleProposal records a Scout candidate. It cannot be created for a missing profile.
func (s *Store) CreateArticleProposal(ctx context.Context, proposal models.ArticleProposal) (*models.ArticleProposal, error) {
	proposal.ID = uuid.NewString()
	proposal.Kind = defaultString(proposal.Kind, "fresh")
	proposal.Status = defaultString(proposal.Status, "proposed")
	proposal.Title = strings.TrimSpace(proposal.Title)
	if proposal.Title == "" || !validProposalKind(proposal.Kind) || !validProposalStatus(proposal.Status) || !validJSON(proposal.CandidateKeyPoints, "[]") {
		return nil, fmt.Errorf("%w: invalid article proposal", ErrInvalidEditorialState)
	}
	proposal.CandidateKeyPoints = defaultString(proposal.CandidateKeyPoints, "[]")
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO article_proposals (id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.EditorialProfileID, proposal.Kind, proposal.Status, proposal.Title, proposal.Thesis, proposal.Audience, proposal.Rationale, proposal.CandidateKeyPoints)
	if err != nil {
		return nil, fmt.Errorf("create article proposal: %w", err)
	}
	return s.GetArticleProposal(ctx, proposal.ID)
}

// GetArticleProposal reads one proposal.
func (s *Store) GetArticleProposal(ctx context.Context, id string) (*models.ArticleProposal, error) {
	p := &models.ArticleProposal{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json, created_at, updated_at
		 FROM article_proposals WHERE id=?`, id).
		Scan(&p.ID, &p.EditorialProfileID, &p.Kind, &p.Status, &p.Title, &p.Thesis, &p.Audience, &p.Rationale, &p.CandidateKeyPoints, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListArticleProposals returns recent proposals for one editorial profile.
func (s *Store) ListArticleProposals(ctx context.Context, profileID string) ([]*models.ArticleProposal, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json, created_at, updated_at
		 FROM article_proposals WHERE editorial_profile_id=? ORDER BY created_at DESC, id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleProposal
	for rows.Next() {
		p := &models.ArticleProposal{}
		if err := rows.Scan(&p.ID, &p.EditorialProfileID, &p.Kind, &p.Status, &p.Title, &p.Thesis, &p.Audience, &p.Rationale, &p.CandidateKeyPoints, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListArticleBriefs returns briefs belonging to proposals for one editorial profile.
func (s *Store) ListArticleBriefs(ctx context.Context, profileID string) ([]*models.ArticleBrief, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT b.id, b.proposal_id, b.status, b.thesis, b.audience, b.outline_markdown, b.material_plan_json, b.conflict_plan_json, b.style, b.target_length, b.confirmed_at, b.created_at, b.updated_at
		 FROM article_briefs b JOIN article_proposals p ON p.id=b.proposal_id
		 WHERE p.editorial_profile_id=? ORDER BY b.updated_at DESC, b.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleBrief
	for rows.Next() {
		brief := &models.ArticleBrief{}
		if err := rows.Scan(&brief.ID, &brief.ProposalID, &brief.Status, &brief.Thesis, &brief.Audience, &brief.Outline, &brief.MaterialPlan, &brief.ConflictPlan, &brief.Style, &brief.TargetLength, &brief.ConfirmedAt, &brief.CreatedAt, &brief.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, brief)
	}
	return out, rows.Err()
}

// SetArticleProposalStatus records an explicit Owner decision about a proposal.
func (s *Store) SetArticleProposalStatus(ctx context.Context, proposalID, status string) error {
	if !validProposalStatus(status) {
		return fmt.Errorf("%w: invalid proposal status", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx,
		`UPDATE article_proposals SET status=?, updated_at=datetime('now') WHERE id=?`, status, proposalID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateArticleBrief records a Curator brief. Only an accepted proposal may create a brief.
func (s *Store) CreateArticleBrief(ctx context.Context, brief models.ArticleBrief) (*models.ArticleBrief, error) {
	proposal, err := s.GetArticleProposal(ctx, brief.ProposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != "accepted" {
		return nil, fmt.Errorf("%w: proposal must be accepted before a brief", ErrInvalidEditorialState)
	}
	brief.ID = uuid.NewString()
	brief.Status = defaultString(brief.Status, "draft")
	brief.MaterialPlan = defaultString(brief.MaterialPlan, "[]")
	brief.ConflictPlan = defaultString(brief.ConflictPlan, "[]")
	if !validBriefStatus(brief.Status) || !validJSON(brief.MaterialPlan, "[]") || !validJSON(brief.ConflictPlan, "[]") {
		return nil, fmt.Errorf("%w: invalid article brief", ErrInvalidEditorialState)
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO article_briefs (id, proposal_id, status, thesis, audience, outline_markdown, material_plan_json, conflict_plan_json, style, target_length)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		brief.ID, brief.ProposalID, brief.Status, brief.Thesis, brief.Audience, brief.Outline, brief.MaterialPlan, brief.ConflictPlan, brief.Style, brief.TargetLength)
	if err != nil {
		return nil, fmt.Errorf("create article brief: %w", err)
	}
	return s.GetArticleBrief(ctx, brief.ID)
}

// GetArticleBrief retrieves a brief by ID.
func (s *Store) GetArticleBrief(ctx context.Context, id string) (*models.ArticleBrief, error) {
	b := &models.ArticleBrief{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, proposal_id, status, thesis, audience, outline_markdown, material_plan_json, conflict_plan_json, style, target_length, confirmed_at, created_at, updated_at
		 FROM article_briefs WHERE id=?`, id).
		Scan(&b.ID, &b.ProposalID, &b.Status, &b.Thesis, &b.Audience, &b.Outline, &b.MaterialPlan, &b.ConflictPlan, &b.Style, &b.TargetLength, &b.ConfirmedAt, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// ConfirmArticleBrief is the explicit Owner authorization point for article generation.
func (s *Store) ConfirmArticleBrief(ctx context.Context, briefID string) error {
	result, err := s.DB.ExecContext(ctx,
		`UPDATE article_briefs SET status='confirmed', confirmed_at=datetime('now'), updated_at=datetime('now') WHERE id=? AND status='draft'`, briefID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: brief is missing or cannot be confirmed", ErrInvalidEditorialState)
	}
	return nil
}

// CreateArticleDraft opens a durable editing object from a confirmed brief.
func (s *Store) CreateArticleDraft(ctx context.Context, briefID, title string) (*models.ArticleDraft, error) {
	brief, err := s.GetArticleBrief(ctx, briefID)
	if err != nil {
		return nil, err
	}
	if brief.Status != "confirmed" {
		return nil, fmt.Errorf("%w: confirmed brief required", ErrInvalidEditorialState)
	}
	proposal, err := s.GetArticleProposal(ctx, brief.ProposalID)
	if err != nil {
		return nil, err
	}
	d := &models.ArticleDraft{ID: uuid.NewString(), EditorialProfileID: proposal.EditorialProfileID, BriefID: brief.ID, Title: strings.TrimSpace(title), Status: "drafting"}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO article_drafts (id, editorial_profile_id, brief_id, title, status) VALUES (?, ?, ?, ?, ?)`,
		d.ID, d.EditorialProfileID, d.BriefID, d.Title, d.Status)
	if err != nil {
		return nil, fmt.Errorf("create article draft: %w", err)
	}
	return s.GetArticleDraft(ctx, d.ID)
}

// GetArticleDraft returns the editing object and its selected current revision ID.
func (s *Store) GetArticleDraft(ctx context.Context, id string) (*models.ArticleDraft, error) {
	d := &models.ArticleDraft{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, editorial_profile_id, brief_id, title, current_revision_id, status, created_at, updated_at FROM article_drafts WHERE id=?`, id).
		Scan(&d.ID, &d.EditorialProfileID, &d.BriefID, &d.Title, &d.CurrentRevisionID, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ListArticleDrafts returns the personal editorial board for one profile.
func (s *Store) ListArticleDrafts(ctx context.Context, profileID string) ([]*models.ArticleDraft, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, editorial_profile_id, brief_id, title, current_revision_id, status, created_at, updated_at
		 FROM article_drafts WHERE editorial_profile_id=? ORDER BY updated_at DESC, id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleDraft
	for rows.Next() {
		d := &models.ArticleDraft{}
		if err := rows.Scan(&d.ID, &d.EditorialProfileID, &d.BriefID, &d.Title, &d.CurrentRevisionID, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CreateArticleRevision appends an immutable snapshot and makes it the draft's current revision.
func (s *Store) CreateArticleRevision(ctx context.Context, revision models.ArticleRevision) (*models.ArticleRevision, error) {
	return s.CreateArticleRevisionWithEvidenceMaps(ctx, revision, nil)
}

// CreateArticleRevisionWithEvidenceMaps atomically appends a revision and its
// evidence relationships. Writer output is not a valid production snapshot
// without its maps, so a map failure must not leave an unreviewable current
// revision behind.
func (s *Store) CreateArticleRevisionWithEvidenceMaps(ctx context.Context, revision models.ArticleRevision, evidenceMaps []models.EvidenceMap) (*models.ArticleRevision, error) {
	if strings.TrimSpace(revision.DraftID) == "" || strings.TrimSpace(revision.Markdown) == "" || !validRevisionOrigin(revision.Origin) {
		return nil, fmt.Errorf("%w: invalid article revision", ErrInvalidEditorialState)
	}
	for _, evidence := range evidenceMaps {
		keyPointIDs := defaultString(evidence.KeyPointIDs, "[]")
		if !validEvidenceMapKind(evidence.Kind) || !validJSON(keyPointIDs, "[]") || (evidence.Kind != models.EvidenceRhetorical && keyPointIDs == "[]") {
			return nil, fmt.Errorf("%w: invalid evidence map", ErrInvalidEditorialState)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM article_revisions WHERE draft_id=?`, revision.DraftID).Scan(&maxVersion); err != nil {
		return nil, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts WHERE id=?`, revision.DraftID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	revision.ID = uuid.NewString()
	revision.Version = maxVersion + 1
	_, err = tx.ExecContext(ctx,
		`INSERT INTO article_revisions (id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.DraftID, revision.Version, revision.Title, revision.Markdown, revision.Origin, revision.Provider, revision.Model, revision.PromptVersion, revision.CostCents)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE article_drafts SET title=?, current_revision_id=?, status='reviewing', updated_at=datetime('now') WHERE id=?`,
		revision.Title, revision.ID, revision.DraftID); err != nil {
		return nil, err
	}
	for _, evidence := range evidenceMaps {
		evidence.ID = uuid.NewString()
		evidence.RevisionID = revision.ID
		evidence.KeyPointIDs = defaultString(evidence.KeyPointIDs, "[]")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO evidence_maps (id, revision_id, kind, excerpt, keypoint_ids_json) VALUES (?, ?, ?, ?, ?)`,
			evidence.ID, evidence.RevisionID, string(evidence.Kind), evidence.Excerpt, evidence.KeyPointIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetArticleRevision(ctx, revision.ID)
}

// GetArticleRevision reads an immutable article revision.
func (s *Store) GetArticleRevision(ctx context.Context, id string) (*models.ArticleRevision, error) {
	r := &models.ArticleRevision{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents, created_at
		 FROM article_revisions WHERE id=?`, id).
		Scan(&r.ID, &r.DraftID, &r.Version, &r.Title, &r.Markdown, &r.Origin, &r.Provider, &r.Model, &r.PromptVersion, &r.CostCents, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListArticleRevisions returns newest revisions first.
func (s *Store) ListArticleRevisions(ctx context.Context, draftID string) ([]*models.ArticleRevision, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents, created_at
		 FROM article_revisions WHERE draft_id=? ORDER BY version DESC`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleRevision
	for rows.Next() {
		r := &models.ArticleRevision{}
		if err := rows.Scan(&r.ID, &r.DraftID, &r.Version, &r.Title, &r.Markdown, &r.Origin, &r.Provider, &r.Model, &r.PromptVersion, &r.CostCents, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateEvidenceMap records the semantic relationship between text and KeyPoint material.
func (s *Store) CreateEvidenceMap(ctx context.Context, evidence models.EvidenceMap) (*models.EvidenceMap, error) {
	evidence.ID = uuid.NewString()
	evidence.KeyPointIDs = defaultString(evidence.KeyPointIDs, "[]")
	if !validEvidenceMapKind(evidence.Kind) || !validJSON(evidence.KeyPointIDs, "[]") {
		return nil, fmt.Errorf("%w: invalid evidence map", ErrInvalidEditorialState)
	}
	if evidence.Kind != models.EvidenceRhetorical && evidence.KeyPointIDs == "[]" {
		return nil, fmt.Errorf("%w: evidence-bearing expression requires KeyPoints", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO evidence_maps (id, revision_id, kind, excerpt, keypoint_ids_json) VALUES (?, ?, ?, ?, ?)`,
		evidence.ID, evidence.RevisionID, string(evidence.Kind), evidence.Excerpt, evidence.KeyPointIDs)
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

// ListEvidenceMaps returns the persisted semantic evidence relationships for one exact revision.
func (s *Store) ListEvidenceMaps(ctx context.Context, revisionID string) ([]*models.EvidenceMap, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, revision_id, kind, excerpt, keypoint_ids_json, created_at FROM evidence_maps WHERE revision_id=? ORDER BY created_at, id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EvidenceMap
	for rows.Next() {
		evidence := &models.EvidenceMap{}
		if err := rows.Scan(&evidence.ID, &evidence.RevisionID, &evidence.Kind, &evidence.Excerpt, &evidence.KeyPointIDs, &evidence.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, evidence)
	}
	return out, rows.Err()
}

// CreateArticleReview records an independent evidence or style review for one exact revision.
func (s *Store) CreateArticleReview(ctx context.Context, review models.ArticleReview) (*models.ArticleReview, error) {
	review.ID = uuid.NewString()
	review.IssuesJSON = defaultString(review.IssuesJSON, "[]")
	if !validReviewKind(review.Kind) || !validReviewStatus(review.Status) || !validJSON(review.IssuesJSON, "[]") {
		return nil, fmt.Errorf("%w: invalid article review", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO article_reviews (id, revision_id, kind, status, issues_json, provider, model) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.RevisionID, review.Kind, review.Status, review.IssuesJSON, review.Provider, review.Model)
	if err != nil {
		return nil, err
	}
	if review.Kind == "evidence" {
		state := "blocked"
		if review.Status == "passed" {
			state = "ready"
		}
		_, _ = s.DB.ExecContext(ctx,
			`UPDATE article_drafts SET status=?, updated_at=datetime('now') WHERE current_revision_id=?`, state, review.RevisionID)
	}
	return &review, nil
}

// ListArticleReviews returns the audit trail for one immutable revision, newest first.
func (s *Store) ListArticleReviews(ctx context.Context, revisionID string) ([]*models.ArticleReview, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, revision_id, kind, status, issues_json, provider, model, created_at
		 FROM article_reviews WHERE revision_id=? ORDER BY created_at DESC, id DESC`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleReview
	for rows.Next() {
		review := &models.ArticleReview{}
		if err := rows.Scan(&review.ID, &review.RevisionID, &review.Kind, &review.Status, &review.IssuesJSON, &review.Provider, &review.Model, &review.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	return out, rows.Err()
}

// IsRevisionReadyForPublication applies the hard evidence gate to an exact revision.
func (s *Store) IsRevisionReadyForPublication(ctx context.Context, revisionID string) (bool, error) {
	var status string
	err := s.DB.QueryRowContext(ctx,
		`SELECT status FROM article_reviews WHERE revision_id=? AND kind='evidence' ORDER BY created_at DESC, id DESC LIMIT 1`, revisionID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "passed", nil
}

// CreateEditorialFeedback persists an Owner's explicit editorial decision.
func (s *Store) CreateEditorialFeedback(ctx context.Context, feedback models.EditorialFeedback) (*models.EditorialFeedback, error) {
	feedback.ID = uuid.NewString()
	feedback.DetailsJSON = defaultString(feedback.DetailsJSON, "{}")
	if strings.TrimSpace(feedback.EntityType) == "" || strings.TrimSpace(feedback.EntityID) == "" || strings.TrimSpace(feedback.Action) == "" || !validJSON(feedback.DetailsJSON, "{}") {
		return nil, fmt.Errorf("%w: invalid editorial feedback", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO editorial_feedback (id, editorial_profile_id, entity_type, entity_id, action, reason, details_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		feedback.ID, feedback.EditorialProfileID, feedback.EntityType, feedback.EntityID, feedback.Action, feedback.Reason, feedback.DetailsJSON)
	if err != nil {
		return nil, err
	}
	return &feedback, nil
}

func validAttribution(value string) bool {
	return value == "light" || value == "standard" || value == "strict"
}
func validSourceType(value models.SourceType) bool {
	return value == models.SourceEpisode || value == models.SourceUpload
}
func sourceTable(value models.SourceType) string {
	if value == models.SourceUpload {
		return "uploads"
	}
	return "episodes"
}

func (s *Store) sourceExists(ctx context.Context, sourceType models.SourceType, sourceID string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id=?`, sourceTable(sourceType)), sourceID).Scan(&count)
	return count > 0, err
}
func validProductionUse(value string) bool {
	return value == "public" || value == "internal" || value == "disabled"
}
func validModelDataPolicy(value models.ModelDataPolicy) bool {
	return value == models.ModelDataExternalAllowed || value == models.ModelDataApprovedProvidersOnly || value == models.ModelDataLocalOnly
}
func validProposalKind(value string) bool {
	return value == "fresh" || value == "evergreen" || value == "follow_up"
}
func validProposalStatus(value string) bool {
	return value == "proposed" || value == "accepted" || value == "parked" || value == "rejected" || value == "merged"
}
func validBriefStatus(value string) bool {
	return value == "draft" || value == "confirmed" || value == "superseded"
}
func validRevisionOrigin(value string) bool {
	return value == "writer" || value == "evidence_reviewer" || value == "style_editor" || value == "owner" || value == "ai_edit"
}
func validEvidenceMapKind(value models.EvidenceMapKind) bool {
	return value == models.EvidenceQuoted || value == models.EvidenceParaphrased || value == models.EvidenceSynthesized || value == models.EvidenceRhetorical
}
func validReviewKind(value string) bool { return value == "evidence" || value == "style" }
func validReviewStatus(value string) bool {
	return value == "passed" || value == "failed" || value == "advisory"
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func validJSON(value, fallback string) bool {
	value = defaultString(value, fallback)
	var target any
	return json.Unmarshal([]byte(value), &target) == nil
}
