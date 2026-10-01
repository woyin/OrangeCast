package store

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"strings"
	"time"
	"unicode"
)

// KnowledgeSearchQuery bounds local-only retrieval and preserves explicit filters.
type KnowledgeSearchQuery struct {
	Question                                                        *provider.FrozenLearningQuestion
	Recall                                                          bool
	SendProvider, RecallProfileID                                   string // internal eligible recall; ordinary local search remains unrestricted
	MetadataOnly                                                    bool   // bounded snippet, no full body transfer; internal recall may request 200 hits
	Text, Kind, SourceType, SourceID, PodcastID, Theme, From, Until string
	Page, PerPage                                                   int
	IncludeDrafts, IncludeHistory                                   bool
}

// KnowledgeSearchHit keeps an identity, ranking reason and source/paragraph position.
type KnowledgeSearchHit struct {
	MatchKind                                                                                                       string
	Key, Kind, ObjectID, SourceType, SourceID, Title, Snippet, SegmentID, SnapshotID, Visibility, CreatedAt, Reason string
	Revision                                                                                                        int
	Position, Rank                                                                                                  float64
}

// KnowledgeSearchResult is a bounded page with an exact filtered count.
type KnowledgeSearchResult struct {
	Hits                 []KnowledgeSearchHit
	Total, Page, PerPage int
}

// The local adapter provides lexical aliases, not general semantic understanding.
var knowledgeAliases = map[string][]string{
	"retrieval practice": {"主动回忆", "回忆练习"}, "主动回忆": {"retrieval practice", "回忆练习"},
	"spaced repetition": {"间隔重复", "间隔学习"}, "间隔学习": {"spaced repetition", "间隔重复"},
	"working memory": {"工作记忆"}, "工作记忆": {"working memory"},
	"source attribution": {"来源归因"}, "来源归因": {"source attribution"},
}

