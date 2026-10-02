package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func offlineAcceptanceFixture(t *testing.T) (*Server, *http.Cookie, string, string, OfflineManifest) {
	t.Helper()
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "offline-accept@example.com", "password123")
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) {
		panic("offline path invoked a model provider")
	}
	device := uuid.NewString()
	if r := queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", fmt.Sprintf(`{"device_id":%q}`, device), true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	ep := seedEpisodeWithTranscript(t, srv, "offline-acceptance")
	audio := []byte("original-local-evidence")
	if err := os.MkdirAll(srv.cfg.EvidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.cfg.EvidenceDir, "accept.mp3"), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, "accept.mp3", "mp3", int64(len(audio)), fmt.Sprintf("%x", sha256.Sum256(audio))); err != nil {
		t.Fatal(err)
	}
	r := queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}]}`, device, ep), true)
	if r.Code != 200 {
		t.Fatalf("manifest %d %s", r.Code, r.Body.String())
	}
	var m OfflineManifest
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return srv, session, device, ep, m
}

func offlineAcceptanceSync(t *testing.T, srv *Server, session *http.Cookie, device string, m OfflineManifest, op store.OfflineOperation) *httptest.ResponseRecorder {
	t.Helper()
	op.PayloadHash = store.OfflineOperationHash(op)
	body, err := json.Marshal(map[string]any{"device_id": device, "namespace": m.Namespace, "epoch": m.Epoch, "operations": []store.OfflineOperation{op}})
	if err != nil {
		t.Fatal(err)
	}
	return queueJSONRequest(t, srv, session, "POST", "/api/offline/sync", string(body), true)
}

func TestOfflineAcceptanceChangedSourceClearDownloadsPreservesDraft(t *testing.T) {
	srv, session, device, ep, m := offlineAcceptanceFixture(t)
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: m.Namespace, UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: ep, SnapshotID: m.Files[0].SnapshotID, EditedAt: time.Now().Unix(), Payload: json.RawMessage(`{"content":"断网期间整理的独立想法"}`)}
	r := offlineAcceptanceSync(t, srv, session, device, m, op)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"ack"`) {
		t.Fatalf("draft %d %s", r.Code, r.Body.String())
	}
	drafts, err := srv.store.ListOfflineOrganizingDrafts(t.Context())
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts %v %v", drafts, err)
	}
	// Replace the real current transcript, rather than manipulating a pack's hash.
	var jobID string
	if err := srv.store.DB.QueryRowContext(t.Context(), `SELECT job_id FROM artifact_versions WHERE source_type=? AND source_id=? AND kind='transcript' ORDER BY version DESC LIMIT 1`, models.SourceEpisode, ep).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	v, err := srv.store.CreateArtifactVersion(t.Context(), models.SourceEpisode, ep, "transcript", "test", "test", "1", jobID, `{"language":"zh","text":"替换原文","segments":[{"id":"new-1","start":0,"end":1,"text":"替换原文"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.store.SetCurrentVersion(t.Context(), models.SourceEpisode, ep, "transcript", v); err != nil {
		t.Fatal(err)
	}
	if r = queueJSONRequest(t, srv, session, "GET", "/api/offline/status?device_id="+device, "", false); r.Code != 409 {
		t.Fatalf("changed status %d %s", r.Code, r.Body.String())
	}
	op.UUID = uuid.NewString()
	r = offlineAcceptanceSync(t, srv, session, device, m, op)
	if r.Code != 409 || !strings.Contains(r.Body.String(), `"status":"conflict"`) {
		t.Fatalf("stale operation %d %s", r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, session, "POST", "/api/offline/clear-downloads", fmt.Sprintf(`{"device_id":%q}`, device), true)
	if r.Code != 200 {
		t.Fatalf("clear %d %s", r.Code, r.Body.String())
	}
	if r = queueJSONRequest(t, srv, session, "GET", "/api/offline/status?device_id="+device, "", false); r.Code != 200 {
		t.Fatalf("recovered %d %s", r.Code, r.Body.String())
	}
	draft, err := srv.store.GetOfflineOrganizingDraft(t.Context(), drafts[0].ID)
	if err != nil || draft.Content != "断网期间整理的独立想法" || draft.SourceAvailable {
		t.Fatalf("preserve independent draft %+v %v", draft, err)
	}
	if r = queueJSONRequest(t, srv, session, "GET", m.Files[0].URL, "", false); r.Code == 200 {
		t.Fatal("cleared pack remained downloadable")
	}
}

func TestOfflineAcceptanceRestartAndNewLoginRejectOldAuthorization(t *testing.T) {
	srv, session, device, _, m := offlineAcceptanceFixture(t)
	req := httptest.NewRequest("GET", "/login", nil)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	var csrf *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cwp_csrf" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no csrf")
	}
	req = httptest.NewRequest("POST", "/login", strings.NewReader("_csrf="+csrf.Value+"&email=offline-accept@example.com&password=password123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrf)
	rr = httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	var second *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cwp_session" {
			second = c
		}
	}
	if rr.Code != 303 || second == nil || second.Value == session.Value {
		t.Fatalf("new login %d", rr.Code)
	}
	for _, path := range []string{"/api/offline/status?device_id=" + device, m.Files[0].URL} {
		r := queueJSONRequest(t, srv, second, "GET", path, "", false)
		if r.Code != 401 && r.Code != 403 {
			t.Fatalf("new session accessed old grant %d %s", r.Code, r.Body.String())
		}
	}
	// This is the startup invalidation invoked by a new server instance on the same DB.
	if err := srv.store.BeginOfflineInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/offline/status?device_id=" + device, m.Files[0].URL} {
		r := queueJSONRequest(t, srv, session, "GET", path, "", false)
		if r.Code != 403 {
			t.Fatalf("old epoch %d %s", r.Code, r.Body.String())
		}
	}
}

func TestOfflineAcceptanceDeletedSourceNeverAcknowledgesPendingMutation(t *testing.T) {
	srv, session, device, ep, m := offlineAcceptanceFixture(t)
	if err := srv.store.DeleteSourceRows(t.Context(), models.SourceEpisode, ep); err != nil {
		t.Fatal(err)
	}
	op := store.OfflineOperation{SchemaVersion: 1, Namespace: m.Namespace, UUID: uuid.NewString(), Kind: "organizing_draft", SourceType: models.SourceEpisode, SourceID: ep, SnapshotID: m.Files[0].SnapshotID, EditedAt: time.Now().Unix(), Payload: json.RawMessage(`{"content":"尚未同步的文字"}`)}
	r := offlineAcceptanceSync(t, srv, session, device, m, op)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"unavailable"`) || strings.Contains(r.Body.String(), `"status":"ack"`) {
		t.Fatalf("deleted source %d %s", r.Code, r.Body.String())
	}
	drafts, err := srv.store.ListOfflineOrganizingDrafts(t.Context())
	if err != nil || len(drafts) != 0 {
		t.Fatalf("unavailable mutation persisted %+v %v", drafts, err)
	}
}

