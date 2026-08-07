package queue

// 音频处理工具：码率选择、时长探测、转码、SHA256、扩展名推断。
// 从 worker.go 抽出，使 worker 聚焦于处理流水线与状态机，音频细节独立。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	maxTranscriptionUploadBytes int64 = 22 << 20
	minEvidenceBitrateKbps            = 16
	maxEvidenceBitrateKbps            = 64
)

// evidenceBitrateKbps 按时长选择可让转录请求（含 multipart 开销）保持在 25MB
// Groq 上限以内的最高标准 MP3 码率。长到 16kbps 仍无法容纳时，需要后续分段策略。
func evidenceBitrateKbps(durationSeconds float64) (int, error) {
	if durationSeconds <= 0 {
		return 0, fmt.Errorf("音频时长无效: %.3f", durationSeconds)
	}
	maxKbps := int(math.Floor(float64(maxTranscriptionUploadBytes*8) / durationSeconds / 1000))
	for _, bitrate := range []int{maxEvidenceBitrateKbps, 56, 48, 40, 32, 24, minEvidenceBitrateKbps} {
		if bitrate <= maxKbps {
			return bitrate, nil
		}
	}
	return 0, fmt.Errorf("音频时长 %.0f 秒即使以 %dkbps 转码仍超过 Groq 单文件上传上限；需要分段转录", durationSeconds, minEvidenceBitrateKbps)
}

// audioDuration 用 ffprobe 读取原始输入时长，以便选择 EvidenceAudio 的码率。
func audioDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

// transcodeAudio 用给定码率转码为 16kHz 单声道 MP3。
func transcodeAudio(in, out string, bitrateKbps int) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", in,
		"-ac", "1",
		"-ar", "16000",
		"-b:a", fmt.Sprintf("%dk", bitrateKbps),
		"-f", "mp3",
		out,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", stderr.String(), err)
	}
	return nil
}
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func guessAudioExt(url string) string {
	low := strings.ToLower(url)
	for _, e := range []string{".mp3", ".m4a", ".wav", ".aac", ".ogg"} {
		if strings.HasSuffix(low, e) {
			return e
		}
	}
	return ".mp3"
}
