package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
	"time"
)

func TestOfflineHTTPAuthorizationManifestBytesAndRevocation(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "offline-http@example.com", "password123")
	device := uuid.NewString()
	for _, route := range []string{"/api/offline/status", "/api/offline/session", "/api/offline/file"} {
		if rec := queueJSONRequest(t, srv, nil, "GET", route, "", false); rec.Code != 401 {
			t.Fatalf("unauthorized %s: %d", route, rec.Code)
		}
	}
	enable := fmt.Sprintf(`{"device_id":%q}`, device)
	if rec := queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", enable, false); rec.Code != 403 {
		t.Fatalf("csrf %d", rec.Code)
	}
	if rec := queueJSONRequest(t, srv, session, "GET", "/api/offline/status?device_id="+device, "", false); rec.Code != 403 {
		t.Fatalf("default disabled %d", rec.Code)
	}
	rec := queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", enable, true)
	if rec.Code != 200 {
		t.Fatalf("enable %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), session.Value) {
		t.Fatal("credential leaked")
	}
	ep := seedEpisodeWithTranscript(t, srv, "offline-http")
	audio := []byte("offline-original-local-audio-bytes")
	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.cfg.EvidenceDir, "offline.mp3"), audio, 0600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(audio))
	if err := srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, "offline.mp3", "mp3", int64(len(audio)), hash); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}],"article_ids":[]}`, device, ep)
	rec = queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", body, true)
	if rec.Code != 200 {
		t.Fatalf("manifest %d %s", rec.Code, rec.Body.String())
	}
	var manifest OfflineManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 || manifest.SchemaVersion != 1 || manifest.ExpiresAt-manifest.IssuedAt != 86400 {
		t.Fatalf("manifest %+v", manifest)
	}
	var file OfflineFile
	for _, f := range manifest.Files {
		if f.Kind == "audio" {
			file = f
		}
	}
	if file.SHA256 != hash || file.Size != int64(len(audio)) || file.SnapshotID == "" {
		t.Fatalf("audio identity %+v", file)
	}
	rec = queueJSONRequest(t, srv, session, http.MethodGet, file.URL, "", false)
	if rec.Code != 200 || rec.Body.String() != string(audio) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range []struct {
		rangeHeader string
		status      int
		want        string
	}{{"bytes=2-5", 206, string(audio[2:6])}, {"bytes=100-", 416, ""}} {
		req := httptest.NewRequest("GET", file.URL, nil)
		req.AddCookie(session)
		req.Header.Set("Range", c.rangeHeader)
		rr := httptest.NewRecorder()
		srv.Router().ServeHTTP(rr, req)
		if rr.Code != c.status || (c.status == 206 && rr.Body.String() != c.want) {
			t.Fatalf("range %s %d %s", c.rangeHeader, rr.Code, rr.Body.String())
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(srv.cfg.TempDir, "cwp-offline-download-*"))
	if len(leftovers) != 0 {
		t.Fatal("verified download temporary leaked")
	}
	// The actual HTTP JSON command hash round-trips and a lost acknowledgement
	// can replay without a second Owner note or a model call.
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: manifest.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "owner_note", SourceType: models.SourceEpisode, SourceID: ep, SnapshotID: file.SnapshotID, Payload: json.RawMessage(`{"Kind":"owner_reflection","Content":"我理解了 <原音> & 学习","AnchorJSON":"{}"}`)}
	op.PayloadHash = store.OfflineOperationHash(op)
	syncBody, _ := json.Marshal(map[string]any{"device_id": device, "namespace": manifest.Namespace, "epoch": manifest.Epoch, "operations": []store.OfflineOperation{op}})
	for i := 0; i < 2; i++ {
		r := queueJSONRequest(t, srv, session, "POST", "/api/offline/sync", string(syncBody), true)
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"ack"`) {
			t.Fatalf("sync %d %s", r.Code, r.Body.String())
		}
	}
	notes, e := srv.store.ListOwnerNotes(t.Context(), models.SourceEpisode, ep)
	if e != nil || len(notes) != 1 {
		t.Fatalf("lost ack duplicated notes %d %v", len(notes), e)
	}
	op.Payload = json.RawMessage(`{"Kind":"owner_reflection","Content":"different","AnchorJSON":"{}"}`)
	op.PayloadHash = store.OfflineOperationHash(op)
	syncBody, _ = json.Marshal(map[string]any{"device_id": device, "namespace": manifest.Namespace, "epoch": manifest.Epoch, "operations": []store.OfflineOperation{op}})
	r := queueJSONRequest(t, srv, session, "POST", "/api/offline/sync", string(syncBody), true)
	if r.Code != 409 || !strings.Contains(r.Body.String(), `"status":"conflict"`) {
		t.Fatalf("changed hash %d %s", r.Code, r.Body.String())
	}
	// Same length replacement cannot pass the frozen byte identity.
	changed := append([]byte{}, audio...)
	changed[0] = 'X'
	if err := os.WriteFile(filepath.Join(srv.cfg.EvidenceDir, "offline.mp3"), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if rec = queueJSONRequest(t, srv, session, "GET", file.URL, "", false); rec.Code != 409 {
		t.Fatalf("changed bytes %d", rec.Code)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/offline/revoke", enable, true); rec.Code != 200 {
		t.Fatalf("revoke %d", rec.Code)
	}
	if rec = queueJSONRequest(t, srv, session, "GET", file.URL, "", false); rec.Code != 403 {
		t.Fatalf("revoked %d", rec.Code)
	}
}

