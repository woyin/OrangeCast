package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func legacyStudyFixture() LegacyStudyScope {
	return LegacyStudyScope{Question: "为什么学习", Segments: []Segment{{ID: "s1", Text: "学习需要实践", Start: 0, End: 10}}}
}
func TestLegacyStudyClientPreservesQAConnectionAndProtocol(t *testing.T) {
	for _, name := range []string{"openai", "groq"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer private-key" {
					t.Errorf("wrong QA route: %s", r.URL.Path)
				}
				var p struct {
					Model    string                 `json:"model"`
					Messages []QuestionStudyMessage `json:"messages"`
					Cap      int                    `json:"max_completion_tokens"`
				}
				if json.NewDecoder(r.Body).Decode(&p) != nil || p.Model != "qa-model" || len(p.Messages) != 2 || p.Cap == 0 {
					t.Errorf("invalid request %+v", p)
				}
				content := `{"answer":"学习需要实践","referenceSegmentIds":["s1"]}`
				if strings.Contains(p.Messages[0].Content, referenceCheckSystemPrompt) {
					content = `{"related":true,"reason":"主题一致"}`
				}
				json.NewEncoder(w).Encode(map[string]any{"model": "qa-model", "choices": []any{map[string]any{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7}})
			}))
			defer ts.Close()
			sel := NewSelector("private-key", "private-key")
			sel.ApplySettings("", ts.URL, "", ts.URL)
			c, err := sel.LegacyStudy(TaskConfig{Provider: name, Model: "qa-model"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := c.Config()
			if cfg.Provider != name || cfg.GenerationModel != "qa-model" || cfg.ReviewModel != "qa-model" || cfg.IndependentModel || len(cfg.ConnectionID) != 64 || strings.Contains(cfg.ConnectionID, "private-key") {
				t.Fatal(cfg)
			}
			scope := legacyStudyFixture()
			a, e := c.Generate(context.Background(), scope)
			if e != nil || a.Failure != "" || !a.UsageKnown || a.InputUnits != 11 || a.OutputUnits != 7 {
				t.Fatalf("%+v %v", a, e)
			}
			parsed, e := ParseLegacyStudyAnswer(scope, a.Content)
			if e != nil {
				t.Fatal(e)
			}
			review, e := c.Review(context.Background(), scope, *parsed.Answer)
			if e != nil {
				t.Fatal(e)
			}
			checked, e := ParseLegacyStudyReview(review.Content)
			if e != nil || !checked.Related || calls.Load() != 2 {
				t.Fatalf("%+v %v calls %d", checked, e, calls.Load())
			}
		})
	}
}
func TestLegacyStudyClientUnknownUsageMismatchAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		known      bool
		failure    bool
	}{{"missing usage", `{"model":"qa-model","choices":[{"message":{"content":"{}"}}]}`, 200, false, false}, {"mismatch", `{"model":"different","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`, 200, true, true}, {"partial usage", `{"model":"qa-model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":3}}`, 200, false, false}, {"invalid content", `{"model":"qa-model","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`, 200, true, true}, {"rate limit", `secret-body`, 429, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			s := NewSelector("key", "")
			s.ApplySettings("", ts.URL, "", "")
			c, e := s.LegacyStudy(TaskConfig{Model: "qa-model"})
			if e != nil {
				t.Fatal(e)
			}
			out, e := c.Generate(context.Background(), legacyStudyFixture())
			if tc.status != 200 {
				if e == nil || strings.Contains(e.Error(), "secret-body") {
					t.Fatal(e)
				}
			} else if e != nil || out.UsageKnown != tc.known || (out.Failure != "") != tc.failure {
				t.Fatalf("%+v %v", out, e)
			}
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
		})
	}
}
func TestLegacyStudyClientCancellation(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer ts.Close()
	s := NewSelector("key", "")
	s.ApplySettings("", ts.URL, "", "")
	c, _ := s.LegacyStudy(TaskConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Generate(ctx, legacyStudyFixture())
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("%v calls %d", err, calls.Load())
	}
}
func TestLegacyStudyFrozenSelectionAndEstimate(t *testing.T) {
	scope := legacyStudyFixture()
	for i := 0; i < 8; i++ {
		scope.History = append(scope.History, StudyChatMessage{Role: "user", Content: string(rune('A' + i))})
	}
	messages, e := LegacyStudyMessages(scope, "generate", nil)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(messages[1].Content, "user：A\n") || strings.Contains(messages[1].Content, "user：B\n") || !strings.Contains(messages[1].Content, "user：C\n") {
		t.Fatal(messages)
	}
	estimate, e := EstimateLegacyStudy(scope, "generate", nil)
	raw, _ := json.Marshal(messages)
	if e != nil || estimate.InputTokens != len(raw)+64 || estimate.OutputTokens != 4096 {
		t.Fatalf("%+v %v", estimate, e)
	}
	for _, content := range []string{`{"answer":"a","referenceSegmentIds":["unknown"]}`, `{"answer":"a","referenceSegmentIds":["s1","unknown"]}`, `{"answer":"a","referenceSegmentIds":["s1","s1"]}`, `{"answer":"a","referenceSegmentIds":["s1"],"extra":true}`} {
		if _, e := ParseLegacyStudyAnswer(scope, content); e == nil {
			t.Fatal(content)
		}
	}
	for _, content := range []string{`{"reason":"x"}`, `{"related":"true","reason":"x"}`, `{"related":true}`, `{"related":true,"reason":"x"} {}`} {
		if _, e := ParseLegacyStudyReview(content); e == nil {
			t.Fatal(content)
		}
	}
	answer := StudyChatMessage{Role: "assistant", Content: "x", ReferenceSegmentIDs: []string{"s1"}}
	review, e := EstimateLegacyStudy(scope, "review", &answer)
	if e != nil || review.OutputTokens != 2048 {
		t.Fatalf("%+v %v", review, e)
	}
	if _, e := LegacyStudyMessages(scope, "unknown", nil); e == nil {
		t.Fatal("unknown stage")
	}
	if _, e := LegacyStudyMessages(scope, "review", nil); e == nil {
		t.Fatal("missing answer")
	}
}
func TestLegacyStudyConnectionValidation(t *testing.T) {
	s := NewSelector("key", "key")
	for _, tc := range []TaskConfig{{Provider: "pod"}, {Provider: "unknown"}, {Model: "\n"}, {Transcription: true}} {
		if _, e := s.LegacyStudy(tc); e == nil {
			t.Fatal(tc)
		}
	}
	s.ApplySettings("", "https://user:pass@example.test", "", "")
	if _, e := s.LegacyStudy(TaskConfig{}); e == nil {
		t.Fatal("credential URL")
	}
	a, _ := NewSelector("key", "").LegacyStudy(TaskConfig{})
	b, _ := NewSelector("other-key", "").LegacyStudy(TaskConfig{})
	if a.Config().GenerationModel != groqAnalysisModel || a.Config().ConnectionID != b.Config().ConnectionID {
		t.Fatal("default model or identity")
	}
	o, _ := NewSelector("", "key").LegacyStudy(TaskConfig{Provider: "openai"})
	if o.Config().GenerationModel != openaiAnalysisModel {
		t.Fatal(o.Config())
	}
}

func TestLegacyStudyClientInFlightCancellationDoesNotResend(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(started); <-release }))
	defer ts.Close()
	s := NewSelector("key", "")
	s.ApplySettings("", ts.URL, "", "")
	c, _ := s.LegacyStudy(TaskConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Generate(ctx, legacyStudyFixture()); done <- e }()
	<-started
	cancel()
	err := <-done
	close(release)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("%v calls %d", err, calls.Load())
	}
}
