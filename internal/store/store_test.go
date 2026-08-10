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
	tp := "groq"
	st := &models.Settings{TranscriptionModel: &tm, TranscriptionProvider: &tp}
	if err := s.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSettings(ctx)
	if got.TranscriptionModel == nil || *got.TranscriptionModel != tm {
		t.Errorf("转录模型应为 %s，实际 %v", tm, got.TranscriptionModel)
	}
	if got.TranscriptionProvider == nil || *got.TranscriptionProvider != tp {
		t.Errorf("转录 Provider 应为 %s，实际 %v", tp, got.TranscriptionProvider)
	}
}

// TestGetUserByID 验证按 ID 查用户与未知 ID 的 ErrNotFound。
func TestGetUserByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil || got.Email != "a@b.com" {
		t.Fatalf("GetUserByID: %v %+v", err, got)
	}
	if _, err := s.GetUserByID(ctx, "nope"); err != ErrNotFound {
		t.Errorf("未知 ID 应 ErrNotFound，实际 %v", err)
	}
}

// TestDeleteExpiredSessions 验证过期会话被清理、未过期保留。
func TestDeleteExpiredSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "a@b.com")

	// 过期会话
	expired, _ := s.CreateSession(ctx, u.ID, "2000-01-01T00:00:00Z")
	// 未过期会话
	active, _ := s.CreateSession(ctx, u.ID, "2099-01-01T00:00:00Z")

	if err := s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	// 过期 token 查不到
	if _, err := s.GetSessionByToken(ctx, expired); err != ErrNotFound {
		t.Errorf("过期会话应被清理，实际 err=%v", err)
	}
	// 未过期 token 仍有效
	if _, err := s.GetSessionByToken(ctx, active); err != nil {
		t.Errorf("未过期会话应保留，err=%v", err)
	}
}

// TestOpen_InvalidPath 验证 Open 对非法数据库路径报错。
func TestOpen_InvalidPath(t *testing.T) {
	// 路径指向一个目录 → sqlite 无法打开
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Fatal("非法数据库路径应报错")
	}
}

// TestGetUserByEmail 验证按邮箱查用户（命中与 ErrNotFound）。
func TestGetUserByEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, s, "find@example.com")

	got, err := s.GetUserByEmail(ctx, "find@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("应返回匹配用户，实际 %+v", got)
	}
	if _, err := s.GetUserByEmail(ctx, "missing@example.com"); err != ErrNotFound {
		t.Errorf("不存在的邮箱应 ErrNotFound，实际 %v", err)
	}
}

// TestOpen_MigrateFails 验证 Open 时迁移失败返回错误。
// 覆盖 Open 中 "执行迁移" 分支（迁移表创建失败场景）。
func TestOpen_MigrateFails(t *testing.T) {
	dir := t.TempDir()
	// 构造一个已有 v0.1 schema 但 schema_migrations 表损坏的库
	db := openRaw(t, filepath.Join(dir, "broken.db"))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	// 预先创建一个 schema_migrations 表但结构错误 → 迁移时 INSERT 失败
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (wrong_col TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := Open(filepath.Join(dir, "broken.db")); err == nil {
		t.Fatal("损坏的 schema_migrations 应导致迁移失败")
	}
}
