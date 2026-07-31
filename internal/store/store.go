package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store 封装 SQLite 数据库句柄。
type Store struct {
	DB *sql.DB
}

// Open 打开数据库并执行 schema 迁移（CREATE IF NOT EXISTS，幂等）。
func Open(path string) (*Store, error) {
	// busy_timeout 避免写锁竞争时立即报错；foreign_keys 开启级联。
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// SQLite 单写者，连接池设为 1 避免写冲突；读多写少可用 WAL 进一步优化。
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("执行 schema: %w", err)
	}
	// WAL 模式提升并发读性能。
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 WAL: %w", err)
	}
	return &Store{DB: db}, nil
}

// Close 关闭数据库。
func (s *Store) Close() error {
	return s.DB.Close()
}
