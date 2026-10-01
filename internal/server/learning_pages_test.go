package server

import (
	"context"
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLearningNotesSearchHistoryAndFrozenEvidence(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "pages@example.com", "password123")
	ep, noteID := seedKnowledgeLearning(t, srv)
	for i := 0; i < 22; i++ {
		if _, e := srv.store.CreateOwnerNote(t.Context(), models.OwnerNote{SourceType: "episode", SourceID: ep, Kind: "owner_reflection", Content: "需要回看的个人理解"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, path := range []string{"/notes", "/notes?page=2", "/notes?kind=keypoint", "/notes?source=episode:" + ep, "/notes/" + noteID + "/history"} {
		rec := doWithCookie(srv, session, "GET", path)
		if rec.Code != 200 {
			t.Fatal(path, rec.Code, rec.Body.String())
		}
	}
	if rec := postForm(t, srv, session, "/notes", ""); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	n, _ := srv.store.GetOwnerNote(t.Context(), noteID)
	var a models.NoteAnchor
	_ = json.Unmarshal([]byte(n.AnchorJSON), &a)
	if rec := doWithCookie(srv, session, "GET", "/evidence/"+a.SnapshotID); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := postForm(t, srv, session, "/evidence/"+a.SnapshotID, ""); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	for _, path := range []string{"/evidence/", "/evidence/a/b", "/evidence/missing", "/notes/missing/history"} {
		if rec := doWithCookie(srv, session, "GET", path); rec.Code != 404 {
			t.Fatal(path, rec.Code)
		}
	}
	if _, e := srv.store.DB.ExecContext(t.Context(), `UPDATE source_snapshots SET status='purged' WHERE id=?`, a.SnapshotID); e != nil {
		t.Fatal(e)
	}
	if rec := doWithCookie(srv, session, "GET", "/evidence/"+a.SnapshotID); rec.Code != 410 {
		t.Fatal(rec.Code)
	}
}
func TestLearningSchedulersRespectIndependentOptIn(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "schedules@example.com", "password123")
	_ = session
	ctx, cancel := context.WithCancel(t.Context())
	srv.StartLearningReviews(ctx)
	srv.StartKnowledgeArticles(ctx)
	cancel()
	prefs := store.LearningReviewSettings{Enabled: true, Timezone: "UTC", Weekday: 1, ClockTime: "00:00"}
	if e := srv.store.SetLearningReviewSettings(t.Context(), prefs); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if e := srv.RunLearningReviews(t.Context(), now); e == nil {
		t.Fatal("missing POD silently admitted")
	}
	srv.cfg.PodBaseURL, srv.cfg.PodAPIKey, srv.cfg.PodModel = "https://example.test/v1", "test", "model"
	if e := srv.RunLearningReviews(t.Context(), now); e != nil {
		t.Fatal(e)
	}
	_, _ = seedKnowledgeLearning(t, srv)
	if e := srv.RunLearningReviews(t.Context(), time.Now()); e != nil {
		t.Fatal(e)
	}
	batches, e := srv.store.ListLearningReviewBatches(t.Context())
	if e != nil || len(batches) != 1 {
		t.Fatal(batches, e)
	}
	if e := srv.RunLearningReviews(t.Context(), time.Now()); e != nil {
		t.Fatal(e)
	}
	if rec := postForm(t, srv, session, "/review/action", "action=settings&timezone=bad&weekday=1&clock_time=18%3A00"); rec.Code != 400 || !strings.Contains(rec.Body.String(), `value="bad"`) {
		t.Fatal("settings draft lost", rec.Code)
	}
	if rec := postForm(t, srv, session, "/review/action", "action=settings&timezone=UTC&weekday=bad&clock_time=18%3A00"); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := postForm(t, srv, session, "/knowledge-articles/candidate", "candidate=missing"); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := doWithCookie(srv, session, "GET", "/knowledge-articles/candidate"); rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	prefs.Enabled = false
	_ = srv.store.SetLearningReviewSettings(t.Context(), prefs)
	if e := srv.RunLearningReviews(t.Context(), time.Now().AddDate(0, 0, 7)); e != nil {
		t.Fatal(e)
	}
}
func TestLearningReadFailuresAreVisible(t *testing.T) {
	srv := newTestServer(t)
	_ = srv.store.DB.Close()
	checks := map[string]func(http.ResponseWriter, *http.Request){"notes": srv.handleNotes, "articles": srv.handleKnowledgeArticles, "review": srv.handleLearningReview, "notehistory": srv.handleNoteHistory, "evidence": srv.handleFrozenEvidence}
	for name, handler := range checks {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("GET", "/"+name+"/missing/history", nil))
		if rec.Code < 400 {
			t.Fatal(name, "database failure hidden", rec.Code)
		}
	}
	if e := srv.RunLearningReviews(t.Context(), time.Now()); e == nil {
		t.Fatal("scheduler swallowed DB error")
	}
}

func TestKnowledgeScopeAndSettingsErrorsKeepSubmittedFields(t *testing.T) {
	srv := newTestServer(t)
	session := claimOwnerAndLogin(t, srv, "preserve@example.com", "password123")
	for _, tc := range []struct{ path, body, want string }{
		{"/knowledge-articles/generate", "theme=保留范围草稿&from=wrong", "保留范围草稿"},
		{"/knowledge-articles/settings", "daily_limit=bad&debounce_minutes=0&audience=保留读者&style=保留风格", "保留风格"},
		{"/knowledge-articles/settings", "enabled=on&daily_limit=1&debounce_minutes=0&audience=保留读者&style=保留风格", "保留读者"},
		{"/knowledge-articles/settings", "daily_limit=11&debounce_minutes=0&audience=保留读者&style=保留风格", "保留风格"},
	} {
		rec := postForm(t, srv, session, tc.path, tc.body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), tc.want) || !strings.Contains(rec.Body.String(), "输入已保留") {
			t.Fatal(tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestDJFrozenAudioAndNoteSegmentsAreUsable(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.NarrationDir = filepath.Join(srv.cfg.DataDir, "narrations")
	for _, dir := range []string{srv.cfg.EvidenceDir, srv.cfg.NarrationDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	session := claimOwnerAndLogin(t, srv, "dj-frozen@example.com", "password123")
	fixture := seedBrowserAcceptanceFixture(t, srv, session)
	rec := doWithCookie(srv, session, "GET", "/sources/episode/"+fixture.LearningSourceID+"/dj")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-note-segments="[`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	plans, err := srv.store.GetLatestDJPlanForSource(t.Context(), models.SourceEpisode, fixture.LearningSourceID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		SnapshotID string `json:"source_snapshot_id"`
	}
	_ = json.Unmarshal([]byte(plans.InputSnapshotJSON), &input)
	if input.SnapshotID == "" {
		t.Fatal("new DJ did not freeze source")
	}
	if _, err = srv.store.DB.ExecContext(t.Context(), `UPDATE evidence_audio SET sha256='replacement' WHERE source_id=?`, fixture.LearningSourceID); err != nil {
		t.Fatal(err)
	}
	rec = doWithCookie(srv, session, "GET", "/sources/episode/"+fixture.LearningSourceID+"/dj")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "原音已替换") || strings.Contains(rec.Body.String(), `src="/api/audio/episode/`) {
		t.Fatal("new audio impersonated frozen plan", rec.Code, rec.Body.String())
	}
}
