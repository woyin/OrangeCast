package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOfflineFailurePackBudgetAndCorruptHistory(t *testing.T) {
	for _, mode := range []string{"negative", "individual-limit", "aggregate-limit", "device-total", "invalid-json", "invalid-id", "corrupt-history", "duplicate-id", "insert-failure"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			ctx := t.Context()
			session := "offline-budget-owner-session"
			d, e := s.EnableOfflineDevice(ctx, session, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			id := uuid.NewString()
			body := fmt.Sprintf(`{"pack_id":%q,"files":[]}`, id)
			want := ErrInvalidEditorialState
			switch mode {
			case "negative":
				body = fmt.Sprintf(`{"pack_id":%q,"files":[{"size":-1}]}`, id)
			case "individual-limit":
				body = fmt.Sprintf(`{"pack_id":%q,"files":[{"size":%d}]}`, id, int64(500<<20)+1)
			case "aggregate-limit":
				body = fmt.Sprintf(`{"pack_id":%q,"files":[{"size":%d},{"size":1}]}`, id, 500<<20)
			case "device-total":
				old := fmt.Sprintf(`{"pack_id":%q,"files":[{"size":%d}]}`, uuid.NewString(), 500<<20)
				if _, e = s.SaveOfflinePack(ctx, session, d.Namespace, old, nil); e != nil {
					t.Fatal(e)
				}
				body = fmt.Sprintf(`{"pack_id":%q,"files":[{"size":1}]}`, id)
			case "invalid-json":
				body = "{"
			case "invalid-id":
				body = `{"pack_id":"invalid"}`
			case "corrupt-history":
				if _, e = s.DB.Exec(`INSERT INTO offline_packs VALUES(?,?,?,?)`, uuid.NewString(), d.Namespace, `{`, `[]`); e != nil {
					t.Fatal(e)
				}
				want = ErrConflict
			case "duplicate-id":
				if _, e = s.SaveOfflinePack(ctx, session, d.Namespace, body, nil); e != nil {
					t.Fatal(e)
				}
				want = nil
			case "insert-failure":
				if _, e = s.DB.Exec(`CREATE TRIGGER offline_fail_pack BEFORE INSERT ON offline_packs BEGIN SELECT RAISE(ABORT,'offline insert failed'); END`); e != nil {
					t.Fatal(e)
				}
				want = nil
			}
			if _, e = s.SaveOfflinePack(ctx, session, d.Namespace, body, nil); e == nil || (want != nil && !errors.Is(e, want)) {
				t.Fatalf("pack accepted or wrong failure: %v", e)
			}
			var count int
			if e = s.DB.QueryRow(`SELECT count(*) FROM offline_packs WHERE id=?`, id).Scan(&count); e != nil {
				t.Fatal(e)
			}
			expected := 0
			if mode == "duplicate-id" {
				expected = 1
			}
			if count != expected {
				t.Fatalf("rejected write changed records: %d", count)
			}
			if _, e = s.ValidateOfflineDevice(ctx, session, d.Namespace); e != nil {
				t.Fatalf("rejected pack invalidated healthy authorization: %v", e)
			}
		})
	}
}

func TestOfflineFailureDeviceRefreshRollback(t *testing.T) {
	for _, event := range []string{"DELETE", "INSERT"} {
		t.Run(event, func(t *testing.T) {
			s := newTestStore(t)
			session := "offline-refresh-owner-session"
			device := uuid.NewString()
			original, e := s.EnableOfflineDevice(t.Context(), session, device)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.DB.Exec(fmt.Sprintf(`CREATE TRIGGER fail_offline_refresh BEFORE %s ON offline_devices BEGIN SELECT RAISE(ABORT,'refresh failed'); END`, event)); e != nil {
				t.Fatal(e)
			}
			if _, e = s.EnableOfflineDevice(t.Context(), session, device); e == nil {
				t.Fatal("failed refresh accepted")
			}
			current, e := s.GetOfflineDevice(t.Context(), session, device)
			if e != nil || current.Namespace != original.Namespace {
				t.Fatalf("failed refresh destroyed old grant: %+v %v", current, e)
			}
		})
	}
}

