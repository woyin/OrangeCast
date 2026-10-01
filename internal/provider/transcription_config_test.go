package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptionTaskModelAndFormatReachWire(t *testing.T) {
	for _, tc := range []struct{ provider, model, format string }{{"openai", "gpt-4o-mini-transcribe", "json"}, {"openai", "whisper-1", "verbose_json"}, {"groq", "whisper-large-v3-turbo", "verbose_json"}} {
		t.Run(tc.provider+tc.model, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				if r.FormValue("model") != tc.model || r.FormValue("response_format") != tc.format {
					t.Errorf("route=%s format=%s", r.FormValue("model"), r.FormValue("response_format"))
				}
				if tc.format == "json" && r.FormValue("timestamp_granularities[]") != "" {
					t.Error("unsupported timestamps sent")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"text":"测试","usage":{"input_tokens":12,"output_tokens":3}}`))
			}))
			defer srv.Close()
			sel := NewSelector("fixture", "fixture")
			sel.ApplySettings("", srv.URL, "", srv.URL)
			b, err := sel.BundleForTask(TaskConfig{Provider: tc.provider, Model: tc.model, Transcription: true})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "clip.wav")
			os.WriteFile(path, []byte("test"), 0600)
			res, err := b.Transcription.Transcribe(path)
			if err != nil || res.Text != "测试" || res.Model != "" || res.Usage.InputUnits != 12 {
				t.Fatalf("%+v %v", res, err)
			}
			if p, ok := b.Analysis.(*OpenAIProvider); ok && p.analysisModel != "" {
				t.Error("ASR model contaminated chat route")
			}
			if p, ok := b.Analysis.(*GroqProvider); ok && p.model != "" {
				t.Error("ASR model contaminated chat route")
			}
		})
	}
}

func TestTranscriptionContextCanceledBeforeRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.wav")
	os.WriteFile(path, []byte("test"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, p := range []interface {
		TranscribeContext(context.Context, string) (*TranscriptResult, error)
	}{NewOpenAIProvider("fixture").WithBaseURL("http://127.0.0.1:1"), NewGroqProvider("fixture").WithBaseURL("http://127.0.0.1:1")} {
		if _, err := p.TranscribeContext(ctx, path); err == nil {
			t.Error("canceled ASR should fail")
		}
	}
}
