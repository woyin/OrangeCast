package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/filehash"
)

func TestVoiceManifestRejectsCorruptOrUnlistedFiles(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	os.WriteFile(db, []byte("snapshot"), 0600)
	dbHash, _ := filehash.SHA256(db)
	name := uuid.NewString() + ".wav"
	recording := filepath.Join(dir, name)
	os.WriteFile(recording, []byte("recording"), 0600)
	hash, _ := filehash.SHA256(recording)
	for _, kind := range []string{"path", "duplicate", "missing", "stat", "size", "hash", "extra"} {
		t.Run(kind, func(t *testing.T) {
			entry := EvidenceEntry{RelPath: name, SizeBytes: 9, SHA256: hash}
			m := Manifest{Format: ManifestFormat, Version: 2, DBSHA256: dbHash, Voice: []EvidenceEntry{entry}}
			files := map[string]string{name: recording}
			switch kind {
			case "path":
				m.Voice[0].RelPath = "../escape"
			case "duplicate":
				m.Voice = append(m.Voice, entry)
			case "missing":
				delete(files, name)
			case "stat":
				files[name] = filepath.Join(dir, "missing")
			case "size":
				m.Voice[0].SizeBytes++
			case "hash":
				m.Voice[0].SHA256 = "wrong"
			case "extra":
				m.Voice = nil
			}
			raw, _ := json.Marshal(m)
			if _, err := validateExtractedArchive(extractedArchive{manifestData: raw, dbPath: db, extractedVoice: files}); err == nil {
				t.Fatal("corruption accepted", kind)
			}
		})
	}
}

func TestPrivateVoiceExtractionRejectsDuplicatesAndNonRegularFiles(t *testing.T) {
	name := "voice-notes/" + uuid.NewString() + ".wav"
	for _, kind := range []string{"duplicate", "symlink", "short-body", "parent-is-file"} {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			hdr := &tar.Header{Name: name, Size: 1, Mode: 0600, Typeflag: tar.TypeReg}
			if kind == "symlink" {
				hdr.Typeflag = tar.TypeSymlink
				hdr.Size = 0
				hdr.Linkname = "/etc/passwd"
			}
			tw.WriteHeader(hdr)
			if kind != "short-body" {
				tw.Write([]byte("x"))
			}
			if kind == "duplicate" {
				tw.WriteHeader(hdr)
				tw.Write([]byte("x"))
			}
			if kind != "short-body" {
				tw.Close()
			}
			dir := t.TempDir()
			if kind == "parent-is-file" {
				os.WriteFile(filepath.Join(dir, "voice-notes"), []byte("obstacle"), 0600)
			}
			if _, err := extractArchive(tar.NewReader(&buf), dir); err == nil {
				t.Fatal("unsafe extraction", kind)
			}
		})
	}
}

func TestCollectVoiceUsesSnapshotAndExcludesTemporaryRecordings(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.db")
	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = collectVoiceEntries(t.Context(), snapshot, nil); err != nil {
		t.Fatal("old schema", err)
	}
	db.Exec(`CREATE TABLE voice_note_drafts(audio_file TEXT,audio_sha256 TEXT,size_bytes INTEGER,state TEXT,keep_audio INTEGER)`)
	name := uuid.NewString() + ".wav"
	file := filepath.Join(dir, name)
	os.WriteFile(file, []byte("retained"), 0600)
	hash, _ := filehash.SHA256(file)
	db.Exec(`INSERT INTO voice_note_drafts VALUES(?,?,8,'uploaded',0)`, name, hash)
	if entries, err := collectVoiceEntries(t.Context(), snapshot, nil); err != nil || len(entries) != 0 {
		t.Fatal("temporary recording backed up", entries, err)
	}
	db.Exec(`UPDATE voice_note_drafts SET state='saved',keep_audio=1`)
	for _, kind := range []string{"no-dir", "invalid-path", "missing", "symlink", "size", "hash", "valid"} {
		t.Run(kind, func(t *testing.T) {
			dirs := []string{dir}
			testName, testHash, size := name, hash, 8
			switch kind {
			case "no-dir":
				dirs = nil
			case "invalid-path":
				testName = "../unsafe.wav"
			case "missing":
				testName = uuid.NewString() + ".wav"
			case "symlink":
				testName = uuid.NewString() + ".wav"
				os.Symlink(file, filepath.Join(dir, testName))
			case "size":
				size++
			case "hash":
				testHash = "different"
			}
			db.Exec(`UPDATE voice_note_drafts SET audio_file=?,audio_sha256=?,size_bytes=?`, testName, testHash, size)
			entries, err := collectVoiceEntries(t.Context(), snapshot, dirs)
			if kind == "valid" {
				if err != nil || len(entries) != 1 {
					t.Fatal(entries, err)
				}
			} else if err == nil {
				t.Fatal("invalid recording accepted", kind)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := collectVoiceEntries(ctx, snapshot, []string{dir}); err == nil {
		t.Fatal("canceled snapshot read accepted")
	}
	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte("not sqlite"), 0600)
	if _, err := collectVoiceEntries(t.Context(), bad, nil); err == nil {
		t.Fatal("invalid database accepted")
	}
}

func TestVoiceInstallFailureDoesNotSelectDatabase(t *testing.T) {
	for _, kind := range []string{"path", "directory", "rename"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			db := filepath.Join(t.TempDir(), "db")
			os.WriteFile(db, []byte("new snapshot"), 0600)
			old := filepath.Join(target, dbFileName)
			os.WriteFile(old, []byte("previous database"), 0600)
			name := uuid.NewString() + ".wav"
			if kind == "path" {
				name = "invalid"
			}
			if kind == "directory" {
				os.WriteFile(filepath.Join(target, "voice-notes"), []byte("obstacle"), 0600)
			}
			err := installArchive(extractedArchive{dbPath: db, extractedVoice: map[string]string{name: filepath.Join(target, "missing")}}, Manifest{Voice: []EvidenceEntry{{RelPath: name}}}, target)
			if err == nil {
				t.Fatal("failed installation accepted")
			}
			got, _ := os.ReadFile(old)
			if string(got) != "previous database" {
				t.Fatal("database selected before recording installation")
			}
		})
	}
}

func TestVoiceArchiveWritingReportsMissingDirectoryPathAndFile(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	os.WriteFile(db, []byte("snapshot"), 0600)
	for _, kind := range []string{"directory", "path", "missing-file"} {
		t.Run(kind, func(t *testing.T) {
			name := uuid.NewString() + ".wav"
			dirs := []string{dir}
			if kind == "directory" {
				dirs = nil
			}
			if kind == "path" {
				name = "../escape"
			}
			m := Manifest{DBFile: dbFileName, Voice: []EvidenceEntry{{RelPath: name, SizeBytes: 1}}}
			if err := writeBackupArchive(filepath.Join(t.TempDir(), "backup.tar.gz"), db, dir, m, dirs...); err == nil {
				t.Fatal("incomplete backup accepted", kind)
			}
		})
	}
}
