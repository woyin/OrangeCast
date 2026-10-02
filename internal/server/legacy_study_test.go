package server

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestLegacyStudyHTTPRejectsUnidentifiedClientsWithoutPaying(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "legacy-id@example.com", "password123")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	t.Cleanup(remote.Close)
	srv.selector.ApplySettings("key", remote.URL, "", "")
	response := postForm(t, srv, cookie, "/api/study-chat", "source_type=episode&source_id=missing&question=q")
	if response.Code != 428 || calls != 0 || !strings.Contains(response.Body.String(), "request_identity_required") {
		t.Fatal(response.Code, response.Body.String(), calls)
	}
	var n int
	srv.store.DB.QueryRow(`SELECT count(*) FROM study_sessions`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func legacyHTTPFixture(t *testing.T) (*Server, *http.Cookie, string, *int) {
	t.Helper()
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "legacy-http@example.com", "password123")
	calls := new(int)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		t.Error("HTTP admission must not call supplier")
	}))
	t.Cleanup(remote.Close)
	srv.selector.ApplySettings("key", remote.URL, "", "")
	p, _ := srv.store.CreatePodcast(t.Context(), "https://legacy.example/feed", "Legacy", "", "")
	srv.store.MergeEpisodes(t.Context(), p.ID, []models.Episode{{GUID: "legacy", Title: "Ep", AudioURL: "https://audio.example/legacy"}})
	episodes, _ := srv.store.ListEpisodes(t.Context(), p.ID)
	id := episodes[0].ID
	seedTranscript(t, srv, id)
	return srv, cookie, id, calls
}
func TestLegacyStudyHTTPFrozenIdempotencyStatusAndCompatibilityHistory(t *testing.T) {
	srv, cookie, id, calls := legacyHTTPFixture(t)
	key := uuid.NewString()
	body := url.Values{"source_type": {"episode"}, "source_id": {id}, "question": {"解释通胀"}, "revision": {"1"}, "request_key": {key}}.Encode()
	response := postForm(t, srv, cookie, "/api/study-chat", body)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var data map[string]any
	json.Unmarshal(response.Body.Bytes(), &data)
	if data["session_id"] == "" || data["turn_id"] == "" || data["job_id"] == "" || data["generated"] != false {
		t.Fatal(data)
	}
	srv.selector.ApplySettings("key", "invalid-endpoint", "", "")
	response = postForm(t, srv, cookie, "/api/study-chat", body)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/study-chat/status?request_key="+key, nil)
	req.AddCookie(cookie)
	response = httptest.NewRecorder()
	srv.Router().ServeHTTP(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), data["job_id"].(string)) {
		t.Fatal(response.Code, response.Body.String())
	}
	response = postForm(t, srv, cookie, "/api/study-chat", strings.Replace(body, url.QueryEscape("解释通胀"), url.QueryEscape("篡改问题"), 1))
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	messages, _ := srv.store.ListStudyMessages(t.Context(), data["session_id"].(string), true)
	if len(messages) != 1 || *calls != 0 {
		t.Fatal(messages, *calls)
	}
}
func TestLegacyStudyHTTPForeignSessionNeverDispatchesOrAppends(t *testing.T) {
	srv, cookie, id, calls := legacyHTTPFixture(t)
	session, _ := srv.store.CreateStudySession(t.Context(), models.SourceEpisode, "different", "foreign")
	srv.store.AppendStudyMessage(t.Context(), session.ID, "user", "其他来源的历史", nil, false)
	body := url.Values{"source_type": {"episode"}, "source_id": {id}, "session_id": {session.ID}, "question": {"通胀"}, "revision": {"1"}, "request_key": {uuid.NewString()}}.Encode()
	response := postForm(t, srv, cookie, "/api/study-chat", body)
	if response.Code != 409 || *calls != 0 {
		t.Fatal(response.Code, response.Body.String(), *calls)
	}
	messages, _ := srv.store.ListStudyMessages(t.Context(), session.ID, true)
	if len(messages) != 1 {
		t.Fatal(messages)
	}
}
func TestLegacyStudyHTTPRetryRejectsUnknownWithoutExplicitConsent(t *testing.T) {
	srv, cookie, id, calls := legacyHTTPFixture(t)
	client, _ := srv.selector.LegacyStudy(provider.TaskConfig{Provider: "groq"})
	turn, job, _, err := srv.store.SubmitLegacyStudyTurn(t.Context(), models.SourceEpisode, id, "", 1, "通胀", uuid.NewString(), client.Config())
	if err != nil {
		t.Fatal(err)
	}
	srv.store.DB.Exec(`UPDATE processing_jobs SET status='failed',remote_call_started=1 WHERE id=?`, job.ID)
	srv.store.FailLegacyStudy(t.Context(), job.ID)
	var revision int
	srv.store.DB.QueryRow(`SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&revision)
	response := postForm(t, srv, cookie, "/api/study-chat/retry", url.Values{"request_key": {uuid.NewString()}, "job_id": {job.ID}, "job_revision": {strconv.Itoa(revision)}}.Encode())
	if response.Code != 409 || *calls != 0 {
		t.Fatal(response.Code, response.Body.String(), *calls)
	}
	current, _ := srv.store.GetLegacyStudyTurn(t.Context(), turn.ID)
	if current.State != "unknown" {
		t.Fatal(current)
	}
}
func TestLegacyStudyControllerPersistentIdentityAndRecovery(t *testing.T) {
	command := exec.Command("node", "testdata/legacy-study-controller.cjs")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
