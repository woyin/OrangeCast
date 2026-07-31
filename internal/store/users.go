package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/breestealth/wisepod/internal/models"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("not found")

// CreateUser 创建用户。email 会被调用方规范化。
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (*models.User, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES (?, ?, ?)`,
		id, email, passwordHash)
	if err != nil {
		return nil, fmt.Errorf("创建用户: %w", err)
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

// GetOrCreateSettings 获取用户设置，不存在则创建默认。
func (s *Store) GetOrCreateSettings(ctx context.Context, userID string) (*models.Settings, error) {
	st := &models.Settings{UserID: userID, ActiveProvider: "groq"}
	err := s.DB.QueryRowContext(ctx,
		`SELECT active_provider, transcription_model, analysis_model, qa_model
		 FROM settings WHERE user_id = ?`, userID).
		Scan(&st.ActiveProvider, &st.TranscriptionModel, &st.AnalysisModel, &st.QAModel)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO settings (user_id, active_provider) VALUES (?, 'groq')`, userID)
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

// UpdateActiveProvider 运行时实时切换 provider（worker 取任务时读取此值）。
func (s *Store) UpdateActiveProvider(ctx context.Context, userID, provider string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (user_id, active_provider) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET active_provider = excluded.active_provider, updated_at = datetime('now')`,
		userID, provider)
	return err
}
