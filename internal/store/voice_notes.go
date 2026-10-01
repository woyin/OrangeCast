package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// VoiceNoteDraft is private working material. Only an explicitly saved reflection
// becomes an OwnerNote; ASR responses never overwrite its editable Text.
type VoiceNoteDraft struct {
	ID              string            `json:"id"`
	SourceType      models.SourceType `json:"source_type"`
	SourceID        string            `json:"source_id"`
	AnchorJSON      string            `json:"anchor_json"`
	UploadSHA256    string            `json:"upload_sha256"`
	AudioSHA256     string            `json:"audio_sha256"`
	AudioFile       string            `json:"-"`
	AudioAvailable  bool              `json:"audio_available"`
	DurationSeconds float64           `json:"duration_seconds"`
	SizeBytes       int64             `json:"size_bytes"`
	Text            string            `json:"text"`
	Revision        int               `json:"revision"`
	State           string            `json:"state"`
	ASRText         string            `json:"asr_text"`
	ASRBaseRevision int               `json:"asr_base_revision"`
	JobID           string            `json:"job_id"`
	NoteID          string            `json:"note_id"`
	KeepAudio       bool              `json:"keep_audio"`
	Error           string            `json:"error"`
	ExpiresAt       string            `json:"expires_at"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

const voiceColumns = `id,source_type,source_id,anchor_json,upload_sha256,audio_sha256,audio_file,duration_seconds,size_bytes,text,revision,state,asr_text,asr_base_revision,job_id,note_id,keep_audio,error,expires_at,created_at,updated_at`

func scanVoice(row interface{ Scan(...any) error }) (*VoiceNoteDraft, error) {
	d := &VoiceNoteDraft{}
	err := row.Scan(&d.ID, &d.SourceType, &d.SourceID, &d.AnchorJSON, &d.UploadSHA256, &d.AudioSHA256, &d.AudioFile, &d.DurationSeconds, &d.SizeBytes, &d.Text, &d.Revision, &d.State, &d.ASRText, &d.ASRBaseRevision, &d.JobID, &d.NoteID, &d.KeepAudio, &d.Error, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// GetVoiceNoteDraft reads a private draft without starting transcription.
func (s *Store) GetVoiceNoteDraft(ctx context.Context, id string) (*VoiceNoteDraft, error) {
	return scanVoice(s.DB.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=? AND state!='deleted'`, id))
}

