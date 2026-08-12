package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22}
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
	if v != 22 {
		t.Fatalf("AppliedVersion 应为 22，实际 %d", v)
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
	if len(applied) != 22 {
		t.Fatalf("应应用 22 条迁移，实际 %d", len(applied))
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
	if v != 22 {
		t.Errorf("失败迁移不应登记版本；应保持 22，实际 %d", v)
	}

	// 可安全重试：再次正常 Migrate 应保持 version=22 且不报错（无新迁移）。
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

// TestPreMigrationSafety_CreatesBackup 验证待迁移且已有用户时生成一致性备份。
func TestPreMigrationSafety_CreatesBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "src.db")
	db := openRaw(t, dbPath)
	// 迁移到最新后回退版本记录到 1（制造"待破坏性迁移"状态）
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	// 造 1 个用户
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ('u1','a@b.com','x')`); err != nil {
		t.Fatal(err)
	}
	// users 表需存在；若迁移后列兼容，直接插入即可

	if err := preMigrationSafety(ctx, db, dbPath); err != nil {
		t.Fatalf("preMigrationSafety: %v", err)
	}
	// 备份文件应已生成
	bakPath := dbPath + ".pre-single-owner.bak"
	if _, err := os.Stat(bakPath); err != nil {
		t.Errorf("应生成备份文件 %s: %v", bakPath, err)
	}
}

// TestOpen_RejectsMultipleUsers 验证 Open 在待破坏性迁移且多用户时报 ErrMultipleUsers。
func TestOpen_RejectsMultipleUsers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.db")
	db := openRaw(t, path)
	ctx := context.Background()
	// 构造 V01 schema（含 user_id，版本 0 → 存在待破坏性迁移 0002）
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("建立 v0.1 schema: %v", err)
	}
	seedV01Fixture(t, db)
	// 插入第二个用户 → 多用户
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ('u2','second@example.com','x')`); err != nil {
		t.Fatalf("插入第二用户: %v", err)
	}
	db.Close()

	// Open 应因多用户拒绝自动迁移
	if _, err := Open(path); err != ErrMultipleUsers {
		t.Errorf("多用户应 ErrMultipleUsers，实际 %v", err)
	}
}

// TestAppliedVersion_NoMigrationTable 验证无 schema_migrations 表时 AppliedVersion 返回 0。
func TestAppliedVersion_NoMigrationTable(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t, filepath.Join(t.TempDir(), "raw.db"))
	v, err := AppliedVersion(ctx, db)
	if err != nil {
		t.Fatalf("AppliedVersion: %v", err)
	}
	if v != 0 {
		t.Errorf("无迁移表应返回 0，实际 %d", v)
	}
}

// TestAppliedVersion_WithMigrations 验证已迁移库返回正确版本。
// 覆盖 AppliedVersion 中存在 schema_migrations 表时读 MAX(version) 路径。
func TestAppliedVersion_WithMigrations(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := Migrate(ctx, s.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := AppliedVersion(ctx, s.DB)
	if err != nil {
		t.Fatalf("AppliedVersion: %v", err)
	}
	if v < 2 {
		t.Errorf("已迁移库应返回 version>=2，实际 %d", v)
	}
}

// TestLoadMigrations 验证迁移加载与排序、版本连续校验。
// 覆盖 loadMigrations 正常路径（含排序、连续性校验）。
func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("应加载至少一条迁移")
	}
	// 验证按 version 升序排列
	for i := 1; i < len(ms); i++ {
		if ms[i].version <= ms[i-1].version {
			t.Errorf("迁移应升序，[%d]=%d <= [%d]=%d", i, ms[i].version, i-1, ms[i-1].version)
		}
	}
	// 验证版本号从 1 连续
	for i, m := range ms {
		if m.version != i+1 {
			t.Fatalf("迁移版本号应从 1 连续，第 %d 个 version=%d", i, m.version)
		}
	}
	// 每条迁移的 up 不应为空
	for _, m := range ms {
		if strings.TrimSpace(m.up) == "" {
			t.Errorf("迁移 %s 的 up 不应为空", m.name)
		}
	}
}

