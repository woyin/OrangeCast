package main

import (
	"context"
	"encoding/json"
	"github.com/woyin/orangecast/internal/provider"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQualityClientOneAttemptReceiptAndFrozenModel(t *testing.T) {
	calls := 0
	mode := "failed"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "exact-model" {
			t.Error(body.Model)
		}
		if r.Header.Get("Authorization") != "Bearer secret-sentinel" {
			t.Error("credential")
		}
		if mode == "failed" {
			w.WriteHeader(503)
			w.Write([]byte(`{"model":"exact-model","usage":{"prompt_tokens":9,"completion_tokens":2},"error":"secret-sentinel"}`))
			return
		}
		w.Write([]byte(`{"model":"different-model","usage":{"prompt_tokens":12,"completion_tokens":4},"choices":[{"message":{"content":"{\"title\":\"自建\",\"blocks\":[]}"}}]}`))
	}))
	defer server.Close()
	p := &podClient{apiKey: "secret-sentinel", baseURL: server.URL, model: "fallback"}
	request := provider.KnowledgeArticleRequest{Stage: "write", PromptVersion: provider.KnowledgeArticlePromptVersion, StageConfigs: provider.FreezeKnowledgeStageConfigs(map[string]string{"write": "exact-model"})}
	_, usage, err := p.KnowledgeArticleStep(context.Background(), request)
	if err == nil || calls != 1 || usage.InputUnits != 9 || !p.QualityReceiptUsageKnown() || strings.Contains(err.Error(), "secret-sentinel") {
		t.Fatalf("receipt %+v %v calls%d", usage, err, calls)
	}
	mode = "mismatch"
	output, usage, err := p.KnowledgeArticleStep(context.Background(), request)
	if err == nil || calls != 2 || usage.InputUnits != 12 || output == nil || p.QualityReceiptModel() != "different-model" {
		t.Fatalf("mismatch %+v %v", usage, err)
	}
}
func TestQualityFrozenFileRejectsOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := frozenWrite(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := frozenWrite(path, []byte("second")); err == nil {
		t.Fatal("overwrote frozen manifest")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "first" {
		t.Fatal(string(raw))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

func TestQualityClientValidationAndTransportFailures(t *testing.T) {
	request := provider.KnowledgeArticleRequest{Stage: "write", PromptVersion: provider.KnowledgeArticlePromptVersion}
	p := &podClient{baseURL: "http://unused", model: "model"}
	badPrompt := request
	badPrompt.PromptVersion = "unsupported"
	if _, _, err := p.KnowledgeArticleStep(t.Context(), badPrompt); err == nil {
		t.Fatal("unknown prompt sent")
	}
	badStage := request
	badStage.Stage = "invalid"
	if _, _, err := p.KnowledgeArticleStep(t.Context(), badStage); err == nil {
		t.Fatal("invalid configuration sent")
	}
	p.baseURL = ":invalid-url"
	if _, _, err := p.KnowledgeArticleStep(t.Context(), request); err == nil {
		t.Fatal("invalid URL accepted")
	}
	p.baseURL = "http://localhost"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := p.KnowledgeArticleStep(ctx, request); err == nil || err.Error() != "remote outcome unknown" {
		t.Fatal("unknown outcome", err)
	}
}
func TestQualityClientBoundedEnvelopeAndContent(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		usageKnown       bool
	}{
		{"invalid envelope", "not-json", "response envelope invalid", false},
		{"empty choices", `{"model":"model","choices":[]}`, "one output required", false},
		{"invalid content", `{"model":"model","choices":[{"message":{"content":"not-json"}}]}`, "invalid output JSON", false},
		{"missing output usage", `{"model":"model","usage":{"prompt_tokens":7},"choices":[{"message":{"content":"{}"}}]}`, "", false},
		{"negative usage", `{"model":"model","usage":{"prompt_tokens":-1,"completion_tokens":2},"choices":[{"message":{"content":"{}"}}]}`, "", false},
		{"fenced zero usage", `{"model":"model","usage":{"prompt_tokens":0,"completion_tokens":0},"choices":[{"message":{"content":"` + "```json\\n{}\\n```" + `"}}]}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(tc.body)) }))
			defer remote.Close()
			p := &podClient{baseURL: remote.URL, model: "model"}
			result, _, err := p.KnowledgeArticleStep(t.Context(), provider.KnowledgeArticleRequest{Stage: "write", PromptVersion: provider.KnowledgeArticlePromptVersion})
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error %v want%s", err, tc.want)
				}
			} else if err != nil || result == nil {
				t.Fatalf("output %v err%v body%s", result, err, tc.body)
			}
			if calls != 1 || p.QualityReceiptUsageKnown() != tc.usageKnown {
				t.Fatalf("calls%d usageknown%v", calls, p.QualityReceiptUsageKnown())
			}
		})
	}
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "truncated"}[short], func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if short {
					w.Header().Set("Content-Length", "100")
					w.Write([]byte("short"))
					return
				}
				w.Write([]byte(strings.Repeat("x", 4*1024*1024+1)))
			}))
			defer remote.Close()
			p := &podClient{baseURL: remote.URL, model: "model"}
			if _, _, err := p.KnowledgeArticleStep(t.Context(), provider.KnowledgeArticleRequest{Stage: "write", PromptVersion: provider.KnowledgeArticlePromptVersion}); err == nil || err.Error() != "bounded response unreadable" {
				t.Fatal(err)
			}
		})
	}
}

func TestQualityClientDoesNotRedirectPaidRequestOrCredential(t *testing.T) {
	redirected := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected++
		t.Error("redirect leaked paid request or credential")
	}))
	defer destination.Close()
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		w.Write([]byte(`{}`))
	}))
	defer remote.Close()
	p := &podClient{apiKey: "private-redirect-key", baseURL: remote.URL, model: "model"}
	_, _, err := p.KnowledgeArticleStep(t.Context(), provider.KnowledgeArticleRequest{Stage: "write", PromptVersion: provider.KnowledgeArticlePromptVersion})
	if err == nil || err.Error() != "remote HTTP 307" || calls != 1 || redirected != 0 {
		t.Fatalf("redirect outcome %v calls%d redirected%d", err, calls, redirected)
	}
}
