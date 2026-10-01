package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"github.com/woyin/orangecast/internal/voice"
)

func voiceJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func voiceError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, store.ErrConflict) {
		code = http.StatusConflict
	}
	if errors.Is(err, store.ErrNotFound) {
		code = http.StatusNotFound
	}
	http.Error(w, "语音草稿操作未完成，原文字仍保留："+err.Error(), code)
}

// File availability is read-only: restored temporary drafts may lack audio.
func (srv *Server) voiceAvailability(d *store.VoiceNoteDraft) {
	path, err := voice.Path(srv.cfg.VoiceDir, d.AudioFile)
	if err == nil {
		fi, e := os.Stat(path)
		d.AudioAvailable = e == nil && fi.Mode().IsRegular()
	}
	if !d.AudioAvailable && d.State != "saved" && d.Error == "" {
		d.Error = "录音文件不可用；文字与历史位置保留，可编辑后保存为笔记。"
	}
}

func (srv *Server) handleVoiceSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	questions, err := srv.store.ListLearningQuestions(r.Context(), "", "active")
	if err != nil {
		voiceError(w, err)
		return
	}
	if len(questions) > 100 {
		questions = questions[:100]
	}
	voiceJSON(w, map[string]any{"session_id": auth.PrivateSessionID(r), "csrf": auth.CSRFValue(r), "questions": questions})
}
func (srv *Server) handleVoiceNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	drafts, err := srv.store.ListVoiceNoteDrafts(r.Context(), offset)
	if err != nil {
		voiceError(w, err)
		return
	}
	for _, d := range drafts {
		srv.voiceAvailability(d)
	}
	next := -1
	if len(drafts) > 50 {
		drafts = drafts[:50]
		next = offset + 50
	}
	srv.tmpl.Render(w, "voice_notes.html", map[string]any{"Drafts": drafts, "CSRF": auth.CSRFValue(r), "NextOffset": next, "Offset": offset})
}

func (srv *Server) handleVoiceUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, voice.MaxBytes+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "录音请求超过20MB或格式无效", 413)
		return
	}
	defer r.MultipartForm.RemoveAll()
	input, header, err := r.FormFile("audio")
	if err != nil {
		voiceError(w, err)
		return
	}
	defer input.Close()
	if header.Size <= 0 || header.Size > voice.MaxBytes {
		http.Error(w, "录音为空或超过20MB", 413)
		return
	}
	if srv.cfg.VoiceDir == "" {
		http.Error(w, "私有录音目录未配置", 503)
		return
	}
	if err = os.MkdirAll(srv.cfg.VoiceDir, 0700); err != nil {
		voiceError(w, err)
		return
	}
	tmp, err := os.CreateTemp(srv.cfg.VoiceDir, ".voice-upload-*")
	if err != nil {
		voiceError(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(input, voice.MaxBytes+1))
	if err != nil || n > voice.MaxBytes {
		http.Error(w, "录音写入失败或超过20MB", 413)
		return
	}
	if err = tmp.Close(); err != nil {
		voiceError(w, err)
		return
	}
	audio, err := voice.Normalize(r.Context(), tmp.Name(), srv.cfg.VoiceDir)
	if err != nil {
		voiceError(w, err)
		return
	}
	path, _ := voice.Path(srv.cfg.VoiceDir, audio.File)
	var capture struct {
		SourceType string          `json:"sourceType"`
		SourceID   string          `json:"sourceId"`
		Anchor     json.RawMessage `json:"anchor"`
	}
	if len(r.FormValue("capture")) > 16000 || json.Unmarshal([]byte(r.FormValue("capture")), &capture) != nil {
		os.Remove(path)
		http.Error(w, "冻结播放位置无效", 400)
		return
	}
	d, created, err := srv.store.CreateVoiceNoteDraft(r.Context(), store.VoiceNoteDraft{ID: r.FormValue("draft_id"), SourceType: models.SourceType(capture.SourceType), SourceID: capture.SourceID, AnchorJSON: string(capture.Anchor), UploadSHA256: audio.UploadSHA256, AudioSHA256: audio.SHA256, AudioFile: audio.File, DurationSeconds: audio.Duration, SizeBytes: audio.Size, Text: r.FormValue("text")})
	if err != nil || !created {
		os.Remove(path)
	}
	if err != nil {
		voiceError(w, err)
		return
	}
	srv.voiceAvailability(d)
	voiceJSON(w, d)
}

