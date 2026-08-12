package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGroqWriterGeneratesValidatedEvidenceMappedDraft(t *testing.T) {
	var received string
	writer := NewGroqProvider("key")
	writer.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		received = messages[1]["content"]
		if jsonMode != "object" {
			t.Fatalf("writer should request JSON object mode: %q", jsonMode)
		}
		return `{"title":"审查成本","markdown":"# 审查成本\n效率需要审查。","evidenceMaps":[{"kind":"paraphrased","excerpt":"效率需要审查。","keyPointIds":["kp-1"]}]}`, 200, nil
	}
	result, err := writer.WriteArticle(ArticleWritingRequest{Thesis: "效率需要审查", Outline: "# 结构", Materials: []ArticleMaterial{{KeyPointID: "kp-1", Content: "效率需要审查", Citations: []string{"seg-1"}}}})
	if err != nil || result.Title != "审查成本" || len(result.EvidenceMaps) != 1 {
		t.Fatalf("writer result should validate: result=%+v err=%v", result, err)
	}
	if !strings.Contains(received, "kp-1") || !strings.Contains(received, "效率需要审查") {
		t.Fatalf("writer must receive selected material only: %q", received)
	}
}

func TestArticleWriterRejectsEmptyOrUnapprovedEvidence(t *testing.T) {
	if _, err := articleWriterInput(ArticleWritingRequest{Thesis: "x", Outline: "# x"}); err == nil {
		t.Fatal("writer input must require selected material")
	}
	_, err := validateArticleWritingResult(&ArticleWritingResult{Markdown: "正文", EvidenceMaps: []ArticleEvidence{{Kind: "paraphrased", Excerpt: "正文", KeyPointIDs: []string{"not-selected"}}}}, []ArticleMaterial{{KeyPointID: "kp-1"}})
	if err == nil {
		t.Fatal("writer must reject evidence map that cites an unselected KeyPoint")
	}
}

func TestGroqScoutValidatesCrossSourceKeyPointProposal(t *testing.T) {
	scout := NewGroqProvider("key")
	scout.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		if jsonMode != "object" || !strings.Contains(messages[1]["content"], "theme-1") {
			t.Fatalf("Scout should use JSON object mode and receive themes: %+v", messages)
		}
		return `{"proposals":[{"kind":"evergreen","title":"跨集选题","thesis":"论点","audience":"读者","rationale":"价值","candidateKeyPointIds":["kp-1","kp-2"]}]}`, 200, nil
	}
	request := ScoutRequest{Themes: []ScoutTheme{{ID: "theme-1", Materials: []ArticleMaterial{{KeyPointID: "kp-1", SourceID: "source-1"}, {KeyPointID: "kp-2", SourceID: "source-2"}}}}}
	result, err := scout.Scout(request)
	if err != nil || len(result.Proposals) != 1 {
		t.Fatalf("Scout output should validate: result=%+v err=%v", result, err)
	}
	_, err = validateScoutResult(&ScoutResult{Proposals: []ScoutProposal{{Kind: "fresh", Title: "单集", CandidateKeyPointIDs: []string{"kp-1"}}}}, request)
	if err == nil {
		t.Fatal("Scout proposal must require multiple sources")
	}
}

func TestScoutRejectsEmptyAndInvalidModelOutput(t *testing.T) {
	if _, err := scoutInput(ScoutRequest{}); err == nil {
		t.Fatal("Scout input must require a confirmed theme")
	}
	request := ScoutRequest{Themes: []ScoutTheme{{Materials: []ArticleMaterial{{KeyPointID: "kp-1", SourceID: "source-1"}, {KeyPointID: "kp-2", SourceID: "source-2"}}}}}
	for _, result := range []*ScoutResult{
		nil,
		{Proposals: nil},
		{Proposals: []ScoutProposal{{Kind: "wrong", Title: "x", CandidateKeyPointIDs: []string{"kp-1", "kp-2"}}}},
		{Proposals: []ScoutProposal{{Kind: "fresh", Title: "x", CandidateKeyPointIDs: []string{"missing", "kp-2"}}}},
		{Proposals: []ScoutProposal{{Kind: "fresh", Title: "重复", CandidateKeyPointIDs: []string{"kp-1", "kp-2"}}, {Kind: "fresh", Title: "重复", CandidateKeyPointIDs: []string{"kp-1", "kp-2"}}}},
	} {
		if _, err := validateScoutResult(result, request); err == nil {
			t.Fatalf("invalid Scout result should be rejected: %+v", result)
		}
	}
	for _, response := range []string{"not-json", `{"proposals":[]}`} {
		scout := NewGroqProvider("key")
		scout.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return response, 200, nil }
		if _, err := scout.Scout(request); err == nil {
			t.Fatalf("invalid Groq Scout response should fail: %q", response)
		}
	}
	scout := NewGroqProvider("key")
	scout.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return "", 500, fmt.Errorf("provider failed") }
	if _, err := scout.Scout(request); err == nil {
		t.Fatal("provider failure should surface")
	}
}

func TestOpenAIScoutParsesResponsesOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("expected Responses endpoint, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"{\"proposals\":[{\"kind\":\"fresh\",\"title\":\"选题\",\"thesis\":\"论点\",\"candidateKeyPointIds\":[\"kp-1\",\"kp-2\"]}]}"}`))
	}))
	defer server.Close()
	provider := NewOpenAIProvider("key").WithBaseURL(server.URL)
	request := ScoutRequest{Themes: []ScoutTheme{{Materials: []ArticleMaterial{{KeyPointID: "kp-1", SourceID: "source-1"}, {KeyPointID: "kp-2", SourceID: "source-2"}}}}}
	result, err := provider.Scout(request)
	if err != nil || len(result.Proposals) != 1 || result.Proposals[0].Title != "选题" {
		t.Fatalf("OpenAI Scout should parse output: result=%+v err=%v", result, err)
	}
}

func TestOpenAIScoutSurfacesResponsesFailures(t *testing.T) {
	request := ScoutRequest{Themes: []ScoutTheme{{Materials: []ArticleMaterial{{KeyPointID: "kp-1", SourceID: "source-1"}, {KeyPointID: "kp-2", SourceID: "source-2"}}}}}
	for _, response := range []struct {
		status int
		body   string
	}{{http.StatusInternalServerError, `failure`}, {http.StatusOK, `not-json`}, {http.StatusOK, `{"output_text":"not-json"}`}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(response.status)
			_, _ = w.Write([]byte(response.body))
		}))
		_, err := NewOpenAIProvider("key").WithBaseURL(server.URL).Scout(request)
		server.Close()
		if err == nil {
			t.Fatalf("OpenAI Scout should surface invalid response: %+v", response)
		}
	}
}
