// Content production persistence (ADR-0021 / roadmap Phase 8).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
		`INSERT INTO editorial_profiles (id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents, per_article_budget_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		profile.ID, profile.Name, profile.TargetAudience, profile.Voice, profile.StyleGuide, profile.SourceAttribution, profile.MonthlyBudgetCents, profile.PerArticleBudgetCents)
	if err != nil {
		return nil, fmt.Errorf("create editorial profile: %w", err)
	}
	return s.GetEditorialProfile(ctx, profile.ID)
}

// GetEditorialProfile returns an editorial profile by stable ID.
func (s *Store) GetEditorialProfile(ctx context.Context, id string) (*models.EditorialProfile, error) {
	p := &models.EditorialProfile{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents, per_article_budget_cents, created_at, updated_at
		 FROM editorial_profiles WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.TargetAudience, &p.Voice, &p.StyleGuide, &p.SourceAttribution, &p.MonthlyBudgetCents, &p.PerArticleBudgetCents, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListEditorialProfiles returns profiles in stable display order.
func (s *Store) ListEditorialProfiles(ctx context.Context) ([]*models.EditorialProfile, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, target_audience, voice, style_guide, source_attribution, monthly_budget_cents, per_article_budget_cents, created_at, updated_at
		 FROM editorial_profiles ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EditorialProfile
	for rows.Next() {
		p := &models.EditorialProfile{}
		if err := rows.Scan(&p.ID, &p.Name, &p.TargetAudience, &p.Voice, &p.StyleGuide, &p.SourceAttribution, &p.MonthlyBudgetCents, &p.PerArticleBudgetCents, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
	return s.CanSendSourceToProvider(ctx, sourceType, sourceID, "")
}

// CanSendSourceToProvider applies the Source's model data policy to the exact
// Provider selected for a task. An empty Provider is never approved by an
// ApprovedProvidersOnly policy.
func (s *Store) CanSendSourceToProvider(ctx context.Context, sourceType models.SourceType, sourceID, providerName string) (bool, error) {
	if !validSourceType(sourceType) {
		return false, fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	var policy models.ModelDataPolicy
	var approvedJSON string
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT model_data_policy, approved_providers_json FROM %s WHERE id=?`, sourceTable(sourceType)), sourceID).Scan(&policy, &approvedJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	switch policy {
	case models.ModelDataExternalAllowed:
		return true, nil
	case models.ModelDataLocalOnly:
		return false, nil
	case models.ModelDataApprovedProvidersOnly:
		var approved []string
		if err := json.Unmarshal([]byte(approvedJSON), &approved); err != nil {
			return false, err
		}
		for _, candidate := range approved {
			if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(providerName)) && strings.TrimSpace(providerName) != "" {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("%w: unknown model data policy", ErrInvalidEditorialState)
	}
}

// SetSourceApprovedProviders configures the explicit allowlist used by
// ApprovedProvidersOnly without changing publication permission.
func (s *Store) SetSourceApprovedProviders(ctx context.Context, sourceType models.SourceType, sourceID string, providers []string) error {
	if !validSourceType(sourceType) {
		return fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	clean := make([]string, 0, len(providers))
	seen := map[string]bool{}
	for _, name := range providers {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" && !seen[name] {
			seen[name] = true
			clean = append(clean, name)
		}
	}
	encoded, _ := json.Marshal(clean)
	result, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET approved_providers_json=? WHERE id=?`, sourceTable(sourceType)), string(encoded), sourceID)
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

// GetSourcePolicy returns all publication and model-data controls together so
// callers cannot accidentally display or update only half of the policy.
func (s *Store) GetSourcePolicy(ctx context.Context, sourceType models.SourceType, sourceID string) (*models.SourcePolicy, error) {
	if !validSourceType(sourceType) {
		return nil, fmt.Errorf("%w: invalid source type", ErrInvalidEditorialState)
	}
	var policy models.SourcePolicy
	var approvedJSON string
	var archivedAt *string
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT production_use,model_data_policy,approved_providers_json,archived_at FROM %s WHERE id=?`, sourceTable(sourceType)), sourceID).
		Scan(&policy.ProductionUse, &policy.ModelDataPolicy, &approvedJSON, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(approvedJSON), &policy.ApprovedProviders); err != nil {
		return nil, err
	}
	policy.Archived = archivedAt != nil
	return &policy, nil
}

