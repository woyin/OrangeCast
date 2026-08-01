package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConsistencyBackup 生成源数据库的一致性快照到 dstPath（不存在则创建，存在则覆盖）。
//
// 实现用 SQLite 的 `VACUUM INTO`：在源库上执行一条语句，直接生成一个事务一致的完整副本，
// 不需要复制运行中的数据库文件（避免 WAL/脏页不一致）。modernc.org/sqlite 支持 `VACUUM INTO`。
//
// 用于：第一次破坏性迁移前的安全网（ADR-0010、Roadmap Phase 1）。
// 失败时目标文件会被清理，源库不受影响。
//
// 注意：srcDB 必须是用 modernc.org/sqlite 打开的同一个 *sql.DB；dstPath 必须是文件路径（不能已 ATTACH）。
func ConsistencyBackup(ctx context.Context, srcDB *sql.DB, dstPath string) error {
	// 目标目录必须存在
	if dir := filepath.Dir(dstPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建备份目录: %w", err)
		}
	}
	// VACUUM INTO 的目标必须是合法文件路径；用单引号包裹并对内部单引号转义。
	// 失败时清理残留目标文件，保证"失败不留下半成品"。
	quoted := sqlQuoteString(dstPath)
	if _, err := srcDB.ExecContext(ctx, fmt.Sprintf("VACUUM INTO %s", quoted)); err != nil {
		_ = os.Remove(dstPath)
		return fmt.Errorf("VACUUM INTO 备份失败: %w", err)
	}
	// 校验产物非空（防御性：某些驱动静默成功但未写文件）
	if fi, err := os.Stat(dstPath); err != nil || fi.Size() == 0 {
		_ = os.Remove(dstPath)
		return fmt.Errorf("备份产物无效（不存在或为空）: %s", dstPath)
	}
	return nil
}

// sqlQuoteString 把字符串包成 SQL 单引号字面量，内部单引号按 SQL 规范双写转义。
func sqlQuoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
