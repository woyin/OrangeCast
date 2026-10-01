package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func studyAnswerFixture(scope QuestionStudyScope) QuestionStudyAnswer {
	material := scope.Materials[0]
	return QuestionStudyAnswer{Version: QuestionStudyPromptVersion, State: "answered", SourceClaims: []QuestionStudyClaim{{Text: "需先核对条件。", Conditions: "在有可靠来源时", References: []QuestionStudyReference{{MaterialKey: material.Key, Revision: material.Revision, SegmentIDs: []string{material.Segments[0].SegmentID}}}}}}
}
func TestQuestionStudyProviderProtocolAndStages(t *testing.T) {
	scope := studyScopeFixture()
	answer := studyAnswerFixture(scope)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-sentinel" {
			t.Error("wrong POD transport")
		}
		var request struct {
			Model    string                 `json:"model"`
			Messages []QuestionStudyMessage `json:"messages"`
			Cap      int                    `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var content string
		var expected []QuestionStudyMessage
		if request.Model == "generate" {
			if request.Cap != 4096 {
				t.Error(request.Cap)
			}
			expected, _ = QuestionStudyMessages(scope)
			raw, _ := json.Marshal(answer)
			content = string(raw)
		} else {
			if request.Model != "review" || request.Cap != 2048 {
				t.Error(request.Model, request.Cap)
			}
			expected, _ = QuestionStudyReviewMessages(scope, answer)
			content = `{"version":"question-study-review-v1","verdict":"accept","reason":"来源和条件相符","checks":[{"key":"source:0","relevant":true,"supported":true,"conditions_preserved":true}]}`
		}
		actual, _ := json.Marshal(request.Messages)
		want, _ := json.Marshal(expected)
		if string(actual) != string(want) {
			t.Error("estimated messages differ from actual sent sequence")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []map[string]any{{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 31, "completion_tokens": 12}})
	}))
	defer server.Close()
	selector := NewSelector("unrelated", "unrelated").WithPod("secret-sentinel", server.URL+"/v1", "fallback")
	client, err := selector.QuestionStudy("generate", "review")
	if err != nil || calls.Load() != 0 || !client.Config().IndependentModel {
		t.Fatal(client, err)
	}
	safe, _ := json.Marshal(client.Config())
	if strings.Contains(string(safe), "secret-sentinel") || strings.Contains(string(safe), server.URL) {
		t.Fatal("unsafe frozen configuration")
	}
	response, err := client.Generate(context.Background(), scope)
	if err != nil || response.Failure != "" || !response.UsageKnown || response.InputUnits != 31 || response.OutputUnits != 12 {
		t.Fatal(response, err)
	}
	parsed, err := ParseQuestionStudyAnswer(scope, response.Content)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Review(context.Background(), scope, *parsed)
	if err != nil || response.Model != "review" {
		t.Fatal(response, err)
	}
	review, err := ParseQuestionStudyReview(*parsed, response.Content)
	if err != nil || review.Verdict != "accept" || calls.Load() != 2 {
		t.Fatal(review, err, calls.Load())
	}
	estimate, err := EstimateQuestionStudyReview(scope, *parsed)
	if err != nil || estimate.OutputTokens != 2048 || !estimate.Approximate || estimate.PriceKnown {
		t.Fatal(estimate, err)
	}
	shared, err := selector.QuestionStudy("", "")
	if err != nil || shared.Config().IndependentModel {
		t.Fatal("same model falsely called independent", err)
	}
}
func TestQuestionStudyProviderRejectsBadReferencesAndReview(t *testing.T) {
	scope := studyScopeFixture()
	for _, name := range []string{"unknown-key", "old-version", "invented-segment", "source-without-reference", "false-consensus", "unknown-field"} {
		t.Run(name, func(t *testing.T) {
			answer := studyAnswerFixture(scope)
			switch name {
			case "unknown-key":
				answer.SourceClaims[0].References[0].MaterialKey = "invented"
			case "old-version":
				answer.SourceClaims[0].References[0].Revision = 2
			case "invented-segment":
				answer.SourceClaims[0].References[0].SegmentIDs = []string{"invented"}
			case "source-without-reference":
				answer.SourceClaims[0].References = nil
			case "false-consensus":
				answer.Consensus = answer.SourceClaims
			}
			raw, _ := json.Marshal(answer)
			if name == "unknown-field" {
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"fake":true}`)
			}
			if _, err := ParseQuestionStudyAnswer(scope, string(raw)); err == nil {
				t.Fatal("bad answer accepted")
			}
		})
	}
	answer := studyAnswerFixture(scope)
	for _, raw := range []string{`{"version":"question-study-review-v1","verdict":"accept","reason":"通过","checks":[]}`, `{"version":"question-study-review-v1","verdict":"accept","reason":"通过","checks":[{"key":"source:0","relevant":true,"supported":false,"conditions_preserved":true}]}`, `{"version":"question-study-review-v1","verdict":"reject","reason":"拒绝","checks":[{"key":"invented","relevant":false}]}`} {
		if _, err := ParseQuestionStudyReview(answer, raw); err == nil {
			t.Fatal("bad review accepted")
		}
	}
	insufficient := QuestionStudyAnswer{Version: QuestionStudyPromptVersion, State: "insufficient", Reason: "材料没有回答该条件。"}
	if err := ValidateQuestionStudyAnswer(scope, insufficient); err != nil {
		t.Fatal("normal lack of answer rejected", err)
	}
}
func TestQuestionStudyProviderNoAutomaticRetryAndNumericFacts(t *testing.T) {
	for _, name := range []string{"http-error", "bad-json", "wrong-model", "missing-usage"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if name == "http-error" {
					http.Error(w, "secret-sentinel", 503)
					return
				}
				model := "generate"
				if name == "wrong-model" {
					model = "untrusted"
				}
				content := `{}`
				if name == "bad-json" {
					content = "secret-sentinel: invalid"
				}
				body := map[string]any{"model": model, "choices": []map[string]any{{"message": map[string]string{"content": content}}}}
				if name != "missing-usage" {
					body["usage"] = map[string]int{"prompt_tokens": 31, "completion_tokens": 12}
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			client, err := NewSelector("", "").WithPod("secret-sentinel", server.URL, "generate").QuestionStudy("", "")
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Generate(t.Context(), studyScopeFixture())
			if calls != 1 {
				t.Fatal("automatic paid retry", calls)
			}
			if name == "http-error" {
				if err == nil || strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), server.URL) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || response == nil {
				t.Fatal(response, err)
			}
			if name == "missing-usage" {
				if response.UsageKnown {
					t.Fatal("missing usage called free")
				}
			} else if !response.UsageKnown || response.InputUnits != 31 || response.OutputUnits != 12 {
				t.Fatal("paid units discarded", response)
			}
			if name == "wrong-model" && (!response.UnverifiedModel || response.Model != "unknown" || response.Failure == "") {
				t.Fatal(response)
			}
			if name == "bad-json" && (response.Content != "" || response.Failure == "") {
				t.Fatal(response)
			}
		})
	}
	if _, err := NewSelector("text-fallback", "text-fallback").QuestionStudy("", ""); err == nil {
		t.Fatal("new task silently routed to unrelated text connection")
	}
}

