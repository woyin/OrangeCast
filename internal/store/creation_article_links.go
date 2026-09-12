// creation_article_links.go 新旧创作契约的持久兼容连接（C08 / ADR-0024 §6）。
//
// 为 CreationProposal/CreationBrief 与 ArticleProposal/ArticleBrief 建立持久
// 一对一映射：Owner 确认新 CreationBrief 后幂等建立兼容 ArticleProposal 和
// ArticleBrief 记录。旧 Article 记录继续可读、可继续、可导出。
// 未确认的新 Brief 不建立映射（不把未确认方向迁成已确认主张）。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// CreationArticleLink 映射行。
type CreationArticleLink struct {
	ID                 string
	CreationProposalID string
	CreationBriefID    string
	ArticleProposalID  string
	ArticleBriefID     string
	ContractVersion    string
	CreatedAt          string
}

// EnsureCreationArticleLink Owner 确认新 Brief 后幂等建立映射：
//  1. 创建兼容 ArticleProposal（继承标题/主张/受众，kind=deep_read 单集深读）；
//  2. 创建兼容 ArticleBrief（继承论文/受众/结构，状态 confirmed）；
//  3. 写入一对一映射行。
//
// 已有映射直接返回（幂等，重复确认/重复桥接只产生一组映射）。
// 不把未确认方向迁成已确认主张。
func (s *Store) EnsureCreationArticleLink(ctx context.Context, creationProposalID, creationBriefID, contractVersion string) (*CreationArticleLink, error) {
	if creationProposalID == "" || creationBriefID == "" {
		return nil, fmt.Errorf("%w: creation proposal and brief IDs required", ErrInvalidEditorialState)
	}
	if contractVersion == "" {
		contractVersion = "v2"
	}

	// 幂等：已有映射直接返回。
	existing, err := s.GetCreationArticleLinkByCreationProposal(ctx, creationProposalID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// 读取新契约对象。
	proposal, err := s.GetCreationProposal(ctx, creationProposalID)
	if err != nil {
		return nil, fmt.Errorf("读取 CreationProposal: %w", err)
	}
	if proposal.Status != "accepted" {
		return nil, fmt.Errorf("%w: proposal not accepted, cannot bridge", ErrInvalidEditorialState)
	}
	brief, err := s.GetCreationBrief(ctx, creationBriefID)
	if err != nil {
		return nil, fmt.Errorf("读取 CreationBrief: %w", err)
	}
	if brief.Status != "confirmed" {
		return nil, fmt.Errorf("%w: brief not confirmed, cannot bridge", ErrInvalidEditorialState)
	}

	// 创建兼容 ArticleProposal。
	ap := models.ArticleProposal{
		EditorialProfileID: proposal.EditorialProfileID,
		Kind:               "deep_read",
		Status:             "accepted",
		Title:              proposal.WorkingTitle,
		Thesis:             proposal.ProposedClaim,
		Audience:           proposal.Audience,
		Rationale:          proposal.Rationale,
		CandidateKeyPoints: proposal.MaterialIDsJSON,
	}
	articleProposal, err := s.CreateArticleProposal(ctx, ap)
	if err != nil {
		return nil, fmt.Errorf("创建兼容 ArticleProposal: %w", err)
	}

	// 创建兼容 ArticleBrief（状态 confirmed）。
	ab := models.ArticleBrief{
		ProposalID:   articleProposal.ID,
		Status:       "confirmed",
		Thesis:       brief.OwnerClaim,
		Audience:     proposal.Audience,
		Outline:      brief.MaterialPlanJSON,
		MaterialPlan: brief.MaterialPlanJSON,
		Style:        "",
	}
	articleBrief, err := s.CreateArticleBrief(ctx, ab)
	if err != nil {
		return nil, fmt.Errorf("创建兼容 ArticleBrief: %w", err)
	}

	// 写入映射。
	link := &CreationArticleLink{
		ID:                 uuid.NewString(),
		CreationProposalID: creationProposalID,
		CreationBriefID:    creationBriefID,
		ArticleProposalID:  articleProposal.ID,
		ArticleBriefID:     articleBrief.ID,
		ContractVersion:    contractVersion,
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO creation_article_links (id, creation_proposal_id, creation_brief_id, article_proposal_id, article_brief_id, contract_version)
		 VALUES (?,?,?,?,?,?)`,
		link.ID, link.CreationProposalID, link.CreationBriefID, link.ArticleProposalID, link.ArticleBriefID, link.ContractVersion)
	if err != nil {
		return nil, fmt.Errorf("写入映射: %w", err)
	}
	return link, nil
}

// GetCreationArticleLinkByCreationProposal 按 CreationProposal 读取映射。
func (s *Store) GetCreationArticleLinkByCreationProposal(ctx context.Context, creationProposalID string) (*CreationArticleLink, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, creation_proposal_id, creation_brief_id, article_proposal_id, article_brief_id, contract_version, created_at
		 FROM creation_article_links WHERE creation_proposal_id=?`, creationProposalID)
	return scanCreationArticleLink(row)
}

// GetCreationArticleLinkByArticleProposal 按 ArticleProposal 读取映射（旧入口反查）。
func (s *Store) GetCreationArticleLinkByArticleProposal(ctx context.Context, articleProposalID string) (*CreationArticleLink, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, creation_proposal_id, creation_brief_id, article_proposal_id, article_brief_id, contract_version, created_at
		 FROM creation_article_links WHERE article_proposal_id=?`, articleProposalID)
	return scanCreationArticleLink(row)
}

func scanCreationArticleLink(row interface{ Scan(...any) error }) (*CreationArticleLink, error) {
	l := &CreationArticleLink{}
	err := row.Scan(&l.ID, &l.CreationProposalID, &l.CreationBriefID, &l.ArticleProposalID, &l.ArticleBriefID, &l.ContractVersion, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}
