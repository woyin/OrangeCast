// translations.go 对齐译文仓储（Translation，CitedDerivative）。
// 译文身份 = 来源 + 转录版本 + 目标语言：重新翻译同一版本会替换该语言的旧译文，
// 不同版本各自保留，原语言 Transcript 永不被覆盖。
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// ReplaceSegmentTranslations 用一次翻译结果替换某来源某转录版本某语言的译文集合。
// 只写入真实存在的 Segment；未译片段保持缺行（页面回退显示原文并标为未译）。
func (s *Store) ReplaceSegmentTranslations(ctx context.Context, sourceType models.SourceType, sourceID string, transcriptVersion int, targetLanguage string, rows []provider.TranslatedSegment, segments []provider.Segment, providerName, model, jobID string) (int, error) {
	lang := strings.TrimSpace(targetLanguage)
	if lang == "" {
		return 0, fmt.Errorf("译文必须指定目标语言")
	}
	valid := make(map[string]bool, len(segments))
	for _, seg := range segments {
		valid[seg.ID] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM segment_translations WHERE source_type=? AND source_id=? AND transcript_version=? AND target_language=?",
		string(sourceType), sourceID, transcriptVersion, lang); err != nil {
		return 0, fmt.Errorf("清理旧译文: %w", err)
	}
	written := 0
	for _, row := range rows {
		text := strings.TrimSpace(row.Text)
		if !valid[row.ID] || text == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO segment_translations (source_type, source_id, transcript_version, target_language, segment_id, text, provider, model, job_id) "+
				"VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(source_type, source_id, transcript_version, target_language, segment_id) "+
				"DO UPDATE SET text=excluded.text, provider=excluded.provider, model=excluded.model, job_id=excluded.job_id",
			string(sourceType), sourceID, transcriptVersion, lang, row.ID, text, providerName, model, jobID); err != nil {
			return 0, fmt.Errorf("写入译文: %w", err)
		}
		written++
	}
	if written == 0 {
		return 0, fmt.Errorf("没有可写入的译文")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// ListSegmentTranslations 读取某来源某目标语言的译文（当前转录版本）。
func (s *Store) ListSegmentTranslations(ctx context.Context, sourceType models.SourceType, sourceID string, transcriptVersion int, targetLanguage string) (map[string]models.SegmentTranslation, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT source_type, source_id, transcript_version, target_language, segment_id, text, provider, model, job_id, created_at "+
			"FROM segment_translations WHERE source_type=? AND source_id=? AND transcript_version=? AND target_language=?",
		string(sourceType), sourceID, transcriptVersion, targetLanguage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]models.SegmentTranslation{}
	for rows.Next() {
		t := models.SegmentTranslation{}
		var sourceTypeRaw string
		if err := rows.Scan(&sourceTypeRaw, &t.SourceID, &t.TranscriptVersion, &t.TargetLanguage, &t.SegmentID, &t.Text, &t.Provider, &t.Model, &t.JobID, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.SourceType = models.SourceType(sourceTypeRaw)
		out[t.SegmentID] = t
	}
	return out, rows.Err()
}

// ListTranslationLanguages 返回某来源已生成译文的语言（按最近生成排序）。
func (s *Store) ListTranslationLanguages(ctx context.Context, sourceType models.SourceType, sourceID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT target_language FROM segment_translations WHERE source_type=? AND source_id=? GROUP BY target_language ORDER BY MAX(created_at) DESC",
		string(sourceType), sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			return nil, err
		}
		out = append(out, lang)
	}
	return out, rows.Err()
}

// DeleteSegmentTranslationsForSource Purge 时删除该来源的全部译文。
func (s *Store) DeleteSegmentTranslationsForSource(ctx context.Context, sourceType models.SourceType, sourceID string) error {
	_, err := s.DB.ExecContext(ctx,
		"DELETE FROM segment_translations WHERE source_type=? AND source_id=?",
		string(sourceType), sourceID)
	return err
}
