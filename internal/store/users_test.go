
package store

import (
	"context"
	"testing"
)

// TestUsers_DBErrors 验证 users/sessions 系列查询在表缺失时返回错误。
// 覆盖 ClaimOwner/GetUserByEmail/GetUserByID/CreateSession/GetSessionByToken 错误分支。
func TestUsers_DBErrors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 先删 sessions 表测试会话错误，再删 users 表测试用户错误
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, "u1", "2027-01-01"); err == nil {
		t.Error("sessions 表缺失时 CreateSession 应报错")
	}
	if _, err := s.GetSessionByToken(ctx, "tok"); err == nil {
		t.Error("sessions 表缺失时 GetSessionByToken 应报错")
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE users`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOwner(ctx, "a@b.com", "$argon2id$x"); err == nil {
		t.Error("users 表缺失时 ClaimOwner 应报错")
	}
	if _, err := s.GetUserByEmail(ctx, "a@b.com"); err == nil {
		t.Error("users 表缺失时 GetUserByEmail 应报错")
	}
	if _, err := s.GetUserByID(ctx, "u1"); err == nil {
		t.Error("users 表缺失时 GetUserByID 应报错")
	}
}

// TestGetSettings_InsertError 验证默认设置创建失败时报错。
// 覆盖 GetSettings 中 "创建默认设置" 错误分支（INSERT 被触发器中止）。
func TestGetSettings_InsertError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM settings`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER abort_settings BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT,'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSettings(ctx); err == nil {
		t.Fatal("settings INSERT 被中止时 GetSettings 应报错")
	}
}

// TestGetSettings_QueryError 验证查询失败时报错。
// 覆盖 GetSettings 中非 ErrNoRows 查询错误分支（关闭 DB）。
func TestGetSettings_QueryError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Close()
	if _, err := s.GetSettings(ctx); err == nil {
		t.Fatal("关闭 DB 时 GetSettings 应报错")
	}
}
