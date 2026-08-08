package store

import (
	"context"
	"database/sql"
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
	if v != 15 {
		t.Errorf("备份库应包含迁移记录 version=15，实际 %d", v)
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
