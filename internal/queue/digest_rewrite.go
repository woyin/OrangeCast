// digest_rewrite.go 渠道改写独立任务（G06 / ADR-0023 §6 / ADR-0024 §5）。
// 输入只指定精读修订与渠道：改写只读该修订已通过验证的块，不再次检索/补事实；
// 输入指纹（块集合 hash）随结果保存——同修订同输入复用既有产物，
// 主文修订变化后不沿用旧渠道结果；失败保留主文（修订已是资产）。
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// EnqueueDigestRewriteJob 入队渠道改写（worker 侧薄封装）。
func (w *Worker) EnqueueDigestRewriteJob(ctx context.Context, digestID, channel string) (*models.ProcessingJob, error) {
	return w.store.EnqueueDigestRewriteJob(ctx, digestID, channel)
}

// doDigestRewriteJob 执行渠道改写：读取修订块 → 指纹比对 → 改写 → 门禁 → 落库。
func (w *Worker) doDigestRewriteJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.DigestRewriter == nil {
		return fmt.Errorf("DigestRewriter Provider 不可用")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot struct {
		DigestID      string `json:"digest_id"`
		DigestVersion string `json:"digest_version"`
		Channel       string `json:"channel"`
	}
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.DigestID == "" {
		return fmt.Errorf("任务缺少修订与渠道快照（应经 EnqueueDigestRewriteJob 入队）")
	}
	d, err := w.store.GetEpisodeDigest(ctx, snapshot.DigestID)
	if err != nil {
		return fmt.Errorf("读取精读修订: %w", err)
	}
	blockPtrs, err := w.store.ListDigestBlocks(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("读取内容块: %w", err)
	}
	blocks := make([]models.DigestBlock, 0, len(blockPtrs))
	for _, b := range blockPtrs {
		blocks = append(blocks, *b)
	}
	// 输入指纹：修订块集合（类型+文本+引用）。
	hashInput := fmt.Sprintf("v%d|", d.Version)
	for _, b := range blocks {
		hashInput += fmt.Sprintf("%s|%s|%v\n", b.Type, b.Text, b.Citations)
	}
	sum := sha256.Sum256([]byte(hashInput))
	inputHash := hex.EncodeToString(sum[:])

	// 同修订同输入已有产物 → 复用，不重复改写。
	if existing, err := w.store.GetDigestRewriteInputHash(ctx, d.ID, snapshot.Channel); err == nil && existing == inputHash && existing != "" {
		result, _ := json.Marshal(map[string]any{"digest_id": d.ID, "channel": snapshot.Channel, "reused": true})
		return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
	}

	w.markRemoteCallStarted(ctx, job) // R02-b：到达远端调用边界
	res, err := bundle.DigestRewriter.RewriteDigest(ctx, provider.DigestRewriteRequest{
		Channel: snapshot.Channel, Blocks: BlocksFromModels(blocks),
		MaxChars: digestXHSMaxChars, MaxHashtags: digestXHSMaxTags,
	})
	if err != nil {
		return fmt.Errorf("渠道改写: %w", err)
	}
	if err := provider.ValidateDigestRewrite(res.Text, digestXHSMaxChars, digestXHSMaxTags); err != nil {
		return fmt.Errorf("渠道格式门禁: %w", err)
	}
	if _, err := w.store.UpsertDigestRewrite(ctx, &models.DigestRewrite{
		DigestID: d.ID, Channel: snapshot.Channel, Text: res.Text,
		Provider: "digest_rewrite", Model: "",
		InputHash: inputHash, DigestVersion: d.Version,
	}); err != nil {
		return fmt.Errorf("保存渠道改写: %w", err)
	}
	result, _ := json.Marshal(map[string]any{"digest_id": d.ID, "channel": snapshot.Channel, "input_hash": inputHash})
	return w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete)
}
