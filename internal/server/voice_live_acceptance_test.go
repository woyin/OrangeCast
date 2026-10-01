package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// TestVoiceLiveAcceptance is opt-in, uses self-authored speech and reads local settings without modifying the Owner database.
func TestVoiceLiveAcceptance(t *testing.T) {
	if os.Getenv("CWP_VOICE_LIVE") != "1" {
		t.Skip("explicit live flag required")
	}
	sourceDB := os.Getenv("CWP_VOICE_SETTINGS_DB")
	if sourceDB == "" {
		t.Fatal("read-only settings database path required")
	}
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(sourceDB)+"?mode=ro")
	if e != nil {
		t.Fatal("settings database unavailable")
	}
	defer db.Close()
	var st models.Settings
	if e = db.QueryRow(`SELECT groq_api_key,groq_base_url,openai_api_key,openai_base_url,transcription_provider,transcription_model FROM settings WHERE id=1`).Scan(&st.GroqAPIKey, &st.GroqBaseURL, &st.OpenAIAPIKey, &st.OpenAIBaseURL, &st.TranscriptionProvider, &st.TranscriptionModel); e != nil {
		t.Fatal("settings snapshot unavailable")
	}
	tc := provider.TaskConfig{Provider: ptrStr(st.TranscriptionProvider), Model: ptrStr(st.TranscriptionModel), Transcription: true}
	if tc.Provider == "" {
		tc.Provider = "groq"
	}
	selector := provider.NewSelector("", "")
	selector.ApplySettingsFrom(&st)
	bundle, e := selector.BundleForTask(tc)
	if e != nil || bundle.Transcription == nil {
		t.Fatal("configured audio provider unavailable")
	}
	reportDir := os.Getenv("CWP_VOICE_LIVE_REPORT")
	if reportDir == "" {
		t.Fatal("private report directory required")
	}
	if e = os.MkdirAll(reportDir, 0700); e != nil {
		t.Fatal(e)
	}
	report := map[string]any{"kind": "self-authored-synthetic-speech", "human": nil, "provider": tc.Provider, "model": provider.EffectiveTaskModel(tc), "status": "preparing", "owner_microphone": false, "mobile": false, "actual_cost_known": false}
	defer func() {
		raw, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(filepath.Join(reportDir, "report.json"), raw, 0600)
	}()
	original := "保存来源证据，区分自己的理解，核对后再保存笔记。"
	aiff := filepath.Join(reportDir, "self-authored.aiff")
	wav := filepath.Join(reportDir, "self-authored.wav")
	if out, e := exec.Command("say", "-v", "Tingting", "-r", "165", "-o", aiff, original).CombinedOutput(); e != nil {
		t.Fatal("local speech generation failed", string(out))
	}
	if out, e := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", aiff, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-y", wav).CombinedOutput(); e != nil {
		t.Fatal("audio normalization failed", string(out))
	}
	raw, e := os.ReadFile(wav)
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(wav, 0600)
	os.Chmod(aiff, 0600)
	srv, cookie, _, capture := voiceHTTPFixture(t)
	settings, e := srv.store.GetSettings(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	settings.TranscriptionProvider = &tc.Provider
	settings.TranscriptionModel = &tc.Model
	if e = srv.store.UpdateSettings(t.Context(), settings); e != nil {
		t.Fatal(e)
	}
	var budget sql.NullInt64
	rows, e := db.Query(`PRAGMA table_info(settings)`)
	if e != nil {
		t.Fatal("budget schema unavailable")
	}
	hasBudget := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if e = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); e != nil {
			rows.Close()
			t.Fatal("budget schema unavailable")
		}
		if name == "monthly_budget_cents" {
			hasBudget = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Fatal("budget schema unavailable")
	}
	if hasBudget {
		if e = db.QueryRow(`SELECT monthly_budget_cents FROM settings WHERE id=1`).Scan(&budget); e != nil {
			t.Fatal("budget snapshot unavailable")
		}
	} else {
		report["budget_snapshot"] = "legacy settings schema has no monthly budget field"
	}
	if budget.Valid {
		var used, held float64
		if e = db.QueryRow(`SELECT COALESCE(SUM(estimated_cost),0) FROM usage_records WHERE estimated_cost IS NOT NULL AND created_at>=datetime('now','start of month')`).Scan(&used); e != nil {
			t.Fatal("usage snapshot unavailable")
		}
		if e = db.QueryRow(`SELECT COALESCE(SUM(estimated_cost_cents),0) FROM budget_reservations WHERE status IN('held','pending_remote') AND created_at>=datetime('now','start of month')`).Scan(&held); e != nil {
			t.Fatal("held budget snapshot unavailable")
		}
		remaining := budget.Int64 - int64(used+held)
		if remaining < 0 {
			remaining = 0
		}
		if e = srv.store.SetOwnerMonthlyBudget(t.Context(), &remaining); e != nil {
			t.Fatal(e)
		}
		var price float64
		if e = db.QueryRow(`SELECT cents_per_minute FROM asr_audio_prices WHERE provider=? AND model=?`, tc.Provider, provider.EffectiveTaskModel(tc)).Scan(&price); e != nil {
			report["status"] = "blocked_audio_price_missing"
			t.Skip("existing budget requires a verified frozen audio estimate")
		}
		if e = srv.store.SetASRAudioPrice(t.Context(), tc.Provider, provider.EffectiveTaskModel(tc), price); e != nil {
			t.Fatal(e)
		}
	}
	response := voiceUploadRequest(t, srv, cookie, uuid.NewString(), capture, raw, true)
	if response.Code != 200 {
		report["status"] = "upload_failed"
		t.Fatal("private upload failed")
	}
	var uploaded store.VoiceNoteDraft
	if e = json.Unmarshal(response.Body.Bytes(), &uploaded); e != nil || uploaded.ID == "" {
		report["status"] = "upload_response_invalid"
		t.Fatal("draft response unavailable")
	}
	d := &uploaded
	response = voicePostJSON(srv, cookie, d.ID, map[string]any{"action": "transcribe", "expected_revision": d.Revision})
	if response.Code != 200 {
		report["status"] = "admission_failed"
		t.Fatal("explicit transcription admission failed")
	}
	d, e = srv.store.GetVoiceNoteDraft(t.Context(), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	ownerText := "我在等待期间写下的自己的解释。"
	d, e = srv.store.EditVoiceNoteDraft(t.Context(), d.ID, ownerText, d.Revision, "")
	if e != nil {
		t.Fatal(e)
	}
	srv.worker.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) { return bundle, nil })
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	report["status"] = "calling"
	if e = srv.worker.ProcessOne(ctx); e != nil {
		report["status"] = "worker_failed"
		t.Fatal("worker execution failed")
	}
	d, e = srv.store.GetVoiceNoteDraft(t.Context(), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	report["status"], report["original"], report["transcribed"], report["error"] = d.State, original, d.ASRText, runPublicError(d.Error)
	for _, key := range []*string{st.GroqAPIKey, st.OpenAIAPIKey} {
		if key != nil && *key != "" {
			report["error"] = strings.ReplaceAll(report["error"].(string), *key, "[认证信息已隐藏]")
		}
	}
	report["duration_seconds"] = d.DurationSeconds
	report["owner_text_preserved"] = d.Text == ownerText
	usage, e := srv.store.ListRunUsage(t.Context(), d.JobID)
	if e != nil {
		t.Fatal(e)
	}
	report["usage"] = usage
	ex, e := srv.store.GetJobExecution(t.Context(), d.JobID)
	if e != nil {
		t.Fatal(e)
	}
	report["response_checkpoint"] = ex.CheckpointJSON != ""
	if d.State != "transcribed" || d.Text != ownerText || len(usage) != 1 || ex.CheckpointJSON == "" {
		t.Fatal("live transcription did not meet the state/receipt contract; see private report")
	}
	terms := 0
	for _, term := range []string{"来源", "理解", "笔记"} {
		if strings.Contains(d.ASRText, term) {
			terms++
		}
	}
	report["expected_terms_matched"] = terms
	if terms < 2 {
		t.Fatal("self-authored speech recognition needs review; see private report")
	}
	report["status"] = "technical_pass"
}
