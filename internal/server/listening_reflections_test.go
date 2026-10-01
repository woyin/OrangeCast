package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/backup"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

func reflectionPost(srv *Server, cookie *http.Cookie, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/listening-reflections", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "reflection-csrf"})
		req.Header.Set("X-CSRF-Token", "reflection-csrf")
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestListeningReflectionSaveHTTPReplayReadonlyAndLimits(t *testing.T) {
	srv, cookie, _, capture := voiceHTTPFixture(t)
	var c store.ListeningCapture
	if err := json.Unmarshal([]byte(capture), &c); err != nil {
		t.Fatal(err)
	}
	cmd := reflectionCommand{Action: "start", ID: uuid.NewString(), Capture: c}
	raw, _ := json.Marshal(cmd)
	for _, tc := range []struct {
		cookie *http.Cookie
		csrf   bool
		code   int
	}{{nil, true, 401}, {cookie, false, 403}, {cookie, true, 200}} {
		rec := reflectionPost(srv, tc.cookie, string(raw), tc.csrf)
		if rec.Code != tc.code {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	var before, after int
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&before)
	for _, path := range []string{"/api/listening-reflections", "/api/listening-reflections?id=" + cmd.ID} {
		rec := doWithCookie(srv, cookie, "GET", path)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/listening-reflections", "/listening-reflections/" + cmd.ID} {
		rec := doWithCookie(srv, cookie, "GET", path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "data-reflection-open") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	srv.store.DB.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("GET writes", before, after)
	}
	cmd = reflectionCommand{ID: cmd.ID, Action: "save", RequestKey: uuid.NewString(), ExpectedRevision: 1, Answers: store.ReflectionAnswers{Remember: "自己总结原音"}}
	raw, _ = json.Marshal(cmd)
	var first store.ListeningReflection
	for range 2 {
		rec := reflectionPost(srv, cookie, string(raw), true)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var r store.ListeningReflection
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if first.ID != "" && first.SavedNoteID != r.SavedNoteID {
			t.Fatal(first, r)
		}
		first = r
	}
	cmd.RequestKey = uuid.NewString()
	raw, _ = json.Marshal(cmd)
	if rec := reflectionPost(srv, cookie, string(raw), true); rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, raw := range []string{`{}`, `{"action":"wrong"}`, `{"action":"start","unexpected":true}`, `{} {}`, strings.Repeat("x", 65<<10)} {
		rec := reflectionPost(srv, cookie, raw, true)
		if rec.Code != 400 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	if rec := doWithCookie(srv, cookie, "GET", "/api/listening-reflections?id=missing"); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	req := httptest.NewRequest("DELETE", "/api/listening-reflections", nil)
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "reflection-csrf"})
	req.Header.Set("X-CSRF-Token", "reflection-csrf")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatal(rec.Code)
	}
	var n int
	srv.store.DB.QueryRow(`SELECT count(*) FROM owner_notes WHERE id=?`, first.SavedNoteID).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestVoiceReflectionHTTPUsesExplicitTextAndRejectsSecondSave(t *testing.T) {
	srv, cookie, audio, capture := voiceHTTPFixture(t)
	var c store.ListeningCapture
	json.Unmarshal([]byte(capture), &c)
	r, err := srv.store.StartListeningReflection(t.Context(), uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	voiceID := uuid.NewString()
	rec := voiceUploadRequest(t, srv, cookie, voiceID, capture, audio, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	cmd := reflectionCommand{ID: r.ID, Action: "voice_adopt", RequestKey: uuid.NewString(), ExpectedRevision: 1, VoiceID: voiceID, VoiceRevision: 1, Field: "apply"}
	raw, _ := json.Marshal(cmd)
	for range 2 {
		rec = reflectionPost(srv, cookie, string(raw), true)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	rec = voicePostJSON(srv, cookie, voiceID, map[string]any{"action": "save", "expected_revision": 1})
	if rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	got, err := srv.store.GetListeningReflection(t.Context(), r.ID)
	if err != nil || got.Answers.Apply != "我的录音草稿文字" || got.Answers.Remember != "" {
		t.Fatal(got, err)
	}
}

func TestListeningReflectionSaveV2BackupRestoresReceiptsAndRelations(t *testing.T) {
	srv, _, _, capture := voiceHTTPFixture(t)
	ctx := t.Context()
	var c store.ListeningCapture
	if err := json.Unmarshal([]byte(capture), &c); err != nil {
		t.Fatal(err)
	}
	q, err := srv.store.CreateLearningQuestion(ctx, store.LearningQuestion{Body: "备份后如何保留理解？"})
	if err != nil {
		t.Fatal(err)
	}
	savedDraft, err := srv.store.StartListeningReflection(ctx, uuid.NewString(), c, q.ID, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	answers := store.ReflectionAnswers{Remember: "自建保存记录", Apply: "检查唯一关系"}
	saved, err := srv.store.SaveListeningReflection(ctx, savedDraft.ID, key, 1, answers)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := srv.store.StartListeningReflection(ctx, uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = srv.store.ChangeListeningReflection(ctx, draft.ID, uuid.NewString(), "edit", 1, store.ReflectionAnswers{Uncertain: "未保存的私人理解"})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "reflections.tar.gz")
	if err = os.MkdirAll(srv.cfg.EvidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = backup.Create(ctx, srv.store, srv.cfg.EvidenceDir, archive, srv.cfg.VoiceDir); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "restored")
	if _, err = backup.Restore(ctx, archive, dir, false); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(dir, "cloudwisepod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	replay, err := restored.SaveListeningReflection(ctx, saved.ID, key, 1, answers)
	if err != nil || replay.SavedNoteID != saved.SavedNoteID {
		t.Fatal(replay, err)
	}
	got, err := restored.GetListeningReflection(ctx, draft.ID)
	if err != nil || got.Answers != draft.Answers || got.Revision != draft.Revision {
		t.Fatal(got, err)
	}
	links, err := restored.ListLearningQuestionRelations(ctx, q.ID)
	if err != nil || len(links) != 1 || links[0].ObjectID != saved.SavedNoteID {
		t.Fatal(links, err)
	}
	if err = restored.DeleteSourceRows(ctx, models.SourceEpisode, c.SourceID); err != nil {
		t.Fatal(err)
	}
	got, err = restored.GetListeningReflection(ctx, draft.ID)
	if err != nil || got.State != "unavailable" || got.Answers.Uncertain != "" {
		t.Fatal(got, err)
	}
	if _, err = restored.GetOwnerNote(ctx, saved.SavedNoteID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	var receipts int
	restored.DB.QueryRow(`SELECT count(*) FROM listening_reflection_actions`).Scan(&receipts)
	if receipts != 0 {
		t.Fatal(receipts)
	}
}
