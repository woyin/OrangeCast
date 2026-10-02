package evalset

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/woyin/orangecast/internal/provider"
	"os"
	"path/filepath"
	"testing"
)

type qualityFake struct{ calls int }

func (p *qualityFake) Name() string { return "fake" }
func (p *qualityFake) KnowledgeArticleStep(context.Context, provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	p.calls++
	return &provider.KnowledgeArticleResult{Title: "自建素材", Blocks: []provider.KnowledgeBlock{{Kind: "synthesis", Text: "保留边界", MaterialIDs: []string{"m"}}, {Kind: "synthesis", Text: "未知条件", MaterialIDs: []string{"m"}}}}, provider.TaskUsage{}, nil
}
func TestArticleQualityRunCheckpointAndUnknown(t *testing.T) {
	raw, _ := json.Marshal(provider.KnowledgeArticleRequest{Topic: &provider.KnowledgeTopic{Title: "自建", Question: "条件?", MaterialIDs: []string{"m"}}, Materials: []provider.KnowledgeMaterial{{ID: "m", Kind: "source_note", Content: "自建文本"}}})
	m := &QualityManifest{Version: 1, Provider: "fake", Fingerprint: "frozen", Cases: []*QualityCase{{ID: "case", State: "accepted", InputJSON: string(raw), Fingerprint: "case-frozen"}}}
	p := &qualityFake{}
	authorize := func(context.Context, string, string) error { return nil }
	path := filepath.Join(t.TempDir(), "private", "receipt.json")
	r, err := RunArticleQuality(t.Context(), m, "case", "fake-model", "v1", "price-v1", path, p, authorize)
	if err != nil || p.calls != 1 || r.UsageKnown || r.Human != nil || r.Output == nil {
		t.Fatalf("run %+v %v", r, err)
	}
	if _, err = RunArticleQuality(t.Context(), m, "case", "fake-model", "v1", "price-v1", path, p, authorize); err != nil || p.calls != 1 {
		t.Fatal("replay paid", err)
	}
	denied := func(context.Context, string, string) error { return fmt.Errorf("revoked") }
	if suppressed, e := RunArticleQuality(t.Context(), m, "case", "fake-model", "v1", "price-v1", path, p, denied); e == nil || suppressed.Output != nil || p.calls != 1 {
		t.Fatal("cache bypass after revoke", e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	r.Status = "dispatched"
	writeQualityCheckpoint(path, r)
	if _, err = RunArticleQuality(t.Context(), m, "case", "fake-model", "v1", "price-v1", path, p, authorize); err == nil || p.calls != 1 {
		t.Fatal("unknown retried")
	}
	report, err := SummarizeArticleQuality([]ArticleQualityRun{r})
	if err != nil || report.UnknownUsage != 1 || report.HumanMissing != 1 || report.EstimationError != nil {
		t.Fatalf("report %+v %v", report, err)
	}
}
func TestArticleQualitySevereHumanError(t *testing.T) {
	h := &LearningHumanScore{Evaluator: "Owner", AnswersQuestion: 5, SourceFidelity: 5, Specificity: 5, Readability: 5, Usefulness: 5, SevereError: true}
	report, err := SummarizeArticleQuality([]ArticleQualityRun{{Human: h}})
	if err != nil || report.SevereFailures != 1 {
		t.Fatal(report, err)
	}
	h.SourceFidelity = 0
	if _, err = SummarizeArticleQuality([]ArticleQualityRun{{Human: h}}); err == nil {
		t.Fatal("invalid human score")
	}
}

func TestArticleQualityCostRegressionGolden(t *testing.T) {
	estimated, actual := 12.0, 15.0
	report, err := SummarizeArticleQuality([]ArticleQualityRun{{Status: "succeeded", UsageKnown: true, EstimatedCost: &estimated, ActualCost: &actual}})
	if err != nil || report.KnownCosts != 1 || report.UnknownCosts != 0 || report.EstimationError == nil || *report.EstimationError != 3 || report.QualityStatus != "pending_human" {
		t.Fatalf("golden %+v %v", report, err)
	}
	unknown, err := SummarizeArticleQuality([]ArticleQualityRun{{Status: "succeeded", UsageKnown: true}})
	if err != nil || unknown.KnownUsage != 1 || unknown.UnknownCosts != 1 || unknown.UnknownEstimates != 1 || unknown.EstimationError != nil {
		t.Fatalf("unknown price %+v %v", unknown, err)
	}
}
