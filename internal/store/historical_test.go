package store

import (
	"path/filepath"
	"testing"
)

// historicalTestStore applies the actual prefix, rather than undoing a current schema.
func historicalTestStore(t *testing.T, version int) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "historical.db")
	db := openRaw(t, path)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > version {
			break
		}
		if err := applyOne(t.Context(), db, m); err != nil {
			t.Fatal(m.name, err)
		}
	}
	return &Store{DB: db}, path
}