func knowledgeMatchQuery(text string) string {
	var groups []string
	variants := []string{strings.ToLower(strings.TrimSpace(text))}
	if aliases := knowledgeAliases[variants[0]]; len(aliases) > 0 {
		variants = append(variants, aliases...)
	}
	for _, variant := range variants {
		words := strings.FieldsFunc(variant, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		var terms []string
		for _, word := range words {
			rs := []rune(word)
			hasHan := false
			for i, r := range rs {
				if unicode.Is(unicode.Han, r) {
					hasHan = true
					if i+1 < len(rs) && unicode.Is(unicode.Han, rs[i+1]) {
						terms = append(terms, "h"+hex.EncodeToString([]byte(string(rs[i:i+2]))))
					} else if len(rs) == 1 {
						terms = append(terms, "h"+hex.EncodeToString([]byte(string(r))))
					}
				}
			}
			if !hasHan {
				terms = append(terms, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
			}
		}
		if len(terms) > 0 {
			groups = append(groups, "("+strings.Join(terms, " AND ")+")")
		}
	}
	return strings.Join(groups, " OR ")
}

// SearchKnowledge retrieves indexed content without scanning vectors or calling a model.
type knowledgeSearchPlan struct {
	Query      KnowledgeSearchQuery
	From, Rank string
	Args       []any
	Empty      bool
}

// knowledgePlan centralizes source, version and Owner scope predicates for FTS
// and vector candidates. Building a plan never queues or calls a provider.
func (s *Store) knowledgePlan(ctx context.Context, q KnowledgeSearchQuery) (knowledgeSearchPlan, error) {
	if len([]rune(q.Text)) > 200 || len(q.Theme) > 200 || len(q.SourceID) > 200 || len(q.PodcastID) > 200 {
		return knowledgeSearchPlan{}, ErrInvalidEditorialState
	}
	if q.SourceType != "" && !validSourceType(models.SourceType(q.SourceType)) {
		return knowledgeSearchPlan{}, ErrInvalidEditorialState
	}
	for _, value := range []string{q.From, q.Until} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return knowledgeSearchPlan{}, ErrInvalidEditorialState
			}
		}
	}
	if q.From != "" && q.Until != "" && q.From > q.Until {
		return knowledgeSearchPlan{}, ErrInvalidEditorialState
	}
	allowed := map[string]bool{"": true, "original": true, "document": true, "keypoint": true, "source_note": true, "owner_reflection": true, "article": true, "notes": true, "materials": true}
	if !allowed[q.Kind] {
		return knowledgeSearchPlan{}, ErrInvalidEditorialState
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Page > 10000 {
		return knowledgeSearchPlan{}, ErrInvalidEditorialState
	}
	maxPage := 100
	if q.Recall && q.MetadataOnly {
		maxPage = 200
	}
	if q.PerPage < 1 || q.PerPage > maxPage {
		q.PerPage = 20
	}
	match := knowledgeMatchQuery(q.Text)
	if q.Recall {
		match = knowledgeRecallQuery(q.Text)
	}
	if strings.TrimSpace(q.Text) != "" && match == "" {
		return knowledgeSearchPlan{Query: q, Empty: true}, nil
	}
	join := ""
	rank := "0.0"
	where := []string{"1=1"}
	var args []any
	if clause, extra := questionMaterialFilter(q.Question); clause != "" {
		where = append(where, clause)
		args = append(args, extra...)
	}
	if match != "" {
		join = " JOIN knowledge_search_fts f ON f.rowid=d.rowid"
		rank = "bm25(knowledge_search_fts,3.0,5.0,1.0)"
		where = append(where, "knowledge_search_fts MATCH ?")
		args = append(args, match)
		clause, extra := knowledgePhraseFilter(q.Text)
		if clause != "" && !q.Recall {
			where = append(where, clause)
			args = append(args, extra...)
		}
	}
	if q.Kind == "materials" {
		where = append(where, "d.kind IN ('keypoint','source_note','owner_reflection')")
	} else if q.Kind == "notes" {
		where = append(where, "d.kind IN ('source_note','owner_reflection')")
	} else if q.Kind != "" {
		where = append(where, "d.kind=?")
		args = append(args, q.Kind)
	}
	for _, filter := range []struct{ field, value string }{{"source_type", q.SourceType}, {"source_id", q.SourceID}} {
		if filter.value != "" {
			where = append(where, "(d."+filter.field+"=? OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m."+filter.field+"=?)))")
			args = append(args, filter.value, filter.value)
		}
	}
	if q.PodcastID != "" {
		where = append(where, "EXISTS(SELECT 1 FROM episodes ep WHERE ep.podcast_id=? AND ((d.source_type='episode' AND ep.id=d.source_id) OR (d.kind='article' AND EXISTS(SELECT 1 FROM knowledge_article_material_refs m WHERE m.article_id=d.object_id AND m.revision=d.revision AND m.source_type='episode' AND m.source_id=ep.id))))")
		args = append(args, q.PodcastID)
	}
	if q.Theme != "" {
		themeMatch := knowledgeMatchQuery(q.Theme)
		if themeMatch == "" {
			return knowledgeSearchPlan{Query: q, Empty: true}, nil
		}
		if match == "" {
			join = " JOIN knowledge_search_fts f ON f.rowid=d.rowid"
			rank = "bm25(knowledge_search_fts,3.0,5.0,1.0)"
		}
		where = append(where, "knowledge_search_fts MATCH ?")
		args = append(args, themeMatch)
	}
	if q.From != "" {
		where = append(where, "d.created_at>=?")
		args = append(args, q.From)
	}
	if q.Until != "" {
		where = append(where, "d.created_at<=?")
		args = append(args, q.Until+" 23:59:59")
	}
	visibility := []string{"'current'"}
	if q.IncludeDrafts {
		visibility = append(visibility, "'draft'")
	}
	if q.IncludeHistory {
		visibility = append(visibility, "'history'", "'invalid'")
	}
	where = append(where, "d.visibility IN ("+strings.Join(visibility, ",")+")")
	// Archive filtering is local; external-send policy is checked only at the paid seam.
	where = append(where, `(d.source_id='' OR (d.source_type='episode' AND EXISTS(SELECT 1 FROM episodes e WHERE e.id=d.source_id AND e.archived_at IS NULL)) OR (d.source_type='upload' AND EXISTS(SELECT 1 FROM uploads u WHERE u.id=d.source_id AND u.archived_at IS NULL)) OR (d.source_type='document' AND EXISTS(SELECT 1 FROM documents x WHERE x.id=d.source_id AND x.archived_at IS NULL)))`)
	if q.Recall && q.SendProvider != "" {
		var policies []string
		for _, source := range []struct{ kind, table string }{{"episode", "episodes"}, {"upload", "uploads"}, {"document", "documents"}} {
			policies = append(policies, "(d.source_type='"+source.kind+"' AND EXISTS(SELECT 1 FROM "+source.table+" p WHERE p.id=d.source_id AND (p.model_data_policy='external_allowed' OR (p.model_data_policy='approved_providers_only' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(p.approved_providers_json) THEN p.approved_providers_json ELSE '[]' END) a WHERE lower(trim(a.value))=lower(trim(?)))))))")
			args = append(args, q.SendProvider)
		}
		where = append(where, "("+strings.Join(policies, " OR ")+")")
		where = append(where, "(d.kind!='keypoint' OR EXISTS(SELECT 1 FROM keypoint_index k WHERE k.id=d.object_id AND k.stale_at IS NULL AND k.evidence_status!='stale' AND k.production_status!='dismissed' AND k.quality_status IN ('ready','owner_confirmed')))")
		if q.RecallProfileID != "" {
			where = append(where, "(d.kind!='keypoint' OR NOT EXISTS(SELECT 1 FROM editorial_relevance er WHERE er.keypoint_id=d.object_id AND er.editorial_profile_id=? AND (er.owner_override='excluded' OR er.assessment='irrelevant')))")
			args = append(args, q.RecallProfileID)
		}
	}
	if q.Kind == "keypoint" {
		join += " JOIN keypoint_index live_k ON live_k.id=d.object_id"
	}
	if q.Kind == "" || q.Kind == "original" {
		var failures int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_search_docs WHERE kind='index_error' AND (?='' OR source_id=?)`, q.SourceID, q.SourceID).Scan(&failures); err != nil {
			return knowledgeSearchPlan{}, err
		}
		if failures > 0 {
			return knowledgeSearchPlan{}, fmt.Errorf("来源转录损坏，全文索引不完整，请处理来源后重建")
		}
	}
	from := " FROM knowledge_search_docs d" + join + " WHERE " + strings.Join(where, " AND ")
	return knowledgeSearchPlan{Query: q, From: from, Rank: rank, Args: args}, nil
}

func (s *Store) SearchKnowledge(ctx context.Context, q KnowledgeSearchQuery) (KnowledgeSearchResult, error) {
	return s.searchKnowledgeKeys(ctx, q, nil)
}

func (s *Store) searchKnowledgeKeys(ctx context.Context, q KnowledgeSearchQuery, keys []string) (KnowledgeSearchResult, error) {
	plan, err := s.knowledgePlan(ctx, q)
	if err != nil {
		return KnowledgeSearchResult{}, err
	}
	q = plan.Query
	result := KnowledgeSearchResult{Page: q.Page, PerPage: q.PerPage}
	if plan.Empty {
		return result, nil
	}
	from, rank, args := plan.From, plan.Rank, plan.Args
	if keys != nil {
		if len(keys) == 0 {
			return result, nil
		}
		if len(keys) > 400 {
			return result, ErrInvalidEditorialState
		}
		marks := make([]string, len(keys))
		for i, key := range keys {
			marks[i] = "?"
			args = append(args, key)
		}
		from += " AND d.key IN (" + strings.Join(marks, ",") + ")"
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&result.Total); err != nil {
		return result, fmt.Errorf("知识索引检索失败: %w", err)
	}
	bodyColumn := "d.body"
	if q.MetadataOnly {
		bodyColumn = "substr(d.body,1,280)"
	}
	query := "SELECT d.key,d.kind,d.object_id,d.revision,d.source_type,d.source_id,d.title," + bodyColumn + ",d.position,d.segment_id,d.snapshot_id,d.visibility,d.created_at," + rank + from + " ORDER BY " + rank + ",d.updated_at DESC,d.key LIMIT ? OFFSET ?"
	paged := append(append([]any(nil), args...), q.PerPage, (q.Page-1)*q.PerPage)
	rows, err := s.DB.QueryContext(ctx, query, paged...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var hit KnowledgeSearchHit
		var body string
		if err := rows.Scan(&hit.Key, &hit.Kind, &hit.ObjectID, &hit.Revision, &hit.SourceType, &hit.SourceID, &hit.Title, &body, &hit.Position, &hit.SegmentID, &hit.SnapshotID, &hit.Visibility, &hit.CreatedAt, &hit.Rank); err != nil {
			return result, err
		}
		hit.Snippet = knowledgeSnippet(body, q.Text, 280)
		hit.MatchKind = "lexical"
		hit.Reason = "本地全文/中文双字词匹配"
		if q.Recall {
			hit.Reason = "本地宽召回：词项重合"
		}
		if strings.TrimSpace(q.Text) == "" && q.Theme == "" {
			hit.Reason = "按更新时间列出"
		}
		result.Hits = append(result.Hits, hit)
	}
	return result, rows.Err()
}
func knowledgeSnippet(body, query string, limit int) string {
	rs := []rune(body)
	if len(rs) <= limit {
		return body
	}
	start := 0
	if idx := strings.Index(strings.ToLower(body), strings.ToLower(strings.TrimSpace(query))); idx > 0 {
		start = len([]rune(body[:idx])) - 40
		if start < 0 {
			start = 0
		}
	}
	end := start + limit
	if end > len(rs) {
		end = len(rs)
	}
	snippet := string(rs[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(rs) {
		snippet += "…"
	}
	return snippet
}

// RebuildKnowledgeSearch rebuilds derived projections transactionally, without remote calls.
func (s *Store) RebuildKnowledgeSearch(ctx context.Context) error {
	// The data selectors are the same ones used for migration backfill; trigger
	// definitions remain unchanged. Replaying only INSERT SELECT statements avoids
	// constructing a second, divergent projection in Go.
	migration, err := migrationsFS.ReadFile("migrations/0060_knowledge_search.sql")
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_search_docs`); err != nil {
		return err
	}
	for _, line := range strings.Split(string(migration), "\n") {
		if strings.HasPrefix(line, "INSERT INTO knowledge_search_docs(") {
			if _, err := tx.ExecContext(ctx, line); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_search_fts(knowledge_search_fts) VALUES('integrity-check')`); err != nil {
		return err
	}
	return tx.Commit()
}

func knowledgePhraseFilter(text string) (string, []any) {
	text = strings.ToLower(strings.TrimSpace(text))
	variants := append([]string{text}, knowledgeAliases[text]...)
	var groups []string
	var args []any
	for _, variant := range variants {
		words := strings.FieldsFunc(variant, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		var terms []string
		for _, word := range words {
			terms = append(terms, "instr(lower(d.title||' '||d.question||' '||d.body),?)>0")
			args = append(args, word)
		}
		if len(terms) > 0 {
			groups = append(groups, "("+strings.Join(terms, " AND ")+")")
		}
	}
	if len(groups) == 0 {
		return "", nil
	}
	return "(" + strings.Join(groups, " OR ") + ")", args
}

// KnowledgeSearchSource is a bounded source selector with human-readable labels.
type KnowledgeSearchSource struct{ SourceType, SourceID, Title string }

// ListKnowledgeSearchSources returns metadata only, never entire library bodies.
func (s *Store) ListKnowledgeSearchSources(ctx context.Context) ([]KnowledgeSearchSource, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT d.source_type,d.source_id,COALESCE(e.title,u.original_filename,doc.title,MAX(d.title)) FROM knowledge_search_docs d LEFT JOIN episodes e ON d.source_type='episode' AND e.id=d.source_id LEFT JOIN uploads u ON d.source_type='upload' AND u.id=d.source_id LEFT JOIN documents doc ON d.source_type='document' AND doc.id=d.source_id WHERE d.source_id!='' GROUP BY d.source_type,d.source_id ORDER BY MAX(d.updated_at) DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeSearchSource
	for rows.Next() {
		var source KnowledgeSearchSource
		if err := rows.Scan(&source.SourceType, &source.SourceID, &source.Title); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func knowledgeRecallQuery(text string) string {
	var terms []string
	for _, token := range strings.Fields(knowledgeTokens(text)) {
		if strings.HasPrefix(token, "h") && len(token) == 7 {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " OR ")
}
