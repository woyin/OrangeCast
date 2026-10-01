package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

func queueJSONRequest(t *testing.T, srv *Server, session *http.Cookie, method, path, body string, withCSRF bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if session != nil {
		request.AddCookie(session)
	}
	if withCSRF {
		rec := doWithCookie(srv, session, http.MethodGet, "/dashboard")
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "cwp_csrf" {
				request.AddCookie(cookie)
				request.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
	}
	result := httptest.NewRecorder()
	srv.Router().ServeHTTP(result, request)
	return result
}

func TestListeningQueueHTTPPrivacyCASAndFallback(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "queue-http@example.com", "password123")
	for _, path := range []string{"/api/listening-queue", "/api/listening-session?source_type=episode&source_id=a&mode=original"} {
		if rec := queueJSONRequest(t, srv, nil, "GET", path, "", false); rec.Code != 401 {
			t.Fatal(path, rec.Code)
		}
	}
	rec := queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", `{"expected_revision":0,"action":"clear"}`, false)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	for _, body := range []string{`{}`, `{"action":"clear"}`, `{"expected_revision":0,"action":"clear","unknown":true}`, `{"expected_revision":0,"action":"clear"}{}`, `{`, strings.Repeat("x", 65537)} {
		if rec := queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", body, true); rec.Code != 400 {
			t.Fatal(body[:min(len(body), 80)], rec.Code)
		}
	}
	if rec := queueJSONRequest(t, srv, session, "PUT", "/api/listening-queue", `{}`, true); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://queue.test/feed", "队列原音", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "queue-http", Title: "未处理也可听", AudioURL: "https://media.test/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	add := `{"expected_revision":0,"action":"add","source_type":"episode","source_id":"` + eps[0].ID + `","mode":"original"}`
	rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", add, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var q models.ListeningQueue
	if err = json.Unmarshal(rec.Body.Bytes(), &q); err != nil || len(q.Items) != 1 || q.Autoplay || !q.Items[0].Unfrozen {
		t.Fatal(q, err)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", add, true); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", `{"expected_revision":0,"action":"clear"}`, true); rec.Code != 409 {
		t.Fatal(rec.Code)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", `{"expected_revision":1,"action":"remove","item_id":"missing"}`, true); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", `{"expected_revision":1,"action":"unknown"}`, true); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	for _, path := range []string{"/listening-queue", "/sources/episode/" + eps[0].ID, "/api/listening-queue"} {
		rec = doWithCookie(srv, session, "GET", path)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, rec.Code)
		}
	}
	rec = doWithCookie(srv, session, "GET", "/sources/episode/"+eps[0].ID)
	body := rec.Body.String()
	if !strings.Contains(body, `id="source-audio"`) || !strings.Contains(body, `controls`) || !strings.Contains(body, "未处理也可听") || !strings.Contains(body, `id="page-view"`) {
		t.Fatal(body)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/listening-queue", `{}`, true); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
}

func TestListeningSessionPhysicalFileAndExactPlan(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "queue-file@example.com", "password123")
	podcast, err := srv.store.CreatePodcast(t.Context(), "https://queue.test/physical", "文件", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.MergeEpisodes(t.Context(), podcast.ID, []models.Episode{{GUID: "queue-file", Title: "文件集", AudioURL: "https://media.test/file.mp3"}}); err != nil {
		t.Fatal(err)
	}
	eps, _ := srv.store.ListEpisodes(t.Context(), podcast.ID)
	id := eps[0].ID
	if err = srv.store.UpsertEvidenceAudio(t.Context(), models.SourceEpisode, id, "missing.wav", "wav", 100, "frozen"); err != nil {
		t.Fatal(err)
	}
	add := `{"expected_revision":0,"action":"add","source_type":"episode","source_id":"` + id + `","mode":"original"}`
	rec := queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", add, true)
	var q models.ListeningQueue
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if rec.Code != 200 || len(q.Items) != 1 || q.Items[0].Available || q.Items[0].Reason != "原音文件缺失" {
		t.Fatal(rec.Code, q)
	}
	play := `{"expected_revision":1,"action":"play","item_id":"` + q.Items[0].ID + `"}`
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", play, true); rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err = os.MkdirAll(srv.cfg.EvidenceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(srv.cfg.EvidenceDir, "missing.wav"), []byte("test bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	checkPath := "/api/listening-session?source_type=episode&source_id=" + id + "&mode=original&audio_sha256=frozen"
	rec = doWithCookie(srv, session, "GET", checkPath)
	var item models.ListeningQueueItem
	_ = json.Unmarshal(rec.Body.Bytes(), &item)
	if rec.Code != 200 || !item.Available {
		t.Fatal(rec.Code, item)
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-queue", play, true); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = doWithCookie(srv, session, "GET", strings.Replace(checkPath, "audio_sha256=frozen", "audio_sha256=old", 1)); rec.Code != 200 || !strings.Contains(rec.Body.String(), "原音已变化") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/listening-session?mode=other", "/api/listening-session?plan_version=wrong", "/api/listening-session?source_type=episode&source_id=" + id + "&mode=dj"} {
		if rec = doWithCookie(srv, session, "GET", path); rec.Code != 400 {
			t.Fatal(path, rec.Code)
		}
	}
	if rec = queueJSONRequest(t, srv, session, "POST", "/api/listening-session", "{}", true); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	seedHighlightAndNarration(t, srv, id)
	plan, err := srv.store.GetLatestDJPlanForSource(t.Context(), models.SourceEpisode, id)
	if err != nil {
		t.Fatal(err)
	}
	path := "/sources/episode/" + id + "/dj?plan_id=" + plan.ID + "&plan_version=1"
	if rec = doWithCookie(srv, session, "GET", path); rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-plan-id="`+plan.ID+`"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = doWithCookie(srv, session, "GET", strings.Replace(path, "plan_version=1", "plan_version=2", 1)); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, session, "GET", "/sources/episode/"+id+"/dj?plan_version=1"); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if err = os.Remove(filepath.Join(srv.cfg.EvidenceDir, "missing.wav")); err != nil {
		t.Fatal(err)
	}
	if rec = doWithCookie(srv, session, "GET", checkPath); rec.Code != 200 || !strings.Contains(rec.Body.String(), "原音文件缺失") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