func TestOfflineFailureCorruptPackSourceAndArticleMetadata(t *testing.T) {
	for _, mode := range []string{"bad-source-json", "bad-manifest-json", "missing-article-id", "missing-article-revision", "missing-article", "missing-pack"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			session := "offline-corrupt-owner-session"
			d, e := s.EnableOfflineDevice(t.Context(), session, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			id := uuid.NewString()
			body := fmt.Sprintf(`{"pack_id":%q,"files":[]}`, id)
			if _, e = s.SaveOfflinePack(t.Context(), session, d.Namespace, body, nil); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "bad-source-json":
				_, e = s.DB.Exec(`UPDATE offline_packs SET sources_json='{' WHERE id=?`, id)
			case "bad-manifest-json":
				_, e = s.DB.Exec(`UPDATE offline_packs SET manifest_json='{' WHERE id=?`, id)
			case "missing-article-id":
				_, e = s.DB.Exec(`UPDATE offline_packs SET manifest_json='{"files":[{"kind":"article","revision":1}]}' WHERE id=?`, id)
			case "missing-article-revision":
				_, e = s.DB.Exec(`UPDATE offline_packs SET manifest_json='{"files":[{"kind":"article","object_id":"missing"}]}' WHERE id=?`, id)
			case "missing-article":
				_, e = s.DB.Exec(`UPDATE offline_packs SET manifest_json='{"files":[{"kind":"article","object_id":"missing","revision":1}]}' WHERE id=?`, id)
			case "missing-pack":
				id = uuid.NewString()
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.GetOfflinePack(t.Context(), session, d.Namespace, id); e == nil {
				t.Fatal("corrupt metadata allowed access")
			}
			if mode != "missing-pack" {
				if e = s.ValidateOfflinePackSources(t.Context(), session, d.Namespace); e == nil {
					t.Fatal("status authorized corrupt download")
				}
			}
			if e = s.DeleteOfflinePacks(t.Context(), session, d.Namespace); e != nil {
				t.Fatalf("user cannot clear corrupt download metadata: %v", e)
			}
		})
	}
}

func TestOfflineFailureClosedDatabaseNeverAuthorizes(t *testing.T) {
	s := newTestStore(t)
	session := "offline-closed-owner-session"
	d, e := s.EnableOfflineDevice(t.Context(), session, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DB.Close(); e != nil {
		t.Fatal(e)
	}
	checks := []func() error{
		func() error { return s.BeginOfflineInstance(t.Context()) },
		func() error { _, e := s.EnableOfflineDevice(t.Context(), session, d.DeviceID); return e },
		func() error { _, e := s.GetOfflineDevice(t.Context(), session, d.DeviceID); return e },
		func() error { _, e := s.ValidateOfflineDevice(t.Context(), session, d.Namespace); return e },
		func() error { return s.RevokeOfflineDevice(t.Context(), session, d.Namespace) },
		func() error { return s.DeleteOfflinePacks(t.Context(), session, d.Namespace) },
		func() error { return s.ValidateOfflinePackSources(t.Context(), session, d.Namespace) },
		func() error { _, e := s.SaveOfflinePack(t.Context(), session, d.Namespace, `{}`, nil); return e },
		func() error { _, e := s.GetOfflinePack(t.Context(), session, d.Namespace, uuid.NewString()); return e },
	}
	for i, f := range checks {
		if e = f(); e == nil || !strings.Contains(e.Error(), "closed") {
			t.Fatalf("closed database check %d: %v", i, e)
		}
	}
}

func TestOfflineFailurePackFrozenSourceChecks(t *testing.T) {
	for _, mode := range []string{"source-cap", "missing-source", "wrong-snapshot", "wrong-audio", "corrupt-transcript"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			session := "offline-frozen-owner-session"
			d, e := s.EnableOfflineDevice(t.Context(), session, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			source, snapshot := offlineTestSource(t, s)
			sources := []OfflineSource{{SourceType: "episode", SourceID: source, SnapshotID: snapshot}}
			switch mode {
			case "source-cap":
				sources = make([]OfflineSource, 11)
			case "missing-source":
				sources[0].SourceID = uuid.NewString()
			case "wrong-snapshot":
				sources[0].SnapshotID = uuid.NewString()
			case "wrong-audio":
				sources[0].AudioSHA256 = strings.Repeat("0", 64)
			case "corrupt-transcript":
				if _, e = s.DB.Exec(`UPDATE artifact_versions SET payload='{' WHERE id=(SELECT content_version_id FROM source_snapshots WHERE id=?)`, snapshot); e != nil {
					t.Fatal(e)
				}
			}
			id := uuid.NewString()
			manifest := fmt.Sprintf(`{"pack_id":%q,"files":[]}`, id)
			if _, e = s.SaveOfflinePack(t.Context(), session, d.Namespace, manifest, sources); e == nil {
				t.Fatal("invalid frozen source saved")
			}
			var count int
			if e = s.DB.QueryRow(`SELECT count(*) FROM offline_packs WHERE id=?`, id).Scan(&count); e != nil || count != 0 {
				t.Fatalf("invalid scope persisted: %d %v", count, e)
			}
		})
	}
}
