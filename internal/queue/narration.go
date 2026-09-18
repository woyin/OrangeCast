// narration.go 解说独立任务（B07 / ADR-0024 §5）。
// 解说按高光/脚本身份独立运行：输入冻结指定高光版本；缓存 key 覆盖文本指纹、
// highlight_id、输入版本、Provider、模型、音色与语言（不再只比较 Provider）。
// 音频先写临时文件并校验，再原子发布文件与数据库记录；失败清理临时文件，
// 单段失败可重试且不阻塞其余段落；Narration 缺失永不阻塞原音。
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// narrationTaskSnapshot 解说任务冻结输入：要解说的高光版本、音色与语言。
type narrationTaskSnapshot struct {
	HighlightVersion   int    `json:"highlight_version"`
	HighlightVersionID string `json:"highlight_version_id"`
	Voice              string `json:"voice"`
	Language           string `json:"language"`
}

// EnqueueNarrationJob 入队解说独立任务（B07）：冻结当前高光版本。
func (w *Worker) EnqueueNarrationJob(ctx context.Context, sourceType models.SourceType, sourceID string, voice, language string) (*models.ProcessingJob, error) {
	version, err := w.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindHighlight)
	if err != nil {
		return nil, fmt.Errorf("尚未生成高光，无法合成解说: %w", err)
	}
	return w.EnqueueNarrationJobForVersion(ctx, sourceType, sourceID, version.Version, version.ID, voice, language)
}

// EnqueueNarrationJobForVersion 冻结指定高光版本入队解说任务（B08 链式衔接用：
// 高光任务完成后按其产出版本衔接，不依赖 current 指针）。
func (w *Worker) EnqueueNarrationJobForVersion(ctx context.Context, sourceType models.SourceType, sourceID string, highlightVersion int, highlightVersionID, voice, language string) (*models.ProcessingJob, error) {
	snapshot, err := json.Marshal(narrationTaskSnapshot{
		HighlightVersion:   highlightVersion,
		HighlightVersionID: highlightVersionID,
		Voice:              voice,
		Language:           language,
	})
	if err != nil {
		return nil, err
	}
	job, created, err := w.store.EnqueueJobIdempotent(ctx, store.JobIntentSpec{
		SourceType: sourceType, SourceID: sourceID, JobType: models.JobNarration,
		IntentID:          fmt.Sprintf("narration:%s:%s:hv%d", sourceType, sourceID, highlightVersion),
		InputSnapshotJSON: string(snapshot),
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil // 同一高光版本的解说任务进行中
	}
	return job, nil
}

// narrationCacheKey 内容感知缓存身份（B07）：任一输入变化都会生成新 key。
func narrationCacheKey(text, highlightID string, inputVersion int, providerName, model, voice, language string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		"v1", text, highlightID, fmt.Sprintf("%d", inputVersion), providerName, model, voice, language,
	}, "\x00")))
	return hex.EncodeToString(h[:])
}

