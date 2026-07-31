package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/breestealth/wisepod/internal/models"
)

// newTestStore 每个测试用独立的临时 SQLite 库。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedUser 创建并返回一个测试用户。
func seedUser(t *testing.T, s *Store, email string) *models.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), email, "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("创建用户: %v", err)
	}
	return u
}

func TestOpen_InitializesSchema(t *testing.T) {
	s := newTestStore(t)
	// 验证关键表存在
	tables := []string{"users", "sessions", "podcasts", "episodes", "uploads",
		"transcripts", "analyses", "processing_jobs", "usage_records", "settings", "search_index"}
	for _, tb := range tables {
		var name string
		err := s.DB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tb).Scan(&name)
		if err != nil {
			t.Errorf("表 %s 未创建: %v", tb, err)
		}
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "a@b.com", "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "a@b.com", "h2"); err == nil {
		t.Error("重复 email 应失败")
	}
}

func TestSession_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	token, err := s.CreateSession(ctx, u.ID, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSessionByToken(ctx, token); err != nil {
		t.Errorf("有效 session 应能查到: %v", err)
	}
	if err := s.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSessionByToken(ctx, token); err != ErrNotFound {
		t.Error("删除后应查不到")
	}
}

func TestSession_ExpiredRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")
	token, _ := s.CreateSession(ctx, u.ID, "2000-01-01T00:00:00Z") // 已过期
	if _, err := s.GetSessionByToken(ctx, token); err != ErrNotFound {
		t.Error("过期 session 应被拒绝")
	}
}

func TestGetOrCreateSettings_DefaultGroq(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	st, err := s.GetOrCreateSettings(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveProvider != "groq" {
		t.Errorf("默认 provider 应为 groq，实际 %s", st.ActiveProvider)
	}
}

func TestUpdateActiveProvider(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	if err := s.UpdateActiveProvider(ctx, u.ID, "openai"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.GetOrCreateSettings(ctx, u.ID)
	if st.ActiveProvider != "openai" {
		t.Errorf("切换后应为 openai，实际 %s", st.ActiveProvider)
	}
	// 再次切回应覆盖
	if err := s.UpdateActiveProvider(ctx, u.ID, "groq"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.GetOrCreateSettings(ctx, u.ID)
	if st.ActiveProvider != "groq" {
		t.Errorf("切回应为 groq，实际 %s", st.ActiveProvider)
	}
}
