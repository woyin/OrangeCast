package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// creationHistorySelect 是 Get/List/Find 共用的列清单（R22：LEFT JOIN 带出确切
// ArticleRevision 身份与 draft/version，供准确链接与幂等登记）。
const creationHistorySelect = "ch.id,ch.editorial_profile_id,ch.status,ch.creation_form,ch.title," +
	"ch.core_claim,ch.audience,ch.content,ch.source_url,ch.created_at,ch.updated_at," +
	"COALESCE(ch.article_revision_id,''),COALESCE(r.draft_id,''),COALESCE(r.version,0)"

// creationHistoryJoin 关联确切 ArticleRevision（历史行可能为外部导入/精读，允许缺省）。
const creationHistoryJoin = " LEFT JOIN article_revisions r ON r.id=ch.article_revision_id"

func validCreationHistoryStatus(status string) bool {
	return status == "published" || status == "unpublished"
}

// CreateCreationHistory imports a published or unpublished work for duplicate detection.
func (s *Store) CreateCreationHistory(ctx context.Context, work models.CreationHistory) (*models.CreationHistory, error) {
	work.ID = uuid.NewString()
	work.Status = strings.TrimSpace(work.Status)
	work.Title = strings.TrimSpace(work.Title)
	work.CreationForm = strings.TrimSpace(work.CreationForm)
	if work.CreationForm == "" {
		work.CreationForm = "article"
	}
	if work.EditorialProfileID == "" || work.Title == "" || !validCreationHistoryStatus(work.Status) {
		return nil, fmt.Errorf("%w: invalid creation history", ErrInvalidEditorialState)
	}
	if _, err := s.GetEditorialProfile(ctx, work.EditorialProfileID); err != nil {
		return nil, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO creation_history (id,editorial_profile_id,status,creation_form,title,core_claim,audience,content,source_url) VALUES (?,?,?,?,?,?,?,?,?)`, work.ID, work.EditorialProfileID, work.Status, work.CreationForm, work.Title, work.CoreClaim, work.Audience, work.Content, work.SourceURL)
	if err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, work.ID)
}

// GetCreationHistory retrieves imported or internal history.
func (s *Store) GetCreationHistory(ctx context.Context, id string) (*models.CreationHistory, error) {
	work := &models.CreationHistory{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+creationHistorySelect+` FROM creation_history ch`+creationHistoryJoin+` WHERE ch.id=?`, id).Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt, &work.ArticleRevisionID, &work.ArticleDraftID, &work.ArticleVersion)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return work, err
}

// ListCreationHistory lists a profile's durable historical work, newest first.
func (s *Store) ListCreationHistory(ctx context.Context, profileID string) ([]*models.CreationHistory, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+creationHistorySelect+` FROM creation_history ch`+creationHistoryJoin+` WHERE ch.editorial_profile_id=? ORDER BY ch.created_at DESC,ch.id DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.CreationHistory{}
	for rows.Next() {
		work := &models.CreationHistory{}
		if err := rows.Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt, &work.ArticleRevisionID, &work.ArticleDraftID, &work.ArticleVersion); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// FindCreationHistoryCandidates performs a transparent lexical prefilter. It is
// only a reminder when history lacks full content; hard-duplicate decisions stay Owner-reviewable.
func (s *Store) FindCreationHistoryCandidates(ctx context.Context, profileID, claim string) ([]*models.CreationHistory, error) {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return nil, nil
	}
	terms := strings.Fields(claim)
	if len(terms) == 0 {
		terms = []string{claim}
	}
	like := "%" + terms[0] + "%"
	rows, err := s.DB.QueryContext(ctx, `SELECT `+creationHistorySelect+` FROM creation_history ch`+creationHistoryJoin+` WHERE ch.editorial_profile_id=? AND (ch.core_claim LIKE ? OR ch.title LIKE ? OR ch.content LIKE ?) ORDER BY ch.created_at DESC`, profileID, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.CreationHistory{}
	for rows.Next() {
		work := &models.CreationHistory{}
		if err := rows.Scan(&work.ID, &work.EditorialProfileID, &work.Status, &work.CreationForm, &work.Title, &work.CoreClaim, &work.Audience, &work.Content, &work.SourceURL, &work.CreatedAt, &work.UpdatedAt, &work.ArticleRevisionID, &work.ArticleDraftID, &work.ArticleVersion); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// RecordDigestHistory 将确切精读修订登记为创作历史（G08 / ADR-0024 §5）：
//   - 无专门画像时复用默认画像，不创建品牌副本；
//   - status 只能是 published（Owner 明确登记发布）或 unpublished；
//   - digest_id 唯一：同修订重复登记幂等返回既有记录（导出/预览不调用本方法）；
//   - digest_content 复制登记时正文——来源删除后历史身份与已登记文本仍可审计
//     （依据有效性由来源快照的审计状态说明，不私存被删原文）。
func (s *Store) RecordDigestHistory(ctx context.Context, digestID, status string) (*models.CreationHistory, error) {
	if status != "published" && status != "unpublished" {
		return nil, fmt.Errorf("%w: invalid history status %q", ErrInvalidEditorialState, status)
	}
	d, err := s.GetEpisodeDigest(ctx, digestID)
	if err != nil {
		return nil, err
	}
	profile, err := s.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		return nil, err
	}
	// 幂等：同修订已有记录直接返回。
	var existingID string
	err = s.DB.QueryRowContext(ctx,
		`SELECT id FROM creation_history WHERE digest_id=?`, digestID).Scan(&existingID)
	if err == nil {
		return s.GetCreationHistory(ctx, existingID)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	blocks, err := s.ListDigestBlocks(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	for _, b := range blocks {
		body.WriteString(b.Text + "\n\n")
	}
	work := models.CreationHistory{
		EditorialProfileID: profile.ID, Status: status, CreationForm: "episode_digest",
		Title: d.Title, Content: body.String(),
	}
	created, err := s.CreateCreationHistory(ctx, work)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE creation_history SET digest_id=?, digest_content=? WHERE id=?`,
		d.ID, body.String(), created.ID); err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, created.ID)
}

// RecordArticleHistory 将确切 ArticleRevision 登记为创作历史（R22 / C13 / ADR-0024 §6）。
// Owner 明确点击"登记发布"/"保存为未发布"时调用；导出/预览不调用本方法。
//   - 画像/标题/内容全部从这条 SQL 内 JOIN 的精确 revision/draft 取（画像为文章
//     所属 draft 的 EditorialProfileID，不误用默认画像）；
//   - 单条 guarded UPSERT（JOIN 条件 current_revision_id=r.id 即 exact-current CAS，
//     partial-index conflict target 即同修订幂等）：跨 Store/进程首次并发也只一行，
//     不需要事务重试；
//   - 状态单调：published 不降级，unpublished 可升 published；
//   - 零行返回时区分 revision 不存在（ErrNotFound）与 stale（ErrConflict）。
func (s *Store) RecordArticleHistory(ctx context.Context, revisionID, status string) (*models.CreationHistory, error) {
	if status != "published" && status != "unpublished" {
		return nil, fmt.Errorf("%w: invalid history status %q", ErrInvalidEditorialState, status)
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO creation_history
		    (id, editorial_profile_id, status, creation_form, title, core_claim, audience, content, source_url, article_revision_id)
		SELECT ?, d.editorial_profile_id, ?, 'article',
		       COALESCE(NULLIF(r.title,''), d.title), '', '', r.markdown, 'revision:' || r.id, r.id
		FROM article_revisions r
		JOIN article_drafts d ON d.id = r.draft_id
		WHERE r.id = ? AND d.current_revision_id = r.id
		ON CONFLICT(article_revision_id) WHERE article_revision_id != ''
		DO UPDATE SET
		    status = CASE WHEN creation_history.status = 'published' THEN 'published' ELSE excluded.status END,
		    editorial_profile_id = excluded.editorial_profile_id,
		    creation_form = excluded.creation_form,
		    title = excluded.title,
		    content = excluded.content,
		    source_url = excluded.source_url,
		    updated_at = datetime('now')
		RETURNING id`, uuid.NewString(), status, revisionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		// 区分：revision 不存在 → ErrNotFound；存在但已不是 current → ErrConflict。
		var currentID string
		rowErr := s.DB.QueryRowContext(ctx,
			`SELECT COALESCE(d.current_revision_id,'')
			 FROM article_revisions r JOIN article_drafts d ON d.id = r.draft_id
			 WHERE r.id = ?`, revisionID).Scan(&currentID)
		if errors.Is(rowErr, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if rowErr != nil {
			return nil, rowErr
		}
		if currentID != revisionID {
			return nil, fmt.Errorf("%w: 修订已不是当前版本（current=%s），不能登记", ErrConflict, currentID)
		}
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, id)
}

// GetArticleHistoryForRevision 读取某修订的登记记录（无则 ErrNotFound）。
func (s *Store) GetArticleHistoryForRevision(ctx context.Context, revisionID string) (*models.CreationHistory, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM creation_history WHERE article_revision_id=?`, revisionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetCreationHistory(ctx, id)
}
