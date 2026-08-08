// Package provider：Narration（解说音轨）TTS 合成（ADR-0019）。
//
// 默认实现 KokoroProvider 通过进程外 CLI 调用 Kokoro TTS 引擎（os/exec），
// 与 audio.go 调用 ffmpeg/ffprobe 同构。Narration 只读 GeneratedDerivative（Gist），
// 永不读可核验内容（Summary/KeyPoint/Quote/原音区间）。
//
// 设计要点：
//   - 引擎未安装时 Available() 返回 false，worker 跳过合成、不阻塞主流程。
//   - 接口抽象为 NarrationProvider，将来换引擎（Piper / 付费 OpenAI TTS）只改实现。
//   - 每段 Narration 强制以固定开场白"AI 解说："开头（听觉分级，ADR-0019 第 2 节）。
package provider

import (
	"fmt"
	"os/exec"
	"strings"
)

// KokoroProvider 通过 kokoro CLI 合成解说音轨（自托管、免费、Apache 2.0，ADR-0019）。
//
// kokoro 二进制预期用法（兼容 kokoro-cli / piper 风格）：
//
//	kokoro --text <text> --voice <voice> --output <wav> [--model <path>]
//
// 若 kokoro CLI 的参数风格不同，可通过 KokoroArgs 自定义。
type KokoroProvider struct {
	binaryPath   string // kokoro 可执行文件路径（默认 "kokoro"）
	defaultVoice string // 默认音色（如 "af_heart"）
	model        string // 模型文件路径（可选，某些发行版需要）

	// synthFn 可注入的合成函数（测试用）；nil 时用真实 exec.Command。
	synthFn func(text, voice, outPath string) error
}

// NewKokoroProvider 构造一个 Kokoro TTS Provider。
func NewKokoroProvider(binaryPath, defaultVoice, model string) *KokoroProvider {
	if binaryPath == "" {
		binaryPath = "kokoro"
	}
	if defaultVoice == "" {
		defaultVoice = "af_heart"
	}
	return &KokoroProvider{binaryPath: binaryPath, defaultVoice: defaultVoice, model: model}
}

// Available 探测 kokoro 二进制是否可执行（PATH 查找或绝对路径存在）。
// 不可用时 worker 跳过 Narration 合成、不阻塞主流程（ADR-0019 R1）。
func (k *KokoroProvider) Available() bool {
	if k.synthFn != nil {
		return true // 测试注入的合成函数总是可用
	}
	_, err := exec.LookPath(k.binaryPath)
	return err == nil
}

// Synthesize 将 text 合成为 wav 写入 outPath。
// 强制在文本前加固定开场白"AI 解说："（听觉分级，让 Owner 一耳朵可辨这是 AI 串场，ADR-0019 第 2 节）。
func (k *KokoroProvider) Synthesize(text, voice, outPath string) (*NarrationResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("合成文本为空")
	}
	if voice == "" {
		voice = k.defaultVoice
	}
	// 听觉分级：固定开场白。用换行分隔，让引擎在开场白后略停顿。
	fullText := "AI 解说：\n" + strings.TrimSpace(text)
	charCount := len([]rune(fullText))

	if k.synthFn != nil {
		if err := k.synthFn(fullText, voice, outPath); err != nil {
			return nil, err
		}
	} else {
		if err := k.runCLI(fullText, voice, outPath); err != nil {
			return nil, err
		}
	}
	// 时长探测交给调用方（worker 已有 audioDuration 助手，复用避免重复实现）。
	return &NarrationResult{
		AudioPath: outPath,
		CharCount: charCount,
		Voice:     voice,
		Model:     "kokoro-82m",
	}, nil
}

// runCLI 调用 kokoro 二进制合成。
func (k *KokoroProvider) runCLI(text, voice, outPath string) error {
	args := []string{
		"--text", text,
		"--voice", voice,
		"--output", outPath,
	}
	if k.model != "" {
		args = append(args, "--model", k.model)
	}
	cmd := exec.Command(k.binaryPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kokoro 合成失败: %w: %s", err, string(out))
	}
	return nil
}

// Name 返回 Provider 名（用于 narrations 表记录 provider 字段）。
func (k *KokoroProvider) Name() string { return "kokoro" }

// DefaultVoice 返回默认音色标识。
func (k *KokoroProvider) DefaultVoice() string { return k.defaultVoice }

// WithSynthFunc 注入测试用合成函数（生产代码不用）。
func (k *KokoroProvider) WithSynthFunc(fn func(text, voice, outPath string) error) *KokoroProvider {
	return &KokoroProvider{
		binaryPath: k.binaryPath, defaultVoice: k.defaultVoice, model: k.model, synthFn: fn,
	}
}
