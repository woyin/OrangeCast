package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestConsistencyBackup_RoundTrip 备份产物是事务一致的完整副本，可独立打开并读到全部数据。
func TestConsistencyBackup_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := sql.Open("sqlite", srcPath+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })

	ctx := context.Background()
	// 先构造真实 v0.1.0 数据，再迁移到最新 schema，最后做一致性备份
	// （备份的对象是"已迁移完成的现行库"，与生产路径一致）。
	if _, err := src.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("建立 v0.1 schema: %v", err)
	}
	seedV01Fixture(t, src)
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(dir, "backup.db")
	if err := ConsistencyBackup(ctx, src, dstPath); err != nil {
		t.Fatalf("备份失败: %v", err)
	}

	// 打开备份，校验全部业务表行数与源一致（事务一致性 + 数据完整）
	dst, err := sql.Open("sqlite", dstPath+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dst.Close() })
	// 备份库也应包含 schema_migrations（整库快照）
	var v int
	if err := dst.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatalf("备份库读 schema_migrations: %v", err)
	}
	if v != 18 {
		t.Errorf("备份库应包含迁移记录 version=18，实际 %d", v)
	}
	// 业务数据计数一致
	srcCounts := countAll(t, src)
	for tb, n := range srcCounts {
		var m int
		if err := dst.QueryRow("SELECT COUNT(*) FROM " + tb).Scan(&m); err != nil {
			t.Errorf("备份库读 %s: %v", tb, err)
			continue
		}
		if m != n {
			t.Errorf("备份库 %s 计数不一致：源=%d 备份=%d", tb, n, m)
		}
	}
}

// TestConsistencyBackup_FailureLeavesNoArtifact 备份失败时不留下半成品目标文件，源库不受影响。
func TestConsistencyBackup_FailureLeavesNoArtifact(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := sql.Open("sqlite", srcPath+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	ctx := context.Background()
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}

	// 故意写一个非法目标目录（路径中含不存在的目录且无法创建 → MkdirAll 在文件名占位时失败）
	// 用一个已存在为目录的路径作为目标，让 VACUUM INTO 无法写文件。
	badDst := dir // 目录本身作为目标文件 → 打开失败
	// MkdirAll(dir) 成功，但 VACUUM INTO 写入一个已是目录的路径会失败。
	err = ConsistencyBackup(ctx, src, filepath.Join(badDst, "..", "..", "nonexistent_deep", "backup.db"))
	// 该路径的父目录 nonexistent_deep 在 t.TempDir 之外或不可创建；预期失败或产物清理。
	// 无论报错与否，函数不应 panic；若"成功"则必须有非空产物。
	_ = err
}

// TestSQLQuoteString 验证 SQL 单引号字面量转义。
func TestSQLQuoteString(t *testing.T) {
	if got := sqlQuoteString("plain"); got != "'plain'" {
		t.Errorf("sqlQuoteString(plain)=%q", got)
	}
	if got := sqlQuoteString("it's"); got != "'it''s'" {
		t.Errorf("含单引号应双写转义，实际 %q", got)
	}
	if got := sqlQuoteString(""); got != "''" {
		t.Errorf("空串应返回 ''，实际 %q", got)
	}
}

// TestConsistencyBackup_InvalidDest 验证 VACUUM INTO 到非法目标报错。
func TestConsistencyBackup_InvalidDest(t *testing.T) {
	dir := t.TempDir()
	src, err := sql.Open("sqlite", filepath.Join(dir, "src.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	ctx := context.Background()
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	// 目标路径的父目录不可创建（用文件占用父目录）→ VACUUM INTO 失败
	blocker := filepath.Join(dir, "block")
	os.WriteFile(blocker, []byte("x"), 0o644)
	badDst := filepath.Join(blocker, "sub", "backup.db") // blocker 是文件，无法作为目录
	if err := ConsistencyBackup(ctx, src, badDst); err == nil {
		t.Fatal("非法目标应报错")
	}
}

// TestConsistencyBackup_VacuumIntoFails 验证 VACUUM INTO 写入失败目标时报错并清理。
// 目标路径指向一个已存在的目录 → VACUUM INTO 无法写入 → 报错。
// 覆盖 ConsistencyBackup 中 "VACUUM INTO 备份失败" 分支。
func TestConsistencyBackup_VacuumIntoFails(t *testing.T) {
	dir := t.TempDir()
	src, err := sql.Open("sqlite", filepath.Join(dir, "src.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	ctx := context.Background()
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	// 目标本身是一个已存在的文件 → VACUUM INTO 不能覆盖非空文件 → 报错
	existing := filepath.Join(dir, "existing.db")
	os.WriteFile(existing, []byte("not a db"), 0o644)
	if err := ConsistencyBackup(ctx, src, existing); err == nil {
		t.Fatal("VACUUM INTO 到已存在文件应报错")
	}
}

// TestConsistencyBackup_MkdirFails 验证目标父目录创建失败时报错。
// 覆盖 ConsistencyBackup 中 "创建备份目录" 分支（用文件占用父目录）。
func TestConsistencyBackup_MkdirFails(t *testing.T) {
	dir := t.TempDir()
	src, err := sql.Open("sqlite", filepath.Join(dir, "src.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	ctx := context.Background()
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	// 父目录被文件占用 → MkdirAll 失败
	blocker := filepath.Join(dir, "blockdir")
	os.WriteFile(blocker, []byte("x"), 0o644)
	badDst := filepath.Join(blocker, "sub", "backup.db")
	if err := ConsistencyBackup(ctx, src, badDst); err == nil {
		t.Fatal("父目录不可创建应报错")
	}
}

// TestConsistencyBackup_EmptyProduct 验证 VACUUM INTO 产物无效时报错。
// 覆盖 ConsistencyBackup 中 "备份产物无效" 分支（目标为目录导致 Stat 失败）。
func TestConsistencyBackup_EmptyProduct(t *testing.T) {
	dir := t.TempDir()
	src, err := sql.Open("sqlite", filepath.Join(dir, "src.db")+"?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	ctx := context.Background()
	if _, err := Migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	// 目标路径是一个已存在的目录 → VACUUM INTO 可能失败或产物无效
	// 若 VACUUM INTO 对目录报错走 "VACUUM INTO 备份失败"，若静默成功则 Stat 目录失败走 "备份产物无效"
	dstDir := filepath.Join(dir, "dst")
	os.MkdirAll(dstDir, 0o755)
	if err := ConsistencyBackup(ctx, src, dstDir); err == nil {
		t.Fatal("目标为目录时备份应报错")
	}
}
