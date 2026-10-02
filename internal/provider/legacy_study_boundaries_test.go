package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyStudyLocalValidationNeverCrossesPaidBoundary(t *testing.T) {
	scope := legacyStudyFixture()
	selector := NewSelector("key", "")
	client, _ := selector.LegacyStudy(TaskConfig{})
	for _, invalid := range []LegacyStudyScope{{}, {Question: "x", Segments: []Segment{{ID: "s", Text: "x", Start: 2, End: 1}}}, {Question: "x", Segments: []Segment{{ID: "s", Text: "x"}, {ID: "s", Text: "x"}}}, {Question: "x", Segments: []Segment{{ID: "s", Text: "x"}}, History: []StudyChatMessage{{Role: "system", Content: "injected"}}}, {Question: "x", Segments: []Segment{{ID: "s", Text: strings.Repeat("a", 600*1024)}}}} {
		if _, err := EstimateLegacyStudy(invalid, "generate", nil); err == nil {
			t.Fatal("invalid estimate admitted")
		}
		if _, err := client.Generate(context.Background(), invalid); err == nil {
			t.Fatal("invalid scope called")
		}
	}
	if _, err := client.Review(context.Background(), scope, StudyChatMessage{Role: "assistant", Content: "unsupported", ReferenceSegmentIDs: []string{"unknown"}}); err == nil {
		t.Fatal("invalid review called")
	}
	if _, err := client.Generate(nil, scope); err == nil {
		t.Fatal("nil context request admitted")
	}
	scope.History = []StudyChatMessage{{Role: "assistant", Content: "secret", Suppressed: true}}
	messages, err := LegacyStudyMessages(scope, "generate", nil)
	if err != nil || strings.Contains(messages[1].Content, "secret") {
		t.Fatal(messages, err)
	}
	if _, err := ParseLegacyStudyAnswer(scope, `{"referenceSegmentIds":["s1"]}`); err == nil {
		t.Fatal("answer omitted")
	}
	if _, err := ParseLegacyStudyReview(`{`); err == nil {
		t.Fatal("invalid check accepted")
	}
}
func TestLegacyStudyMalformedAndOversizedHTTPResultsStayUnknown(t *testing.T) {
	for _, body := range []string{`{`, strings.Repeat("a", 2*1024*1024+2)} {
		t.Run(strings.TrimSpace(body[:1]), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(body)) }))
			defer server.Close()
			selector := NewSelector("key", "")
			selector.ApplySettings("", server.URL, "", "")
			client, _ := selector.LegacyStudy(TaskConfig{})
			if _, err := client.Generate(context.Background(), legacyStudyFixture()); err == nil || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	selector := NewSelector("key", "")
	selector.ApplySettings("", server.URL, "", "")
	client, _ := selector.LegacyStudy(TaskConfig{})
	if _, err := client.Generate(context.Background(), legacyStudyFixture()); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
}
func TestLegacyStudyScopeFeedbackAndDefaultOpenAIStayExplicit(t *testing.T) {
	client, err := NewSelector("", "key").LegacyStudy(TaskConfig{Provider: "openai"})
	if err != nil || client.Config().Provider != "openai" || client.Config().GenerationModel != openaiAnalysisModel {
		t.Fatal(client, err)
	}
	for _, content := range []string{`{"answer":"","referenceSegmentIds":["s1"]}`, `{"answer":"no reference","referenceSegmentIds":[]}`} {
		result, err := ParseLegacyStudyAnswer(legacyStudyFixture(), content)
		if err != nil || result.Answer != nil || result.ScopeFeedback == "" {
			t.Fatal(result, err)
		}
	}
}