func TestOfflineAcceptanceEvidencePathCannotEscapeAndRemoteOnlyCannotDownload(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "remote-only"} {
		t.Run(kind, func(t *testing.T) {
			srv, session, device, ep, m := offlineAcceptanceFixture(t)
			// Metadata has the genuine external byte hash: rejection must rely on the
			// evidence directory boundary, not accidental hash mismatch.
			outside := filepath.Join(t.TempDir(), "private.mp3")
			content := []byte("PRIVATE OUTSIDE EVIDENCE ROOT")
			if err := os.WriteFile(outside, content, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "traversal":
				rel, err := filepath.Rel(srv.cfg.EvidenceDir, outside)
				if err != nil {
					t.Fatal(err)
				}
				if err = srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, rel, "mp3", int64(len(content)), fmt.Sprintf("%x", sha256.Sum256(content))); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(outside, filepath.Join(srv.cfg.EvidenceDir, "outside.mp3")); err != nil {
					t.Fatal(err)
				}
				if err := srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, ep, "outside.mp3", "mp3", int64(len(content)), fmt.Sprintf("%x", sha256.Sum256(content))); err != nil {
					t.Fatal(err)
				}
			case "remote-only":
				if _, err := srv.store.DB.ExecContext(t.Context(), `DELETE FROM evidence_audio WHERE source_type=? AND source_id=?`, models.SourceEpisode, ep); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []*httptest.ResponseRecorder{
				queueJSONRequest(t, srv, session, "GET", m.Files[0].URL, "", false),
				queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}]}`, device, ep), true),
			} {
				if r.Code >= 200 && r.Code < 300 {
					t.Fatalf("unsafe audio accepted %d %s", r.Code, r.Body.String())
				}
				if strings.Contains(r.Body.String(), string(content)) {
					t.Fatal("external private bytes leaked")
				}
			}
		})
	}
}

func TestOfflineAcceptanceFrozenNoteDownloadAndCatalog(t *testing.T) {
	srv, session, device, ep, _ := offlineAcceptanceFixture(t)
	note, err := srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "第一版个人思考", ReferencesJSON: `["seg-1"]`})
	if err != nil {
		t.Fatal(err)
	}
	r := queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q}]}`, device, ep), true)
	if r.Code != 200 {
		t.Fatalf("manifest %d %s", r.Code, r.Body.String())
	}
	var m OfflineManifest
	if err = json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	var file OfflineFile
	for _, f := range m.Files {
		if f.Kind == "note" {
			file = f
		}
	}
	if file.ObjectID != note.ID || file.Revision != 1 {
		t.Fatalf("note identity %+v", file)
	}
	if _, err = srv.store.UpdateOwnerNote(t.Context(), note.ID, "第二版个人思考", note.CitationsJSON, note.ReferencesJSON, note.Revision); err != nil {
		t.Fatal(err)
	}
	r = queueJSONRequest(t, srv, session, "GET", file.URL, "", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "第一版个人思考") || strings.Contains(r.Body.String(), "第二版个人思考") {
		t.Fatalf("frozen note %d %s", r.Code, r.Body.String())
	}
	if fmt.Sprintf("%x", sha256.Sum256(r.Body.Bytes())) != file.SHA256 {
		t.Fatal("frozen note hash mismatch")
	}
	r = queueJSONRequest(t, srv, session, "GET", "/api/offline/catalog", "", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), ep) || !strings.Contains(r.Body.String(), "offline-acceptance 单集") {
		t.Fatalf("catalog %d %s", r.Code, r.Body.String())
	}
	for _, path := range []string{"/api/offline/session", "/api/offline/drafts"} {
		r = queueJSONRequest(t, srv, session, "GET", path, "", false)
		if r.Code != 200 {
			t.Fatalf("read %s %d %s", path, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), session.Value) {
			t.Fatal("read leaked session credential")
		}
	}
}

