package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store 封装 SQLite 数据库句柄。
type Store struct {
	DB *sql.DB
}

// Open 打开数据库并执行有序迁移。
//
// 迁移由 internal/store/migrate.go 的 Migrate 驱动：
//   - 全新库：建立 schema_migrations 并按序应用所有迁移。
//   - v0.1.0 旧库：CREATE IF NOT EXISTS 的 baseline 迁移幂等空跑，随后登记为 version=1，
//     历史数据保持不变。
//   - 破坏性迁移（0002 移除 user_id）前：若数据库已有数据，先生成一致性备份；
//     并校验 users 数量——超过 1 个时拒绝迁移（ErrMultipleUsers），要求显式选择 Owner。
//
// schemaSQL 仍保留以兼容历史测试与外部脚本；正式 schema 演进由 migrations/ 目录承载。
func Open(path string) (*Store, error) {
	// busy_timeout 避免写锁竞争时立即报错；foreign_keys 开启级联。
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// SQLite 单写者，连接池设为 1 避免写冲突；读多写少可用 WAL 进一步优化。
	db.SetMaxOpenConns(1)

	// WAL 模式提升并发读性能（先于迁移设置，便于一致性备份快照）。
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 WAL: %w", err)
	}
	ctx := context.Background()
	if err := preMigrationSafety(ctx, db, path); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("执行迁移: %w", err)
	}
	return &Store{DB: db}, nil
}

// preMigrationSafety 在破坏性迁移前执行安全网：单 Owner 守卫 + 一致性备份。
// 仅当存在待应用的破坏性迁移（0002）时触发；全新库或已迁移库直接跳过。
func preMigrationSafety(ctx context.Context, db *sql.DB, dbPath string) error {
	pending, err := hasPendingDestructiveMigration(ctx, db)
	if err != nil {
		return fmt.Errorf("检查待迁移状态: %w", err)
	}
	if !pending {
		return nil
	}
	// 单 Owner 守卫：多个用户时拒绝自动迁移，要求显式选择。
	if err := RequireSafeForSingleOwner(ctx, db); err != nil {
		return err
	}
	// 非全新库（已有用户）才生成备份，避免空库浪费。
	if users, _ := CountUsers(ctx, db); users >= 1 {
		backupPath := dbPath + ".pre-single-owner.bak"
		if err := ConsistencyBackup(ctx, db, backupPath); err != nil {
			// 备份失败不阻断迁移本身，但明确告警；迁移仍可重试。
			fmt.Fprintf(os.Stderr, "warn: 破坏性迁移前备份失败（%s）：已继续迁移，建议手动备份\n", err)
		}
	}
	return nil
}

// Close 关闭数据库。
func (s *Store) Close() error {
	return s.DB.Close()
}