// doNarrationJob 执行解说独立任务：对冻结高光版本的每段 Gist 合成解说。
// 引擎不可用 → 任务显式失败（原因可见）；单段失败不阻塞其余段，原因保留在 checkpoint。
func (w *Worker) doNarrationJob(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle) error {
	if bundle.Narration == nil {
		return fmt.Errorf("Narration Provider 不可用")
	}
	if !bundle.Narration.Available() {
		return fmt.Errorf("解说引擎不可用（未安装或探测失败），Narration 标记为不可用；原音不受影响")
	}
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("读取任务契约: %w", err)
	}
	var snapshot narrationTaskSnapshot
	if err := json.Unmarshal([]byte(exec.InputSnapshotJSON), &snapshot); err != nil || snapshot.HighlightVersion == 0 {
		return fmt.Errorf("任务缺少冻结的高光版本快照（应经 EnqueueNarrationJob 入队）")
	}
	av, err := w.store.GetArtifactVersion(ctx, job.SourceType, job.SourceID, store.KindHighlight, snapshot.HighlightVersion)
	if err != nil {
		return fmt.Errorf("读取冻结高光版本 %d: %w", snapshot.HighlightVersion, err)
	}
	var hs provider.HighlightSet
	if err := json.Unmarshal([]byte(av.Payload), &hs); err != nil {
		return fmt.Errorf("解析高光载荷: %w", err)
	}
	if err := os.MkdirAll(w.narrationDir, 0o755); err != nil {
		return fmt.Errorf("创建 narrations 目录: %w", err)
	}

	providerName := bundle.Narration.Name()
	narrated := 0
	var firstErr error
	for _, h := range hs.Highlights {
		if h.ID == "" || h.Gist == "" {
			continue
		}
		cacheKey := narrationCacheKey(h.Gist, h.ID, snapshot.HighlightVersion, providerName, "", snapshot.Voice, snapshot.Language)
		// 缓存命中：相同输入直接复用，不重复合成。
		if row, err := w.store.GetNarrationByCacheKey(ctx, job.SourceType, job.SourceID, h.ID, cacheKey); err == nil && row != nil {
			narrated++
			continue
		}
		nextVersion := w.nextNarrationVersion(ctx, job.SourceType, job.SourceID, h.ID)
		relPath := fmt.Sprintf("%s_%s_%s_%d.wav", job.SourceType, job.SourceID, h.ID, nextVersion)
		finalPath := filepath.Join(w.narrationDir, relPath)
		tmpPath := finalPath + ".tmp"
		result, err := bundle.Narration.Synthesize(h.Gist, snapshot.Voice, tmpPath)
		if err != nil {
			os.Remove(tmpPath) // 失败清理临时文件
			if firstErr == nil {
				firstErr = fmt.Errorf("Highlight %s 合成失败: %w", h.ID, err)
			}
			continue // 单段失败可重试，不阻塞其余段
		}
		// 发布前校验临时文件：半写文件不可发布、不可播放。
		duration, durErr := audioDuration(tmpPath)
		if durErr != nil || duration <= 0 {
			os.Remove(tmpPath)
			if firstErr == nil {
				firstErr = fmt.Errorf("Highlight %s 的音频校验失败（半写文件已清理）", h.ID)
			}
			continue
		}
		// 原子发布：先文件后数据库。
		if err := os.Rename(tmpPath, finalPath); err != nil {
			os.Remove(tmpPath)
			if firstErr == nil {
				firstErr = fmt.Errorf("Highlight %s 音频发布失败: %w", h.ID, err)
			}
			continue
		}
		voice := snapshot.Voice
		if voice == "" {
			voice = result.Voice
		}
		if _, _, err := w.store.CreateNarrationCached(ctx, job.SourceType, job.SourceID, h.ID, cacheKey, voice, result.Model, relPath, duration, result.CharCount, providerName); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("Highlight %s Narration 写库失败: %w", h.ID, err)
			}
			continue
		}
		w.recordCallUsage(ctx, job, "narration:"+h.ID, providerName, result.Model, provider.TaskUsage{InputUnits: result.CharCount, OutputUnits: int(duration)})
		narrated++
	}
	result, _ := json.Marshal(map[string]any{
		"highlight_version": snapshot.HighlightVersion,
		"narrated":          narrated,
		"total":             len(hs.Highlights),
	})
	if err := w.store.SaveJobResult(ctx, job.ID, string(result), models.JobResultComplete); err != nil {
		return err
	}
	// 部分段失败：任务完成但原因可见（失败段可单独重试；原音不受影响）。
	if firstErr != nil {
		_ = w.store.SaveJobCheckpoint(ctx, job.ID, fmt.Sprintf(`{"partial_failure":%q}`, firstErr.Error()))
	}
	// R10：清单脚本项目（开场/过渡/收尾/无解说 Gist）按计划身份数成进队，
	// 失败只记入 firstErr，不阻塞解说任务与其余段落。
	w.synthesizePlanScripts(ctx, job, bundle, snapshot, &narrated, &firstErr)
	if firstErr != nil {
		_ = w.store.SaveJobCheckpoint(ctx, job.ID, fmt.Sprintf(`{"partial_failure":%q}`, firstErr.Error()))
	}
	return nil
}

