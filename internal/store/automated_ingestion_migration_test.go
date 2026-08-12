package store

import (
	"context"
	"testing"
)

func TestMigration0020_AutomatedIngestionJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	version, err := AppliedVersion(ctx, s.DB)
	if err != nil || version < 20 {
		t.Fatalf("migration 0020 should be applied: version=%d err=%v", version, err)
	}
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(processing_jobs)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "is_automated" {
			if notNull != 1 || defaultValue == nil {
				t.Fatalf("is_automated should be NOT NULL with a default: notNull=%d default=%v", notNull, defaultValue)
			}
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("processing_jobs.is_automated is missing")
}
