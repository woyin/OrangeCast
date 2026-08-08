package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openRaw 打开一个不带迁移的 SQLite 库，用于构造"迁移前"的历史状态。
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestMigrate_FreshDB_AppliesAll 验证全新库按序应用全部迁移（0001/0002/0003）并登记到最新版本。
func TestMigrate_FreshDB_AppliesAll(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "fresh.db"))

	applied, err := Migrate(context.Background(), db)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	if len(applied) != len(want) {
		t.Fatalf("应应用 %v，实际 %v", want, applied)
	}
	for i := range want {
		if applied[i] != want[i] {
			t.Fatalf("应用顺序应为 %v，实际 %v", want, applied)
		}
	}
	// schema_migrations 已登记到最新版本
	v, _ := AppliedVersion(context.Background(), db)
	if v != 15 {
		t.Fatalf("AppliedVersion 应为 15，实际 %d", v)
	}
	// 关键表存在（含 schema_migrations）
	for _, tb := range []string{"users", "podcasts", "episodes", "transcripts",
		"analyses", "processing_jobs", "settings", "schema_migrations"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tb).Scan(&name); err != nil {
			t.Errorf("表 %s 未创建: %v", tb, err)
		}
	}
}

// TestMigrate_Idempotent 重复调用 Migrate 不应再次应用已记录迁移，也不应报错。
func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "idem.db"))

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	second, err := Migrate(context.Background(), db)
	if err != nil {
		t.Fatalf("第二次 Migrate 报错: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("重复 Migrate 不应再应用迁移，实际 %v", second)
	}
}

// TestMigrate_V01FixtureUpgrade 验证真实 v0.1.0 数据库可平滑升级：
// 数据计数、关联关系与登录凭据保持一致。这是 Phase 1 的核心退出条件。
func TestMigrate_V01FixtureUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v01.db")
	db := openRaw(t, path)

	// 1) 构造真实 v0.1.0 状态：用旧 schema.sql 建表，插入有计数的关联数据。
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("建立 v0.1 schema: %v", err)
	}
	// 1 user, 1 podcast, 2 episodes, 1 transcript, 1 analysis, 2 jobs, 1 settings
	seedV01Fixture(t, db)

	// 升级前快照计数
	before := countAll(t, db)

	// 2) 跑迁移：0001 幂等空跑（表已存在），0002/0003 完成单 Owner 收敛。
	applied, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	if len(applied) != 15 {
		t.Fatalf("应应用 15 条迁移，实际 %d", len(applied))
	}
	after := countAll(t, db)

	// 3) 数据计数完全保持
	for k, v := range before {
		if after[k] != v {
			t.Errorf("升级改变了 %s 计数：before=%d after=%d", k, v, after[k])
		}
	}

	// 4) 登录凭据保持可校验
	var email, hash string
	if err := db.QueryRow(`SELECT email, password_hash FROM users WHERE id='u1'`).Scan(&email, &hash); err != nil {
		t.Fatal(err)
	}
	if email != "owner@example.com" || hash != "$argon2id$fixture" {
		t.Errorf("凭据被篡改: email=%s hash=%s", email, hash)
	}

	// 5) 关联关系保持（episode 仍属于 podcast，FK 级联仍生效）
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE podcast_id='p1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("episode 关联计数应为 2，实际 %d", n)
	}
}

// TestMigrate_FailedMigration_SafeRetry 验证迁移失败时事务回滚、原库不被部分修改、可安全重试。
func TestMigrate_FailedMigration_SafeRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fail.db")
	db := openRaw(t, path)

	// 先正常应用全部迁移
	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// 注入一条必然失败的迁移：引用不存在的表 + 故意语法错（version=4，位于全部真实迁移之后）。
	bad := migration{version: 12, name: "0007_broken", up: "CREATE TABLE boom (id INTEGER); SELECT no_such_column FROM no_such_table;"}
	if err := applyOne(context.Background(), db, bad); err == nil {
		t.Fatal("期望失败迁移报错，实际成功")
	}

	// 原库不被部分修改：boom 表不应存在（事务回滚），schema_migrations 不应记录 version=5。
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='boom'`).Scan(&name); err != sql.ErrNoRows {
		t.Errorf("失败迁移不应留下中间表 boom: %v", err)
	}
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 15 {
		t.Errorf("失败迁移不应登记版本；应保持 15，实际 %d", v)
	}

	// 可安全重试：再次正常 Migrate 应保持 version=15 且不报错（无新迁移）。
	applied, err := Migrate(context.Background(), db)
	if err != nil {
		t.Fatalf("重试 Migrate 报错: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("重试不应应用新迁移，实际 %v", applied)
	}
}

// seedV01Fixture 构造真实 v0.1.0 数据：1 user / 1 podcast / 2 episodes / 1 transcript / 1 analysis / 2 jobs / 1 settings。
func seedV01Fixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	stmts := []string{
		`INSERT INTO users (id, email, password_hash) VALUES ('u1', 'owner@example.com', '$argon2id$fixture')`,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ('tok1', 'u1', '2099-01-01 00:00:00')`,
		`INSERT INTO podcasts (id, user_id, feed_url, title) VALUES ('p1', 'u1', 'https://feed.example.com/rss', 'Test Pod')`,
		`INSERT INTO episodes (id, user_id, podcast_id, guid, title, audio_url) VALUES ('e1','u1','p1','g1','Ep One','https://a.example.com/1.mp3')`,
		`INSERT INTO episodes (id, user_id, podcast_id, guid, title, audio_url) VALUES ('e2','u1','p1','g2','Ep Two','https://a.example.com/2.mp3')`,
		`INSERT INTO uploads (id, user_id, original_filename, content_type, size_bytes) VALUES ('up1','u1','talk.mp3','audio/mpeg',123456)`,
		`INSERT INTO transcripts (id, user_id, source_type, source_id, plain_text, segments_json) VALUES ('t1','u1','episode','e1','hello world','[]')`,
		`INSERT INTO analyses (id, user_id, source_type, source_id, title, summary, content_json) VALUES ('a1','u1','episode','e1','Title','Summary','{}')`,
		`INSERT INTO processing_jobs (id, user_id, source_type, source_id, job_type, status) VALUES ('j1','u1','episode','e1','transcribe','succeeded')`,
		`INSERT INTO processing_jobs (id, user_id, source_type, source_id, job_type, status) VALUES ('j2','u1','episode','e1','analyze','succeeded')`,
		`INSERT INTO usage_records (id, user_id, operation, provider) VALUES ('ur1','u1','transcription','groq')`,
		`INSERT INTO settings (user_id, active_provider) VALUES ('u1','groq')`,
		`INSERT INTO search_index (user_id, source_type, source_id, title, body) VALUES ('u1','episode','e1','Title','hello world')`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed fixture %q: %v", s, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// countAll 返回各业务表的行数，用于升级前后一致性比对。
