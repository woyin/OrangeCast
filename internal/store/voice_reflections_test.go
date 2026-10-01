package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestVoiceReflectionExplicitAdoptionAndSingleNote(t *testing.T) {
	s, v := voiceStoreFixture(t)
	ctx := t.Context()
	var anchor models.NoteAnchor
	if err := json.Unmarshal([]byte(v.AnchorJSON), &anchor); err != nil {
		t.Fatal(err)
	}
	c := ListeningCapture{SourceType: string(v.SourceType), SourceID: v.SourceID, Anchor: anchor}
	r, err := s.StartListeningReflection(ctx, uuid.NewString(), c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ChangeListeningReflection(ctx, r.ID, uuid.NewString(), "edit", 1, ReflectionAnswers{Remember: "Owner已有解释", Apply: "Owner原计划"})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, err = s.DB.Exec(`CREATE TEMP TRIGGER fail_voice_adopt BEFORE INSERT ON voice_reflection_adoptions BEGIN SELECT RAISE(ABORT,'fault');END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdoptVoiceIntoReflection(ctx, r.ID, key, v.ID, "uncertain", r.Revision, v.Revision); err == nil {
		t.Fatal("fault committed")
	}
	unchanged, _ := s.GetListeningReflection(ctx, r.ID)
	if unchanged.Answers.Uncertain != "" || unchanged.Revision != r.Revision {
		t.Fatal(unchanged)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_voice_adopt`); err != nil {
		t.Fatal(err)
	}
	adopted, err := s.AdoptVoiceIntoReflection(ctx, r.ID, key, v.ID, "uncertain", r.Revision, v.Revision)
	if err != nil || adopted.Answers.Uncertain != v.Text || adopted.Answers.Remember != "Owner已有解释" || adopted.Answers.Apply != "Owner原计划" {
		t.Fatal(adopted, err)
	}
	replay, err := s.AdoptVoiceIntoReflection(ctx, r.ID, key, v.ID, "uncertain", r.Revision, v.Revision)
	if err != nil || replay.Revision != adopted.Revision {
		t.Fatal(replay, err)
	}
	if _, err = s.AdoptVoiceIntoReflection(ctx, r.ID, key, v.ID, "apply", r.Revision, v.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.SaveVoiceNoteDraft(ctx, v.ID, v.Revision, "", 0, false); !errors.Is(err, ErrConflict) {
		t.Fatal("second formal save allowed", err)
	}
	v, err = s.QueueVoiceASR(ctx, v.ID, v.Revision, provider.TaskConfig{Provider: "groq", Model: "fixture"}, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), adopted.Revision, adopted.Answers); !errors.Is(err, ErrConflict) {
		t.Fatal("deleted recording while call queued", err)
	}
	ex, err := s.GetJobExecution(ctx, v.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var in VoiceASRInput
	if err = json.Unmarshal([]byte(ex.InputSnapshotJSON), &in); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitVoiceASR(ctx, v.JobID, in, "迟到转写建议"); err != nil {
		t.Fatal(err)
	}
	unchanged, err = s.GetListeningReflection(ctx, r.ID)
	if err != nil || unchanged.Answers.Uncertain != adopted.Answers.Uncertain || unchanged.Revision != adopted.Revision {
		t.Fatal("ASR overwrote Owner", unchanged, err)
	}
	saved, err := s.SaveListeningReflection(ctx, r.ID, uuid.NewString(), adopted.Revision, adopted.Answers)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := s.GetVoiceNoteDraft(ctx, v.ID)
	if err != nil || voice.State != "saved" || voice.NoteID != saved.SavedNoteID || voice.AudioFile != "" || voice.ReflectionID != r.ID {
		t.Fatal(voice, err)
	}
	var n, files int
	s.DB.QueryRow(`SELECT count(*) FROM owner_notes`).Scan(&n)
	s.DB.QueryRow(`SELECT count(*) FROM voice_audio_cleanup`).Scan(&files)
	if n != 1 || files != 1 {
		t.Fatal(n, files)
	}
}

func TestVoiceReflectionRejectsMismatchedCaptureAndRevision(t *testing.T) {
	s, v := voiceStoreFixture(t)
	var anchor models.NoteAnchor
	json.Unmarshal([]byte(v.AnchorJSON), &anchor)
	anchor.Position++
	r, err := s.StartListeningReflection(t.Context(), uuid.NewString(), ListeningCapture{SourceType: string(v.SourceType), SourceID: v.SourceID, Anchor: anchor}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, field    string
		rev, voiceRev int
		want          error
	}{
		{"bad", "remember", 1, 1, ErrInvalidEditorialState}, {uuid.NewString(), "invalid", 1, 1, ErrInvalidEditorialState}, {uuid.NewString(), "remember", 0, 1, ErrInvalidEditorialState}, {uuid.NewString(), "remember", 1, 0, ErrInvalidEditorialState},
		{uuid.NewString(), "remember", 2, 1, ErrConflict}, {uuid.NewString(), "remember", 1, 2, ErrConflict}, {uuid.NewString(), "remember", 1, 1, ErrConflict},
	} {
		if _, err = s.AdoptVoiceIntoReflection(t.Context(), r.ID, tc.key, v.ID, tc.field, tc.rev, tc.voiceRev); !errors.Is(err, tc.want) {
			t.Fatal(tc, err)
		}
	}
}
