package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKokoroProvider_SynthesizePrependsOpeningLabel (ADR-0019 听觉分级)
// Synthesize 必须在文本前加固定开场白"AI 解说："，让 Owner 一耳朵可辨这是 AI 串场。
func TestKokoroProvider_SynthesizePrependsOpeningLabel(t *testing.T) {
	var capturedText string
	k := NewKokoroProvider("kokoro", "af_heart", "").
		WithSynthFunc(func(text, voice, outPath string) error {
			capturedText = text
			// 写一个占位 wav 让 outPath 存在
			return os.WriteFile(outPath, []byte("fake-wav"), 0o644)
		})
	dir := t.TempDir()
	out := filepath.Join(dir, "n1.wav")
	res, err := k.Synthesize("通胀就是钱越来越不值钱", "", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(capturedText, "AI 解说：") {
		t.Errorf("合成文本应以'AI 解解：'开头，实际：%q", capturedText)
	}
	if res.CharCount == 0 {
		t.Error("应返回非零字符数")
	}
	if res.Voice != "af_heart" {
		t.Errorf("默认音色应为 af_heart，实际 %s", res.Voice)
	}
}

// TestKokoroProvider_AvailableWithoutBinary (ADR-0019 R1)
// 未注入 synthFn 且 kokoro 二进制不在 PATH 时，Available() 应返回 false。
func TestKokoroProvider_AvailableWithoutBinary(t *testing.T) {
	k := NewKokoroProvider("definitely-not-a-real-binary-xyz", "af_heart", "")
	if k.Available() {
		t.Error("不存在的二进制应 Available()==false")
	}
	// 注入 synthFn 后应视为可用。
	k2 := k.WithSynthFunc(func(text, voice, outPath string) error { return nil })
	if !k2.Available() {
		t.Error("注入 synthFn 后应 Available()==true")
	}
}

// TestKokoroProvider_SynthesizeEmptyTextRejected
func TestKokoroProvider_SynthesizeEmptyTextRejected(t *testing.T) {
	k := NewKokoroProvider("kokoro", "af_heart", "").
		WithSynthFunc(func(text, voice, outPath string) error { return nil })
	if _, err := k.Synthesize("   ", "", "/tmp/x.wav"); err == nil {
		t.Error("空文本应报错")
	}
}

// TestKokoroProvider_Getters 验证 Name/DefaultVoice 返回配置值。
func TestKokoroProvider_Getters(t *testing.T) {
	k := NewKokoroProvider("kokoro", "af_heart", "model.bin")
	if k.Name() != "kokoro" {
		t.Errorf("Name() = %q", k.Name())
	}
	if k.DefaultVoice() != "af_heart" {
		t.Errorf("DefaultVoice() = %q", k.DefaultVoice())
	}
}