// ListVoiceNoteDrafts bounds history and keeps deleted content out of the UI.
func (s *Store) ListVoiceNoteDrafts(ctx context.Context, offset int) ([]*VoiceNoteDraft, error) {
	if offset < 0 {
		return nil, ErrInvalidEditorialState
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE state!='deleted' ORDER BY created_at DESC,id DESC LIMIT 51 OFFSET ?`, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*VoiceNoteDraft{}
	for rows.Next() {
		d, e := scanVoice(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CreateVoiceNoteDraft binds a validated private recording to its original frozen
// listening identity. A duplicate upload must have exactly the same fingerprint.
func (s *Store) CreateVoiceNoteDraft(ctx context.Context, d VoiceNoteDraft) (*VoiceNoteDraft, bool, error) {
	if _, err := uuid.Parse(d.ID); err != nil {
		return nil, false, ErrInvalidEditorialState
	}
	if d.SourceType != models.SourceEpisode && d.SourceType != models.SourceUpload || d.DurationSeconds <= 0 || d.DurationSeconds > 300 || math.IsNaN(d.DurationSeconds) || d.SizeBytes <= 0 || d.SizeBytes > 20<<20 || len(d.Text) > 40000 || len(d.UploadSHA256) != 64 || len(d.AudioSHA256) != 64 || filepath.Base(d.AudioFile) != d.AudioFile || !strings.HasSuffix(d.AudioFile, ".wav") {
		return nil, false, ErrInvalidEditorialState
	}

	var anchor models.NoteAnchor
	if json.Unmarshal([]byte(d.AnchorJSON), &anchor) != nil || anchor.SnapshotID == "" || anchor.NoPosition {
		return nil, false, ErrInvalidEditorialState
	}
	refs, _ := json.Marshal(anchor.SegmentIDs)
	n, err := s.prepareOwnerNote(ctx, models.OwnerNote{SourceType: string(d.SourceType), SourceID: d.SourceID, Kind: "owner_reflection", Content: "录音草稿", AnchorJSON: d.AnchorJSON, ReferencesJSON: string(refs)})
	if err != nil {
		return nil, false, err
	}
	d.AnchorJSON = n.AnchorJSON
	if existing, err := s.GetVoiceNoteDraft(ctx, d.ID); err == nil {
		if existing.UploadSHA256 != d.UploadSHA256 || existing.SourceType != d.SourceType || existing.SourceID != d.SourceID || existing.AnchorJSON != d.AnchorJSON {
			return nil, false, ErrConflict
		}
		return existing, false, nil
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO voice_note_drafts(id,source_type,source_id,anchor_json,upload_sha256,audio_sha256,audio_file,duration_seconds,size_bytes,text)VALUES(?,?,?,?,?,?,?,?,?,?)`, d.ID, d.SourceType, d.SourceID, d.AnchorJSON, d.UploadSHA256, d.AudioSHA256, d.AudioFile, d.DurationSeconds, d.SizeBytes, d.Text)
	if isUniqueConstraintErr(err) {
		existing, e := s.GetVoiceNoteDraft(ctx, d.ID)
		if e != nil {
			return nil, false, e
		}
		if existing.UploadSHA256 != d.UploadSHA256 || existing.AnchorJSON != d.AnchorJSON {
			return nil, false, ErrConflict
		}
		return existing, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out, err := s.GetVoiceNoteDraft(ctx, d.ID)
	return out, true, err
}

// EditVoiceNoteDraft applies an Owner edit/adoption using an explicit revision.
func (s *Store) EditVoiceNoteDraft(ctx context.Context, id, text string, expected int, adoptJob string) (*VoiceNoteDraft, error) {
	if expected < 1 || len(text) > 40000 {
		return nil, ErrInvalidEditorialState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, err := scanVoice(tx.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if d.State == "deleted" || d.State == "saved" || d.Revision != expected {
		return nil, ErrConflict
	}
	if adoptJob != "" {
		if d.JobID != adoptJob || d.ASRText == "" {
			return nil, ErrConflict
		}
		text = d.ASRText
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voice_note_drafts SET text=?,revision=revision+1,updated_at=datetime('now') WHERE id=?`, text, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetVoiceNoteDraft(ctx, id)
}

// VoiceASRInput freezes audio, model and an audio-duration estimate at admission.
type VoiceASRInput struct {
	DraftID         string  `json:"draft_id"`
	AudioSHA256     string  `json:"audio_sha256"`
	AudioFile       string  `json:"audio_file"`
	DurationSeconds float64 `json:"duration_seconds"`
	BaseRevision    int     `json:"base_revision"`
	ConnectionID    string  `json:"connection_id"`
	Connection      string  `json:"connection"`
	PriceKnown      bool    `json:"price_known"`
	CentsPerMinute  float64 `json:"cents_per_minute"`
	EstimateVersion string  `json:"estimate_version"`
}

// SetASRAudioPrice records an exact route's audio-minute price, separate from
// text-token prices. It is an Owner-supplied estimate, not a supplier invoice.
func (s *Store) SetASRAudioPrice(ctx context.Context, p, m string, cents float64) error {
	if p != "openai" && p != "groq" || strings.TrimSpace(m) == "" || math.IsNaN(cents) || math.IsInf(cents, 0) || cents < 0 || cents > 1e6 {
		return ErrInvalidEditorialState
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO asr_audio_prices(provider,model,cents_per_minute)VALUES(?,?,?) ON CONFLICT(provider,model)DO UPDATE SET cents_per_minute=excluded.cents_per_minute`, p, m, cents)
	return err
}

// QueueVoiceASR starts only from an explicit Owner action. Retry creates a new
// attempt while preserving any prior unknown call and paid receipt.
func (s *Store) QueueVoiceASR(ctx context.Context, id string, expected int, tc provider.TaskConfig, connection string, retry bool, connectionIDs ...string) (*VoiceNoteDraft, error) {
	if tc.Provider != "groq" && tc.Provider != "openai" {
		return nil, ErrInvalidEditorialState
	}
	model := provider.EffectiveModel(tc.Provider, tc.Model, "transcribe")
	var price float64
	priceErr := s.DB.QueryRowContext(ctx, `SELECT cents_per_minute FROM asr_audio_prices WHERE provider=? AND model=?`, tc.Provider, model).Scan(&price)
	if priceErr != nil && !errors.Is(priceErr, sql.ErrNoRows) {
		return nil, priceErr
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, err := scanVoice(tx.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if d.State == "queued" || d.State == "transcribing" || d.State == "transcribed" {
		return d, nil
	}
	if d.State == "deleted" || d.State == "saved" || d.AudioFile == "" || d.Revision != expected || d.State == "failed" && !retry {
		return nil, ErrConflict
	}
	jobID := uuid.NewString()
	input := VoiceASRInput{DraftID: id, AudioSHA256: d.AudioSHA256, AudioFile: d.AudioFile, DurationSeconds: d.DurationSeconds, BaseRevision: d.Revision, Connection: connection, PriceKnown: priceErr == nil, CentsPerMinute: price, EstimateVersion: "voice-audio-minute-v1"}
	if len(connectionIDs) == 1 {
		input.ConnectionID = connectionIDs[0]
	}
	raw, _ := json.Marshal(input)
	checkpoint := ""
	if retry && d.JobID != "" {
		var old string
		if err = tx.QueryRowContext(ctx, `SELECT checkpoint_json FROM processing_jobs WHERE id=?`, d.JobID).Scan(&old); err != nil {
			return nil, err
		}
		// The queue validates the complete checkpoint before use. Preserve the paid
		// identity; only the new receiving job ID changes.
		if old != "" {
			var cp map[string]any
			if json.Unmarshal([]byte(old), &cp) != nil {
				return nil, ErrInvalidEditorialState
			}
			if cp["configured_provider"] != tc.Provider || cp["configured_model"] != model || cp["audio_sha256"] != d.AudioSHA256 {
				return nil, ErrConflict
			}
			if cp["origin_job_id"] == nil {
				cp["origin_job_id"] = cp["job_id"]
			}
			cp["job_id"] = jobID
			b, _ := json.Marshal(cp)
			checkpoint = string(b)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,intent_id,input_snapshot_json,configured_provider,configured_model,config_version,checkpoint_json)VALUES(?,'voice_note',?,'transcribe','queued',?,?,?,?,?,?)`, jobID, id, "voice-asr:"+jobID, string(raw), tc.Provider, model, "voice-asr-v1", checkpoint); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voice_note_drafts SET state='queued',job_id=?,asr_base_revision=?,error='',updated_at=datetime('now') WHERE id=?`, jobID, d.Revision, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetVoiceNoteDraft(ctx, id)
}

// HoldVoiceASRBudget uses verified duration and frozen audio prices, sharing the
// existing global atomic reservation instead of interpreting seconds as tokens.
func (s *Store) HoldVoiceASRBudget(ctx context.Context, jobID string, input VoiceASRInput) (*models.BudgetReservation, error) {
	if input.DurationSeconds <= 0 || input.DurationSeconds > 300 || input.EstimateVersion != "voice-audio-minute-v1" {
		return nil, ErrBudgetIncomplete
	}
	budget, err := s.GetOwnerMonthlyBudget(ctx)
	if err != nil || budget == nil {
		return nil, err
	}
	if !input.PriceKnown {
		return nil, fmt.Errorf("%w: 语音转录未登记每分钟预估价格", ErrBudgetUnpriced)
	}
	amount := int64(math.Ceil(input.DurationSeconds * input.CentsPerMinute / 60))
	return s.holdBudgetAmount(ctx, jobID, "voice_transcription", *budget, amount)
}

// CommitVoiceASR stores a separate proposal and never rewrites an Owner edit.
func (s *Store) CommitVoiceASR(ctx context.Context, jobID string, in VoiceASRInput, text string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := scanVoice(tx.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=?`, in.DraftID))
	if err != nil {
		return err
	}
	if d.State != "deleted" && d.State != "saved" && d.JobID == jobID && d.AudioSHA256 == in.AudioSHA256 {
		if _, err = tx.ExecContext(ctx, `UPDATE voice_note_drafts SET state='transcribed',asr_text=?,asr_base_revision=?,error='',updated_at=datetime('now') WHERE id=?`, text, in.BaseRevision, in.DraftID); err != nil {
			return err
		}
	}
	// Discarded content is not resurrected. Call identity and usage stay auditable.
	resultJSON := `{"voice_response_saved":true}`
	if d.State == "deleted" || d.State == "saved" || d.JobID != jobID {
		resultJSON = `{"discarded":true}`
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET result_state='complete',result_json=?,checkpoint_json=CASE WHEN ? IN('deleted','saved') THEN '' ELSE checkpoint_json END WHERE id=?`, resultJSON, d.State, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// FailVoiceASR marks a live draft without reviving a deleted/saved draft.
func (s *Store) FailVoiceASR(ctx context.Context, id, jobID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE voice_note_drafts SET state='failed',error=?,updated_at=datetime('now') WHERE id=? AND job_id=? AND state IN('queued','transcribing')`, reason, id, jobID)
	return err
}

// DeleteVoiceNoteDraft tombstones a draft and schedules retryable file cleanup.
func (s *Store) DeleteVoiceNoteDraft(ctx context.Context, id string, expected int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := scanVoice(tx.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=?`, id))
	if err != nil {
		return err
	}
	if d.State == "deleted" {
		return nil
	}
	if d.Revision != expected {
		return ErrConflict
	}
	if d.AudioFile != "" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO voice_audio_cleanup(file)VALUES(?)`, d.AudioFile); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE processing_jobs SET status='failed',last_error='Owner删除了语音草稿' WHERE source_type='voice_note' AND source_id=? AND status='queued'`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voice_note_drafts SET state='deleted',revision=revision+1,text='',asr_text='',anchor_json='{}',audio_file='',error='',updated_at=datetime('now') WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveVoiceNoteDraft creates exactly one personal reflection and question link
// together with the draft state. References keep the original historical anchor.
func (s *Store) SaveVoiceNoteDraft(ctx context.Context, id string, expected int, questionID string, questionRevision int, keepAudio bool) (*models.OwnerNote, error) {
	d, err := s.GetVoiceNoteDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.State == "saved" {
		return s.GetOwnerNote(ctx, d.NoteID)
	}
	var a models.NoteAnchor
	if json.Unmarshal([]byte(d.AnchorJSON), &a) != nil {
		return nil, ErrInvalidEditorialState
	}
	refs, _ := json.Marshal(a.SegmentIDs)
	n, err := s.prepareOwnerNote(ctx, models.OwnerNote{SourceType: string(d.SourceType), SourceID: d.SourceID, Kind: "owner_reflection", Content: d.Text, AnchorJSON: d.AnchorJSON, ReferencesJSON: string(refs)})
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := scanVoice(tx.QueryRowContext(ctx, `SELECT `+voiceColumns+` FROM voice_note_drafts WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if current.State == "saved" {
		tx.Rollback()
		return s.GetOwnerNote(ctx, current.NoteID)
	}
	if current.State == "deleted" || current.Revision != expected || current.Text != d.Text || current.State == "queued" || current.State == "transcribing" {
		return nil, ErrConflict
	}
	if err = checkQuestionSource(ctx, tx, n.SourceType, n.SourceID); err != nil {
		return nil, err
	}
	if questionID != "" {
		var rev, count int
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM learning_questions WHERE id=?`, questionID).Scan(&rev); err != nil {
			return nil, err
		}
		if rev != questionRevision {
			return nil, ErrConflict
		}
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_question_links WHERE question_id=?`, questionID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 200 {
			return nil, ErrInvalidEditorialState
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO owner_notes(id,source_type,source_id,kind,content,citations_json,references_json,anchor_json)VALUES(?,?,?,?,?,?,?,?)`, n.ID, n.SourceType, n.SourceID, n.Kind, n.Content, n.CitationsJSON, n.ReferencesJSON, n.AnchorJSON); err != nil {
		return nil, err
	}
	if questionID != "" {
		if err = insertQuestionRelation(ctx, tx, questionID, provider.LearningQuestionLink{Kind: "note", ObjectID: n.ID, SourceType: n.SourceType, SourceID: n.SourceID, Version: 1}, "confirmed", "owner"); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE learning_questions SET revision=revision+1,updated_at=datetime('now') WHERE id=?`, questionID); err != nil {
			return nil, err
		}
		if err = questionOperation(ctx, tx, questionID, questionRevision+1, "voice_note", n.ID); err != nil {
			return nil, err
		}
	}
	file := current.AudioFile
	if !keepAudio && file != "" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO voice_audio_cleanup(file)VALUES(?)`, file); err != nil {
			return nil, err
		}
		file = ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voice_note_drafts SET state='saved',revision=revision+1,note_id=?,keep_audio=?,audio_file=?,updated_at=datetime('now') WHERE id=?`, n.ID, keepAudio, file, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOwnerNote(ctx, n.ID)
}

// FreezeSourceASREstimate adds verified audio facts without dropping an ingestion
// depth snapshot. The price is frozen before the first supplier call.
func (s *Store) FreezeSourceASREstimate(ctx context.Context, jobID, providerName, model, audioHash string, duration float64) (VoiceASRInput, error) {
	var in VoiceASRInput
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return in, ErrBudgetIncomplete
	}
	ex, err := s.GetJobExecution(ctx, jobID)
	if err != nil {
		return in, err
	}
	snapshot := map[string]json.RawMessage{}
	if ex.InputSnapshotJSON != "" {
		if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &snapshot); err != nil {
			return in, err
		}
	}
	if raw, ok := snapshot["audio_estimate"]; ok {
		if err = json.Unmarshal(raw, &in); err != nil {
			return in, err
		}
		if in.AudioSHA256 != audioHash {
			return in, ErrConflict
		}
		return in, nil
	}
	in = VoiceASRInput{AudioSHA256: audioHash, DurationSeconds: duration, EstimateVersion: "voice-audio-minute-v1"}
	err = s.DB.QueryRowContext(ctx, `SELECT cents_per_minute FROM asr_audio_prices WHERE provider=? AND model=?`, providerName, model).Scan(&in.CentsPerMinute)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return in, err
	}
	in.PriceKnown = err == nil
	raw, _ := json.Marshal(in)
	snapshot["audio_estimate"] = raw
	frozen, _ := json.Marshal(snapshot)
	result, err := s.DB.ExecContext(ctx, `UPDATE processing_jobs SET input_snapshot_json=? WHERE id=? AND input_snapshot_json=? AND remote_call_started=0 AND status IN('queued','running')`, string(frozen), jobID, ex.InputSnapshotJSON)
	if err != nil {
		return in, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return in, ErrConflict
	}
	return in, nil
}

// HoldSourceASRBudget uses the same monetary reservation with long-form audio.
func (s *Store) HoldSourceASRBudget(ctx context.Context, jobID string, in VoiceASRInput) (*models.BudgetReservation, error) {
	if in.DurationSeconds <= 0 || math.IsNaN(in.DurationSeconds) || math.IsInf(in.DurationSeconds, 0) || in.EstimateVersion != "voice-audio-minute-v1" {
		return nil, ErrBudgetIncomplete
	}
	budget, err := s.GetOwnerMonthlyBudget(ctx)
	if err != nil || budget == nil {
		return nil, err
	}
	if !in.PriceKnown {
		return nil, ErrBudgetUnpriced
	}
	amount := math.Ceil(in.DurationSeconds * in.CentsPerMinute / 60)
	if math.IsInf(amount, 0) || amount > math.MaxInt64 {
		return nil, ErrBudgetIncomplete
	}
	return s.holdBudgetAmount(ctx, jobID, "transcription", *budget, int64(amount))
}
