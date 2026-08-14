package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
)

// ErrNotFound 表示查询的记录不存在（各仓储统一的 Not Found 错误）。
var ErrNotFound = errors.New("not found")

// ErrOwnerExists 表示实例已被认领，不能再创建第二个 Owner（ADR-0003）。
var ErrOwnerExists = errors.New("实例已被认领")

// ClaimOwner 首次认领唯一 Owner：仅当 users 表为空时插入，返回新 Owner。
// 认领是原子的（INSERT ... SELECT ... WHERE NOT EXISTS），并发下不会产生第二个 Owner。
func (s *Store) ClaimOwner(ctx context.Context, email, passwordHash string) (*models.User, error) {
	id := uuid.NewString()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash)
		 SELECT ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM users)`,
		id, email, passwordHash)
	if err != nil {
		return nil, fmt.Errorf("认领 Owner: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrOwnerExists
	}
	return s.GetUserByEmail(ctx, email)
}

// GetUserByEmail 按 email 查用户。
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByID 按 id 查用户。
func (s *Store) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateSession 创建会话。
func (s *Store) CreateSession(ctx context.Context, userID, expiresAt string) (string, error) {
	token := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("创建会话: %w", err)
	}
	return token, nil
}

// GetSessionByToken 按 token 查会话，返回未过期的会话对应的 userID。
func (s *Store) GetSessionByToken(ctx context.Context, token string) (string, error) {
	var userID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token = ? AND expires_at > datetime('now')`, token).
		Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return userID, nil
}

// DeleteSession 删除会话（登出）。
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteExpiredSessions 清理过期会话。
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= datetime('now')`)
	return err
}

// GetSettings 读取实例级单例设置；不存在则创建默认单行（id=1）。
func (s *Store) GetSettings(ctx context.Context) (*models.Settings, error) {
	st := &models.Settings{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT transcription_model, analysis_model, highlight_model, qa_model,
		        writer_model, scout_model, curator_model, evidence_reviewer_model, style_editor_model,
		        transcription_provider, analysis_provider, highlight_provider, qa_provider,
		        writer_provider, scout_provider, curator_provider, evidence_reviewer_provider, style_editor_provider,
		        groq_api_key, groq_base_url, openai_api_key, openai_base_url
		 FROM settings WHERE id = 1`).
		Scan(&st.TranscriptionModel, &st.AnalysisModel, &st.HighlightModel, &st.QAModel,
			&st.WriterModel, &st.ScoutModel, &st.CuratorModel, &st.EvidenceReviewerModel, &st.StyleEditorModel,
			&st.TranscriptionProvider, &st.AnalysisProvider, &st.HighlightProvider, &st.QAProvider,
			&st.WriterProvider, &st.ScoutProvider, &st.CuratorProvider, &st.EvidenceReviewerProvider, &st.StyleEditorProvider,
			&st.GroqAPIKey, &st.GroqBaseURL, &st.OpenAIAPIKey, &st.OpenAIBaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO settings (id) VALUES (1)`)
		if err != nil {
			return nil, fmt.Errorf("创建默认设置: %w", err)
		}
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

// UpdateSettings 更新实例级 Provider + Model 配置。
func (s *Store) UpdateSettings(ctx context.Context, st *models.Settings) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (id, transcription_model, analysis_model, highlight_model, qa_model,
		    writer_model, scout_model, curator_model, evidence_reviewer_model, style_editor_model,
		    transcription_provider, analysis_provider, highlight_provider, qa_provider,
		    writer_provider, scout_provider, curator_provider, evidence_reviewer_provider, style_editor_provider,
		    groq_api_key, groq_base_url, openai_api_key, openai_base_url)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   transcription_model = excluded.transcription_model,
		   analysis_model = excluded.analysis_model,
		   highlight_model = excluded.highlight_model,
		   qa_model = excluded.qa_model,
		   writer_model = excluded.writer_model,
		   scout_model = excluded.scout_model,
		   curator_model = excluded.curator_model,
		   evidence_reviewer_model = excluded.evidence_reviewer_model,
		   style_editor_model = excluded.style_editor_model,
		   transcription_provider = excluded.transcription_provider,
		   analysis_provider = excluded.analysis_provider,
		   highlight_provider = excluded.highlight_provider,
		   qa_provider = excluded.qa_provider,
		   writer_provider = excluded.writer_provider,
		   scout_provider = excluded.scout_provider,
		   curator_provider = excluded.curator_provider,
		   evidence_reviewer_provider = excluded.evidence_reviewer_provider,
		   style_editor_provider = excluded.style_editor_provider,
		   groq_api_key = excluded.groq_api_key,
		   groq_base_url = excluded.groq_base_url,
		   openai_api_key = excluded.openai_api_key,
		   openai_base_url = excluded.openai_base_url,
		   updated_at = datetime('now')`,
		st.TranscriptionModel, st.AnalysisModel, st.HighlightModel, st.QAModel,
		st.WriterModel, st.ScoutModel, st.CuratorModel, st.EvidenceReviewerModel, st.StyleEditorModel,
		st.TranscriptionProvider, st.AnalysisProvider, st.HighlightProvider, st.QAProvider,
		st.WriterProvider, st.ScoutProvider, st.CuratorProvider, st.EvidenceReviewerProvider, st.StyleEditorProvider,
		st.GroqAPIKey, st.GroqBaseURL, st.OpenAIAPIKey, st.OpenAIBaseURL)
	return err
}