func (srv *Server) handleVoiceDraft(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/voice-notes/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if r.Method == http.MethodGet {
		d, err := srv.store.GetVoiceNoteDraft(r.Context(), id)
		if err != nil {
			voiceError(w, err)
			return
		}
		if len(parts) == 2 && parts[1] == "audio" {
			path, e := voice.Path(srv.cfg.VoiceDir, d.AudioFile)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Disposition", "inline; filename=private-recording.wav")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.ServeFile(w, r, path)
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		srv.voiceAvailability(d)
		voiceJSON(w, d)
		return
	}
	if r.Method != http.MethodPost || len(parts) != 1 {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var action struct {
		Action           string `json:"action"`
		Revision         int    `json:"expected_revision"`
		Text             string `json:"text"`
		JobID            string `json:"job_id"`
		QuestionID       string `json:"question_id"`
		QuestionRevision int    `json:"question_revision"`
		KeepAudio        bool   `json:"keep_audio"`
	}
	if json.NewDecoder(r.Body).Decode(&action) != nil {
		http.Error(w, "请求无效", 400)
		return
	}
	var d *store.VoiceNoteDraft
	var err error
	switch action.Action {
	case "edit", "adopt":
		jobID := ""
		if action.Action == "adopt" {
			jobID = action.JobID
			if jobID == "" {
				http.Error(w, "缺少转写响应身份", 400)
				return
			}
		}
		d, err = srv.store.EditVoiceNoteDraft(r.Context(), id, action.Text, action.Revision, jobID)
	case "transcribe", "retry":
		st, e := srv.store.GetSettings(r.Context())
		if e != nil {
			voiceError(w, e)
			return
		}
		tc := provider.TaskConfig{Provider: ptrStr(st.TranscriptionProvider), Model: ptrStr(st.TranscriptionModel), Transcription: true}
		if tc.Provider == "" {
			tc.Provider = "groq"
		}
		if srv.cfg.VoiceASRProvider != "" {
			if tc.Provider != srv.cfg.VoiceASRProvider {
				tc.Model = ""
			}
			tc.Provider = srv.cfg.VoiceASRProvider
		}
		if srv.cfg.VoiceASRModel != "" {
			tc.Model = srv.cfg.VoiceASRModel
		}
		connection := "default"
		if srv.cfg.VoiceASRBaseURL != "" {
			connection = "voice"
		}
		connectionID := srv.selector.TranscriptionConnectionID(tc.Provider)
		if connection == "voice" {
			connectionID = provider.TranscriptionConnectionFingerprint(tc.Provider, srv.cfg.VoiceASRBaseURL)
		}
		d, err = srv.store.QueueVoiceASR(r.Context(), id, action.Revision, tc, connection, action.Action == "retry", connectionID)
	case "save":
		note, e := srv.store.SaveVoiceNoteDraft(r.Context(), id, action.Revision, action.QuestionID, action.QuestionRevision, action.KeepAudio)
		if e != nil {
			voiceError(w, e)
			return
		}
		if srv.worker != nil {
			_ = srv.worker.CleanupVoiceFiles(r.Context())
		}
		voiceJSON(w, map[string]any{"saved": true, "note": note})
		return
	case "delete":
		err = srv.store.DeleteVoiceNoteDraft(r.Context(), id, action.Revision)
		if err == nil {
			if srv.worker != nil {
				_ = srv.worker.CleanupVoiceFiles(r.Context())
			}
			voiceJSON(w, map[string]any{"deleted": true})
			return
		}
	default:
		http.Error(w, "操作无效", 400)
		return
	}
	if err != nil {
		voiceError(w, err)
		return
	}
	srv.voiceAvailability(d)
	voiceJSON(w, d)
}

func (srv *Server) handleVoicePrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	cents, err := strconv.ParseFloat(r.FormValue("cents_per_minute"), 64)
	if err == nil {
		err = srv.store.SetASRAudioPrice(r.Context(), r.FormValue("provider"), r.FormValue("model"), cents)
	}
	if err != nil {
		voiceError(w, err)
		return
	}
	http.Redirect(w, r, "/voice-notes", http.StatusSeeOther)
}
