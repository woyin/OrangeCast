package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPodTransportIsIndependentAndFreezesModel(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer pod-secret" {
			t.Errorf("incorrect dedicated endpoint/auth")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "frozen-model" || body["max_completion_tokens"] != float64(8192) {
			t.Errorf("wrong model/output limit: %v", body["model"])
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"topics":[],"reason":"材料不足"}`}}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 7}})
	}))
	defer service.Close()
	sel := NewSelector("groq-original", "openai-original").WithPod("pod-secret", service.URL+"/v1", "current-model")
	bundle, err := sel.BundleForTask(TaskConfig{Provider: "pod", Model: "frozen-model"})
	if err != nil {
		t.Fatal(err)
	}
	result, usage, err := bundle.KnowledgeArticle.KnowledgeArticleStep(context.Background(), KnowledgeArticleRequest{Stage: "discover"})
	if err != nil || result.Reason != "材料不足" || usage.InputUnits != 12 || usage.OutputUnits != 7 {
		t.Fatalf("response: %v %+v %+v", err, result, usage)
	}
	if bundle.Transcription != nil || bundle.KnowledgeArticle.Name() != "pod" {
		t.Fatal("text-only provider must preserve distinct identity")
	}
	old, _ := sel.Bundle("openai")
	if old.Analysis.Name() != "openai" || sel.openaiAPIKey != "openai-original" || sel.groqAPIKey != "groq-original" {
		t.Fatal("POD must not override old providers")
	}
	if _, err := NewSelector("", "").Bundle("pod"); err == nil {
		t.Fatal("missing credentials must fail")
	}
}

func TestKnowledgeArticleProviderRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"unauthorized", `{"error":"denied"}`, 401}, {"invalid-envelope", "invalid", 200}, {"invalid-content", `{"choices":[{"message":{"content":"invalid"}}]}`, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer service.Close()
			p := NewOpenAIProvider("test").WithBaseURL(service.URL)
			if _, _, err := p.KnowledgeArticleStep(t.Context(), KnowledgeArticleRequest{Stage: "discover"}); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func knowledgeTestMaterials() []KnowledgeMaterial {
	return []KnowledgeMaterial{{ID: "source", Kind: "keypoint", Citations: []string{"seg-1"}, Evidence: "真实原文"}, {ID: "note", Kind: "owner_reflection", Content: "个人理解"}}
}

func TestKnowledgeAttributionAndTopicGates(t *testing.T) {
	materials := knowledgeTestMaterials()
	good := KnowledgeTopic{Title: "如何理解证据", Question: "怎样避免误读？", Thesis: "保留上下文再进行综合", Outline: "来源、理解、应用", Score: 90, Sufficient: true, MaterialIDs: []string{"source", "note"}}
	if err := ValidateKnowledgeTopics([]KnowledgeTopic{good}, materials); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*KnowledgeTopic){func(t *KnowledgeTopic) { t.Title = "" }, func(t *KnowledgeTopic) { t.Score = 101 }, func(t *KnowledgeTopic) { t.MaterialIDs = []string{"source", "invented"} }, func(t *KnowledgeTopic) { t.MaterialIDs = []string{"source", "source"} }, func(t *KnowledgeTopic) { t.Missing = []string{"关键证据"} }, func(t *KnowledgeTopic) { t.MaterialIDs = []string{"source"} }} {
		v := good
		mutate(&v)
		if ValidateKnowledgeTopics([]KnowledgeTopic{v}, materials) == nil {
			t.Fatal("invalid direction accepted")
		}
	}
	if ValidateKnowledgeTopics([]KnowledgeTopic{good, good, good, good}, materials) == nil {
		t.Fatal("unbounded topics")
	}
	if SelectKnowledgeTopic([]KnowledgeTopic{good}, []KnowledgeTopic{good}) != nil {
		t.Fatal("duplicate must not be selected")
	}
	weak := good
	weak.Score = 60
	if SelectKnowledgeTopic([]KnowledgeTopic{weak}, nil) != nil {
		t.Fatal("weak direction selected")
	}
	insufficient := good
	insufficient.Sufficient = false
	if SelectKnowledgeTopic([]KnowledgeTopic{insufficient}, nil) != nil {
		t.Fatal("insufficient direction selected")
	}
	if SelectKnowledgeTopic([]KnowledgeTopic{weak, good}, nil) == nil {
		t.Fatal("valid direction missing")
	}
	if !KnowledgeTopicDuplicate(good, KnowledgeTopic{Title: "如何理解证据！"}) || KnowledgeTopicDuplicate(good, KnowledgeTopic{Title: "烹饪番茄的温度", Thesis: "先洗净再加热"}) {
		t.Fatal("title/thesis comparison")
	}
	blocks := []KnowledgeBlock{{Kind: "source", Text: "来源表达", MaterialIDs: []string{"source"}}, {Kind: "synthesis", Text: "综合理解", MaterialIDs: []string{"source", "note"}}}
	if err := ValidateKnowledgeBlocks("文章", blocks, materials); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []KnowledgeBlock{{Kind: "source", Text: "把个人看法当事实", MaterialIDs: []string{"note"}}, {Kind: "reflection", Text: "偷归给用户", MaterialIDs: []string{"source"}}, {Kind: "source", Text: "未知材料", MaterialIDs: []string{"unknown"}}, {Kind: "unknown", Text: "未知身份", MaterialIDs: []string{"source"}}, {Kind: "source", Text: "无引用"}, {Kind: "source", Text: "[编造](https://invented.example)", MaterialIDs: []string{"source"}}} {
		v := append([]KnowledgeBlock(nil), blocks...)
		v[0] = bad
		if ValidateKnowledgeBlocks("文章", v, materials) == nil {
			t.Fatal("invalid attribution accepted")
		}
	}
	if ValidateKnowledgeBlocks("", blocks, materials) == nil || ValidateKnowledgeBlocks("文章", nil, materials) == nil {
		t.Fatal("empty article accepted")
	}
	for _, r := range []*KnowledgeArticleResult{nil, {}, {Passed: boolPtr(true), Issues: []string{"still wrong"}}, {Passed: boolPtr(false)}} {
		if ValidateKnowledgeResult(KnowledgeArticleRequest{Stage: "review"}, r) == nil {
			t.Fatal("invalid review accepted")
		}
	}
	if ValidateKnowledgeResult(KnowledgeArticleRequest{Stage: "review_final"}, &KnowledgeArticleResult{Passed: boolPtr(false), Issues: []string{"需修订"}}) != nil {
		t.Fatal("valid failed review")
	}
	if ValidateKnowledgeResult(KnowledgeArticleRequest{Stage: "unknown"}, &KnowledgeArticleResult{}) == nil {
		t.Fatal("unknown stage")
	}
}
func boolPtr(v bool) *bool { return &v }

func TestKnowledgeTopicOutlineCompatibility(t *testing.T) {
	for _, body := range []string{`{"title":"标题","outline":"来源与应用"}`, `{"title":"标题","outline":["来源","应用"]}`, `{"title":"标题","outline":null}`, `{"title":"标题"}`} {
		var topic KnowledgeTopic
		if err := json.Unmarshal([]byte(body), &topic); err != nil || topic.Title != "标题" {
			t.Fatalf("compatible outline: %v %+v", err, topic)
		}
		if body == `{"title":"标题","outline":["来源","应用"]}` && topic.Outline != "来源\n应用" {
			t.Fatal("section order was lost")
		}
	}
	for _, body := range []string{`{`, `{"outline":{"title":"wrong shape"}}`, `{"outline":["章节",2]}`, `{"score":"90"}`} {
		var topic KnowledgeTopic
		if json.Unmarshal([]byte(body), &topic) == nil {
			t.Fatal("invalid topic type accepted")
		}
	}
}

func TestKnowledgeResultWritingAndDuplicateIdentity(t *testing.T) {
	materials := knowledgeTestMaterials()
	topic := KnowledgeTopic{Title: "可靠地理解学习材料", Thesis: "来源与理解应保持可辨", Question: "怎样理解？", Outline: "来源与解释", MaterialIDs: []string{"source", "note"}, Sufficient: true, Score: 80}
	blocks := []KnowledgeBlock{{Kind: "source", Text: "来源整理", MaterialIDs: []string{"source"}}, {Kind: "synthesis", Text: "综合建议", MaterialIDs: []string{"source", "note"}}}
	req := KnowledgeArticleRequest{Stage: "write", Materials: materials, Topic: &topic}
	result := &KnowledgeArticleResult{Title: topic.Title, Blocks: blocks}
	if err := ValidateKnowledgeResult(req, result); err != nil {
		t.Fatal(err)
	}
	req.Topic = nil
	if ValidateKnowledgeResult(req, result) == nil {
		t.Fatal("missing direction accepted")
	}
	req.Topic = &topic
	req.History = []KnowledgeTopic{topic}
	if ValidateKnowledgeResult(req, result) == nil {
		t.Fatal("old proposition rewritten as new article")
	}
	req.History = nil
	result.Blocks = nil
	if ValidateKnowledgeResult(req, result) == nil {
		t.Fatal("missing draft accepted")
	}
	if err := ValidateKnowledgeResult(KnowledgeArticleRequest{Stage: "discover", Materials: materials}, &KnowledgeArticleResult{Topics: []KnowledgeTopic{topic}}); err != nil {
		t.Fatal(err)
	}
	if !KnowledgeTopicDuplicate(KnowledgeTopic{Title: "abcdefghijX"}, KnowledgeTopic{Title: "abcdefghijY"}) {
		t.Fatal("near-identical title not recognized")
	}
	for _, h := range []KnowledgeTopic{{}, {Title: "二"}, {Title: "另一"}} {
		if KnowledgeTopicDuplicate(KnowledgeTopic{Title: "一"}, h) {
			t.Fatal("unrelated short title incorrectly blocked")
		}
	}
}
