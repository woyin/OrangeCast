package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/store"
)

func TestVoiceReadOnlyHistoryAndRestoredMissingAudio(t *testing.T) {
	srv, cookie, audio, capture := voiceHTTPFixture(t)
	id := uuid.NewString()
	rec := voiceUploadRequest(t, srv, cookie, id, capture, audio, true)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var d store.VoiceNoteDraft
	json.Unmarshal(rec.Body.Bytes(), &d)
	if !d.AudioAvailable {
		t.Fatal("uploaded audio unavailable")
	}
	stored, _ := srv.store.GetVoiceNoteDraft(t.Context(), id)
	os.Remove(filepath.Join(srv.cfg.VoiceDir, stored.AudioFile))
	rec = doWithCookie(srv, cookie, "GET", "/api/voice-notes/"+id)
	json.Unmarshal(rec.Body.Bytes(), &d)
	if d.AudioAvailable || !strings.Contains(d.Error, "录音文件不可用") {
		t.Fatal("restored missing recording hidden", rec.Body.String())
	}
	for i := 0; i < 51; i++ {
		_, err := srv.store.DB.Exec(`INSERT INTO voice_note_drafts(id,source_type,source_id,anchor_json,upload_sha256,audio_sha256,audio_file,duration_seconds,size_bytes,text)SELECT ?,source_type,source_id,anchor_json,upload_sha256,audio_sha256,'',1,1,'分页草稿' FROM voice_note_drafts WHERE id=?`, uuid.NewString(), id)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/voice-notes?offset=-1", "/voice-notes?offset=50", "/api/voice-notes/session"} {
		rec = doWithCookie(srv, cookie, "GET", path)
		if rec.Code != 200 {
			t.Fatal(path, rec.Code, rec.Body.String())
		}
	}
	var count int
	srv.store.DB.QueryRow(`SELECT count(*) FROM processing_jobs WHERE source_type='voice_note'`).Scan(&count)
	if count != 0 {
		t.Fatal("GET invoked ASR")
	}
	if rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "save", "expected_revision": 1}); rec.Code != 200 {
		t.Fatal("missing audio prevented text save", rec.Code, rec.Body.String())
	}
}

func TestVoiceHTTPMethodsActionsAndPriceValidation(t *testing.T) {
	srv, cookie, _, _ := voiceHTTPFixture(t)
	for _, handler := range []http.HandlerFunc{srv.handleVoiceSession, srv.handleVoiceNotes, srv.handleVoiceUpload, srv.handleVoicePrice} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("PATCH", "/voice-notes", nil))
		if rec.Code != 405 {
			t.Fatal("method guard", rec.Code)
		}
	}
	for _, path := range []string{"/api/voice-notes/", "/api/voice-notes/missing/extra/path", "/api/voice-notes/missing/audio"} {
		rec := httptest.NewRecorder()
		srv.handleVoiceDraft(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatal(path, rec.Code)
		}
	}
	for _, body := range []string{"invalid", `{"action":"invalid"}`, `{"action":"adopt"}`} {
		rec := httptest.NewRecorder()
		srv.handleVoiceDraft(rec, httptest.NewRequest("POST", "/api/voice-notes/missing", bytes.NewBufferString(body)))
		if rec.Code != 400 {
			t.Fatal(body, rec.Code)
		}
	}
	for _, price := range []string{"invalid", "-1", "NaN", "2.5"} {
		form := url.Values{"_csrf": {"voice-csrf"}, "provider": {"groq"}, "model": {"exact-asr"}, "cents_per_minute": {price}}
		req := httptest.NewRequest("POST", "/voice-notes/price", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "voice-csrf"})
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		want := 400
		if price == "2.5" {
			want = 303
		}
		if rec.Code != want {
			t.Fatal(price, rec.Code, rec.Body.String())
		}
	}
}
