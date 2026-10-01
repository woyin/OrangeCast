package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVoiceConfigurationRejectsTextRoutesAndPartialCredentials(t *testing.T) {
	t.Setenv("SESSION_SECRET", "test-session-secret-at-least-32chars")
	for _, tc := range []struct {
		name, provider, model, url, key string
		valid                           bool
	}{{"default", "", "", "", "", true}, {"openai", "openai", "gpt-4o-mini-transcribe", "", "", true}, {"custom", "groq", "whisper-large-v3", "https://audio.example.test/v1", "fixture", true}, {"pod-text", "pod", "text", "", "", false}, {"partial", "groq", "whisper-large-v3", "https://audio.example.test/v1", "", false}, {"missing-model", "openai", "", "https://audio.example.test/v1", "fixture", false}, {"url-secret", "openai", "asr", "https://user:secret@example.test/v1", "fixture", false}, {"url-query", "openai", "asr", "https://example.test/v1?key=secret", "fixture", false}, {"url-invalid", "openai", "asr", "://invalid", "fixture", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VOICE_ASR_PROVIDER", tc.provider)
			t.Setenv("VOICE_ASR_MODEL", tc.model)
			t.Setenv("VOICE_ASR_BASE_URL", tc.url)
			t.Setenv("VOICE_ASR_API_KEY", tc.key)
			cfg, err := Load()
			if (err == nil) != tc.valid {
				t.Fatal(cfg, err)
			}
			if cfg != nil && cfg.VoiceDir != filepath.Join(cfg.DataDir, "voice-notes") {
				t.Fatal(cfg.VoiceDir)
			}
		})
	}
}
func TestVoiceDirectoryHasPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{DataDir: dir, VoiceDir: filepath.Join(dir, "voice")}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfg.VoiceDir)
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Fatal(fi, err)
	}
}
