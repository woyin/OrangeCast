package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/config"
)

// TestKnowledgeArticleLiveConnection is an explicitly enabled compatibility
// check using synthetic learning materials; it never reads the user's library.
func TestKnowledgeArticleLiveConnection(t *testing.T) {
	if os.Getenv("CWP_KNOWLEDGE_LIVE") != "1" {
		t.Skip("requires explicit live endpoint opt-in")
	}
	if err := config.LoadEnvironmentFile("../../.env"); err != nil {
		t.Fatal("cannot read local environment file")
	}
	cfg := config.Config{PodBaseURL: os.Getenv("POD_BASE_URL"), PodAPIKey: os.Getenv("POD_API_KEY"), PodModel: os.Getenv("POD_MODEL")}
	if cfg.ValidatePod() != nil || !cfg.PodAvailable() {
		t.Fatal("dedicated POD connection is not configured")
	}
	bundle, err := NewSelector("", "").WithPod(cfg.PodAPIKey, cfg.PodBaseURL, cfg.PodModel).Bundle("pod")
	if err != nil {
		t.Fatal("cannot construct dedicated provider")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	req := KnowledgeArticleRequest{Stage: "discover", Audience: "希望从播客中学习的个人读者", Style: "清楚、具体，材料有限时可以写短文，不补充外部事实", Materials: []KnowledgeMaterial{
		{ID: "source-1", Kind: "source_note", SourceTitle: "合成学习材料", Content: "听后先合上笔记，用自己的话解释一个重点，再回到原文检查遗漏。", Citations: []string{"seg-1"}, Evidence: "讲者：听完可以合上笔记，先用自己的话解释一个重点。解释不清楚时，再回到原文找遗漏。"},
		{ID: "source-2", Kind: "source_note", SourceTitle: "合成学习材料", Content: "几天后再次解释，并对照先前笔记，记录理解发生了什么变化。", Citations: []string{"seg-2"}, Evidence: "讲者：几天后再解释一次，对照以前的笔记，记录这次理解有什么变化。"},
		{ID: "reflection-1", Kind: "owner_reflection", SourceTitle: "合成个人笔记", Content: "我听完常觉得自己懂了，但独立解释时会发现逻辑跳步。希望把解释不清楚的位置记下来。"},
	}}
	for _, stage := range []string{"discover", "write", "review", "revise", "review_final"} {
		req.Stage = stage
		result, usage, err := bundle.KnowledgeArticle.KnowledgeArticleStep(ctx, req)
		if err != nil {
			// Transport errors may contain private endpoint details; keep output safe.
			status := regexp.MustCompile(`HTTP [0-9]{3}`).FindString(err.Error())
			if status == "" {
				status = "transport/response error"
			}
			types := ""
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				types += fmt.Sprintf(" %T", cause)
			}
			var shape *json.UnmarshalTypeError
			if errors.As(err, &shape) {
				types += fmt.Sprintf(" field=%s value_type=%s expected=%v", shape.Field, shape.Value, shape.Type)
			}
			t.Fatalf("live %s call failed (%s; types=%s; input=%d output=%d); endpoint details omitted", stage, status, types, usage.InputUnits, usage.OutputUnits)
		}
		if err := ValidateKnowledgeResult(req, result); err != nil {
			t.Fatalf("live %s violated output contract: %v", stage, err)
		}
		t.Logf("stage=%s input_units=%d output_units=%d", stage, usage.InputUnits, usage.OutputUnits)
		switch stage {
		case "discover":
			req.Topic = SelectKnowledgeTopic(result.Topics, nil)
			if req.Topic == nil {
				t.Fatal("synthetic supported direction was not selected")
			}
		case "write", "revise":
			req.Blocks = result.Blocks
		case "review", "review_final":
			if *result.Passed {
				t.Log("live article passed its model review; this is compatibility evidence, not human quality acceptance")
				return
			}
			req.Issues = result.Issues
		}
	}
	t.Log("live article requires review after one revision; bounded state is supported")
}
