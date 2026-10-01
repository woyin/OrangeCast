package queue

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func voiceQueueFixture(t *testing.T) (*store.Store, *Worker, *store.VoiceNoteDraft) {
	t.Helper()
	s, w := newTestWorker(t)
	ctx := t.Context()
	source := seedEpisode(t, s)
	job, _ := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	v, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, source, store.KindTranscript, "fixture", "timestamp-model", "1", job.ID, `{"segments":[{"id":"seg-1","start":10,"end":20,"text":"原音"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCurrentVersion(ctx, models.SourceEpisode, source, store.KindTranscript, v)
	s.MarkJobRunning(ctx, job.ID)
	s.MarkJobSucceeded(ctx, job.ID)
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, source)
	if err != nil {
		t.Fatal(err)
	}
	anchor, _ := json.Marshal(models.NoteAnchor{SnapshotID: snap.ID, Version: snap.ContentVersion, Position: 12, SegmentIDs: []string{"seg-1"}, Mode: "original"})
	dir := filepath.Join(t.TempDir(), "voice")
	os.Mkdir(dir, 0700)
	w.WithVoice(dir, "", "", "", "")
	file := uuid.NewString() + ".wav"
	path := filepath.Join(dir, file)
	os.WriteFile(path, []byte("normalized fixture audio"), 0600)
	hash, _ := filehash.SHA256(path)
	d, _, err := s.CreateVoiceNoteDraft(ctx, store.VoiceNoteDraft{ID: uuid.NewString(), SourceType: models.SourceEpisode, SourceID: source, AnchorJSON: string(anchor), UploadSHA256: hash, AudioSHA256: hash, AudioFile: file, DurationSeconds: 1, SizeBytes: 24, Text: "原草稿"})
	if err != nil {
		t.Fatal(err)
	}
	return s, w, d
}

func TestVoiceQueueKnownResponseRecoveryAfterDraftDeletion(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := s.ClaimNextJob(ctx, leaseDuration)
	if job.ID != d.JobID {
		t.Fatal("wrong job")
	}
	f := &countedTranscriber{}
	path := filepath.Join(w.voiceDir, d.AudioFile)
	cp, err := w.transcribeDurably(ctx, job, f, path, d.AudioSHA256)
	if err != nil || cp == nil {
		t.Fatal(err)
	}
	if err = s.DeleteVoiceNoteDraft(ctx, d.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = w.CleanupVoiceFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("audio not deleted", err)
	}
	// Recovery has no file or credentials. It still retains exactly one receipt.
	if err = w.doVoiceASR(ctx, job, &provider.ProviderBundle{}); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, job.ID)
	if ex.ResultState != models.JobResultComplete || ex.CheckpointJSON != "" || f.calls != 1 {
		t.Fatal(ex, f.calls)
	}
	if _, err = s.GetVoiceNoteDraft(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("draft resurrected", err)
	}
	var receipts int
	s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE receipt_id=?`, job.ID+":transcription").Scan(&receipts)
	if receipts != 1 {
		t.Fatal(receipts)
	}
}

func TestVoiceWorkerASRDoesNotProduceSourceArtifactsOrOverwriteText(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	f := &countedTranscriber{}
	w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{Transcription: f}, nil
	})
	d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	s.EditVoiceNoteDraft(ctx, d.ID, "Owner在转写期间修改", 1, "")
	if err = w.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetVoiceNoteDraft(ctx, d.ID)
	if got.State != "transcribed" || got.Text != "Owner在转写期间修改" || got.ASRText != "Owner理解" || got.ASRBaseRevision != 1 {
		t.Fatal(got)
	}
	var versions, notes, analyses int
	s.DB.QueryRow(`SELECT count(*) FROM artifact_versions WHERE source_type='voice_note'`).Scan(&versions)
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&notes)
	s.DB.QueryRow(`SELECT count(*) FROM processing_jobs WHERE source_type='voice_note' AND job_type='analyze'`).Scan(&analyses)
	if versions != 0 || notes != 0 || analyses != 0 || f.calls != 1 {
		t.Fatal(versions, notes, analyses, f.calls)
	}
}

func TestVoiceExpiryCleanupAndOrphanSafety(t *testing.T) {
	s, w, d := voiceQueueFixture(t)
	ctx := t.Context()
	file := filepath.Join(w.voiceDir, d.AudioFile)
	orphan := filepath.Join(w.voiceDir, uuid.NewString()+".wav")
	os.WriteFile(orphan, []byte("orphan"), 0600)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(orphan, old, old)
	recent := filepath.Join(w.voiceDir, uuid.NewString()+".wav")
	os.WriteFile(recent, []byte("recent upload"), 0600)
	if err := w.CleanupVoiceFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("live draft deleted", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("orphan not reclaimed", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("active upload deleted", err)
	}
	s.DB.Exec(`UPDATE voice_note_drafts SET expires_at=datetime('now','-1 day') WHERE id=?`, d.ID)
	if err := w.CleanupVoiceFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired recording survived", err)
	}
}

func TestVoiceRetryPreservesPaidIdentityAndUnknownAttempts(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "known"}[known], func(t *testing.T) {
			s, w, d := voiceQueueFixture(t)
			ctx := t.Context()
			f := &countedTranscriber{}
			d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
			if err != nil {
				t.Fatal(err)
			}
			oldID := d.JobID
			job, _ := s.ClaimNextJob(ctx, leaseDuration)
			if known {
				if _, err = w.transcribeDurably(ctx, job, f, filepath.Join(w.voiceDir, d.AudioFile)); err != nil {
					t.Fatal(err)
				}
			} else {
				s.MarkJobRemoteCallStarted(ctx, job.ID)
				s.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown)
			}
			s.MarkJobFailed(ctx, job.ID, "simulated persistence interruption")
			s.FailVoiceASR(ctx, d.ID, job.ID, "需要恢复")
			d, err = s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", true)
			if err != nil {
				t.Fatal(err)
			}
			w.WithBundleResolver(func(*models.ProcessingJob) (*provider.ProviderBundle, error) {
				return &provider.ProviderBundle{Transcription: f}, nil
			})
			if err = w.ProcessOne(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetVoiceNoteDraft(ctx, d.ID)
			if got.State != "transcribed" || f.calls != 1 {
				t.Fatal(got, f.calls)
			}
			old, _ := s.GetJobExecution(ctx, oldID)
			if !known && old.ResultState != models.JobResultUnknown {
				t.Fatal("unknown fact lost", old)
			}
			var receipts int
			s.DB.QueryRow(`SELECT count(*) FROM usage_records WHERE operation='transcription'`).Scan(&receipts)
			if receipts != 1 {
				t.Fatal("paid response billed twice", receipts)
			}
		})
	}
}