func TestQuestionStudyProviderPreservesOwnerIdentityAndDistinctConditions(t *testing.T) {
	scope := studyScopeFixture()
	owner := QuestionStudyMaterial{Key: "owner:one", Kind: "owner_reflection", SourceType: "document", SourceID: "one", Revision: 1, Content: "这是我的理解。"}
	owner.ContentHash = QuestionStudyMaterialHash(owner)
	scope.Materials = append(scope.Materials, owner)
	answer := QuestionStudyAnswer{Version: QuestionStudyPromptVersion, State: "answered", OwnerUnderstanding: []QuestionStudyOwnerReference{{MaterialKey: owner.Key, Revision: 1}}}
	if err := ValidateQuestionStudyAnswer(scope, answer); err != nil {
		t.Fatal(err)
	}
	answer.SourceClaims = []QuestionStudyClaim{{Text: "来源说这是我的理解", References: []QuestionStudyReference{{MaterialKey: owner.Key, Revision: 1}}}}
	if err := ValidateQuestionStudyAnswer(scope, answer); err == nil {
		t.Fatal("Owner reflection relabeled as source claim")
	}
	other := scope.Materials[0]
	other.Key = "document:two:paragraph"
	other.SourceID = "two"
	other.ContentHash = QuestionStudyMaterialHash(other)
	scope.Materials = append(scope.Materials, other)
	refs := []QuestionStudyReference{{MaterialKey: scope.Materials[0].Key, Revision: 1, SegmentIDs: []string{"paragraph"}}, {MaterialKey: other.Key, Revision: 1, SegmentIDs: []string{"paragraph"}}}
	answer = QuestionStudyAnswer{Version: QuestionStudyPromptVersion, State: "answered", Disagreements: []QuestionStudyClaim{{Text: "目的不同可能导致结论不同。", Conditions: "限定在不同任务目标中", References: refs}}}
	if err := ValidateQuestionStudyAnswer(scope, answer); err != nil {
		t.Fatal(err)
	}
	answer.Disagreements[0].Conditions = ""
	if err := ValidateQuestionStudyAnswer(scope, answer); err == nil {
		t.Fatal("conditions silently removed")
	}
}
