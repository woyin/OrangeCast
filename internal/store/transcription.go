package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// CommitSourceTranscription atomically selects the immutable response, invalidates
// old research evidence, admits its analysis continuation and saves the job result.
// A crash can leave a paid checkpoint, but cannot leave half of this application.
func (s *Store) CommitSourceTranscription(ctx context.Context, job *models.ProcessingJob, providerName, model string, result *provider.TranscriptResult) error {
	if job.SourceType != models.SourceEpisode && job.SourceType != models.SourceUpload {
		return ErrInvalidEditorialState
	}
	if result != nil && len(result.Segments) == 0 {
		return fmt.Errorf("转录模型未返回可定位的片段，已保留响应；处理节目须明确选择支持时间戳的模型")
	}
	if result == nil {
		return ErrInvalidEditorialState
	}
	payload, err := json.Marshal(provider.TranscriptPayload{Language: result.Language, Text: result.Text, Segments: result.Segments})
	if err != nil {
		return err
	}
	st, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	tc := provider.TaskConfig{Provider: ptrValue(st.AnalysisProvider), Model: ptrValue(st.AnalysisModel)}
	if tc.Provider == "" {
		tc.Provider = "groq"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, status, snapshot string
	if err := tx.QueryRowContext(ctx, `SELECT result_state,status,input_snapshot_json FROM processing_jobs WHERE id=?`, job.ID).Scan(&state, &status, &snapshot); err != nil {
		return err
	}
	if state == models.JobResultComplete {
		return nil
	}
	if state == models.JobResultUnknown || status != "queued" && status != "running" {
		return ErrInvalidEditorialState
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM artifact_versions WHERE source_type=? AND source_id=? AND kind='transcript'`, job.SourceType, job.SourceID).Scan(&version); err != nil {
		return fmt.Errorf("创建转录版本: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_versions(id,source_type,source_id,kind,version,provider,model,prompt_version,job_id,payload)VALUES(?,?,?,'transcript',?,?,?,'1',?,?)`, uuid.NewString(), job.SourceType, job.SourceID, version, providerName, model, job.ID, string(payload)); err != nil {
		return fmt.Errorf("创建转录版本: %w", err)
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET current_transcript_version=?,processing_status='transcribed' WHERE id=?`, sourceTable(job.SourceType)), version, job.SourceID)
	if err != nil {
		return fmt.Errorf("设置当前转录版本: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if _, err := s.reopenResolutionsTx(ctx, tx, `resolution_source_id=? AND resolution_source_type=? AND status='resolved' AND resolution_version<>?`, job.SourceID, job.SourceType, version); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM processing_jobs WHERE source_type=? AND source_id=? AND job_type='analyze' AND status IN('queued','running') LIMIT 1`, job.SourceType, job.SourceID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		intent := ""
		if snapshot != "" {
			intent = "ingest:transcription:" + job.ID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,is_automated,input_snapshot_json,intent_id,configured_provider,configured_model)VALUES(?,?,?,'analyze','queued',?,?,?,?,?)`, uuid.NewString(), job.SourceType, job.SourceID, boolToInt(job.Automated), snapshot, intent, tc.Provider, provider.EffectiveModel(tc.Provider, tc.Model, "analyze")); err != nil {
			return fmt.Errorf("入队分析: %w", err)
		}
	} else if err != nil {
		return err
	}
	resultJSON, _ := json.Marshal(map[string]any{"transcript_version": version})
	if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET result_state='complete',result_json=?,updated_at=datetime('now') WHERE id=?`, string(resultJSON), job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func ptrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
