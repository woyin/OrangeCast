package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLearningReviewBoundedGroundingContract(t *testing.T) {
	req := KnowledgeArticleRequest{Stage: "weekly_review", Materials: []KnowledgeMaterial{{ID: "real", Kind: "owner_reflection"}}}
	q := LearningReviewQuestion{Question: "为什么我的理解变化？", AnswerBasis: "个人解释前后有变化，不能推为来源事实。", MaterialIDs: []string{"real"}}
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Questions: []LearningReviewQuestion{q}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []LearningReviewQuestion{{Question: "", AnswerBasis: "x", MaterialIDs: q.MaterialIDs}, {Question: "x", AnswerBasis: "", MaterialIDs: q.MaterialIDs}, {Question: "x", AnswerBasis: "x"}, {Question: "x", AnswerBasis: "x", MaterialIDs: []string{"fake"}}, {Question: "x", AnswerBasis: "x", MaterialIDs: []string{"real", "real"}}, {Question: strings.Repeat("字", 2001), AnswerBasis: "x", MaterialIDs: q.MaterialIDs}, {Question: "x", AnswerBasis: strings.Repeat("字", 4001), MaterialIDs: q.MaterialIDs}} {
		if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Questions: []LearningReviewQuestion{bad}}) == nil {
			t.Fatal("invalid question admitted")
		}
	}
	if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Questions: []LearningReviewQuestion{q, q}}) == nil {
		t.Fatal("duplicate questions")
	}
	if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Questions: make([]LearningReviewQuestion, 6)}) == nil {
		t.Fatal("over limit")
	}
	if ValidateKnowledgeResult(req, &KnowledgeArticleResult{}) == nil {
		t.Fatal("unexplained empty batch")
	}
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Reason: "材料不足"}); err != nil {
		t.Fatal(err)
	}
}
func TestLearningReviewPODTransportInstructions(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[0].Content, "不评价用户掌握度") {
			t.Error("weekly contract missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"questions":[],"reason":"材料不足"}`}}}})
	}))
	defer service.Close()
	p := NewOpenAIProvider("test").WithBaseURL(service.URL)
	result, _, err := p.KnowledgeArticleStep(context.Background(), KnowledgeArticleRequest{Stage: "weekly_review", PromptVersion: KnowledgeArticlePromptVersion})
	if err != nil || result.Reason != "材料不足" {
		t.Fatal(err)
	}
}
