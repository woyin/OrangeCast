package store

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func voiceStoreFixture(t *testing.T) (*Store, *VoiceNoteDraft) {
	t.Helper()
	s := newTestStore(t)
	ctx := t.Context()
	source := seedEpisodeForArtifact(t, s)
	job, err := s.EnqueueJob(ctx, models.SourceEpisode, source, models.JobTranscribe)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateArtifactVersion(ctx, models.SourceEpisode, source, KindTranscript, "fixture", "timestamp-model", "1", job.ID, `{"segments":[{"id":"seg-1","start":10,"end":20,"text":"原音资料"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCurrentVersion(ctx, models.SourceEpisode, source, KindTranscript, v); err != nil {
		t.Fatal(err)
	}
	s.MarkJobRunning(ctx, job.ID)
	s.MarkJobSucceeded(ctx, job.ID)
	snap, err := s.FreezeSourceSnapshot(ctx, models.SourceEpisode, source)
	if err != nil {
		t.Fatal(err)
	}
	anchor, _ := json.Marshal(models.NoteAnchor{SnapshotID: snap.ID, Version: snap.ContentVersion, Position: 12, SegmentIDs: []string{"seg-1"}, Mode: "original"})
	d, created, err := s.CreateVoiceNoteDraft(ctx, VoiceNoteDraft{ID: uuid.NewString(), SourceType: models.SourceEpisode, SourceID: source, AnchorJSON: string(anchor), UploadSHA256: strings.Repeat("a", 64), AudioSHA256: strings.Repeat("b", 64), AudioFile: uuid.NewString() + ".wav", DurationSeconds: 3, SizeBytes: 96044, Text: "我的原始理解"})
	if err != nil || !created {
		t.Fatal(created, err)
	}
	return s, d
}

func TestVoiceDraftSeparateASRRevisionAndIdempotentSave(t *testing.T) {
	s, d := voiceStoreFixture(t)
	ctx := t.Context()
	duplicate, created, err := s.CreateVoiceNoteDraft(ctx, *d)
	if err != nil || created || duplicate.ID != d.ID {
		t.Fatal(duplicate, created, err)
	}
	d, err = s.QueueVoiceASR(ctx, d.ID, d.Revision, provider.TaskConfig{Provider: "groq", Model: "asr-fixed"}, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	before := d.JobID
	duplicate, err = s.QueueVoiceASR(ctx, d.ID, d.Revision, provider.TaskConfig{Provider: "openai", Model: "changed"}, "default", false)
	if err != nil || duplicate.JobID != before {
		t.Fatal("duplicate starts new call", duplicate, err)
	}
	d, err = s.EditVoiceNoteDraft(ctx, d.ID, "等待转写时自己修改", d.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, d.JobID)
	var in VoiceASRInput
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
	if err = s.CommitVoiceASR(ctx, d.JobID, in, "模型转写建议"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetVoiceNoteDraft(ctx, d.ID)
	if d.Text != "等待转写时自己修改" || d.ASRText != "模型转写建议" || d.Revision != 2 || d.ASRBaseRevision != 1 {
		t.Fatalf("overwrite Owner: %+v", d)
	}
	if _, err = s.EditVoiceNoteDraft(ctx, d.ID, "旧窗口", 1, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.EditVoiceNoteDraft(ctx, d.ID, "", 2, "other-job"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	d, err = s.EditVoiceNoteDraft(ctx, d.ID, "", 2, d.JobID)
	if err != nil || d.Text != "模型转写建议" {
		t.Fatal(d, err)
	}
	q, err := s.CreateLearningQuestion(ctx, LearningQuestion{Body: "如何理解原音？", Goal: "整理个人理解", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveVoiceNoteDraft(ctx, d.ID, d.Revision, q.ID, q.Revision+1, false); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	note, err := s.SaveVoiceNoteDraft(ctx, d.ID, d.Revision, q.ID, q.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveVoiceNoteDraft(ctx, d.ID, d.Revision, q.ID, q.Revision, false)
	if err != nil || second.ID != note.ID {
		t.Fatal(second, err)
	}
	if note.Kind != "owner_reflection" || note.CitationsJSON != "[]" || note.ReferencesJSON != `["seg-1"]` || note.AnchorJSON != d.AnchorJSON {
		t.Fatalf("voice became source evidence: %+v", note)
	}
	var notes, cleanup int
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&notes)
	s.DB.QueryRow(`SELECT count(*) FROM voice_audio_cleanup`).Scan(&cleanup)
	if notes != 1 || cleanup != 1 {
		t.Fatalf("save duplicated: %d %d", notes, cleanup)
	}
	links, _ := s.ListLearningQuestionRelations(ctx, q.ID)
	if len(links) != 1 || links[0].ObjectID != note.ID {
		t.Fatal(links)
	}
}

func TestVoiceDraftDeleteLateResponseAndSourcePurge(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "OwnerDelete", true: "SourcePurge"}[purge], func(t *testing.T) {
			s, d := voiceStoreFixture(t)
			ctx := t.Context()
			d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq"}, "default", false)
			if err != nil {
				t.Fatal(err)
			}
			ex, _ := s.GetJobExecution(ctx, d.JobID)
			var in VoiceASRInput
			json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
			if purge {
				err = s.DeleteSourceRows(ctx, d.SourceType, d.SourceID)
			} else {
				err = s.DeleteVoiceNoteDraft(ctx, d.ID, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CommitVoiceASR(ctx, d.JobID, in, "迟到转写"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetVoiceNoteDraft(ctx, d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			var state, text, file string
			s.DB.QueryRow(`SELECT state,text,audio_file FROM voice_note_drafts WHERE id=?`, d.ID).Scan(&state, &text, &file)
			if state != "deleted" || text != "" || file != "" {
				t.Fatalf("resurrected %s %s %s", state, text, file)
			}
			if err = s.FailVoiceASR(ctx, d.ID, d.JobID, "失败"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVoiceAudioBudgetIsSeparateFromTextAndFrozen(t *testing.T) {
	s, d := voiceStoreFixture(t)
	ctx := t.Context()
	budget := int64(100)
	s.SetOwnerMonthlyBudget(ctx, &budget)
	s.SetModelPrice(ctx, models.ModelPrice{Provider: "groq", Model: "audio-model", InputCentsPerMillion: 1, OutputCentsPerMillion: 1})
	d, err := s.QueueVoiceASR(ctx, d.ID, 1, provider.TaskConfig{Provider: "groq", Model: "audio-model"}, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.GetJobExecution(ctx, d.JobID)
	var in VoiceASRInput
	json.Unmarshal([]byte(ex.InputSnapshotJSON), &in)
	if _, err = s.HoldVoiceASRBudget(ctx, d.JobID, in); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatal("seconds were billed as tokens", err)
	}
	s.SetASRAudioPrice(ctx, "groq", "audio-model", 20)
	if _, err = s.HoldVoiceASRBudget(ctx, d.JobID, in); !errors.Is(err, ErrBudgetUnpriced) {
		t.Fatal("frozen missing price changed", err)
	}
	in.PriceKnown = true
	in.CentsPerMinute = 20
	reservation, err := s.HoldVoiceASRBudget(ctx, d.JobID, in)
	if err != nil || reservation.EstimatedCostCents != 1 {
		t.Fatal(reservation, err)
	}
	if again, err := s.HoldVoiceASRBudget(ctx, d.JobID, in); err != nil || again.ID != reservation.ID {
		t.Fatal(again, err)
	}
	for _, v := range []float64{-1, math.Inf(1), math.NaN(), 1e7} {
		if s.SetASRAudioPrice(ctx, "groq", "audio-model", v) == nil {
			t.Fatal("invalid price")
		}
	}
	if _, err = s.HoldVoiceASRBudget(ctx, d.JobID, VoiceASRInput{}); !errors.Is(err, ErrBudgetIncomplete) {
		t.Fatal(err)
	}
}

func TestVoiceHistoricalAnchorAndRetainedAudio(t *testing.T) {
	s, d := voiceStoreFixture(t)
	ctx := t.Context()
	job, _ := s.EnqueueAnalyze(ctx, d.SourceType, d.SourceID)
	v, err := s.CreateArtifactVersion(ctx, d.SourceType, d.SourceID, KindTranscript, "fixture", "new", "1", job.ID, `{"segments":[{"id":"new-seg","start":1,"end":2,"text":"新版资料"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCurrentVersion(ctx, d.SourceType, d.SourceID, KindTranscript, v)
	n, err := s.SaveVoiceNoteDraft(ctx, d.ID, 1, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if n.AnchorJSON != d.AnchorJSON {
		t.Fatal("anchor moved")
	}
	saved, _ := s.GetVoiceNoteDraft(ctx, d.ID)
	if !saved.KeepAudio || saved.AudioFile != d.AudioFile {
		t.Fatal(saved)
	}
	if _, err = s.EditVoiceNoteDraft(ctx, d.ID, "误改", saved.Revision, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.DeleteVoiceNoteDraft(ctx, d.ID, saved.Revision); err != nil {
		t.Fatal(err)
	}
	n2, err := s.GetOwnerNote(ctx, n.ID)
	if err != nil || n2.Content != n.Content {
		t.Fatal("deleting recording removed saved note", n2, err)
	}
	if err = s.DeleteVoiceNoteDraft(ctx, d.ID, 1); err != nil {
		t.Fatal("idempotent delete", err)
	}
}

func TestVoiceDraftValidationAndCAS(t *testing.T) {
	s, d := voiceStoreFixture(t)
	ctx := t.Context()
	for _, change := range []func(*VoiceNoteDraft){func(d *VoiceNoteDraft) { d.ID = "not-uuid" }, func(d *VoiceNoteDraft) { d.DurationSeconds = 301 }, func(d *VoiceNoteDraft) { d.DurationSeconds = math.NaN() }, func(d *VoiceNoteDraft) { d.AudioFile = "../bad.wav" }, func(d *VoiceNoteDraft) { d.SizeBytes = 21 << 20 }, func(d *VoiceNoteDraft) { d.SourceType = "document" }, func(d *VoiceNoteDraft) { d.AnchorJSON = "{}" }} {
		bad := *d
		bad.ID = uuid.NewString()
		change(&bad)
		if _, _, err := s.CreateVoiceNoteDraft(ctx, bad); err == nil {
			t.Fatal("invalid admission")
		}
	}
	bad := *d
	bad.UploadSHA256 = strings.Repeat("c", 64)
	if _, _, err := s.CreateVoiceNoteDraft(ctx, bad); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.EditVoiceNoteDraft(ctx, d.ID, "", 0, ""); err == nil {
		t.Fatal("invalid revision")
	}
	if err := s.DeleteVoiceNoteDraft(ctx, d.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if list, err := s.ListVoiceNoteDrafts(ctx, 0); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err := s.ListVoiceNoteDrafts(ctx, -1); err == nil {
		t.Fatal("invalid pagination")
	}
}
