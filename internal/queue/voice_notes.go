package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"github.com/woyin/orangecast/internal/voice"
)

// WithVoice wires the private directory and optional independent audio connection.
// Existing transcription settings remain the default; POD is never an audio route.
func (w *Worker) WithVoice(dir, providerName, model, baseURL, key string) *Worker {
	w.voiceDir = dir
	if baseURL != "" && key != "" {
		w.voiceSelector = provider.NewSelector(key, key)
		w.voiceSelector.ApplySettings("", baseURL, "", baseURL)
	}
	return w
}

func (w *Worker) doVoiceASR(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	ex, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return err
	}
	var in store.VoiceASRInput
	if json.Unmarshal([]byte(ex.InputSnapshotJSON), &in) != nil || in.DraftID != job.SourceID {
		return store.ErrInvalidEditorialState
	}

	// A response checkpoint no longer needs the recording file (it may already
	// have been deleted). Preserve receipt facts before checking applicability.
	if ex.CheckpointJSON != "" {
		cp, e := w.transcribeDurably(ctx, job, nil, "", in.AudioSHA256)
		if e != nil {
			return e
		}
		return w.commitVoiceResponse(ctx, job.ID, in, cp.Result.Text)
	}
	d, err := w.store.GetVoiceNoteDraft(ctx, in.DraftID)
	if errors.Is(err, store.ErrNotFound) {
		return w.store.SaveJobResult(ctx, job.ID, `{"discarded":true}`, models.JobResultComplete)
	}
	if err != nil {
		return err
	}
	if d.State == "saved" || d.JobID != job.ID {
		return w.store.SaveJobResult(ctx, job.ID, `{"superseded":true}`, models.JobResultComplete)
	}
	path, err := voice.Path(w.voiceDir, in.AudioFile)
	if err != nil {
		return err
	}
	hash, err := filehash.SHA256(path)
	if err != nil {
		return err
	}
	if hash != in.AudioSHA256 || d.AudioSHA256 != in.AudioSHA256 {
		return fmt.Errorf("录音指纹已变化，不能继续转写")
	}
	if _, err = w.store.DB.ExecContext(ctx, `UPDATE voice_note_drafts SET state='transcribing',updated_at=datetime('now') WHERE id=? AND job_id=? AND state IN('queued','transcribing')`, d.ID, job.ID); err != nil {
		return err
	}
	cp, err := w.transcribeDurably(ctx, job, bundle.Transcription, path, in.AudioSHA256)
	if err != nil {
		return err
	}
	return w.commitVoiceResponse(ctx, job.ID, in, cp.Result.Text)
}

func (w *Worker) commitVoiceResponse(ctx context.Context, jobID string, in store.VoiceASRInput, text string) error {
	if len(text) > 40000 {
		return fmt.Errorf("转写文字超过草稿容量；已保存响应和用量，请人工核对")
	}
	return w.store.CommitVoiceASR(ctx, jobID, in, text)
}

// CleanupVoiceFiles retries durable delete intents and expires unsaved drafts.
// Orphan upload/normalization files older than one hour are safe to reclaim;
// live database references protect recordings during ordinary crash recovery.
func (w *Worker) CleanupVoiceFiles(ctx context.Context) error {
	if w.voiceDir == "" {
		return nil
	}
	rows, err := w.store.DB.QueryContext(ctx, `SELECT id,revision FROM voice_note_drafts WHERE state NOT IN('saved','deleted') AND expires_at<=datetime('now') LIMIT 100`)
	if err != nil {
		return err
	}
	type expired struct {
		id       string
		revision int
	}
	var drafts []expired
	for rows.Next() {
		var d expired
		if err = rows.Scan(&d.id, &d.revision); err != nil {
			rows.Close()
			return err
		}
		drafts = append(drafts, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range drafts {
		if err = w.store.DeleteVoiceNoteDraft(ctx, d.id, d.revision); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
	rows, err = w.store.DB.QueryContext(ctx, `SELECT file FROM voice_audio_cleanup LIMIT 100`)
	if err != nil {
		return err
	}
	var files []string
	for rows.Next() {
		var file string
		if err = rows.Scan(&file); err != nil {
			rows.Close()
			return err
		}
		files = append(files, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, file := range files {
		path, e := voice.Path(w.voiceDir, file)
		if e != nil {
			return e
		}
		if e = os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if _, e = w.store.DB.ExecContext(ctx, `DELETE FROM voice_audio_cleanup WHERE file=?`, file); e != nil {
			return e
		}
	}
	entries, err := os.ReadDir(w.voiceDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if time.Since(info.ModTime()) < time.Hour {
			continue
		}
		name := entry.Name()
		if len(name) < 7 {
			continue
		}
		var count int
		if e = w.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM voice_note_drafts WHERE audio_file=? AND state!='deleted'`, name).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			continue
		}
		path := filepath.Join(w.voiceDir, name)
		if _, e = voice.Path(w.voiceDir, name); e != nil && name[:7] != ".voice-" {
			continue
		}
		if e = os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
