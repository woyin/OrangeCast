package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/store"
)

func TestVoiceRecordingBackupRestoreAndExplicitDirectory(t *testing.T) {
	source := t.TempDir()
	s := buildFixture(t, source)
	dir := filepath.Join(t.TempDir(), "private-voice-custom")
	os.Mkdir(dir, 0700)
	name := uuid.NewString() + ".wav"
	path := filepath.Join(dir, name)
	os.WriteFile(path, []byte("private retained audio"), 0600)
	hash, _ := filehash.SHA256(path)
	_, err := s.DB.Exec(`INSERT INTO voice_note_drafts(id,source_type,source_id,anchor_json,upload_sha256,audio_sha256,audio_file,duration_seconds,size_bytes,text,state,keep_audio)VALUES(?,'episode','fixture','{}',?,?,?,1,22,'个人理解','saved',1)`, uuid.NewString(), hash, hash, name)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "voice.tar.gz")
	if _, err = Create(t.Context(), s, filepath.Join(source, "evidence"), archive); err == nil {
		t.Fatal("silently omitted retained voice files")
	}
	m, err := Create(t.Context(), s, filepath.Join(source, "evidence"), archive, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || len(m.Voice) != 1 || m.Voice[0].SHA256 != hash {
		t.Fatal(m)
	}
	target := filepath.Join(t.TempDir(), "restored")
	restored, err := Restore(t.Context(), archive, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Voice) != 1 {
		t.Fatal(restored)
	}
	got, err := os.ReadFile(filepath.Join(target, "voice-notes", name))
	if err != nil || string(got) != "private retained audio" {
		t.Fatal(string(got), err)
	}
	fi, _ := os.Stat(filepath.Join(target, "voice-notes", name))
	if fi.Mode().Perm() != 0600 {
		t.Fatal("private permissions", fi.Mode())
	}
	db, err := store.Open(filepath.Join(target, "cloudwisepod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var file string
	db.DB.QueryRow(`SELECT audio_file FROM voice_note_drafts WHERE state='saved'`).Scan(&file)
	if file != name {
		t.Fatal(file)
	}
	os.WriteFile(path, []byte("tampered file"), 0600)
	if _, err = Create(t.Context(), s, filepath.Join(source, "evidence"), archive, dir); err == nil {
		t.Fatal("tampered retained audio accepted")
	}
}

func TestRestoreVoicePathAndManifestValidation(t *testing.T) {
	for _, name := range []string{"voice-notes/../../secret.wav", "voice-notes/not-uuid.wav", "/absolute", "evidence/../escape"} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1})
		tw.Write([]byte("x"))
		tw.Close()
		if _, err := extractArchive(tar.NewReader(&b), t.TempDir()); err == nil {
			t.Fatal("unsafe archive path", name)
		}
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "database")
	os.WriteFile(db, []byte("database fixture"), 0600)
	hash, _ := filehash.SHA256(db)
	m := Manifest{Format: ManifestFormat, Version: 1, DBSHA256: hash}
	raw, _ := json.Marshal(m)
	if _, err := validateExtractedArchive(extractedArchive{manifestData: raw, dbPath: db}); err != nil {
		t.Fatal("legacy v1 rejected", err)
	}
	m.Voice = []EvidenceEntry{{RelPath: uuid.NewString() + ".wav", SHA256: "missing", SizeBytes: 1}}
	raw, _ = json.Marshal(m)
	if _, err := validateExtractedArchive(extractedArchive{manifestData: raw, dbPath: db}); err == nil {
		t.Fatal("v1 voice extension accepted")
	}
	m.Version = 2
	raw, _ = json.Marshal(m)
	if _, err := validateExtractedArchive(extractedArchive{manifestData: raw, dbPath: db, extractedVoice: map[string]string{}}); err == nil {
		t.Fatal("missing voice accepted")
	}
}