func TestOfflineHTTPEmptyBoundsAndStaticIsolation(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "offline-empty@example.com", "password123")
	device := uuid.NewString()
	if rec := queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", fmt.Sprintf(`{"device_id":%q}`, device), true); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec := queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[],"article_ids":[]}`, device), true)
	if rec.Code != 200 {
		t.Fatalf("empty manifest %d %s", rec.Code, rec.Body.String())
	}
	var m OfflineManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 {
		t.Fatal("empty should stay empty")
	}
	rec = queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", `{"device_id":"`+strings.Repeat("x", 65536)+`"}`, true)
	if rec.Code != 400 {
		t.Fatalf("oversize %d", rec.Code)
	}
	for _, path := range []string{"/offline", "/sw.js"} {
		rec = queueJSONRequest(t, srv, nil, "GET", path, "", false)
		if rec.Code != 200 {
			t.Fatalf("static %s %d", path, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("static shell must not issue csrf/session")
		}
		if strings.Contains(rec.Body.String(), session.Value) {
			t.Fatal("static private credential")
		}
	}
	rec = queueJSONRequest(t, srv, nil, "GET", "/sw.js", "", false)
	if rec.Header().Get("Service-Worker-Allowed") != "/" {
		t.Fatal("worker scope")
	}
}

func TestOfflineHTTPFrozenExcerptManifestAndIndependentSync(t *testing.T) {
	srv, session, device, ep, m := offlineAcceptanceFixture(t)
	snap, err := srv.store.GetSourceSnapshot(t.Context(), m.Files[0].SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := srv.store.CreateLearningExcerpt(t.Context(), snap.ID, []string{"seg-2"})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}],"excerpt_ids":[%q]}`, device, ep, ex.ID)
	rec := queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", body, true)
	if rec.Code != 200 {
		t.Fatalf("excerpt manifest %d %s", rec.Code, rec.Body.String())
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Excerpts) != 1 || m.Excerpts[0]["excerpt_id"] != ex.ID || m.Excerpts[0]["start_seconds"] != float64(1) || m.Excerpts[0]["end_seconds"] != float64(2) {
		t.Fatalf("frozen excerpt %+v", m.Excerpts)
	}
	progress := models.ListeningProgress{Mode: "excerpt", ExcerptID: ex.ID, AudioSHA256: ex.AudioSHA256, SnapshotID: ex.SnapshotID, SnapshotVersion: snap.ContentVersion, SegmentIDs: ex.SegmentIDs, StartSeconds: ex.StartSeconds, EndSeconds: ex.EndSeconds, ItemOffsetSeconds: 1.5, Speed: 1, Seq: 1}
	payload, _ := json.Marshal(progress)
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: m.Namespace, EditedAt: time.Now().Unix(), UUID: uuid.NewString(), Kind: "excerpt_progress", SourceType: models.SourceEpisode, SourceID: ep, SnapshotID: snap.ID, Payload: payload}
	for i := 0; i < 2; i++ {
		rec = offlineAcceptanceSync(t, srv, session, device, m, op)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ack"`) {
			t.Fatalf("excerpt sync %d %s", rec.Code, rec.Body.String())
		}
	}
	saved, err := srv.store.GetLearningExcerptProgress(t.Context(), models.SourceEpisode, ep, ex.ID)
	if err != nil || saved.Revision != 1 || saved.ItemOffsetSeconds != 1.5 {
		t.Fatalf("excerpt saved %+v %v", saved, err)
	}
	if _, err = srv.store.GetListeningProgressMode(t.Context(), models.SourceEpisode, ep, "original"); err != store.ErrNotFound {
		t.Fatalf("excerpt polluted whole source %v", err)
	}
	rec = queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[],"excerpt_ids":[%q]}`, device, ex.ID), true)
	if rec.Code != 409 {
		t.Fatalf("unselected excerpt source %d", rec.Code)
	}
}

func TestOfflineOperationBrowserGoHashInterop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable; browser hash interoperability requires Node")
	}
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: "namespace", EditedAt: 1700000000, UUID: "11111111-1111-4111-8111-111111111111", Kind: "owner_note", ExpectedRevision: 2, SourceType: models.SourceEpisode, SourceID: "source", SnapshotID: "snapshot", Payload: json.RawMessage(`{"Kind":"owner_reflection","Content":"听与学 <原音> & 理解\u2028 下一行\u2029","AnchorJSON":"{}"}`)}
	wire, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	script := `global.crypto=require('node:crypto').webcrypto;const core=require('./static/offline-core.js');core.operationHash(JSON.parse(process.argv[1])).then(hash=>process.stdout.write(hash));`
	output, err := exec.Command(node, "-e", script, string(wire)).CombinedOutput()
	if err != nil {
		t.Fatalf("browser hash %v %s", err, output)
	}
	if string(output) != store.OfflineOperationHash(op) {
		t.Fatalf("hash mismatch browser %s Go %s", output, store.OfflineOperationHash(op))
	}
}
