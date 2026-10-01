// Package voice manages bounded private recordings separately from source evidence.
package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/filehash"
)

// MaxBytes and MaxSeconds apply to both browser and server admission.
const (
	MaxBytes          int64 = 20 << 20
	MaxSeconds              = 300
	pcmBytesPerSecond       = 32000
)

type recordingFile interface {
	io.WriteCloser
	Sync() error
}

// File creation is the narrow disk-failure seam: production always uses private
// exclusive files, tests can simulate ENOSPC after decoding without a real full disk.
var createPCMFile = os.CreateTemp
var createRecordingFile = func(path string) (recordingFile, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

// Audio contains only server-derived media facts and an opaque private filename.
type Audio struct {
	File         string
	UploadSHA256 string
	SHA256       string
	Duration     float64
	Size         int64
}

type boundedWriter struct {
	w           io.Writer
	size, limit int64
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if b.size+int64(len(p)) > b.limit {
		return 0, fmt.Errorf("音频输出超过允许容量")
	}
	n, err := b.w.Write(p)
	b.size += int64(n)
	return n, err
}

// Normalize validates real containers/streams and measures decoded duration. It
// works for MediaRecorder WebM without duration metadata and never trusts MIME.
// Input/output/network/duration limits keep decoding local and bounded.
func Normalize(ctx context.Context, input, dir string) (Audio, error) {
	var out Audio
	fi, err := os.Stat(input)
	if err != nil {
		return out, err
	}
	if !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > MaxBytes {
		return out, fmt.Errorf("录音为空或超过20MB")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "format=format_name:stream=codec_type", "-of", "json", input)
	var probeOutput bytes.Buffer
	probe.Stdout = &boundedWriter{w: &probeOutput, limit: 64 << 10}
	err = probe.Run()
	raw := probeOutput.Bytes()
	if err != nil {
		return out, fmt.Errorf("录音不是可读取的音频格式")
	}
	var metadata struct {
		Format struct {
			Name string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return out, fmt.Errorf("音频格式无法确认")
	}
	formats := map[string]bool{"wav": true, "ogg": true, "matroska": true, "webm": true, "mov": true, "mp4": true, "m4a": true, "3gp": true, "3g2": true, "mj2": true, "mp3": true, "flac": true}
	allowed := false
	for _, name := range strings.Split(metadata.Format.Name, ",") {
		allowed = allowed || formats[name]
	}
	if !allowed || len(metadata.Streams) != 1 || metadata.Streams[0].Type != "audio" {
		return out, fmt.Errorf("只接受单条音频流的WAV、WebM、Ogg、MP4、MP3或FLAC录音")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return out, err
	}
	pcm, err := createPCMFile(dir, ".voice-pcm-*")
	if err != nil {
		return out, err
	}
	defer os.Remove(pcm.Name())
	defer pcm.Close()
	bounded := &boundedWriter{w: pcm, limit: (MaxSeconds + 1) * pcmBytesPerSecond}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe", "-i", input, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", "16000", "-t", "301", "-f", "s16le", "pipe:1")
	cmd.Stdout = bounded
	if err = cmd.Run(); err != nil {
		return out, fmt.Errorf("录音解码失败或超过5分钟")
	}
	duration := float64(bounded.size) / pcmBytesPerSecond
	if duration <= 0 || duration > MaxSeconds {
		return out, fmt.Errorf("实际录音时长须在0至5分钟之间")
	}
	hash, err := filehash.SHA256(input)
	if err != nil {
		return out, err
	}
	filename := uuid.NewString() + ".wav"
	path := filepath.Join(dir, filename)
	f, err := createRecordingFile(path)
	if err != nil {
		return out, err
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+bounded.size))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], 16000)
	binary.LittleEndian.PutUint32(header[28:], pcmBytesPerSecond)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(bounded.size))
	if _, err = f.Write(header); err != nil {
		return out, err
	}
	if _, err = pcm.Seek(0, 0); err != nil {
		return out, err
	}
	if _, err = io.Copy(f, pcm); err != nil {
		return out, err
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	if err = f.Close(); err != nil {
		return out, err
	}
	normalizedHash, err := filehash.SHA256(path)
	if err != nil {
		return out, err
	}
	success = true
	return Audio{File: filename, UploadSHA256: hash, SHA256: normalizedHash, Duration: duration, Size: bounded.size + 44}, nil
}

// Path accepts only server-produced basenames; no user path can escape the directory.
func Path(dir, file string) (string, error) {
	if dir == "" || !strings.HasSuffix(file, ".wav") || filepath.Base(file) != file {
		return "", fmt.Errorf("非法录音文件身份")
	}
	if _, err := uuid.Parse(strings.TrimSuffix(file, ".wav")); err != nil {
		return "", err
	}
	return filepath.Join(dir, file), nil
}
