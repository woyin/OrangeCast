package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/models"
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

// seedUser 认领 Owner 并返回（仅当实例未认领时有效）。
func seedUser(t *testing.T, s *Store, email string) *models.User {
	t.Helper()
	u, err := s.ClaimOwner(context.Background(), email, "$argon2id$fakehash")
	if err != nil {
		t.Fatalf("认领 Owner: %v", err)
	}
	return u
}

func TestOpen_InitializesSchema(t *testing.T) {
	s := newTestStore(t)
	// 验证关键表存在
	tables := []string{"users", "sessions", "podcasts", "episodes", "uploads",
		"transcripts", "analyses", "processing_jobs", "usage_records", "settings", "search_index", "schema_migrations"}
	for _, tb := range tables {
		var name string
		err := s.DB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tb).Scan(&name)
		if err != nil {
			t.Errorf("表 %s 未创建: %v", tb, err)
		}
	}
}

func TestOpen_NoUserIDInContentTables(t *testing.T) {
	s := newTestStore(t)
	// 内容表不应再有 user_id 列（ADR-0007）
	for _, tb := range []string{"podcasts", "episodes", "uploads", "transcripts", "analyses", "processing_jobs", "usage_records"} {
		var n int
		err := s.DB.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='user_id'`, tb).Scan(&n)
		if err != nil {
			t.Fatalf("检查 %s 列: %v", tb, err)
		}
		if n != 0 {
			t.Errorf("表 %s 仍包含 user_id 列（应已移除）", tb)
		}
	}
}

func TestClaimOwner_OnlyOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.ClaimOwner(ctx, "a@b.com", "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOwner(ctx, "c@d.com", "h2"); err != ErrOwnerExists {
		t.Errorf("第二次认领应返回 ErrOwnerExists，实际 %v", err)
	}
	// 仍只有一个用户
	n, _ := CountUsers(ctx, s.DB)
	if n != 1 {
		t.Errorf("用户数应为 1，实际 %d", n)
	}
}

func TestClaimOwner_AtomicallySingleOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 并发认领：最多一个成功
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := s.ClaimOwner(ctx, "x@b.com", "h")
			done <- err
		}()
	}
	ok := 0
	for i := 0; i < 8; i++ {
		if err := <-done; err == nil {
			ok++
		} else if err != ErrOwnerExists {
			t.Errorf("意外错误: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("并发认领应恰好 1 个成功，实际 %d", ok)
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

func TestGetSettings_SingletonDefault(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.TranscriptionModel != nil || st.AnalysisModel != nil || st.QAModel != nil {
		t.Error("默认设置不应有自定义模型")
	}
}

func TestUpdateSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tm := "whisper-large-v3"
	if err := s.UpdateSettings(ctx, &tm, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := s.GetSettings(ctx)
	if st.TranscriptionModel == nil || *st.TranscriptionModel != tm {
		t.Errorf("转录模型应为 %s，实际 %v", tm, st.TranscriptionModel)
	}
}
