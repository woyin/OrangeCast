// practice_reviews.go 练习问题仓储（K02 学习实践）。
// 问题是“来源的现场产物”：挂 Citation 到当前转录版本的 Segment；同一转录版本重新
// 生成时替换旧问题，不保留没有来源的孤儿行。Owner 自评与复习安排仍在 daily_reviews 一侧。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// ErrNoReviewQuestions 表示生成结果没有任何可核验的问题（全部引用无效）。
var ErrNoReviewQuestions = errors.New("没有可核验的练习问题")

// reviewQuestionColumns 所有读取共用的列顺序，避免 scan 顺序漂移。
const reviewQuestionColumns = "id, source_type, source_id, source_title, transcript_version, prompt, " +
	"answer_hint, citations_json, time_start, time_end, provider, model, job_id, created_at"

// SourceTitle 一张来源的身份与展示标题。
type SourceTitle struct {
	ID    string
	Title string
}

// ReplaceReviewQuestions 用一次生成结果替换某来源某转录版本的问题集合。
// citations 只保留当前版本中真实存在的 Segment（模型编造 ID 被丢弃），时间范围由程序解析。
func (s *Store) ReplaceReviewQuestions(ctx context.Context, sourceType models.SourceType, sourceID, sourceTitle string, transcriptVersion int, items []provider.PracticeItem, segments []provider.Segment, providerName, model, jobID string) ([]models.ReviewQuestion, error) {
	segMap := segmentMap(segments)
	type prepared struct {
		prompt, hint string
		ids          []string
		start, end   float64
	}
	var rows []prepared
	for _, item := range items {
		prompt := strings.TrimSpace(item.Question)
		if prompt == "" {
			continue
		}
		var ids []string
		for _, id := range item.Citations {
			if _, ok := segMap[id]; ok {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue // 无真实依据的问题不得入库
		}
		start, end, ok := provider.ResolveCitationSpan(ids, segments)
		if !ok {
			continue
		}
		rows = append(rows, prepared{prompt: prompt, hint: strings.TrimSpace(item.AnswerHint), ids: ids, start: start, end: end})
	}
	if len(rows) == 0 {
		return nil, ErrNoReviewQuestions
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM review_questions WHERE source_type=? AND source_id=? AND transcript_version=?",
		string(sourceType), sourceID, transcriptVersion); err != nil {
		return nil, fmt.Errorf("清理旧练习问题: %w", err)
	}
	for _, r := range rows {
		citations, _ := json.Marshal(r.ids)
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO review_questions "+
				"(id, source_type, source_id, source_title, transcript_version, prompt, answer_hint, "+
				"citations_json, relation_kind, time_start, time_end, provider, model, job_id) "+
				"VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			uuid.NewString(), string(sourceType), sourceID, sourceTitle, transcriptVersion, r.prompt, r.hint,
			string(citations), string(models.RelationCitation), r.start, r.end, providerName, model, jobID); err != nil {
			return nil, fmt.Errorf("写入练习问题: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListReviewQuestions(ctx, sourceType, sourceID)
}

// ListReviewQuestions 返回某来源当前的练习问题（最近生成在前）。
func (s *Store) ListReviewQuestions(ctx context.Context, sourceType models.SourceType, sourceID string) ([]models.ReviewQuestion, error) {
	return s.listReviewQuestions(ctx,
		"SELECT "+reviewQuestionColumns+" FROM review_questions WHERE source_type=? AND source_id=? "+
			"ORDER BY created_at DESC, rowid DESC",
		string(sourceType), sourceID)
}

// ListRecentReviewQuestions 跨来源列出最近生成的练习问题（学习首页“今天可以自测”）。
func (s *Store) ListRecentReviewQuestions(ctx context.Context, limit int) ([]models.ReviewQuestion, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	return s.listReviewQuestions(ctx,
		"SELECT "+reviewQuestionColumns+" FROM review_questions ORDER BY created_at DESC, rowid DESC LIMIT ?", limit)
}

// GetReviewQuestion 按 ID 读取一条练习问题。
func (s *Store) GetReviewQuestion(ctx context.Context, id string) (*models.ReviewQuestion, error) {
	list, err := s.listReviewQuestions(ctx, "SELECT "+reviewQuestionColumns+" FROM review_questions WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// ListSourcesWithTranscripts 列出拥有当前转录版本的来源（练习问题批量生成入口）。
func (s *Store) ListSourcesWithTranscripts(ctx context.Context, sourceType models.SourceType) ([]SourceTitle, error) {
	query := "SELECT e.id, e.title FROM episodes e " +
		"JOIN artifact_versions a ON a.source_type='episode' AND a.source_id=e.id AND a.kind='transcript' AND a.version=e.current_transcript_version " +
		"WHERE e.archived_at IS NULL ORDER BY e.published_at DESC, e.title"
	if sourceType == models.SourceUpload {
		query = "SELECT u.id, u.original_filename FROM uploads u " +
			"JOIN artifact_versions a ON a.source_type='upload' AND a.source_id=u.id AND a.kind='transcript' AND a.version=u.current_transcript_version " +
			"WHERE u.archived_at IS NULL ORDER BY u.created_at DESC"
	}
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceTitle
	for rows.Next() {
		var item SourceTitle
		if err := rows.Scan(&item.ID, &item.Title); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteReviewQuestionsForSource Purge 时删除该来源的全部练习问题。
func (s *Store) DeleteReviewQuestionsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		"DELETE FROM review_questions WHERE source_type=? AND source_id=?",
		string(sourceType), sourceID)
	return err
}

func (s *Store) listReviewQuestions(ctx context.Context, query string, args ...any) ([]models.ReviewQuestion, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ReviewQuestion
	for rows.Next() {
		q := models.ReviewQuestion{}
		var sourceType, citations string
		if err := rows.Scan(&q.ID, &sourceType, &q.SourceID, &q.SourceTitle, &q.TranscriptVersion,
			&q.Prompt, &q.AnswerHint, &citations, &q.TimeStart, &q.TimeEnd,
			&q.Provider, &q.Model, &q.JobID, &q.CreatedAt); err != nil {
			return nil, err
		}
		q.SourceType = models.SourceType(sourceType)
		if err := json.Unmarshal([]byte(citations), &q.Citations); err != nil {
			q.Citations = nil
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
