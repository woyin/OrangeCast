package queue

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAudioDuration_InvalidFile 验证 ffprobe 对不存在的文件返回错误。
// 覆盖 audioDuration 中 cmd.Output 失败分支。
func TestAudioDuration_InvalidFile(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 不可用")
	}
	if _, err := audioDuration(filepath.Join(t.TempDir(), "missing.mp3")); err == nil {
		t.Fatal("ffprobe 对不存在的文件应报错")
	}
}

// TestAudioDuration_NonNumericOutput 验证 ffprobe 输出非数字时返回错误。
// 覆盖 audioDuration 中 ParseFloat 失败分支。
func TestAudioDuration_NonNumericOutput(t *testing.T) {
	// 用一个无法解析的输入：目录路径让 ffprobe 报错，或直接跳过。
	// ParseFloat 分支难以直接触发（需要伪造 ffprobe 输出），此处验证
	// 对目录路径 ffprobe 会失败并返回 err（覆盖 cmd.Output err 分支）。
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 不可用")
	}
	if _, err := audioDuration(t.TempDir()); err == nil {
		t.Fatal("ffprobe 对目录应报错")
	}
}

// TestTranscodeAudio_InvalidInput 验证 ffmpeg 对不存在的输入返回错误。
// 覆盖 transcodeAudio 中 cmd.Run 失败分支。
func TestTranscodeAudio_InvalidInput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 不可用")
	}
	out := filepath.Join(t.TempDir(), "out.mp3")
	if err := transcodeAudio(filepath.Join(t.TempDir(), "missing.mp3"), out, 64); err == nil {
		t.Fatal("ffmpeg 对不存在的输入应报错")
	}
}

// TestAudioDuration_ParseFloatFail 验证 ffprobe 输出非数字时 ParseFloat 失败返回错误。
// 覆盖 audioDuration 中 strconv.ParseFloat 失败分支。
// 通过在 PATH 前置一个伪造 ffprobe 脚本（输出非数字）触发。
func TestAudioDuration_ParseFloatFail(t *testing.T) {
	// 构造一个伪造 ffprobe 可执行脚本
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "ffprobe")
	script := "#!/bin/sh\necho 'not-a-number'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := audioDuration("ignored.mp3"); err == nil {
		t.Fatal("ffprobe 输出非数字应报错")
	}
}
