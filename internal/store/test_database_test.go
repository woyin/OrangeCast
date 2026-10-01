package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/testdb"
)

func TestClonedDatabaseMatchesFreshSchemaAndKeepsConnectionFunctions(t *testing.T) {
	fresh := newFreshTestStore(t)
	cloned := newTestStore(t)
	schema := func(s *Store) string {
		rows, err := s.DB.Query(`SELECT type,name,COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := ""
		for rows.Next() {
			var kind, name, sql string
			if err = rows.Scan(&kind, &name, &sql); err != nil {
				t.Fatal(err)
			}
			result += kind + name + sql + "\n"
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if schema(fresh) != schema(cloned) {
		t.Fatal("cloned schema differs")
	}
	var tokens string
	var foreignKeys int
	if err := cloned.DB.QueryRow(`SELECT cwp_search_tokens('主动回忆')`).Scan(&tokens); err != nil || tokens == "" {
		t.Fatal("SQLite functions missing", err)
	}
	if err := cloned.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("foreign keys missing", err)
	}
	if _, err := cloned.EnsureDefaultEditorialProfile(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fresh.DB.QueryRow(`SELECT COUNT(*) FROM editorial_profiles`).Scan(&count); err != nil || count != 0 {
		t.Fatal("mutable state shared", err, count)
	}
}

func BenchmarkTestDatabaseInitialization(b *testing.B) {
	for _, mode := range []string{"fresh", "clone"} {
		b.Run(mode, func(b *testing.B) {
			dir := b.TempDir()
			for i := 0; i < b.N; i++ {
				path := filepath.Join(dir, fmt.Sprintf("test-%d.db", i))
				var s *Store
				var err error
				if mode == "fresh" {
					s, err = Open(path)
				} else {
					s, err = testdb.Open(path, "migrations", Open)
				}
				if err != nil {
					b.Fatal(err)
				}
				if err = s.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
