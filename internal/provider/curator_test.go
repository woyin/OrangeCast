package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateCuratorResultRejectsUnapprovedMaterial(t *testing.T) {
	request := CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1"}}}
	valid := &CuratorResult{Thesis: "论点", Outline: "# 结构", SelectedKeyPointIDs: []string{"kp-1"}}
	if _, err := validateCuratorResult(valid, request); err != nil {
		t.Fatalf("authorized brief should validate: %v", err)
	}
	invalid := &CuratorResult{Thesis: "论点", Outline: "# 结构", SelectedKeyPointIDs: []string{"invented"}}
	if _, err := validateCuratorResult(invalid, request); err == nil {
		t.Fatal("Curator must not introduce material outside the accepted proposal")
	}
}

func TestCuratorInputRequiresProposalAndMaterials(t *testing.T) {
	if _, err := curatorInput(CuratorRequest{Title: "选题"}); err == nil {
		t.Fatal("curator input must require candidate materials")
	}
	if _, err := curatorInput(CuratorRequest{Materials: []ArticleMaterial{{KeyPointID: "kp-1"}}}); err == nil {
		t.Fatal("curator input must require a proposal title")
	}
	input, err := curatorInput(CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1", Content: "观点"}}})
	if err != nil || !strings.Contains(input, "kp-1") || !strings.Contains(input, "已接受提案") {
		t.Fatalf("curator input should embed proposal and materials: %q err=%v", input, err)
	}
}

func TestGroqCurateProducesValidatedBrief(t *testing.T) {
	var received string
	curator := NewGroqProvider("key")
	curator.chatCompleteFn = func(messages []map[string]string, jsonMode string) (string, int, error) {
		received = messages[1]["content"]
		if jsonMode != "object" {
			t.Fatalf("curator should request JSON object mode: %q", jsonMode)
		}
		return `{"thesis":"论点","audience":"读者","outline":"# 结构","selectedKeyPointIds":["kp-1"],"rejectedKeyPointIds":["kp-2"],"conflictPlan":["并列呈现"],"style":"清晰","targetLength":2000}`, 200, nil
	}
	request := CuratorRequest{Title: "选题", Thesis: "原始论点", Audience: "读者", Voice: "清晰", Materials: []ArticleMaterial{{KeyPointID: "kp-1", Content: "观点一"}, {KeyPointID: "kp-2", Content: "观点二"}}}
	result, err := curator.Curate(context.Background(), request)
	if err != nil || result.Thesis != "论点" || len(result.SelectedKeyPointIDs) != 1 || len(result.RejectedKeyPointIDs) != 1 {
		t.Fatalf("Groq curator should return validated brief: result=%+v err=%v", result, err)
	}
	if !strings.Contains(received, "kp-1") || !strings.Contains(received, "选题") {
		t.Fatalf("curator prompt must carry proposal and materials: %q", received)
	}
}

func TestGroqCurateRejectsInvalidOutput(t *testing.T) {
	request := CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1"}}}
	for _, response := range []string{"not-json", `{"thesis":"论点","outline":"# 结构","selectedKeyPointIds":[]}`, `{"thesis":"论点","outline":"# 结构","selectedKeyPointIds":["kp-1","kp-2"]}`} {
		curator := NewGroqProvider("key")
		curator.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return response, 200, nil }
		if _, err := curator.Curate(context.Background(), request); err == nil {
			t.Fatalf("invalid Groq curator output should fail: %q", response)
		}
	}
	failing := NewGroqProvider("key")
	failing.chatCompleteFn = func([]map[string]string, string) (string, int, error) { return "", 500, context.Canceled }
	if _, err := failing.Curate(context.Background(), request); err == nil {
		t.Fatal("provider failure should surface from curator")
	}
}

func TestOpenAICurateParsesChatOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("expected chat/completions endpoint, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"thesis\":\"论点\",\"audience\":\"读者\",\"outline\":\"# 结构\",\"selectedKeyPointIds\":[\"kp-1\"],\"conflictPlan\":[],\"style\":\"清晰\"}"}}]}`))
	}))
	defer server.Close()
	request := CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1", Content: "观点"}}}
	result, err := NewOpenAIProvider("key").WithBaseURL(server.URL).Curate(context.Background(), request)
	if err != nil || result.Thesis != "论点" || len(result.SelectedKeyPointIDs) != 1 {
		t.Fatalf("OpenAI curator should parse output: result=%+v err=%v", result, err)
	}
}

func TestOpenAICurateSurfacesFailures(t *testing.T) {
	request := CuratorRequest{Title: "选题", Materials: []ArticleMaterial{{KeyPointID: "kp-1"}}}
	for _, response := range []struct {
		status int
		body   string
	}{{http.StatusInternalServerError, `failure`}, {http.StatusOK, `not-json`}, {http.StatusOK, `{"output_text":"not-json"}`}, {http.StatusOK, `{"output_text":"{\"thesis\":\"论点\",\"outline\":\"# 结构\",\"selectedKeyPointIds\":[\"ghost\"]}"}`}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(response.status)
			_, _ = w.Write([]byte(response.body))
		}))
		_, err := NewOpenAIProvider("key").WithBaseURL(server.URL).Curate(context.Background(), request)
		server.Close()
		if err == nil {
			t.Fatalf("OpenAI curator should surface invalid response: %+v", response)
		}
	}
}