// planScriptItemID 清单脚本身份：音频绑定确切计划（plan ID + 位置），
// 旧计划音频不被新计划覆盖（新计划使用新伪 ID 独立版本化）。
func planScriptItemID(planID string, position int) string {
	return fmt.Sprintf("plan:%s:p%d", planID, position)
}

// synthesizePlanScripts R10：为最新 DJ 清单的脚本项目（开场/过渡/收尾/无解说的
// Gist）合成音频；脚本指纹（文本+计划版本+伪 ID）参与缓存身份，重试不重复合成；
// 单项失败不阻塞其余项。音频仍存 narrationDir，不进入 EvidenceAudio、不作引用证据。
func (w *Worker) synthesizePlanScripts(ctx context.Context, job *models.ProcessingJob, bundle *provider.ProviderBundle, snapshot narrationTaskSnapshot, narrated *int, firstErr *error) {
	plan, err := w.store.GetLatestDJPlanForSource(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return // 无清单：无事可做（不阻塞解说任务）
	}
	providerName := bundle.Narration.Name()
	for _, it := range plan.Items {
		if it.Kind != models.DJItemNarration || it.ScriptText == "" {
			continue // 已有真实解说音频的项无脚本文本，不重复合成
		}
		itemID := planScriptItemID(plan.ID, it.Position)
		cacheKey := narrationCacheKey(it.ScriptText, itemID, plan.HighlightVersion, providerName, "", snapshot.Voice, snapshot.Language)
		if row, err := w.store.GetNarrationByCacheKey(ctx, job.SourceType, job.SourceID, itemID, cacheKey); err == nil && row != nil {
			*narrated++
			continue // 脚本指纹命中：不重复合成
		}
		nextVersion := w.nextNarrationVersion(ctx, job.SourceType, job.SourceID, itemID)
		relPath := fmt.Sprintf("%s_%s_%s_%d.wav", job.SourceType, job.SourceID, itemID, nextVersion)
		finalPath := filepath.Join(w.narrationDir, relPath)
		tmpPath := finalPath + ".tmp"
		result, err := bundle.Narration.Synthesize(it.ScriptText, snapshot.Voice, tmpPath)
		if err != nil {
			os.Remove(tmpPath)
			if *firstErr == nil {
				*firstErr = fmt.Errorf("清单脚本位置 %d 合成失败: %w", it.Position, err)
			}
			continue
		}
		duration, durErr := audioDuration(tmpPath)
		if durErr != nil || duration <= 0 {
			os.Remove(tmpPath)
			if *firstErr == nil {
				*firstErr = fmt.Errorf("清单脚本位置 %d 的音频校验失败", it.Position)
			}
			continue
		}
		if err := os.Rename(tmpPath, finalPath); err != nil {
			os.Remove(tmpPath)
			if *firstErr == nil {
				*firstErr = fmt.Errorf("清单脚本位置 %d 音频发布失败: %w", it.Position, err)
			}
			continue
		}
		voice := snapshot.Voice
		if voice == "" {
			voice = result.Voice
		}
		if _, _, err := w.store.CreateNarrationCached(ctx, job.SourceType, job.SourceID, itemID, cacheKey, voice, result.Model, relPath, duration, result.CharCount, providerName); err != nil {
			if *firstErr == nil {
				*firstErr = fmt.Errorf("清单脚本位置 %d 写库失败: %w", it.Position, err)
			}
			continue
		}
		w.recordCallUsage(ctx, job, "narration:"+itemID, providerName, result.Model, provider.TaskUsage{InputUnits: result.CharCount, OutputUnits: int(duration)})
		*narrated++
	}
}