func TestOfflineAcceptancePassedArticleOnlyDownloadRevokedPolicy(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "offline-article@example.com", "password123")
	ep, _ := seedKnowledgeLearning(t, srv)
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel, srv.cfg.PodReviewModel = "https://example.test/v1", "test", "writer", "reviewer"
	fake := &knowledgeStageFake{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{KnowledgeArticle: fake}, nil
	})
	r := postForm(t, srv, session, "/knowledge-articles/generate", "")
	if r.Code != 303 {
		t.Fatal(r.Code, r.Body.String())
	}
	for i := 0; i < 4; i++ {
		if err := srv.worker.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	article, err := srv.store.GetKnowledgeArticle(t.Context(), strings.TrimPrefix(r.Header().Get("Location"), "/knowledge-articles/"))
	if err != nil || article.PassedRevision != 1 {
		t.Fatalf("passed article %+v %v", article, err)
	}
	before := fake.calls
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { panic("offline article invoked provider") }
	device := uuid.NewString()
	r = queueJSONRequest(t, srv, session, "POST", "/api/offline/enable", fmt.Sprintf(`{"device_id":%q}`, device), true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, session, "GET", "/api/offline/catalog", "", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), article.ID) || !strings.Contains(r.Body.String(), "怎样区分来源与个人理解") {
		t.Fatalf("article catalog %d %s", r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, session, "POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[],"article_ids":[%q]}`, device, article.ID), true)
	if r.Code != 200 {
		t.Fatalf("article manifest %d %s", r.Code, r.Body.String())
	}
	var m OfflineManifest
	if err = json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 1 || m.Files[0].Kind != "article" || m.Files[0].Revision != 1 {
		t.Fatalf("article file %+v", m.Files)
	}
	r = queueJSONRequest(t, srv, session, "GET", m.Files[0].URL, "", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"passed":true`) || !strings.Contains(r.Body.String(), "怎样区分来源与个人理解") {
		t.Fatalf("article download %d %s", r.Code, r.Body.String())
	}
	if fmt.Sprintf("%x", sha256.Sum256(r.Body.Bytes())) != m.Files[0].SHA256 {
		t.Fatal("article hash mismatch")
	}
	policy, err := srv.store.GetSourcePolicy(t.Context(), models.SourceEpisode, ep)
	if err != nil {
		t.Fatal(err)
	}
	policy.ModelDataPolicy = models.ModelDataLocalOnly
	if err = srv.store.UpdateSourcePolicy(t.Context(), models.SourceEpisode, ep, *policy); err != nil {
		t.Fatal(err)
	}
	r = queueJSONRequest(t, srv, session, "GET", m.Files[0].URL, "", false)
	if r.Code != 409 {
		t.Fatalf("policy revoked article %d %s", r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, session, "GET", "/api/offline/status?device_id="+device, "", false)
	if r.Code != 409 {
		t.Fatalf("article-only stale pack %d %s", r.Code, r.Body.String())
	}
	r = queueJSONRequest(t, srv, session, "GET", "/api/offline/catalog", "", false)
	if r.Code != 200 || strings.Contains(r.Body.String(), article.ID) {
		t.Fatalf("catalog kept revoked article %d %s", r.Code, r.Body.String())
	}
	if fake.calls != before {
		t.Fatal("offline caused article model work")
	}
}

func TestOfflineAcceptanceMalformedCommandsDoNotChangeAuthorizationOrPacks(t *testing.T) {
	srv, session, device, ep, m := offlineAcceptanceFixture(t)
	tooMany := make([]map[string]string, 11)
	for i := range tooMany {
		tooMany[i] = map[string]string{"source_type": "episode", "source_id": ep}
	}
	overLimit, _ := json.Marshal(map[string]any{"device_id": device, "sources": tooMany})
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/offline/manifest", `{`, 400},
		{"POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q} {}`, device), 400},
		{"POST", "/api/offline/manifest", string(overLimit), 400},
		{"POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"episode","source_id":%q},{"source_type":"episode","source_id":%q}]}`, device, ep, ep), 400},
		{"POST", "/api/offline/manifest", fmt.Sprintf(`{"device_id":%q,"sources":[{"source_type":"document","source_id":"unsupported"}]}`, device), 400},
		{"POST", "/api/offline/unknown", `{}`, 404},
		{"GET", "/api/offline/enable", "", 405},
		{"POST", "/api/offline/status", `{}`, 405},
		{"POST", "/api/offline/file", `{}`, 405},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := queueJSONRequest(t, srv, session, c.method, c.path, c.body, true)
			if r.Code != c.status {
				t.Fatalf("%s %s %d %s", c.method, c.path, r.Code, r.Body.String())
			}
		})
	}
	r := queueJSONRequest(t, srv, session, "GET", m.Files[0].URL, "", false)
	if r.Code != 200 {
		t.Fatalf("invalid requests damaged existing pack %d %s", r.Code, r.Body.String())
	}
}
