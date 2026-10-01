package provider

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeFrozenMessagesAndOutputContract(t *testing.T) {
	for _, version := range []string{"knowledge-article-v1", "knowledge-article-v2", "knowledge-article-v3", KnowledgeArticlePromptVersion} {
		t.Run(version, func(t *testing.T) {
			req := KnowledgeArticleRequest{PromptVersion: version, Stage: "discover", Instructions: "中文 English JSON", Materials: knowledgeTestMaterials()}
			req.StageConfigs = FreezeKnowledgeStageConfigs(map[string]string{"discover": "admitted"})
			req.Candidates = []KnowledgeRecallCandidate{{MaterialID: "private-local-only", Title: "private title"}}
			est, err := EstimateKnowledgeRequest(req, "legacy")
			if err != nil {
				t.Fatal(err)
			}
			req.Estimate = est
			system, input, err := KnowledgeArticleMessages(req)
			if err != nil || est.InputTokens <= len([]rune(input))/4 || !est.Approximate {
				t.Fatal(est, err)
			}
			if version == KnowledgeArticlePromptVersion && (strings.Contains(input, "private-local-only") || strings.Contains(input, "stage_configs") || strings.Contains(input, "input_fingerprint")) {
				t.Fatal("local admission data leaked", input)
			}
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Model    string                           `json:"model"`
					Max      int                              `json:"max_completion_tokens"`
					Messages []struct{ Role, Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				wantModel, wantCap := "legacy", 8192
				if version == KnowledgeArticlePromptVersion {
					wantModel, wantCap = "admitted", 2048
				}
				if payload.Model != wantModel || payload.Max != wantCap || len(payload.Messages) != 2 || payload.Messages[0].Content != system || payload.Messages[1].Content != input {
					t.Errorf("wrong serialization or routing: %+v", payload)
				}
				w.Write([]byte(`{"choices":[{"message":{"content":"{\"topics\":[],\"reason\":\"不足\"}"}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`))
			}))
			defer service.Close()
			_, usage, err := NewOpenAIProvider("test").WithModel("legacy").WithBaseURL(service.URL).KnowledgeArticleStep(t.Context(), req)
			if err != nil || usage.InputUnits != 100 {
				t.Fatal(usage, err)
			}
		})
	}
}

func TestKnowledgeEstimateRejectsInvalidRoutingAndTracksActualPrice(t *testing.T) {
	for _, req := range []KnowledgeArticleRequest{{PromptVersion: "invalid"}, {PromptVersion: KnowledgeArticlePromptVersion, Stage: "unknown"}, {PromptVersion: KnowledgeArticlePromptVersion, Stage: "write", StageConfigs: map[string]KnowledgeStageConfig{"write": {Model: "frozen", MaxOutputTokens: 1}}}, {PromptVersion: KnowledgeArticlePromptVersion, Stage: "write", StageConfigs: map[string]KnowledgeStageConfig{"write": {Model: "frozen", MaxOutputTokens: 20000}}}} {
		if _, err := EstimateKnowledgeRequest(req, "model"); err == nil {
			t.Fatal("invalid routing accepted", req)
		}
	}
	req := KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePromptVersion, Stage: "review", ReviewModel: "review"}
	cfg, err := KnowledgeConfigForStage(req, "writer")
	if err != nil || cfg.Model != "review" {
		t.Fatal(cfg, err)
	}
	req.Stage = "write"
	if _, err := KnowledgeConfigForStage(req, ""); err == nil {
		t.Fatal("missing model accepted")
	}
	e := &KnowledgeEstimate{}
	if _, known := e.CostForUnits(1, 1); known {
		t.Fatal("unknown free")
	}
	if cost, known := e.CostForUnits(0, 0); cost != 0 || !known {
		t.Fatal(cost, known)
	}
	e.PriceKnown = true
	e.InputCentsPerMillion = 20
	e.OutputCentsPerMillion = 80
	if cost, known := e.CostForUnits(10000, 10000); cost != 1 || !known {
		t.Fatal(cost, known)
	}
	e.InputCentsPerMillion = 0
	e.OutputCentsPerMillion = 0
	if cost, known := e.CostForUnits(10000, 10000); cost != 0 || !known {
		t.Fatal(cost, known)
	}
	var nilEstimate *KnowledgeEstimate
	if _, known := nilEstimate.CostForUnits(1, 1); known {
		t.Fatal("nil price known")
	}
}

func TestKnowledgeCalibrationPreservesOldAdmittedEstimate(t *testing.T) {
	req := KnowledgeArticleRequest{PromptVersion: KnowledgeArticlePromptVersion, Stage: "discover", Instructions: "学习时说明自己的理解。English text。", Materials: knowledgeTestMaterials()}
	calibrated, err := EstimateKnowledgeRequest(req, "google/gemini-3.8-flash-high")
	if err != nil || calibrated.Method != "messages-heuristic-gemini-flash-v1" || calibrated.OutputTokens != 8192 {
		t.Fatal(calibrated, err)
	}
	req.Estimate = &KnowledgeEstimate{Method: "messages-heuristic-v1"}
	old, err := EstimateKnowledgeRequest(req, "google/gemini-3.8-flash-high")
	if err != nil || old.Method != "messages-heuristic-v1" || old.InputTokens <= calibrated.InputTokens || old.InputFingerprint != calibrated.InputFingerprint {
		t.Fatal("calibration rewrote admitted input", old, err)
	}
	req.Estimate.Method = "unsupported"
	if _, err := EstimateKnowledgeRequest(req, "model"); err == nil {
		t.Fatal("unversioned estimate accepted")
	}
	req.Estimate = nil
	req.Materials[0].Position = math.NaN()
	if _, _, err := KnowledgeArticleMessages(req); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, err := EstimateKnowledgeRequest(req, "model"); err == nil {
		t.Fatal("invalid JSON estimated")
	}
	if _, _, err := NewOpenAIProvider("test").KnowledgeArticleStep(t.Context(), req); err == nil {
		t.Fatal("invalid JSON sent")
	}
}
