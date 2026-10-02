// Command quality-eval explicitly runs one accepted private case through isolated writing and review.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/evalset"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "质量运行未完成：", err)
		os.Exit(1)
	}
}

type qualityStore interface {
	BuildArticleQualityManifest(context.Context, []string, string) (*store.ArticleQualityManifest, error)
	FreezeArticleQualityEstimate(context.Context, string, string, *provider.KnowledgeArticleRequest) error
	AdmitArticleQualityStage(context.Context, string, string, string, *provider.KnowledgeEstimate) error
	FinishArticleQualityStage(context.Context, string, models.UsageReceipt) error
	Close() error
}

type runDependencies struct {
	open            func(string) (qualityStore, error)
	loadEnvironment func(string) error
}

func run() error {
	return runArgs(os.Args[1:], os.Stderr, runDependencies{
		open:            func(path string) (qualityStore, error) { return store.Open(path) },
		loadEnvironment: config.LoadEnvironmentFile,
	})
}

func runArgs(args []string, stderr io.Writer, deps runDependencies) error {
	flags := flag.NewFlagSet("quality-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	db := flags.String("db", "", "明确指定Owner数据库")
	id := flags.String("case", "", "明确接纳案例ID")
	out := flags.String("output", "", "私有运行目录；不同对照使用不同目录")
	execute := flags.Bool("run", false, "显式授权一次写作及一次独立审校")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受位置参数")
	}
	if *db == "" || *id == "" || *out == "" {
		return fmt.Errorf("必须指定 --db --case --output")
	}
	s, err := deps.open(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	frozen, err := s.BuildArticleQualityManifest(ctx, []string{*id}, "pod")
	if err != nil {
		return err
	}
	if !*execute {
		raw, _ := json.MarshalIndent(frozen, "", "  ")
		return frozenWrite(filepath.Join(*out, "preview-manifest.json"), raw)
	}
	if err := deps.loadEnvironment(".env"); err != nil {
		return err
	}
	cfg := &config.Config{PodBaseURL: os.Getenv("POD_BASE_URL"), PodAPIKey: os.Getenv("POD_API_KEY"), PodModel: os.Getenv("POD_MODEL"), PodWriteModel: os.Getenv("POD_WRITE_MODEL"), PodReviewModel: os.Getenv("POD_REVIEW_MODEL"), PodDiscoveryModel: os.Getenv("POD_DISCOVERY_MODEL"), PodSelectionModel: os.Getenv("POD_SELECTION_MODEL")}
	if err = cfg.ValidatePod(); err != nil {
		return err
	}
	if cfg.PodAPIKey == "" || cfg.PodBaseURL == "" || cfg.PodModel == "" {
		return fmt.Errorf("POD配置不完整")
	}
	var m evalset.QualityManifest
	raw, _ := json.Marshal(frozen)
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	authorize := func(ctx context.Context, id, name string) error {
		_, err := s.BuildArticleQualityManifest(ctx, []string{id}, name)
		return err
	}
	var input provider.KnowledgeArticleRequest
	if err = json.Unmarshal([]byte(m.Cases[0].InputJSON), &input); err != nil {
		return err
	}
	if input.Topic == nil {
		return fmt.Errorf("原案例缺少冻结选题，不自动替换")
	}
	prompt := input.PromptVersion
	if prompt == "" {
		prompt = provider.KnowledgeArticlePromptVersion
	}
	models := cfg.KnowledgeStageModels()
	input.Stage = "write"
	input.StageConfigs = provider.FreezeKnowledgeStageConfigs(models)
	input.ReviewModel = models["review"]
	input.PromptVersion = prompt
	suffix := evalset.ArticleQualityExpectedInstruction(m.Cases[0].Expected)
	if suffix != "" && !strings.HasSuffix(input.Instructions, suffix) {
		input.Instructions += suffix
	}
	if err = s.FreezeArticleQualityEstimate(ctx, "pod", models["write"], &input); err != nil {
		return err
	}
	b, _ := json.Marshal(input)
	m.Cases[0].InputJSON = string(b)
	b, _ = json.Marshal(m)
	m.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(b))
	b, _ = json.MarshalIndent(m, "", "  ")
	if err = frozenWrite(filepath.Join(*out, "manifest.json"), b); err != nil {
		return err
	}
	client := &podClient{apiKey: cfg.PodAPIKey, baseURL: cfg.PodBaseURL, model: models["write"]}
	written, err := runQualityStage(ctx, s, &m, *id, models["write"], prompt, filepath.Join(*out, "write.json"), input, client, authorize)
	if err != nil {
		report, _ := evalset.SummarizeArticleQuality([]evalset.ArticleQualityRun{written})
		b, _ := json.MarshalIndent(report, "", "  ")
		if e := privateWrite(filepath.Join(*out, "report.json"), b); e != nil {
			return e
		}
		return fmt.Errorf("写作失败，检查私有receipt；禁止自动重试：%w", err)
	}
	if written.Output == nil || len(written.Output.Blocks) == 0 {
		return fmt.Errorf("写作无正文，保留receipt")
	}
	input.Stage = "review"
	input.Blocks = written.Output.Blocks
	if err = s.FreezeArticleQualityEstimate(ctx, "pod", models["review"], &input); err != nil {
		return err
	}
	b, _ = json.Marshal(input)
	m.Cases[0].InputJSON = string(b)
	b, _ = json.Marshal(m)
	m.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(b))
	b, _ = json.MarshalIndent(m, "", "  ")
	if err = frozenWrite(filepath.Join(*out, "review-manifest.json"), b); err != nil {
		return err
	}
	reviewer := &podClient{apiKey: cfg.PodAPIKey, baseURL: cfg.PodBaseURL, model: models["review"]}
	reviewed, reviewErr := runQualityStage(ctx, s, &m, *id, models["review"], prompt, filepath.Join(*out, "review.json"), input, reviewer, authorize)
	report, err := evalset.SummarizeArticleQuality([]evalset.ArticleQualityRun{written, reviewed})
	if err != nil {
		return err
	}
	b, _ = json.MarshalIndent(report, "", "  ")
	if err = privateWrite(filepath.Join(*out, "report.json"), b); err != nil {
		return err
	}
	if reviewErr != nil {
		return fmt.Errorf("审校失败，保留独立receipt：%w", reviewErr)
	}
	return nil
}

