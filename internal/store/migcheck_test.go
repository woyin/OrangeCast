package store

import (
	"context"
	"testing"
)

func TestMigration0011_RelationKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v, err := AppliedVersion(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v < 11 {
		t.Fatalf("应已应用到 0011，实际 %d", v)
	}
	for _, tbl := range []string{"keypoint_index", "annotations", "pins", "collection_items"} {
		var cid int
		var name, typ string
		var notnull int
		var dflt any
		var pk int
		found := false
		rows, err := s.DB.QueryContext(ctx, "PRAGMA table_info("+tbl+")")
		if err != nil {
			t.Fatalf("table_info %s: %v", tbl, err)
		}
		for rows.Next() {
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name == "relation_kind" {
				found = true
				ds, _ := dflt.(string)
				if ds != "'citation'" && ds != "citation" {
					t.Errorf("%s.relation_kind 默认值应为 citation，实际 %v", tbl, dflt)
				}
				if notnull != 1 {
					t.Errorf("%s.relation_kind 应 NOT NULL", tbl)
				}
			}
		}
		rows.Close()
		if !found {
			t.Errorf("%s 缺少 relation_kind 列", tbl)
		}
	}
}
