package queue

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type contextualVoiceFixture struct {
	name     string
	calls    int
	response *provider.TranscriptResult
	err      error
	after    func()
}

func (f *contextualVoiceFixture) Name() string { return f.name }
func (f *contextualVoiceFixture) Transcribe(string) (*provider.TranscriptResult, error) {
	panic("context-aware method required")
}
func (f *contextualVoiceFixture) TranscribeContext(ctx context.Context, _ string) (*provider.TranscriptResult, error) {
	f.calls++
	if f.after != nil {
		f.after()
	}
	return f.response, f.err
}

func TestDurableASRFailuresDoNotReplayPaidCalls(t *testing.T) {
	for _, kind := range []string{"missing-job", "missing-file", "wrong-hash", "no-provider", "start-write", "empty-response", "invalid-response", "checkpoint-write", "receipt-write", "audio-unit-write", "duration-write", "canceled-after-response", "long-text"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
			if err != nil {
				t.Fatal(err)
			}
			job, _ := s.ClaimNextJob(ctx, leaseDuration)
			file := filepath.Join(w.voiceDir, d.AudioFile)
			hash := d.AudioSHA256
			f := &contextualVoiceFixture{name: "groq", response: &provider.TranscriptResult{Text: "个人理解", Model: "actual-asr"}}
			var p provider.TranscriptionProvider = f
			switch kind {
			case "missing-job":
				job.ID = "missing"
			case "missing-file":
				file += "missing"
			case "wrong-hash":
				hash = "changed"
			case "no-provider":
				p = nil
			case "start-write":
				s.DB.Exec(`CREATE TRIGGER fail_start BEFORE UPDATE OF remote_call_started ON processing_jobs BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "empty-response":
				f.response = nil
			case "invalid-response":
				f.response.Segments = []provider.Segment{{Start: math.NaN()}}
			case "checkpoint-write":
				s.DB.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF checkpoint_json ON processing_jobs BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "receipt-write":
				s.DB.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON usage_records BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "audio-unit-write":
				s.DB.Exec(`CREATE TRIGGER fail_unit BEFORE UPDATE OF unit_kind ON usage_records BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "duration-write":
				s.DB.Exec(`CREATE TRIGGER fail_duration BEFORE UPDATE OF audio_seconds ON usage_records BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "canceled-after-response":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				f.after = cancel
				defer cancel()
			case "long-text":
				f.response.Text = strings.Repeat("a", 40001)
			}
			cp, err := w.transcribeDurably(ctx, job, p, file, hash)
			if kind == "long-text" {
				if err != nil {
					t.Fatal(err)
				}
				ex, _ := s.GetJobExecution(t.Context(), job.ID)
				var in store.VoiceASRInput
				json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
				err = w.commitVoiceResponse(ctx, job.ID, in, cp.Result.Text)
			}
			if err == nil {
				t.Fatal("failure accepted", kind)
			}
			ex, e := s.GetJobExecution(t.Context(), job.ID)
			if kind == "canceled-after-response" {
				if e != nil || ex.CheckpointJSON == "" {
					t.Fatal("canceled response lost", ex, e)
				}
			}
			if kind == "receipt-write" {
				s.DB.Exec(`DROP TRIGGER fail_receipt`)
				if _, e = w.transcribeDurably(t.Context(), job, p, file, hash); e != nil {
					t.Fatal(e)
				}
				if f.calls != 1 {
					t.Fatal("receipt failure replayed paid call", f.calls)
				}
			}
			if kind == "start-write" && f.calls != 0 {
				t.Fatal("called before durable start")
			}
		})
	}
}

func TestVoiceWorkerEligibilityAndEndpointIdentity(t *testing.T) {
	for _, kind := range []string{"invalid-input", "deleted", "saved", "wrong-job", "bad-path", "missing-file", "changed-file", "update-failure", "provider-error", "unknown", "custom-missing", "custom-changed", "custom-valid", "default-changed", "default-valid"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			connection := "default"
			if strings.HasPrefix(kind, "custom") {
				connection = "voice"
			}
			d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, connection, false)
			if err != nil {
				t.Fatal(err)
			}
			job, _ := s.ClaimNextJob(ctx, leaseDuration)
			ex, _ := s.GetJobExecution(ctx, job.ID)
			var in store.VoiceASRInput
			json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
			f := &contextualVoiceFixture{name: "groq", response: &provider.TranscriptResult{Text: "个人理解", Model: "actual-asr"}}
			switch kind {
			case "invalid-input":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='invalid' WHERE id=?`, job.ID)
			case "deleted":
				s.DeleteVoiceNoteDraft(ctx, d.ID, 1)
			case "saved":
				s.DB.Exec(`UPDATE voice_note_drafts SET state='saved' WHERE id=?`, d.ID)
			case "wrong-job":
				s.DB.Exec(`UPDATE voice_note_drafts SET job_id='newer' WHERE id=?`, d.ID)
			case "bad-path":
				in.AudioFile = "../escape"
			case "missing-file":
				os.Remove(filepath.Join(w.voiceDir, d.AudioFile))
			case "changed-file":
				os.WriteFile(filepath.Join(w.voiceDir, d.AudioFile), []byte("changed"), 0600)
			case "update-failure":
				s.DB.Exec(`CREATE TRIGGER fail_state BEFORE UPDATE OF state ON voice_note_drafts BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "provider-error":
				f.err = errors.New("network interrupted")
			case "unknown":
				s.MarkJobRemoteCallStarted(ctx, job.ID)
			case "custom-changed", "custom-valid":
				w.WithVoice(w.voiceDir, "groq", "model", "https://audio.example/v1", "fake-key")
				in.ConnectionID = w.voiceSelector.TranscriptionConnectionID("groq")
				if kind == "custom-changed" {
					in.ConnectionID = "old endpoint"
				}
			case "default-changed":
				in.ConnectionID = "old endpoint"
			case "default-valid":
				in.ConnectionID = w.selector.TranscriptionConnectionID("groq")
			}
			if kind == "bad-path" || strings.Contains(kind, "changed") || strings.HasSuffix(kind, "valid") {
				raw, _ := json.Marshal(in)
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json=? WHERE id=?`, string(raw), job.ID)
			}
			if strings.HasPrefix(kind, "custom") || strings.HasPrefix(kind, "default") {
				bundle, e := w.bundleFor(job)
				if strings.HasSuffix(kind, "valid") {
					if e != nil || bundle.Transcription == nil {
						t.Fatal(bundle, e)
					}
				} else if e == nil {
					t.Fatal("endpoint silently changed")
				}
				return
			}
			if kind == "unknown" {
				err = w.processJob(ctx, job)
			} else {
				err = w.doVoiceASR(ctx, job, &provider.ProviderBundle{Transcription: f})
			}
			discarded := kind == "deleted" || kind == "saved" || kind == "wrong-job"
			if discarded {
				if err != nil || f.calls != 0 {
					t.Fatal("discarded task called provider", err, f.calls)
				}
			} else if err == nil {
				t.Fatal("invalid task accepted", kind)
			}
			if kind == "unknown" {
				ex, _ = s.GetJobExecution(ctx, job.ID)
				if ex.ResultState != models.JobResultUnknown || f.calls != 0 {
					t.Fatal("unknown replayed", ex, f.calls)
				}
			}
		})
	}
}

