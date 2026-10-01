package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// KnowledgeListQuery bounds metadata pages without bounding background history checks.
type KnowledgeListQuery struct {
	Text, Status, Theme string
	Page, PerPage       int
}

// KnowledgeArticlePage contains article metadata only; exact bodies remain on the detail seam.
type KnowledgeArticlePage struct {
	Items                []*KnowledgeArticleRecord
	Total, Page, PerPage int
}

// KnowledgeCandidatePage retains both usable and rejected directions with an exact count.
type KnowledgeCandidatePage struct {
	Items                []*KnowledgeTopicCandidate
	Total, Page, PerPage int
}

// KnowledgeSourcePage contains title-only search options plus an out-of-page current selection.
type KnowledgeSourcePage struct {
	Items                []KnowledgeSearchSource
	Total, Page, PerPage int
	SelectedUnavailable  bool
}

func normalizeKnowledgeList(q KnowledgeListQuery) (KnowledgeListQuery, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	if q.Page > 10000 || len([]rune(q.Text)) > 200 || len([]rune(q.Theme)) > 200 || len(q.Status) > 40 {
		return q, ErrInvalidEditorialState
	}
	return q, nil
}

func knowledgeListFilter(q KnowledgeListQuery, content string) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if q.Status != "" {
		where = append(where, "status=?")
		args = append(args, q.Status)
	}
	for _, v := range []string{q.Text, q.Theme} {
		if strings.TrimSpace(v) != "" {
			where = append(where, "instr(lower("+content+"),lower(?))>0")
			args = append(args, strings.TrimSpace(v))
		}
	}
	return strings.Join(where, " AND "), args
}

// ListKnowledgeArticlesPage searches bounded metadata in deterministic chronological order.
func (s *Store) ListKnowledgeArticlesPage(ctx context.Context, q KnowledgeListQuery) (KnowledgeArticlePage, error) {
	q, err := normalizeKnowledgeList(q)
	page := KnowledgeArticlePage{Page: q.Page, PerPage: q.PerPage}
	if err != nil {
		return page, err
	}
	where, args := knowledgeListFilter(q, "title||' '||thesis||' '||topic_json")
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_articles WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,profile_id,status,stage,title,thesis,reason,provider,model,review_model,working_revision,passed_revision,created_at,updated_at FROM knowledge_articles WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		v := &KnowledgeArticleRecord{}
		if err = rows.Scan(&v.ID, &v.ProfileID, &v.Status, &v.Stage, &v.Title, &v.Thesis, &v.Reason, &v.Provider, &v.Model, &v.ReviewModel, &v.WorkingRevision, &v.PassedRevision, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, v)
	}
	return page, rows.Err()
}

// ListKnowledgeTopicCandidatesPage searches all directions independently of the article page.
func (s *Store) ListKnowledgeTopicCandidatesPage(ctx context.Context, q KnowledgeListQuery) (KnowledgeCandidatePage, error) {
	q, err := normalizeKnowledgeList(q)
	page := KnowledgeCandidatePage{Page: q.Page, PerPage: q.PerPage}
	if err != nil {
		return page, err
	}
	where, args := knowledgeListFilter(q, "topic_json")
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_topic_candidates WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,batch_id,direction_hash,topic_json,status,article_id,reason,selection_json,created_at FROM knowledge_topic_candidates WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		v := &KnowledgeTopicCandidate{}
		if err = rows.Scan(&v.ID, &v.BatchID, &v.DirectionHash, &v.TopicJSON, &v.Status, &v.ArticleID, &v.Reason, &v.SelectionJSON, &v.CreatedAt); err != nil {
			return page, err
		}
		if err = json.Unmarshal([]byte(v.TopicJSON), &v.Topic); err != nil {
			return page, err
		}
		page.Items = append(page.Items, v)
	}
	return page, rows.Err()
}

const knowledgeSourceMetadata = `SELECT d.source_type,d.source_id,COALESCE(e.title,u.original_filename,doc.title,MAX(d.title)) AS title,MAX(d.updated_at) AS updated_at FROM knowledge_search_docs d LEFT JOIN episodes e ON d.source_type='episode' AND e.id=d.source_id LEFT JOIN uploads u ON d.source_type='upload' AND u.id=d.source_id LEFT JOIN documents doc ON d.source_type='document' AND doc.id=d.source_id WHERE d.source_id!='' GROUP BY d.source_type,d.source_id`

// SearchKnowledgeSources searches the entire metadata projection and retains a stable selected identity.
func (s *Store) SearchKnowledgeSources(ctx context.Context, q KnowledgeListQuery, selected string) (KnowledgeSourcePage, error) {
	q, err := normalizeKnowledgeList(q)
	page := KnowledgeSourcePage{Page: q.Page, PerPage: q.PerPage}
	if err != nil {
		return page, err
	}
	if len(selected) > 250 {
		return page, ErrInvalidEditorialState
	}
	from := " FROM (" + knowledgeSourceMetadata + ") WHERE instr(lower(title),lower(?))>0"
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*)"+from, q.Text).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT source_type,source_id,title"+from+" ORDER BY updated_at DESC,source_type,source_id LIMIT ? OFFSET ?", q.Text, q.PerPage, (q.Page-1)*q.PerPage)
	if err != nil {
		return page, err
	}
	found := selected == ""
	for rows.Next() {
		var v KnowledgeSearchSource
		if err = rows.Scan(&v.SourceType, &v.SourceID, &v.Title); err != nil {
			rows.Close()
			return page, err
		}
		if string(v.SourceType)+":"+v.SourceID == selected {
			found = true
		}
		page.Items = append(page.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if !found {
		ids := strings.SplitN(selected, ":", 2)
		if len(ids) != 2 {
			return page, fmt.Errorf("%w: invalid selected source", ErrInvalidEditorialState)
		}
		var v KnowledgeSearchSource
		err = s.DB.QueryRowContext(ctx, "SELECT source_type,source_id,title FROM ("+knowledgeSourceMetadata+") WHERE source_type=? AND source_id=?", ids[0], ids[1]).Scan(&v.SourceType, &v.SourceID, &v.Title)
		if err == nil {
			page.Items = append([]KnowledgeSearchSource{v}, page.Items...)
		} else if errors.Is(err, sql.ErrNoRows) {
			page.SelectedUnavailable = true
		} else {
			return page, err
		}
	}
	return page, nil
}
