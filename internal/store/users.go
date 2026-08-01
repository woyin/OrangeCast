package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/google/uuid"
)

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
		`SELECT transcription_model, analysis_model, qa_model FROM settings WHERE id = 1`).
		Scan(&st.TranscriptionModel, &st.AnalysisModel, &st.QAModel)
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

// UpdateSettings 更新实例级模型偏好。
func (s *Store) UpdateSettings(ctx context.Context, transcriptionModel, analysisModel, qaModel *string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (id, transcription_model, analysis_model, qa_model)
		 VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   transcription_model = excluded.transcription_model,
		   analysis_model = excluded.analysis_model,
		   qa_model = excluded.qa_model,
		   updated_at = datetime('now')`,
		transcriptionModel, analysisModel, qaModel)
	return err
}