func countAll(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	tables := []string{"users", "sessions", "podcasts", "episodes", "uploads",
		"transcripts", "analyses", "processing_jobs", "usage_records", "settings"}
	out := map[string]int{}
	for _, tb := range tables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tb).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tb, err)
		}
		out[tb] = n
	}
	return out
}

// TestHasPendingDestructiveMigration 验证破坏性迁移判定：
// 无迁移表 → pending；已应用 ≥2 → 无 pending；应用 <2 → pending。
func TestHasPendingDestructiveMigration(t *testing.T) {
	ctx := context.Background()

	// 全新库（无迁移表）→ pending
	raw := openRaw(t, filepath.Join(t.TempDir(), "raw.db"))
	pending, err := hasPendingDestructiveMigration(ctx, raw)
	if err != nil {
		t.Fatalf("hasPendingDestructiveMigration: %v", err)
	}
	if !pending {
		t.Error("无迁移表应判定 pending")
	}

	// 已应用 ≥2 → 无 pending
	s2 := newTestStore(t)
	if _, err := Migrate(ctx, s2.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pending, err = hasPendingDestructiveMigration(ctx, s2.DB)
	if err != nil {
		t.Fatalf("hasPendingDestructiveMigration: %v", err)
	}
	if pending {
		t.Error("已应用全部迁移应无 pending")
	}

	// 插入 version=1 记录 → pending（<2）
	s3 := newTestStore(t)
	if _, err := Migrate(ctx, s3.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 清空迁移表并插入 version=1
	if _, err := s3.DB.ExecContext(ctx, `DELETE FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.DB.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	pending, err = hasPendingDestructiveMigration(ctx, s3.DB)
	if err != nil {
		t.Fatalf("hasPendingDestructiveMigration: %v", err)
	}
	if !pending {
		t.Error("应用版本 <2 应判定 pending")
	}
}

// TestCountUsers 验证 CountUsers：无 users 表返回 0，有表返回行数。
func TestCountUsers(t *testing.T) {
	ctx := context.Background()
	// 无 users 表
	raw := openRaw(t, filepath.Join(t.TempDir(), "c1.db"))
	n, err := CountUsers(ctx, raw)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("无 users 表应返回 0，实际 %d", n)
	}

	// 有表但 0 行
	s := newTestStore(t)
	n, err = CountUsers(ctx, s.DB)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("空 users 表应返回 0，实际 %d", n)
	}
}

// TestRequireSafeForSingleOwner 验证单 Owner 守卫：1 用户通过、多用户拒绝。
func TestRequireSafeForSingleOwner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// 0 用户通过
	if err := RequireSafeForSingleOwner(ctx, s.DB); err != nil {
		t.Fatalf("0 用户应通过: %v", err)
	}
	// 1 用户通过
	if _, err := s.ClaimOwner(ctx, "a@b.com", "$argon2id$fakehash"); err != nil {
		t.Fatalf("ClaimOwner: %v", err)
	}
	if err := RequireSafeForSingleOwner(ctx, s.DB); err != nil {
		t.Fatalf("1 用户应通过: %v", err)
	}
	// 多用户拒绝
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ('u2','b@c.com','x')`); err != nil {
		t.Fatalf("插入第二个用户: %v", err)
	}
	if err := RequireSafeForSingleOwner(ctx, s.DB); err != ErrMultipleUsers {
		t.Errorf("多用户应 ErrMultipleUsers，实际 %v", err)
	}
}
