package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOfflineFailureRequestBoundaries(t *testing.T) {
	srv, session, device, ep, m := offlineAcceptanceFixture(t)
	for _, tc := range []struct {
		route, body string
		code        int
	}{
		{"enable", `{`, 400}, {"enable", `{"device_id":"x","unknown":1}`, 400},
		{"enable", fmt.Sprintf(`{"device_id":%q} {}`, device), 400},
		{"enable", `{"device_id":"not-a-uuid"}`, 400},
		{"clear-downloads", fmt.Sprintf(`{"device_id":%q}`, uuid.NewString()), 403},
		{"revoke", fmt.Sprintf(`{"device_id":%q}`, uuid.NewString()), 403},
		{"manifest", fmt.Sprintf(`{"device_id":%q}`, uuid.NewString()), 403},
		{"sync", fmt.Sprintf(`{"device_id":%q,"namespace":"wrong","epoch":%q}`, device, m.Epoch), 403},
		{"sync", fmt.Sprintf(`{"device_id":%q,"namespace":%q,"epoch":"wrong"}`, device, m.Namespace), 403},
		{"sync", fmt.Sprintf(`{"device_id":%q,"operations":[%s]}`, device, strings.TrimSuffix(strings.Repeat(`{},`, 101), ",")), 400},
		{"manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"document","source_id":"remote"}]}`, device), 400},
		{"manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q},{"source_type":"episode","source_id":%q}]}`, device, ep, ep), 400},
		{"manifest", fmt.Sprintf(`{"device_id":%q,"sources":[%s]}`, device, strings.TrimSuffix(strings.Repeat(`{},`, 11), ",")), 400},
		{"manifest", fmt.Sprintf(`{"device_id":%q,"article_ids":["missing"]}`, device), 404},
		{"missing", `{}`, 404},
	} {
		t.Run(tc.route+tc.body[:min(len(tc.body), 30)], func(t *testing.T) {
			r := queueJSONRequest(t, srv, session, "POST", "/api/offline/"+tc.route, tc.body, true)
			if r.Code != tc.code {
				t.Fatalf("status=%d expected=%d body=%s", r.Code, tc.code, r.Body.String())
			}
			if strings.Contains(r.Body.String(), session.Value) {
				t.Fatal("failure exposed credential")
			}
			if tc.code == 400 && !strings.Contains(r.Body.String(), `"error":"invalid_request"`) {
				t.Fatalf("invalid command wrongly reported material unavailable: %s", r.Body.String())
			}
		})
	}
	for _, route := range []string{"status", "file", "enable", "catalog", "drafts"} {
		if r := queueJSONRequest(t, srv, session, "PUT", "/api/offline/"+route, `{}`, true); r.Code != 405 {
			t.Fatalf("method %s: %d", route, r.Code)
		}
	}
	// A malformed command must not leave any Owner text behind.
	notes, err := srv.store.ListOfflineOrganizingDrafts(t.Context())
	if err != nil || len(notes) != 0 {
		t.Fatalf("malformed request mutated drafts: %v %v", notes, err)
	}
}

func TestOfflineFailureAudioFilesystemAndTemporaryCleanup(t *testing.T) {
	for _, mode := range []string{"missing-root", "missing-file", "directory", "wrong-size", "not-ready", "temporary-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			srv, session, _, ep, m := offlineAcceptanceFixture(t)
			var audio OfflineFile
			for _, f := range m.Files {
				if f.Kind == "audio" {
					audio = f
				}
			}
			path := filepath.Join(srv.cfg.EvidenceDir, "accept.mp3")
			switch mode {
			case "missing-root":
				srv.cfg.EvidenceDir = filepath.Join(t.TempDir(), "missing")
			case "missing-file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "wrong-size":
				if err := os.WriteFile(path, []byte("changed length"), 0600); err != nil {
					t.Fatal(err)
				}
			case "not-ready":
				if _, err := srv.store.DB.Exec(`UPDATE evidence_audio SET status='failed' WHERE source_id=?`, ep); err != nil {
					t.Fatal(err)
				}
			case "temporary-unavailable":
				srv.cfg.TempDir = filepath.Join(t.TempDir(), "is-file")
				if err := os.WriteFile(srv.cfg.TempDir, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := queueJSONRequest(t, srv, session, "GET", audio.URL, "", false)
			if r.Code == 200 || strings.Contains(r.Body.String(), "original-local-evidence") {
				t.Fatalf("unavailable audio served: %d %s", r.Code, r.Body.String())
			}
			if mode == "missing-root" || mode == "missing-file" {
				if r.Code != 404 || !strings.Contains(r.Body.String(), `"error":"material_unavailable"`) {
					t.Fatalf("missing evidence not reported unavailable: %d %s", r.Code, r.Body.String())
				}
			}
			if strings.Contains(r.Body.String(), path) || strings.Contains(r.Body.String(), srv.cfg.TempDir) {
				t.Fatal("filesystem path exposed")
			}
			files, err := filepath.Glob(filepath.Join(srv.cfg.TempDir, "cwp-offline-download-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary leaked %v %v", files, err)
			}
		})
	}
}

func TestOfflineFailureStoredManifestCorruptionAndExpiry(t *testing.T) {
	for _, mode := range []string{"json", "unknown-kind", "wrong-hash", "missing-file-id", "expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			srv, session, device, _, m := offlineAcceptanceFixture(t)
			file := m.Files[1]
			want := 409
			switch mode {
			case "json":
				if _, e := srv.store.DB.Exec(`UPDATE offline_packs SET manifest_json='{' WHERE id=?`, m.PackID); e != nil {
					t.Fatal(e)
				}
			case "unknown-kind", "wrong-hash":
				for i := range m.Files {
					if m.Files[i].ID == file.ID {
						if mode == "unknown-kind" {
							m.Files[i].Kind = "private-recording"
							want = 400
						} else {
							m.Files[i].SHA256 = strings.Repeat("0", 64)
						}
					}
				}
				body, e := json.Marshal(m)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = srv.store.DB.Exec(`UPDATE offline_packs SET manifest_json=? WHERE id=?`, string(body), m.PackID); e != nil {
					t.Fatal(e)
				}
			case "missing-file-id":
				file.URL = strings.Replace(file.URL, file.ID, uuid.NewString(), 1)
				want = 404
			case "expired":
				if _, e := srv.store.DB.Exec(`UPDATE offline_devices SET expires_at=0 WHERE device_id=?`, device); e != nil {
					t.Fatal(e)
				}
				want = 410
			case "revoked":
				if r := queueJSONRequest(t, srv, session, "POST", "/api/offline/revoke", fmt.Sprintf(`{"device_id":%q}`, device), true); r.Code != 200 {
					t.Fatal(r.Body.String())
				}
				want = 403
			}
			r := queueJSONRequest(t, srv, session, "GET", file.URL, "", false)
			if r.Code != want {
				t.Fatalf("%s status %d want %d: %s", mode, r.Code, want, r.Body.String())
			}
			if strings.Contains(r.Body.String(), "替换原文") || strings.Contains(r.Body.String(), "seg-2") {
				t.Fatal("failed grant exposed transcript")
			}
		})
	}
}

func TestOfflineStaticHEADAndMethodContract(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/offline", "/sw.js"} {
		r := queueJSONRequest(t, srv, nil, http.MethodHead, path, "", false)
		if r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("X-Content-Type-Options") != "nosniff" || r.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("static HEAD %s %d %v", path, r.Code, r.Header())
		}
		if len(r.Result().Cookies()) != 0 {
			t.Fatal("static issued private cookie")
		}
	}
}
