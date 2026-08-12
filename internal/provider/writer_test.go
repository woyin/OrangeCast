package provider

import (
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
