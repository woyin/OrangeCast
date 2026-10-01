package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func voiceHTTPFixture(t *testing.T) (*Server, *http.Cookie, []byte, string) {
	t.Helper()
	srv := newTestServer(t)
	srv.cfg.VoiceDir = filepath.Join(srv.cfg.DataDir, "voice-notes")
	srv.worker.WithVoice(srv.cfg.VoiceDir, "", "", "", "")
	cookie := claimOwnerAndLogin(t, srv, "voice@example.com", "password123")
	source, _ := seedKnowledgeLearning(t, srv)
	snap, err := srv.store.FreezeSourceSnapshot(t.Context(), models.SourceEpisode, source)
	if err != nil {
		t.Fatal(err)
	}
	capture, _ := json.Marshal(map[string]any{"sourceType": "episode", "sourceId": source, "title": "录音测试节目", "anchor": models.NoteAnchor{SnapshotID: snap.ID, Version: snap.ContentVersion, SegmentIDs: []string{"seg-1"}, Position: 2, Mode: "original"}})
	raw := make([]byte, 44+32000)
	copy(raw, "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 1)
	binary.LittleEndian.PutUint32(raw[24:], 16000)
	binary.LittleEndian.PutUint32(raw[28:], 32000)
	binary.LittleEndian.PutUint16(raw[32:], 2)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], 32000)
	return srv, cookie, raw, string(capture)
}
func voiceUploadRequest(t *testing.T, srv *Server, cookie *http.Cookie, id, capture string, audio []byte, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("draft_id", id)
	mw.WriteField("capture", capture)
	mw.WriteField("text", "我的录音草稿文字")
	file, _ := mw.CreateFormFile("audio", "recording.webm")
	file.Write(audio)
	mw.Close()
	req := httptest.NewRequest("POST", "/api/voice-notes/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "voice-csrf"})
		req.Header.Set("X-CSRF-Token", "voice-csrf")
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}
func voicePostJSON(srv *Server, cookie *http.Cookie, id string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/voice-notes/"+id, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: "voice-csrf"})
	req.Header.Set("X-CSRF-Token", "voice-csrf")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

type voiceHTTPTranscriber struct{ calls int }

func (f *voiceHTTPTranscriber) Name() string { return "voice-http-fixture" }
func (f *voiceHTTPTranscriber) Transcribe(string) (*provider.TranscriptResult, error) {
	f.calls++
	return &provider.TranscriptResult{Text: "转写建议不会覆盖Owner文字", Model: "fixture-asr", Usage: provider.TaskUsage{InputUnits: 3, OutputUnits: 2}}, nil
}

func TestVoiceHTTPPrivateUploadExplicitASRAndSave(t *testing.T) {
	srv, cookie, audio, capture := voiceHTTPFixture(t)
	id := uuid.NewString()
	rec := voiceUploadRequest(t, srv, cookie, id, capture, audio, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var d store.VoiceNoteDraft
	json.Unmarshal(rec.Body.Bytes(), &d)
	if d.State != "uploaded" || strings.Contains(rec.Body.String(), "audio_file") {
		t.Fatal(rec.Body.String())
	}
	duplicate := voiceUploadRequest(t, srv, cookie, id, capture, audio, true)
	if duplicate.Code != 200 {
		t.Fatal(duplicate.Code, duplicate.Body.String())
	}
	entries, _ := os.ReadDir(srv.cfg.VoiceDir)
	if len(entries) != 1 {
		t.Fatal("duplicate recording files", entries)
	}
	f := &voiceHTTPTranscriber{}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Transcription: f}, nil
	})
	for _, path := range []string{"/voice-notes", "/api/voice-notes/session", "/api/voice-notes/" + id, "/api/voice-notes/" + id + "/audio"} {
		page := doWithCookie(srv, cookie, "GET", path)
		if page.Code != 200 || page.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, page.Code, page.Body.String())
		}
	}
	if f.calls != 0 {
		t.Fatal("GET started ASR")
	}
	rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "transcribe", "expected_revision": 1})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "edit", "expected_revision": 1, "text": "我在等待期间编辑"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err := srv.worker.ProcessOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := srv.store.GetVoiceNoteDraft(t.Context(), id)
	if err != nil || current.Text != "我在等待期间编辑" || current.ASRText == "" || f.calls != 1 {
		t.Fatal(current, err, f.calls)
	}
	rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "adopt", "expected_revision": 2, "job_id": current.JobID})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "save", "expected_revision": 3, "keep_audio": false})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var saved struct {
		Saved bool
		Note  models.OwnerNote
	}
	json.Unmarshal(rec.Body.Bytes(), &saved)
	again := voicePostJSON(srv, cookie, id, map[string]any{"action": "save", "expected_revision": 3})
	if again.Code != 200 {
		t.Fatal(again.Code, again.Body.String())
	}
	if !saved.Saved || saved.Note.Kind != "owner_reflection" || saved.Note.CitationsJSON != "[]" {
		t.Fatal(saved)
	}
	entries, _ = os.ReadDir(srv.cfg.VoiceDir)
	if len(entries) != 0 {
		t.Fatal("default recording retention", entries)
	}
	if page := doWithCookie(srv, cookie, "GET", "/api/voice-notes/"+id+"/audio"); page.Code != 404 {
		t.Fatal(page.Code)
	}
}

func TestVoiceHTTPRejectsUnauthenticatedCSRFAndInvalidAudio(t *testing.T) {
	srv, cookie, audio, capture := voiceHTTPFixture(t)
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		csrf   bool
		audio  []byte
		want   int
	}{{"auth", nil, true, audio, 401}, {"csrf", cookie, false, audio, 403}, {"fake-mime", cookie, true, []byte("not audio"), 400}, {"empty", cookie, true, nil, 413}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := voiceUploadRequest(t, srv, tc.cookie, uuid.NewString(), capture, tc.audio, tc.csrf)
			if rec.Code != tc.want {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
	id := uuid.NewString()
	rec := voiceUploadRequest(t, srv, cookie, id, capture, audio, true)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "edit", "expected_revision": 99, "text": "旧窗口"}); rec.Code != 409 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = voicePostJSON(srv, cookie, id, map[string]any{"action": "delete", "expected_revision": 1}); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = doWithCookie(srv, cookie, "GET", "/api/voice-notes/"+id); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec = doWithCookie(srv, cookie, "GET", "/api/voice-notes/../secret"); rec.Code == 200 {
		t.Fatal("path accepted")
	}
}