func TestVoiceCleanupProtectsReferencesAndReportsFileFailures(t *testing.T) {
	for _, kind := range []string{"no-dir", "missing-dir", "not-directory", "unsafe-intent", "remove-error", "outbox-write", "temporary-orphans"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			switch kind {
			case "no-dir":
				w.voiceDir = ""
			case "missing-dir":
				os.RemoveAll(w.voiceDir)
			case "not-directory":
				w.voiceDir = filepath.Join(t.TempDir(), "file")
				os.WriteFile(w.voiceDir, []byte("file"), 0600)
			case "unsafe-intent":
				s.DB.Exec(`INSERT INTO voice_audio_cleanup(file) VALUES('../escape')`)
			case "remove-error":
				path := filepath.Join(w.voiceDir, uuid.NewString()+".wav")
				os.Mkdir(path, 0700)
				os.WriteFile(filepath.Join(path, "child"), []byte("x"), 0600)
				s.DB.Exec(`INSERT INTO voice_audio_cleanup(file) VALUES(?)`, filepath.Base(path))
			case "outbox-write":
				s.DB.Exec(`INSERT INTO voice_audio_cleanup(file) VALUES(?)`, uuid.NewString()+".wav")
				s.DB.Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON voice_audio_cleanup BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			case "temporary-orphans":
				old := time.Now().Add(-2 * time.Hour)
				for _, name := range []string{"tiny", "unrelated-file", ".voice-incomplete", d.AudioFile} {
					path := filepath.Join(w.voiceDir, name)
					if name != d.AudioFile {
						os.WriteFile(path, []byte("x"), 0600)
					}
					os.Chtimes(path, old, old)
				}
				os.Mkdir(filepath.Join(w.voiceDir, "folder"), 0700)
			}
			err := w.CleanupVoiceFiles(ctx)
			wantError := kind == "not-directory" || kind == "unsafe-intent" || kind == "remove-error" || kind == "outbox-write"
			if wantError != (err != nil) {
				t.Fatal(kind, err)
			}
			if kind == "temporary-orphans" {
				if _, err = os.Stat(filepath.Join(w.voiceDir, d.AudioFile)); err != nil {
					t.Fatal("live recording removed")
				}
				if _, err = os.Stat(filepath.Join(w.voiceDir, ".voice-incomplete")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("old normalization file retained", err)
				}
			}
		})
	}
}