// UpdateSourcePolicy atomically updates both independent permissions and the
// Provider allowlist represented by ApprovedProvidersOnly.
func (s *Store) UpdateSourcePolicy(ctx context.Context, sourceType models.SourceType, sourceID string, policy models.SourcePolicy) error {
	if !validSourceType(sourceType) || !validProductionUse(policy.ProductionUse) || !validModelDataPolicy(policy.ModelDataPolicy) {
		return fmt.Errorf("%w: invalid source production policy", ErrInvalidEditorialState)
	}
	clean := make([]string, 0, len(policy.ApprovedProviders))
	seen := map[string]bool{}
	for _, name := range policy.ApprovedProviders {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" && !seen[name] {
			seen[name] = true
			clean = append(clean, name)
		}
	}
	if policy.ModelDataPolicy == models.ModelDataApprovedProvidersOnly && len(clean) == 0 {
		return fmt.Errorf("%w: approved provider policy requires an allowlist", ErrInvalidEditorialState)
	}
	encoded, _ := json.Marshal(clean)
	archived := any(nil)
	if policy.Archived {
		archived = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	result, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET production_use=?,model_data_policy=?,approved_providers_json=?,archived_at=? WHERE id=?`, sourceTable(sourceType)), policy.ProductionUse, string(policy.ModelDataPolicy), string(encoded), archived, sourceID)
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
		`SELECT s.source_type, s.source_id, COALESCE(o.title, ''), s.created_at
		 FROM editorial_source_scopes s
		 LEFT JOIN (
			SELECT 'episode' source_type, id, title FROM episodes
			UNION ALL SELECT 'upload' source_type, id, original_filename title FROM uploads
			UNION ALL SELECT 'document' source_type, id, title FROM documents
		 ) o ON o.source_type=s.source_type AND o.id=s.source_id
		 WHERE s.editorial_profile_id=? ORDER BY s.created_at DESC, s.source_id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceScopeEntry
	for rows.Next() {
		entry := SourceScopeEntry{EditorialProfileID: profileID}
		if err := rows.Scan(&entry.SourceType, &entry.SourceID, &entry.Title, &entry.CreatedAt); err != nil {
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
	SourceID, Title    string
	CreatedAt          string
}

// SourceOption is one Source selectable for an EditorialProfile's SourceScope.
type SourceOption struct {
	SourceType      models.SourceType
	SourceID, Title string
}

// ListSourceOptions exposes the exact SourceScope choices with human-readable
// titles; callers never need to ask an Owner to copy an opaque database ID.
func (s *Store) ListSourceOptions(ctx context.Context) ([]SourceOption, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT source_type,id,title FROM (
		SELECT 'episode' source_type,id,title,created_at FROM episodes
		UNION ALL SELECT 'upload',id,original_filename,created_at FROM uploads
		UNION ALL SELECT 'document',id,title,created_at FROM documents)
		ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceOption
	for rows.Next() {
		var option SourceOption
		if err := rows.Scan(&option.SourceType, &option.SourceID, &option.Title); err != nil {
			return nil, err
		}
		out = append(out, option)
	}
	return out, rows.Err()
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
		`INSERT INTO article_proposals (id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json,provider,model,prompt_version,cost_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.EditorialProfileID, proposal.Kind, proposal.Status, proposal.Title, proposal.Thesis, proposal.Audience, proposal.Rationale, proposal.CandidateKeyPoints, proposal.Provider, proposal.Model, proposal.PromptVersion, proposal.CostCents)
	if err != nil {
		return nil, fmt.Errorf("create article proposal: %w", err)
	}
	return s.GetArticleProposal(ctx, proposal.ID)
}

// GetArticleProposal reads one proposal.
func (s *Store) GetArticleProposal(ctx context.Context, id string) (*models.ArticleProposal, error) {
	p := &models.ArticleProposal{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json,provider,model,prompt_version,cost_cents, created_at, updated_at
		 FROM article_proposals WHERE id=?`, id).
		Scan(&p.ID, &p.EditorialProfileID, &p.Kind, &p.Status, &p.Title, &p.Thesis, &p.Audience, &p.Rationale, &p.CandidateKeyPoints, &p.Provider, &p.Model, &p.PromptVersion, &p.CostCents, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListArticleProposals returns recent proposals for one editorial profile.
func (s *Store) ListArticleProposals(ctx context.Context, profileID string) ([]*models.ArticleProposal, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, editorial_profile_id, kind, status, title, thesis, audience, rationale, candidate_keypoints_json,provider,model,prompt_version,cost_cents, created_at, updated_at
		 FROM article_proposals WHERE editorial_profile_id=? ORDER BY created_at DESC, id DESC LIMIT 200`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleProposal
	for rows.Next() {
		p := &models.ArticleProposal{}
		if err := rows.Scan(&p.ID, &p.EditorialProfileID, &p.Kind, &p.Status, &p.Title, &p.Thesis, &p.Audience, &p.Rationale, &p.CandidateKeyPoints, &p.Provider, &p.Model, &p.PromptVersion, &p.CostCents, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
		 WHERE p.editorial_profile_id=? ORDER BY b.updated_at DESC, b.id DESC LIMIT 200`, profileID)
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

// GetArticleBriefByProposal returns the most recent ArticleBrief generated for a proposal.
func (s *Store) GetArticleBriefByProposal(ctx context.Context, proposalID string) (*models.ArticleBrief, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM article_briefs WHERE proposal_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, proposalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetArticleBrief(ctx, id)
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
		 FROM article_drafts WHERE editorial_profile_id=? ORDER BY updated_at DESC, id DESC LIMIT 200`, profileID)
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

// GetArticleDraftByBrief resolves the single production draft associated with
// an Owner-confirmed Brief. It is used by idempotent Writer retries.
func (s *Store) GetArticleDraftByBrief(ctx context.Context, briefID string) (*models.ArticleDraft, error) {
	d := &models.ArticleDraft{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, editorial_profile_id, brief_id, title, current_revision_id, status, created_at, updated_at
		 FROM article_drafts WHERE brief_id=? ORDER BY created_at,id LIMIT 1`, briefID).
		Scan(&d.ID, &d.EditorialProfileID, &d.BriefID, &d.Title, &d.CurrentRevisionID, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ClaimEditorialTask acquires or re-acquires a durable paid-task lease. A
// completed claim is never re-run; a failed or expired claim may be retried.
func (s *Store) ClaimEditorialTask(ctx context.Context, taskKind, idempotencyKey string) (bool, error) {
	if strings.TrimSpace(taskKind) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return false, fmt.Errorf("%w: editorial task identity is required", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO editorial_task_claims (id,task_kind,idempotency_key,status,lease_until)
		 VALUES (?,?,?,'running',datetime('now','+10 minutes'))`, uuid.NewString(), taskKind, idempotencyKey)
	if err != nil {
		return false, err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		return true, nil
	}
	result, err = s.DB.ExecContext(ctx,
		`UPDATE editorial_task_claims SET status='running',attempt_count=attempt_count+1,last_error=NULL,lease_until=datetime('now','+10 minutes'),updated_at=datetime('now')
		 WHERE task_kind=? AND idempotency_key=? AND (status='failed' OR (status='running' AND lease_until < datetime('now')))`, taskKind, idempotencyKey)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// FinishEditorialTask closes a claimed editorial task as completed or failed and releases its lease.
func (s *Store) FinishEditorialTask(ctx context.Context, taskKind, idempotencyKey string, taskErr error) error {
	status, lastError := "completed", any(nil)
	if taskErr != nil {
		status, lastError = "failed", taskErr.Error()
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE editorial_task_claims SET status=?,last_error=?,lease_until=NULL,updated_at=datetime('now') WHERE task_kind=? AND idempotency_key=?`,
		status, lastError, taskKind, idempotencyKey)
	return err
}

// SaveEditorialTaskResult stores a validated JSON result payload on its claimed editorial task.
func (s *Store) SaveEditorialTaskResult(ctx context.Context, taskKind, idempotencyKey, payload string) error {
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("%w: editorial task result must be JSON", ErrInvalidEditorialState)
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE editorial_task_claims SET result_json=?,updated_at=datetime('now') WHERE task_kind=? AND idempotency_key=?`, payload, taskKind, idempotencyKey)
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

// GetEditorialTaskResult reads a finished editorial task's JSON result; ErrNotFound before completion.
func (s *Store) GetEditorialTaskResult(ctx context.Context, taskKind, idempotencyKey string) (string, error) {
	var payload sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT result_json FROM editorial_task_claims WHERE task_kind=? AND idempotency_key=?`, taskKind, idempotencyKey).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !payload.Valid {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return payload.String, nil
}

// CreateArticleRevision appends an immutable snapshot and makes it the draft's current revision.
func (s *Store) CreateArticleRevision(ctx context.Context, revision models.ArticleRevision) (*models.ArticleRevision, error) {
	return s.CreateArticleRevisionWithEvidenceMaps(ctx, revision, nil)
}

func validateArticleRevisionInput(revision models.ArticleRevision, evidenceMaps []models.EvidenceMap) error {
	if strings.TrimSpace(revision.DraftID) == "" || strings.TrimSpace(revision.Markdown) == "" || !validRevisionOrigin(revision.Origin) {
		return fmt.Errorf("%w: invalid article revision", ErrInvalidEditorialState)
	}
	for _, evidence := range evidenceMaps {
		keyPointIDs := defaultString(evidence.KeyPointIDs, "[]")
		if !validEvidenceMapKind(evidence.Kind) || !validJSON(keyPointIDs, "[]") || (evidence.Kind != models.EvidenceRhetorical && keyPointIDs == "[]") {
			return fmt.Errorf("%w: invalid evidence map", ErrInvalidEditorialState)
		}
	}
	return nil
}

func (s *Store) insertArticleRevisionTx(ctx context.Context, tx *sql.Tx, revision models.ArticleRevision) (models.ArticleRevision, error) {
	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM article_revisions WHERE draft_id=?`, revision.DraftID).Scan(&maxVersion); err != nil {
		return revision, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_drafts WHERE id=?`, revision.DraftID).Scan(&exists); err != nil {
		return revision, err
	}
	if exists == 0 {
		return revision, ErrNotFound
	}
	revision.ID = uuid.NewString()
	revision.Version = maxVersion + 1
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO article_revisions (id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.DraftID, revision.Version, revision.Title, revision.Markdown, revision.Origin, revision.Provider, revision.Model, revision.PromptVersion, revision.CostCents); err != nil {
		return revision, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE article_drafts SET title=?, current_revision_id=?, status='reviewing', updated_at=datetime('now') WHERE id=?`,
		revision.Title, revision.ID, revision.DraftID); err != nil {
		return revision, err
	}
	return revision, nil
}

func insertEvidenceMapsTx(ctx context.Context, tx *sql.Tx, revisionID string, evidenceMaps []models.EvidenceMap) error {
	for _, evidence := range evidenceMaps {
		evidence.ID = uuid.NewString()
		evidence.RevisionID = revisionID
		evidence.KeyPointIDs = defaultString(evidence.KeyPointIDs, "[]")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO evidence_maps (id, revision_id, kind, excerpt, keypoint_ids_json) VALUES (?, ?, ?, ?, ?)`,
			evidence.ID, evidence.RevisionID, string(evidence.Kind), evidence.Excerpt, evidence.KeyPointIDs); err != nil {
			return err
		}
		var keyPointIDs []string
		if err := json.Unmarshal([]byte(evidence.KeyPointIDs), &keyPointIDs); err != nil {
			return err
		}
		for _, keyPointID := range keyPointIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE keypoint_index SET production_status=? WHERE id=?`, string(models.KeyPointUsed), keyPointID); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateArticleRevisionWithEvidenceMaps atomically appends a revision and its
// evidence relationships. Writer output is not a valid production snapshot
// without its maps, so a map failure must not leave an unreviewable current
// revision behind.
func (s *Store) CreateArticleRevisionWithEvidenceMaps(ctx context.Context, revision models.ArticleRevision, evidenceMaps []models.EvidenceMap) (*models.ArticleRevision, error) {
	if err := validateArticleRevisionInput(revision, evidenceMaps); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	revision, err = s.insertArticleRevisionTx(ctx, tx, revision)
	if err != nil {
		return nil, err
	}
	if err := insertEvidenceMapsTx(ctx, tx, revision.ID, evidenceMaps); err != nil {
		return nil, err
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
		`SELECT id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents, evidence_invalidated_at, evidence_invalidation_reason, created_at
		 FROM article_revisions WHERE id=?`, id).
		Scan(&r.ID, &r.DraftID, &r.Version, &r.Title, &r.Markdown, &r.Origin, &r.Provider, &r.Model, &r.PromptVersion, &r.CostCents, &r.EvidenceInvalidatedAt, &r.EvidenceInvalidationReason, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListArticleRevisions returns newest revisions first.
func (s *Store) ListArticleRevisions(ctx context.Context, draftID string) ([]*models.ArticleRevision, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, draft_id, version, title, markdown, origin, provider, model, prompt_version, cost_cents, evidence_invalidated_at, evidence_invalidation_reason, created_at
		 FROM article_revisions WHERE draft_id=? ORDER BY version DESC LIMIT 200`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleRevision
	for rows.Next() {
		r := &models.ArticleRevision{}
		if err := rows.Scan(&r.ID, &r.DraftID, &r.Version, &r.Title, &r.Markdown, &r.Origin, &r.Provider, &r.Model, &r.PromptVersion, &r.CostCents, &r.EvidenceInvalidatedAt, &r.EvidenceInvalidationReason, &r.CreatedAt); err != nil {
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
	if review.Kind == "evidence" && (review.Provider == nil || strings.TrimSpace(*review.Provider) == "" || review.Model == nil || strings.TrimSpace(*review.Model) == "") {
		return nil, fmt.Errorf("%w: evidence review requires trusted provider provenance", ErrInvalidEditorialState)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO article_reviews (id, revision_id, kind, status, issues_json, provider, model, prompt_version, cost_cents) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.RevisionID, review.Kind, review.Status, review.IssuesJSON, review.Provider, review.Model, review.PromptVersion, review.CostCents)
	if err != nil {
		return nil, err
	}
	if err := s.refreshDraftReviewState(ctx, review.RevisionID); err != nil {
		return nil, err
	}
	return &review, nil
}

// ListArticleReviews returns the audit trail for one immutable revision, newest first.
func (s *Store) ListArticleReviews(ctx context.Context, revisionID string) ([]*models.ArticleReview, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, revision_id, kind, status, issues_json, provider, model, prompt_version, cost_cents, created_at
		 FROM article_reviews WHERE revision_id=? ORDER BY created_at DESC, id DESC`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ArticleReview
	for rows.Next() {
		review := &models.ArticleReview{}
		if err := rows.Scan(&review.ID, &review.RevisionID, &review.Kind, &review.Status, &review.IssuesJSON, &review.Provider, &review.Model, &review.PromptVersion, &review.CostCents, &review.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	return out, rows.Err()
}

// ListArticleReviewsForDraft loads the complete review history in one query,
// avoiding one round trip per immutable revision on the detail page.
func (s *Store) ListArticleReviewsForDraft(ctx context.Context, draftID string) (map[string][]*models.ArticleReview, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT ar.id,ar.revision_id,ar.kind,ar.status,ar.issues_json,ar.provider,ar.model,ar.prompt_version,ar.cost_cents,ar.created_at
		FROM article_reviews ar JOIN article_revisions r ON r.id=ar.revision_id WHERE r.draft_id=? ORDER BY ar.created_at DESC,ar.id DESC`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]*models.ArticleReview{}
	for rows.Next() {
		review := &models.ArticleReview{}
		if err := rows.Scan(&review.ID, &review.RevisionID, &review.Kind, &review.Status, &review.IssuesJSON, &review.Provider, &review.Model, &review.PromptVersion, &review.CostCents, &review.CreatedAt); err != nil {
			return nil, err
		}
		out[review.RevisionID] = append(out[review.RevisionID], review)
	}
	return out, rows.Err()
}

// IsRevisionReadyForPublication applies the hard evidence gate to an exact revision.
func (s *Store) IsRevisionReadyForPublication(ctx context.Context, revisionID string) (bool, error) {
	var ready bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT r.evidence_invalidated_at IS NULL
		 AND COALESCE((SELECT status='passed' AND provider IS NOT NULL AND model IS NOT NULL FROM article_reviews WHERE revision_id=r.id AND kind='evidence' ORDER BY created_at DESC,id DESC LIMIT 1),0)
		 AND EXISTS(SELECT 1 FROM article_reviews WHERE revision_id=r.id AND kind='style')
		 FROM article_revisions r WHERE r.id=?`, revisionID).Scan(&ready)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ready, nil
}

func (s *Store) refreshDraftReviewState(ctx context.Context, revisionID string) error {
	ready, err := s.IsRevisionReadyForPublication(ctx, revisionID)
	if err != nil {
		return err
	}
	state := "reviewing"
	if ready {
		state = "ready"
	} else {
		var failed int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM article_reviews WHERE revision_id=? AND kind='evidence' AND status='failed'`, revisionID).Scan(&failed); err != nil {
			return err
		}
		if failed > 0 {
			state = "blocked"
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE article_drafts SET status=?, updated_at=datetime('now') WHERE current_revision_id=?`, state, revisionID)
	return err
}

// InvalidateRevisionsForSource durably revokes publication eligibility before
// a Source's KeyPoints are removed. The audit reason survives the purge.
type editorialQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func sourceKeyPointIDs(ctx context.Context, queryer editorialQueryer, sourceType models.SourceType, sourceID string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id FROM keypoint_index WHERE source_type=? AND source_id=?`, string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

func revisionsReferencingKeyPoints(ctx context.Context, queryer editorialQueryer, keyPointIDs map[string]bool) (map[string]bool, error) {
	affected := map[string]bool{}
	if len(keyPointIDs) == 0 {
		return affected, nil
	}
	rows, err := queryer.QueryContext(ctx, `SELECT revision_id,keypoint_ids_json FROM evidence_maps`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var revisionID, encoded string
		if err := rows.Scan(&revisionID, &encoded); err != nil {
			rows.Close()
			return nil, err
		}
		var ids []string
		if err := json.Unmarshal([]byte(encoded), &ids); err != nil {
			rows.Close()
			return nil, err
		}
		for _, id := range ids {
			if keyPointIDs[id] {
				affected[revisionID] = true
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return affected, nil
}

func invalidateRevisions(ctx context.Context, queryer editorialQueryer, affected map[string]bool, reason string) error {
	for revisionID := range affected {
		if _, err := queryer.ExecContext(ctx, `UPDATE article_revisions SET evidence_invalidated_at=COALESCE(evidence_invalidated_at,datetime('now')), evidence_invalidation_reason=COALESCE(evidence_invalidation_reason,?) WHERE id=?`, reason, revisionID); err != nil {
			return err
		}
		if _, err := queryer.ExecContext(ctx, `UPDATE article_drafts SET status='blocked',updated_at=datetime('now') WHERE current_revision_id=?`, revisionID); err != nil {
			return err
		}
	}
	return nil
}

// InvalidateRevisionsForSource marks article revisions evidence-invalidated when a Source's KeyPoints disappear or change.
func (s *Store) InvalidateRevisionsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	ids, err := sourceKeyPointIDs(ctx, s.DB, sourceType, sourceID)
	if err != nil {
		return err
	}
	affected, err := revisionsReferencingKeyPoints(ctx, s.DB, ids)
	if err != nil {
		return err
	}
	return invalidateRevisions(ctx, s.DB, affected, fmt.Sprintf("source purged: %s/%s", sourceType, sourceID))
}

// InvalidateAndDeleteKeyPointsForSource makes the publication revocation and
// removal of the source's material projection one atomic database decision.
// A purge can safely retry this operation after interruption.
func (s *Store) InvalidateAndDeleteKeyPointsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := sourceKeyPointIDs(ctx, tx, sourceType, sourceID)
	if err != nil {
		return err
	}
	affected, err := revisionsReferencingKeyPoints(ctx, tx, ids)
	if err != nil {
		return err
	}
	if err := invalidateRevisions(ctx, tx, affected, fmt.Sprintf("source purged: %s/%s", sourceType, sourceID)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM keypoint_search WHERE keypoint_id IN (SELECT id FROM keypoint_index WHERE source_type=? AND source_id=?)`, string(sourceType), sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM keypoint_index WHERE source_type=? AND source_id=?`, string(sourceType), sourceID); err != nil {
		return err
	}
	return tx.Commit()
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
	return value == models.SourceEpisode || value == models.SourceUpload || value == models.SourceDocument
}
func sourceTable(value models.SourceType) string {
	if value == models.SourceDocument {
		return "documents"
	}
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
	return value == "fresh" || value == "evergreen" || value == "follow_up" || value == "deep_read"
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
