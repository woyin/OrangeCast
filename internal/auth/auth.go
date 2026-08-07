package auth

import (
	"errors"
	"strings"

	"github.com/alexedwards/argon2id"
)

var (
	ErrInvalidEmail     = errors.New("invalid email")
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
)

// NormalizeEmail 规范化邮箱（小写 + 去首尾空格）。email 唯一约束依赖规范化一致性。
func NormalizeEmail(email string) string {
	email = strings.TrimSpace(email)
	return strings.ToLower(email)
}

// ValidateEmail 简单校验邮箱格式：含 @ 和 .，且有非空本地部分与域名。
func ValidateEmail(email string) error {
	at := strings.Index(email, "@")
	if at <= 0 || at == len(email)-1 { // @ 不在首位/末位
		return ErrInvalidEmail
	}
	if !strings.Contains(email[at+1:], ".") || len(email) < 5 {
		return ErrInvalidEmail
	}
	return nil
}

// ValidatePassword 密码长度校验。
func ValidatePassword(pw string) error {
	if len(pw) < 8 {
		return ErrPasswordTooShort
	}
	return nil
}

// HashPassword 用 argon2id 哈希密码（Go 现代最佳实践，抗 GPU/ASIC）。
func HashPassword(pw string) (string, error) {
	return argon2id.CreateHash(pw, argon2id.DefaultParams)
}

// VerifyPassword 校验密码与哈希。
func VerifyPassword(pw, hash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(pw, hash)
}
