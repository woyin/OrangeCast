package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

// migrations 目录下每个 .sql 文件是一次有序迁移。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration 记录一次可记录版本的数据库迁移。
type migration struct {
	version int
	name    string
	up      string
}

// schemaMigrationsTable 记录已应用迁移版本。
const schemaMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (datetime('now'))
)`

// loadMigrations 从 embed FS 读取并按 version 排序。
// 文件名格式：<4位数字>_<名称>.sql，例如 0001_baseline.sql。
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("读取 migrations 目录: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%04d_", &version); err != nil {
			return nil, fmt.Errorf("无法解析迁移版本号 %q: %w", e.Name(), err)
		}
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("读取迁移 %s: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), ".sql")
		ms = append(ms, migration{version: version, name: name, up: string(data)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	// 校验版本号连续且唯一（防止漏号或重号造成隐性偏移）。
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("迁移版本号不连续：期望 %d，实际 %d（%s）", i+1, m.version, m.name)
		}
	}
	return ms, nil
}

// AppliedVersion 返回当前数据库已应用到的最高迁移版本；全新库返回 0。
func AppliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	// 全新库或 v0.1.0 旧库尚未有 schema_migrations 表：视为 version=0。
	var has int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&has); err != nil {
		return 0, err
	}
	if has == 0 {
		return 0, nil
	}
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// Migrate 执行所有尚未应用的迁移，每个迁移在独立事务中运行。
//
// 语义：
//   - 迁移按 version 升序逐个应用；每次成功后立即提交并写入 schema_migrations。
//   - 某个迁移失败时，其事务回滚，函数返回错误；此前已提交的迁移保持有效，
//     schema_migrations 反映它们，因此失败后可安全重试（下一轮从不一致点继续）。
//   - 全新库：schema_migrations 不存在时自动创建（version=0）。
//   - v0.1.0 旧库：表已存在但无 schema_migrations，0001 的 CREATE IF NOT EXISTS 幂等空跑，
//     随后记为 version=1。
//
// 返回本次新应用的版本号列表（按顺序）。
func Migrate(ctx context.Context, db *sql.DB) ([]int, error) {
	if _, err := db.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return nil, fmt.Errorf("创建 schema_migrations: %w", err)
	}
	applied, err := AppliedVersion(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("读取已应用版本: %w", err)
	}
	ms, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	var newlyApplied []int
	for _, m := range ms {
		if m.version <= applied {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return newlyApplied, fmt.Errorf("迁移 %04d %s 失败: %w", m.version, m.name, err)
		}
		newlyApplied = append(newlyApplied, m.version)
	}
	return newlyApplied, nil
}

// applyOne 在单个事务内执行一条迁移并记录版本。
// 事务保证：迁移 SQL 与 schema_migrations 写入要么全部提交，要么全部回滚。
func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.up); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrMultipleUsers 表示数据库存在多个用户，无法安全收敛为单 Owner。
// 调用方必须要求 Owner 显式选择保留哪一个，再重试迁移。
var ErrMultipleUsers = fmt.Errorf("检测到多个用户：单 Owner 迁移需显式选择保留哪一个")

// usersTableName 存在性检查（兼容全新库与 v0.1 库）。
func usersTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountUsers 返回 users 表行数；表不存在返回 0。
func CountUsers(ctx context.Context, db *sql.DB) (int, error) {
	exists, err := usersTableExists(ctx, db)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// RequireSafeForSingleOwner 在破坏性迁移（移除 user_id）前校验 Owner 收敛可行性：
//   - 全新库（无 users 表）或 0/1 个用户：可安全继续。
//   - 多个用户：返回 ErrMultipleUsers，调用方必须让 Owner 显式选择。
//
// roadmap Phase 2 要求"超过 1 个时停止并要求显式选择，不能猜测或静默合并"。
func RequireSafeForSingleOwner(ctx context.Context, db *sql.DB) error {
	n, err := CountUsers(ctx, db)
	if err != nil {
		return err
	}
	if n > 1 {
		return ErrMultipleUsers
	}
	return nil
}

// hasPendingDestructiveMigration 判断是否存在尚未应用、且属于破坏性（移除 user_id）的迁移。
// 当前唯一破坏性迁移是 0002；若已应用（version>=2）则无需 guard。
// schema_migrations 表尚未创建（全新库）时视为 version=0，仍有 pending。
func hasPendingDestructiveMigration(ctx context.Context, db *sql.DB) (bool, error) {
	exists, err := migrationTableExists(ctx, db)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	applied, err := AppliedVersion(ctx, db)
	if err != nil {
		return false, err
	}
	return applied < 2, nil
}

// migrationTableExists 判断 schema_migrations 是否存在。
func migrationTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
