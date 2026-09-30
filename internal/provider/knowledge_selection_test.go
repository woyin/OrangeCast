package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeSelectionEvidenceAndNovelty(t *testing.T) {
	mats := []KnowledgeMaterial{{ID: "a", Kind: "source_note", Evidence: "[s1] 原文甲", Citations: []string{"s1"}}, {ID: "b", Kind: "owner_reflection"}, {ID: "c", Kind: "source_note", Evidence: "[s2] 反方", Citations: []string{"s2"}}}
	topic := KnowledgeTopic{Title: "怎样让知识记得更久", Question: "怎样让知识记得更久", Thesis: "主动回忆并检查依据", Outline: "先回忆再核对", Score: 90, Sufficient: true, MaterialIDs: []string{"a", "b"}, Selection: []KnowledgeSelection{{MaterialID: "a", Role: "support", Selected: true, Reason: "支持来源"}, {MaterialID: "b", Role: "complement", Selected: true, Reason: "个人实践"}, {MaterialID: "c", Role: "opposition", Selected: false, Reason: "适用条件不同，需另一个问题"}}}
	req := KnowledgeArticleRequest{Stage: "select", DiscoveryBatchID: "batch", Materials: mats}
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Topics: []KnowledgeTopic{topic}}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*KnowledgeTopic){func(t *KnowledgeTopic) { t.Selection = nil }, func(t *KnowledgeTopic) { t.Selection[0].Selected = false }, func(t *KnowledgeTopic) { t.Selection[2] = t.Selection[0] }, func(t *KnowledgeTopic) { t.Selection[0].Reason = "" }, func(t *KnowledgeTopic) { t.Selection[0].Role = "invented" }, func(t *KnowledgeTopic) { t.Selection[2].MaterialID = "invented" }, func(t *KnowledgeTopic) { t.Audience = strings.Repeat("字", 501) }, func(t *KnowledgeTopic) { t.Increment = strings.Repeat("字", 1001) }} {
		copy := topic
		copy.Selection = append([]KnowledgeSelection(nil), topic.Selection...)
		change(&copy)
		if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Topics: []KnowledgeTopic{copy}}) == nil {
			t.Fatal("bad selection admitted")
		}
	}
	if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Topics: []KnowledgeTopic{topic, topic}}) == nil {
		t.Fatal("multiple selections admitted")
	}
	previous := topic
	previous.ArticleID = "old"
	previous.Title = "如何牢牢记住知识"
	previous.Question = previous.Title
	if !KnowledgeTopicDuplicate(topic, previous) {
		t.Fatal("same question escaped by title synonym")
	}
	continuation := topic
	continuation.Increment = "新材料明确另一适用条件"
	continuation.FollowUpID = "old"
	continuation.MaterialIDs = []string{"a", "c"}
	continuation.Selection = append([]KnowledgeSelection(nil), topic.Selection...)
	continuation.Selection[1].Selected = false
	continuation.Selection[2].Selected = true
	if KnowledgeTopicDuplicate(continuation, previous) {
		t.Fatal("new evidence followup blocked")
	}
	req.History = []KnowledgeTopic{previous}
	if err := ValidateKnowledgeResult(req, &KnowledgeArticleResult{Topics: []KnowledgeTopic{continuation}}); err != nil {
		t.Fatal(err)
	}
	continuation.FollowUpID = "fake"
	if ValidateKnowledgeResult(req, &KnowledgeArticleResult{Topics: []KnowledgeTopic{continuation}}) == nil {
		t.Fatal("invented old article")
	}
	continuation.FollowUpID = "old"
	continuation.MaterialIDs = previous.MaterialIDs
	if !KnowledgeTopicDuplicate(continuation, previous) {
		t.Fatal("fake increment without new material")
	}
	distinct := topic
	distinct.Audience = "数学教师"
	previous.Audience = "小学学生"
	if KnowledgeTopicDuplicate(distinct, previous) {
		t.Fatal("different explicit audience")
	}
	different := topic
	different.Question = "怎样安排睡眠？"
	different.Thesis = "先准备固定作息"
	different.MaterialIDs = []string{"c"}
	if KnowledgeTopicDuplicate(different, previous) {
		t.Fatal("same title used to reject distinct supported question")
	}
}
func TestKnowledgeV3SelectionPrompt(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[0].Content, "opposition") || !strings.Contains(body.Messages[0].Content, "quotes") {
			t.Error("new contract missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"topics":[],"reason":"缺少反方条件"}`}}}})
	}))
	defer service.Close()
	req := KnowledgeArticleRequest{Stage: "select", PromptVersion: KnowledgeArticlePromptVersion}
	p := NewOpenAIProvider("test").WithBaseURL(service.URL)
	_, _, err := p.KnowledgeArticleStep(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
}
