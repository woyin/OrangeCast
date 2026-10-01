package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func wavFixture(t *testing.T, seconds int) string {
	t.Helper()
	size := seconds * pcmBytesPerSecond
	raw := make([]byte, 44+size)
	copy(raw, "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(36+size))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 1)
	binary.LittleEndian.PutUint32(raw[24:], 16000)
	binary.LittleEndian.PutUint32(raw[28:], 32000)
	binary.LittleEndian.PutUint16(raw[32:], 2)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], uint32(size))
	path := filepath.Join(t.TempDir(), "clip.wav")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestNormalizeRealWAVAndWebM(t *testing.T) {
	input := wavFixture(t, 1)
	for _, format := range []string{"wav", "webm"} {
		t.Run(format, func(t *testing.T) {
			path := input
			if format == "webm" {
				path = filepath.Join(t.TempDir(), "browser.webm")
				if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", input, "-c:a", "libopus", path).CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
			}
			dir := filepath.Join(t.TempDir(), "private")
			audio, err := Normalize(t.Context(), path, dir)
			if err != nil {
				t.Fatal(err)
			}
			if audio.Duration < 0.95 || audio.Duration > 1.1 || len(audio.SHA256) != 64 || len(audio.UploadSHA256) != 64 || audio.Size <= 44 {
				t.Fatal(audio)
			}
			target, err := Path(dir, audio.File)
			if err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(target)
			if err != nil || fi.Mode().Perm() != 0600 {
				t.Fatal(fi, err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatal("intermediates leaked", entries)
			}
		})
	}
}
func TestNormalizeRejectsBytesFormatDurationAndDiskFailure(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "directory", "oversize", "text", "too_long", "unwritable"} {
		t.Run(kind, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "input")
			dir := filepath.Join(t.TempDir(), "voice")
			switch kind {
			case "empty":
				os.WriteFile(input, nil, 0600)
			case "directory":
				os.Mkdir(input, 0700)
			case "oversize":
				f, _ := os.Create(input)
				f.Truncate(MaxBytes + 1)
				f.Close()
			case "text":
				os.WriteFile(input, []byte("this is not audio"), 0600)
			case "too_long":
				input = wavFixture(t, 301)
			case "unwritable":
				input = wavFixture(t, 1)
				os.WriteFile(dir, []byte("directory blocked"), 0600)
			}
			if _, err := Normalize(t.Context(), input, dir); err == nil {
				t.Fatal("invalid recording admitted")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) > 0 {
				t.Fatal("failed validation left files", entries)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Normalize(ctx, wavFixture(t, 1), t.TempDir()); err == nil {
		t.Fatal("canceled decoding succeeded")
	}
}
func TestNormalizeRejectsVideoAndNetworkPlaylist(t *testing.T) {
	video := filepath.Join(t.TempDir(), "video.mp4")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=black:s=16x16:d=0.1", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "0.1", "-c:v", "mpeg4", "-c:a", "aac", video).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if _, err := Normalize(t.Context(), video, t.TempDir()); err == nil {
		t.Fatal("video accepted")
	}
	playlist := filepath.Join(t.TempDir(), "remote.m3u8")
	os.WriteFile(playlist, []byte("#EXTM3U\n#EXTINF:10,\nhttps://example.invalid/audio.ts\n"), 0600)
	if _, err := Normalize(t.Context(), playlist, t.TempDir()); err == nil {
		t.Fatal("network playlist accepted")
	}
}
func TestPrivatePathAndCapacity(t *testing.T) {
	for _, name := range []string{"../secret.wav", "/secret.wav", "bad.wav", "x.mp3", ""} {
		if _, err := Path(t.TempDir(), name); err == nil {
			t.Fatal(name)
		}
	}
	var output bytes.Buffer
	b := &boundedWriter{w: &output, limit: 3}
	if n, err := b.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if _, err := b.Write([]byte("d")); err == nil || output.String() != "abc" {
		t.Fatal(output.String(), err)
	}
}
func TestNormalizeUnsupportedDecoderMetadata(t *testing.T) {
	for _, metadata := range []string{"invalid-json", `{"format":{"format_name":"image2"},"streams":[{"codec_type":"audio"}]}`, `{"format":{"format_name":"wav"},"streams":[]}`} {
		t.Run(strings.ReplaceAll(metadata, "/", "-"), func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\ncat <<'JSON'\n" + metadata + "\nJSON\n"
			os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(script), 0700)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			if _, err := Normalize(t.Context(), wavFixture(t, 1), t.TempDir()); err == nil {
				t.Fatal("unsupported metadata admitted")
			}
		})
	}
}

type faultRecording struct {
	file   *os.File
	stage  string
	writes int
}

func (f *faultRecording) Write(p []byte) (int, error) {
	f.writes++
	if f.stage == "header" || f.stage == "body" && f.writes > 1 {
		return 0, os.ErrPermission
	}
	return f.file.Write(p)
}
func (f *faultRecording) Sync() error {
	if f.stage == "sync" {
		return os.ErrPermission
	}
	return f.file.Sync()
}
func (f *faultRecording) Close() error {
	if f.stage == "close" {
		f.file.Close()
		return os.ErrPermission
	}
	if f.stage == "vanished" {
		f.file.Close()
		os.Remove(f.file.Name())
		return nil
	}
	return f.file.Close()
}

func TestNormalizeDiskFailuresNeverLeaveAnAdmittedRecording(t *testing.T) {
	originalPCM, originalRecording := createPCMFile, createRecordingFile
	t.Cleanup(func() { createPCMFile = originalPCM; createRecordingFile = originalRecording })
	for _, stage := range []string{"temporary", "create", "header", "body", "sync", "close", "vanished"} {
		t.Run(stage, func(t *testing.T) {
			createPCMFile = originalPCM
			createRecordingFile = originalRecording
			if stage == "temporary" {
				createPCMFile = func(string, string) (*os.File, error) { return nil, os.ErrPermission }
			}
			createRecordingFile = func(path string) (recordingFile, error) {
				if stage == "create" {
					return nil, os.ErrPermission
				}
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				return &faultRecording{file: f, stage: stage}, nil
			}
			dir := t.TempDir()
			if _, err := Normalize(t.Context(), wavFixture(t, 1), dir); err == nil {
				t.Fatal("failed private write was accepted")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("failed recording files leaked", entries)
			}
		})
	}
}