func TestSourceASRBudgetRequiresAudioPriceBeforeDownload(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := t.Context()
	source := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	budget := int64(100)
	s.SetOwnerMonthlyBudget(ctx, &budget)
	fetched := false
	w.WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		fetched = true
		return "", func() {}, errors.New("download should not start")
	})
	if err := w.holdJobBudget(ctx, job); !errors.Is(err, store.ErrBudgetUnpriced) || fetched {
		t.Fatal("unpriced audio downloaded", err, fetched)
	}
	s.SetASRAudioPrice(ctx, "groq", provider.EffectiveModel("groq", "", "transcribe"), 2)
	seedEvidence(t, s, w, models.SourceEpisode, source)
	if err := w.holdJobBudget(ctx, job); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if !strings.Contains(ex.InputSnapshotJSON, "audio_estimate") {
		t.Fatal("duration not frozen")
	}
}

func TestVoiceASRWorkerFailuresKeepDraftAndRemoteFacts(t *testing.T) {
	for _, kind := range []string{"unknown", "network", "bad-input", "missing-execution", "closed-store"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
			if err != nil {
				t.Fatal(err)
			}
			f := &contextualVoiceFixture{name: "groq", err: errors.New("remote response lost")}
			w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{Transcription: f}, nil
			})
			switch kind {
			case "unknown":
				s.SaveJobResult(ctx, d.JobID, "", models.JobResultUnknown)
			case "bad-input":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='bad' WHERE id=?`, d.JobID)
			case "missing-execution":
				if err = w.doVoiceASR(ctx, &models.ProcessingJob{ID: "missing"}, &provider.ProviderBundle{}); err == nil {
					t.Fatal("missing execution accepted")
				}
				return
			case "closed-store":
				s.Close()
				if err = w.CleanupVoiceFiles(ctx); err == nil {
					t.Fatal("closed cleanup accepted")
				}
				return
			}
			if err = w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetVoiceNoteDraft(ctx, d.ID)
			if got.State != "failed" || got.Text != "原草稿" {
				t.Fatal("failed task lost owner text", got)
			}
			if kind == "unknown" && f.calls != 0 {
				t.Fatal("unknown replayed")
			}
		})
	}
}

func TestVoiceFrozenRouteAndBudgetRejectUnreadableContracts(t *testing.T) {
	for _, kind := range []string{"route-input", "budget-input", "route-job", "budget-job", "budget-closed"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			d, _ = s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
			job, _ := s.GetJob(ctx, d.JobID)
			switch kind {
			case "route-input", "budget-input":
				s.DB.Exec(`UPDATE processing_jobs SET input_snapshot_json='not-json' WHERE id=?`, job.ID)
			case "route-job", "budget-job":
				job.ID = "missing"
			case "budget-closed":
				s.Close()
			}
			// Only the contract retrieval is broken, so route/config parsing cannot hide it.
			w.taskConfigFor = func(*models.ProcessingJob) (provider.TaskConfig, error) {
				return provider.TaskConfig{Provider: "groq"}, nil
			}
			var err error
			if strings.HasPrefix(kind, "route") {
				_, err = w.bundleFor(job)
			} else {
				err = w.holdJobBudget(ctx, job)
			}
			if err == nil {
				t.Fatal("unreadable frozen contract accepted", kind)
			}
		})
	}
}

func TestVoiceCleanupDatabaseFailuresRetainDeleteIntents(t *testing.T) {
	for _, kind := range []string{"expired-delete", "outbox-query"} {
		t.Run(kind, func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			if kind == "expired-delete" {
				s.DB.Exec(`UPDATE voice_note_drafts SET expires_at=datetime('now','-1 day') WHERE id=?`, d.ID)
				s.DB.Exec(`CREATE TRIGGER fail_expiration BEFORE UPDATE OF state ON voice_note_drafts BEGIN SELECT RAISE(ABORT,'disk full'); END`)
			} else {
				s.DB.Exec(`DROP TABLE voice_audio_cleanup`)
			}
			if err := w.CleanupVoiceFiles(t.Context()); err == nil {
				t.Fatal("cleanup failure hidden")
			}
			if _, err := os.Stat(filepath.Join(w.voiceDir, d.AudioFile)); err != nil {
				t.Fatal("recording removed without durable intent", err)
			}
		})
	}
}

func TestSourceKnownASRResponseRecoversWithoutProviderOrNewHold(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := t.Context()
	source := seedEpisode(t, s)
	seedEvidence(t, s, w, models.SourceEpisode, source)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	f := &countedTranscriber{}
	path, _ := w.ensureEvidence(ctx, job)
	if _, err := w.transcribeDurably(ctx, job, f, path); err != nil {
		t.Fatal(err)
	}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		t.Fatal("known paid response attempted route resolution")
		return nil, nil
	})
	budget := int64(0)
	s.SetOwnerMonthlyBudget(ctx, &budget)
	if err := w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if ex.ResultState != models.JobResultComplete || f.calls != 1 {
		t.Fatal(ex, f.calls)
	}
}

func TestSourceASRBudgetFailuresRemainBeforePaidCall(t *testing.T) {
	for _, kind := range []string{"daily-limit", "settings", "price-query", "download", "duration", "freeze"} {
		t.Run(kind, func(t *testing.T) {
			s, w := newTestWorker(t)
			ctx := t.Context()
			source := seedEpisode(t, s)
			job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
			budget := int64(100)
			s.SetOwnerMonthlyBudget(ctx, &budget)
			s.SetASRAudioPrice(ctx, "groq", provider.EffectiveModel("groq", "", "transcribe"), 2)
			w.taskConfigFor = func(*models.ProcessingJob) (provider.TaskConfig, error) {
				return provider.TaskConfig{Provider: "groq"}, nil
			}
			switch kind {
			case "daily-limit":
				job.Automated = true
				limit := int64(0)
				s.SetAutoDailyJobLimit(ctx, &limit)
			case "settings":
				s.DB.Exec(`DROP TABLE settings`)
			case "price-query":
				s.DB.Exec(`DROP TABLE asr_audio_prices`)
			case "download":
				w.WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
					return "", nil, errors.New("download interrupted")
				})
			case "duration":
				seedEvidence(t, s, w, models.SourceEpisode, source)
				a, _ := s.GetEvidenceAudio(ctx, models.SourceEpisode, source)
				os.WriteFile(filepath.Join(w.evidenceDir, a.RelPath), []byte("not audio"), 0600)
				hash, _ := filehash.SHA256(filepath.Join(w.evidenceDir, a.RelPath))
				s.UpsertEvidenceAudio(ctx, models.SourceEpisode, source, a.RelPath, "mp3", 9, hash)
			case "freeze":
				seedEvidence(t, s, w, models.SourceEpisode, source)
				s.MarkJobRemoteCallStarted(ctx, job.ID)
			}
			if err := w.holdJobBudget(ctx, job); err == nil {
				t.Fatal("broken budget admitted", kind)
			}
			ex, _ := s.GetJobExecution(ctx, job.ID)
			if kind != "freeze" && ex.RemoteCallStarted {
				t.Fatal("budget failure started paid call")
			}
		})
	}
}

func TestVoiceRecoveryRejectsCorruptCheckpointAndRetainsIt(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	d, _ = s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
	job, _ := s.ClaimNextJob(ctx, leaseDuration)
	s.SaveJobCheckpoint(ctx, job.ID, `{"result":{"text":"do not apply"}}`)
	if err := w.doVoiceASR(ctx, job, &provider.ProviderBundle{}); err == nil {
		t.Fatal("corrupt checkpoint applied")
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if ex.CheckpointJSON == "" {
		t.Fatal("unusable paid evidence erased")
	}
	s.DB.Exec(`DROP TABLE voice_note_drafts`)
	s.DB.Exec(`UPDATE processing_jobs SET checkpoint_json='' WHERE id=?`, job.ID)
	if err := w.doVoiceASR(ctx, job, &provider.ProviderBundle{}); err == nil {
		t.Fatal("unreadable draft accepted")
	}
}

func TestTranscriptionCannotHideUnknownResultPersistenceFailure(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	d, _ = s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
	job, _ := s.ClaimNextJob(ctx, leaseDuration)
	s.MarkJobRemoteCallStarted(ctx, job.ID)
	s.DB.Exec(`CREATE TRIGGER fail_unknown BEFORE UPDATE OF result_state ON processing_jobs BEGIN SELECT RAISE(ABORT,'disk full'); END`)
	if err := w.processJob(ctx, job); err == nil {
		t.Fatal("unknown persistence error hidden")
	}
	if _, err := w.transcribeDurably(ctx, job, nil, filepath.Join(w.voiceDir, d.AudioFile)); err == nil {
		t.Fatal("unknown persistence error hidden")
	}
}

func TestSourceTranscriptionDownloadFailureCannotStartRemoteCall(t *testing.T) {
	s, w := newTestWorker(t)
	ctx := t.Context()
	source := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	w.WithRawAudioResolver(func(context.Context, *models.ProcessingJob) (string, func(), error) {
		return "", nil, errors.New("local download interrupted")
	})
	if err := w.doTranscribe(ctx, job, &provider.ProviderBundle{}); err == nil {
		t.Fatal("download error hidden")
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if ex.RemoteCallStarted {
		t.Fatal("paid call started without input")
	}
}