// budgetedQualityClient admits one persisted paid stage immediately before transport.
// The runner's private receipt must reach disk before its numerical usage is settled.
type budgetedQualityClient struct {
	*podClient
	store                     qualityStore
	identity, contract, model string
	estimate                  *provider.KnowledgeEstimate
	dispatched                bool
}

func (p *budgetedQualityClient) KnowledgeArticleStep(ctx context.Context, input provider.KnowledgeArticleRequest) (*provider.KnowledgeArticleResult, provider.TaskUsage, error) {
	if err := p.store.AdmitArticleQualityStage(ctx, p.identity, p.contract, p.model, p.estimate); err != nil {
		return nil, provider.TaskUsage{}, err
	}
	p.dispatched = true
	return p.podClient.KnowledgeArticleStep(ctx, input)
}
func runQualityStage(ctx context.Context, s qualityStore, manifest *evalset.QualityManifest, id, model, prompt, path string, input provider.KnowledgeArticleRequest, client *podClient, authorize func(context.Context, string, string) error) (evalset.ArticleQualityRun, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return evalset.ArticleQualityRun{}, err
	}
	raw, _ := json.Marshal([]string{absolute, id, input.Stage})
	identity := fmt.Sprintf("quality:%x", sha256.Sum256(raw))
	price := priceFingerprint(input, model)
	raw, _ = json.Marshal([]string{manifest.Fingerprint, model, prompt, price})
	contract := fmt.Sprintf("%x", sha256.Sum256(raw))
	_, statErr := os.Stat(path)
	p := &budgetedQualityClient{podClient: client, store: s, identity: identity, contract: contract, model: model, estimate: input.Estimate}
	run, runErr := evalset.RunArticleQuality(ctx, manifest, id, model, prompt, price, path, p, authorize)
	if p.dispatched || statErr == nil {
		raw, readErr := os.ReadFile(path)
		var persisted evalset.ArticleQualityRun
		if readErr == nil && json.Unmarshal(raw, &persisted) == nil && (persisted.Status == "succeeded" || persisted.Status == "failed") && persisted.ManifestHash == run.ManifestHash && run.ManifestHash == manifest.Fingerprint && run.CaseID == id && run.Model == model && run.PromptVersion == prompt && run.PriceVersion == price && persisted.Model == run.Model && persisted.Status == run.Status && persisted.Usage == run.Usage {
			receipt := models.UsageReceipt{ReceiptID: identity, AttemptID: identity, Operation: "article_quality_" + input.Stage, Provider: "pod", Model: run.ActualModel, InputUnits: run.Usage.InputUnits, OutputUnits: run.Usage.OutputUnits}
			if run.UsageKnown && run.ActualModel == model && run.Input.Estimate != nil {
				receipt.CostCents, receipt.CostKnown = run.Input.Estimate.CostForUnits(receipt.InputUnits, receipt.OutputUnits)
			}
			// Cancellation must not discard a metered response already persisted locally.
			settleCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			finishErr := s.FinishArticleQualityStage(settleCtx, identity, receipt)
			cancel()
			if finishErr != nil {
				return run, finishErr
			}
		}
	}
	return run, runErr
}

func privateWrite(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func priceFingerprint(input provider.KnowledgeArticleRequest, model string) string {
	e := input.Estimate
	if e == nil {
		var err error
		e, err = provider.EstimateKnowledgeRequest(input, model)
		if err != nil || e == nil {
			return "unknown-price"
		}
	}
	raw, _ := json.Marshal(struct {
		Model, Version string
		Input, Output  int64
		Known          bool
	}{model, e.PriceUpdatedAt, e.InputCentsPerMillion, e.OutputCentsPerMillion, e.PriceKnown})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func frozenWrite(path string, raw []byte) error {
	if previous, err := os.ReadFile(path); err == nil {
		if string(previous) != string(raw) {
			return fmt.Errorf("冻结manifest冲突，旧文件保留")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return privateWrite(path, raw)
}