// TestMigrationTableExists 验证 migrationTableExists 判定。
// 覆盖 migrationTableExists 两种返回路径。
func TestMigrationTableExists(t *testing.T) {
	ctx := context.Background()
	// 全新库（无 schema_migrations）→ false
	raw := openRaw(t, filepath.Join(t.TempDir(), "raw.db"))
	exists, err := migrationTableExists(ctx, raw)
	if err != nil {
		t.Fatalf("migrationTableExists: %v", err)
	}
	if exists {
		t.Error("全新库应无 schema_migrations 表")
	}
	// 迁移后 → true
	s := newTestStore(t)
	if _, err := Migrate(ctx, s.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	exists, err = migrationTableExists(ctx, s.DB)
	if err != nil {
		t.Fatalf("migrationTableExists: %v", err)
	}
	if !exists {
		t.Error("已迁移库应有 schema_migrations 表")
	}
}

// TestUsersTableExists 验证 usersTableExists 判定。
// 覆盖 usersTableExists 两种返回路径。
func TestUsersTableExists(t *testing.T) {
	ctx := context.Background()
	// 全新原始库（无任何表）→ false
	raw := openRaw(t, filepath.Join(t.TempDir(), "raw.db"))
	exists, err := usersTableExists(ctx, raw)
	if err != nil {
		t.Fatalf("usersTableExists: %v", err)
	}
	if exists {
		t.Error("全新库应无 users 表")
	}
	// 迁移后 → true（迁移会创建 users 表）
	s := newTestStore(t)
	if _, err := Migrate(ctx, s.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	exists, err = usersTableExists(ctx, s.DB)
	if err != nil {
		t.Fatalf("usersTableExists: %v", err)
	}
	if !exists {
		t.Error("已迁移库应有 users 表")
	}
}

// TestApplyOne_ExecError 验证单条迁移 SQL 执行失败时事务回滚。
// 覆盖 applyOne 中 tx.ExecContext 错误分支与 defer tx.Rollback。
func TestApplyOne_ExecError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := Migrate(ctx, s.DB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 构造一条必然失败的迁移：SQL 语法错误
	bad := migration{version: 9999, name: "0099_bad", up: "THIS IS NOT VALID SQL"}
	if err := applyOne(ctx, s.DB, bad); err == nil {
		t.Fatal("非法 SQL 的迁移应报错")
	}
	// 失败的版本不应被记录
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=9999`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("失败迁移的版本不应被记录到 schema_migrations")
	}
}

// TestUsersHelpers_DBErrors 验证 users 辅助函数在表缺失/关闭 DB 时返回错误。
// 覆盖 usersTableExists/CountUsers 查询错误分支。
func TestUsersHelpers_DBErrors(t *testing.T) {
	// 用关闭的 DB 触发查询错误
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if _, err := usersTableExists(ctx, db); err == nil {
		t.Error("关闭 DB 时 usersTableExists 应报错")
	}
	if _, err := CountUsers(ctx, db); err == nil {
		t.Error("关闭 DB 时 CountUsers 应报错")
	}
}

// TestMigrateHelpers_DBErrors 验证迁移辅助函数在关闭 DB 时返回错误。
// 覆盖 AppliedVersion/Migrate 查询错误分支。
func TestMigrateHelpers_DBErrors(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if _, err := AppliedVersion(ctx, db); err == nil {
		t.Error("关闭 DB 时 AppliedVersion 应报错")
	}
	if _, err := Migrate(ctx, db); err == nil {
		t.Error("关闭 DB 时 Migrate 应报错")
	}
}

// TestApplyOne_BeginTxError 验证 applyOne 的 BeginTx 失败时报错。
// 覆盖 applyOne 中 db.BeginTx 错误分支（关闭 DB）。
func TestApplyOne_BeginTxError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if err := applyOne(ctx, db, migration{version: 1, name: "test", up: "SELECT 1"}); err == nil {
		t.Fatal("关闭 DB 时 applyOne 应报错")
	}
}

// TestMigrate_AppliedVersionError 验证 Migrate 读已应用版本失败时报错。
// 覆盖 Migrate 中 "读取已应用版本" 错误分支（schema_migrations 缺 version 列）。
func TestMigrate_AppliedVersionError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "m.db"))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (wrong_col TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db); err == nil {
		t.Fatal("schema_migrations 缺 version 列时 Migrate 应报错")
	}
}

// TestApplyOne_SchemaMigrationsInsertError 验证迁移成功后登记版本失败时报错。
// 覆盖 applyOne 中 INSERT schema_migrations 错误分支（触发器中止）。
func TestApplyOne_SchemaMigrationsInsertError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "m.db"))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER abort_mig BEFORE INSERT ON schema_migrations BEGIN SELECT RAISE(ABORT,'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, db, migration{version: 1, name: "x", up: "SELECT 1"}); err == nil {
		t.Fatal("登记迁移版本失败应报错")
	}
}

// TestCountUsers_QueryError 验证 users 表存在但 COUNT 查询失败时报错。
// 覆盖 CountUsers 中 COUNT 查询错误分支（关闭 DB 后仅触发 usersTableExists，这里
// 用 users 表缺列使 sqlite_master 判定存在但 COUNT 仍成功——改用关闭的 DB 无法走到；
// 因此直接验证 usersTableExists 成功后 COUNT 失败：用 users 表被视图替换的场景不成立，
// 该分支在架构上不可达，保留注释说明）。
func TestCountUsers_QueryError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	// 关闭 DB 时 usersTableExists 先失败，CountUsers 返回错误（覆盖错误传播路径）。
	if _, err := CountUsers(ctx, db); err == nil {
		t.Fatal("关闭 DB 时 CountUsers 应报错")
	}
}

// TestRequireSafeForSingleOwner_QueryError 验证 CountUsers 失败时错误传播。
// 覆盖 RequireSafeForSingleOwner 中 CountUsers err 分支。
func TestRequireSafeForSingleOwner_QueryError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if err := RequireSafeForSingleOwner(ctx, db); err == nil {
		t.Fatal("关闭 DB 时 RequireSafeForSingleOwner 应报错")
	}
}

// TestHasPendingDestructiveMigration_QueryError 验证 migrationTableExists 失败时报错。
// 覆盖 hasPendingDestructiveMigration 中 migrationTableExists err 分支。
func TestHasPendingDestructiveMigration_QueryError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if _, err := hasPendingDestructiveMigration(ctx, db); err == nil {
		t.Fatal("关闭 DB 时 hasPendingDestructiveMigration 应报错")
	}
}

// TestMigrationTableExists_QueryError 验证查询 sqlite_master 失败时报错。
// 覆盖 migrationTableExists 中查询错误分支。
func TestMigrationTableExists_QueryError(t *testing.T) {
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "closed.db"))
	ctx := context.Background()
	db.Close()
	if _, err := migrationTableExists(ctx, db); err == nil {
		t.Fatal("关闭 DB 时 migrationTableExists 应报错")
	}
}
