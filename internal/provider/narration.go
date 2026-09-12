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
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// KokoroProvider 通过 kokoro CLI 合成解说音轨（自托管、免费、Apache 2.0，ADR-0019）。
//
// kokoro 二进制预期用法（兼容 kokoro-cli / piper 风格）：
//
//	kokoro --text <text> --voice <voice> --output <wav> [--model <path>]
//
// 若 kokoro CLI 的参数风格不同，可通过 KokoroArgs 自定义。
type KokoroProvider struct {
	binaryPath   string        // kokoro 可执行文件路径（默认 "kokoro"）
	defaultVoice string        // 默认音色（中文用 zf_/zm_ 前缀，如 zf_xiaobei；英文 af_heart）
	model        string        // 模型文件路径（可选，某些发行版需要）
	language     string        // 语言（en|zh；中文需发行版安装 misaki[zh] G2P）
	synthTimeout time.Duration // 单次合成超时（D03；默认 120s）

	// KokoroArgs 自定义 CLI 参数模板（D03）：不同发行版参数风格不同；
	// 占位符 {text} {voice} {output} {model}；nil 时用默认 --text/--voice/--output。
	KokoroArgs []string

	// synthFn 可注入的合成函数（测试用）；nil 时用真实 exec.Command。
	synthFn func(ctx context.Context, text, voice, outPath string) error
}

// NewKokoroProvider 构造一个 Kokoro TTS Provider。
func NewKokoroProvider(binaryPath, defaultVoice, model string) *KokoroProvider {
	if binaryPath == "" {
		binaryPath = "kokoro"
	}
	if defaultVoice == "" {
		defaultVoice = "af_heart"
	}
	return &KokoroProvider{binaryPath: binaryPath, defaultVoice: defaultVoice, model: model, synthTimeout: 120 * time.Second}
}

// WithLanguage 设置语言（D03）：zh 时建议 zf_/zm_ 音色（官方 v1.0 中文音色，
// 需发行版安装 misaki[zh]）；配置不匹配由 Preflight 显式提示。
func (k *KokoroProvider) WithLanguage(lang string) *KokoroProvider {
	k.language = lang
	return k
}

// WithSynthTimeout 设置单次合成超时（D03）。
func (k *KokoroProvider) WithSynthTimeout(d time.Duration) *KokoroProvider {
	k.synthTimeout = d
	return k
}

// Preflight 预检（D03）：引擎可用性、中文语音配置一致性；返回人读结论，
// 不调用付费服务、不落任何文件。
func (k *KokoroProvider) Preflight() (string, error) {
	if !k.Available() {
		return "", fmt.Errorf("解说引擎 %q 不可用：未安装或不在 PATH；Narration 标记为不可用，原音与学习功能不受影响", k.binaryPath)
	}
	msg := fmt.Sprintf("引擎 %q 可用；音色 %q", k.binaryPath, k.defaultVoice)
	if k.language == "zh" && !strings.HasPrefix(k.defaultVoice, "zf_") && !strings.HasPrefix(k.defaultVoice, "zm_") {
		return msg + "；注意：语言配置为中文但音色非 zf_/zm_ 前缀（Kokoro v1.0 中文音色），中文合成可能不成立", nil
	}
	return msg + "；配置一致", nil
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
	return k.SynthesizeContext(context.Background(), text, voice, outPath)
}

// SynthesizeContext 带上下文的合成（D03）：超时受控；合成后校验输出文件存在
// 且非空（半写文件不可发布，最终时长校验由调用方 ffprobe 复核）；
// 错误信息只含引擎名与退出原因，不回显完整命令与配置。
func (k *KokoroProvider) SynthesizeContext(ctx context.Context, text, voice, outPath string) (*NarrationResult, error) {
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
		if err := k.synthFn(ctx, fullText, voice, outPath); err != nil {
			return nil, err
		}
	} else {
		if err := k.runCLI(ctx, fullText, voice, outPath); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("合成产物不存在（引擎未生成输出文件）")
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("合成产物为空文件")
	}
	// 时长探测交给调用方（worker 已有 audioDuration 助手，复用避免重复实现）。
	return &NarrationResult{
		AudioPath: outPath,
		CharCount: charCount,
		Voice:     voice,
		Model:     "kokoro-82m",
	}, nil
}

// runCLI 调用 kokoro 二进制合成（D03）：context 超时受控；错误脱敏
// （截断输出、不回显参数与路径配置）。
func (k *KokoroProvider) runCLI(ctx context.Context, text, voice, outPath string) error {
	timeout := k.synthTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := k.KokoroArgs
	if len(args) == 0 {
		args = []string{"--text", "{text}", "--voice", "{voice}", "--output", "{output}"}
	}
	resolved := make([]string, 0, len(args)+2)
	for _, a := range args {
		switch a {
		case "{text}":
			resolved = append(resolved, text)
		case "{voice}":
			resolved = append(resolved, voice)
		case "{output}":
			resolved = append(resolved, outPath)
		case "{model}":
			if k.model != "" {
				resolved = append(resolved, k.model)
			}
		default:
			resolved = append(resolved, a)
		}
	}
	cmd := exec.CommandContext(ctx, k.binaryPath, resolved...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if len(reason) > 200 {
			reason = reason[:200] + "…"
		}
		if ctx.Err() != nil {
			return fmt.Errorf("kokoro 合成超时（>%ds）或被取消；引擎输出末段：%s", int(timeout.Seconds()), reason)
		}
		return fmt.Errorf("kokoro 合成失败：%v；引擎输出末段：%s", err, reason)
	}
	return nil
}

// Name 返回 Provider 名（用于 narrations 表记录 provider 字段）。
func (k *KokoroProvider) Name() string { return "kokoro" }

// DefaultVoice 返回默认音色标识。
func (k *KokoroProvider) DefaultVoice() string { return k.defaultVoice }

// WithSynthFunc 注入测试用合成函数（生产代码不用）。
func (k *KokoroProvider) WithSynthFunc(fn func(ctx context.Context, text, voice, outPath string) error) *KokoroProvider {
	return &KokoroProvider{
		binaryPath: k.binaryPath, defaultVoice: k.defaultVoice, model: k.model, language: k.language, synthFn: fn,
	}
}
