package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// transcriptionCheckpoint explicitly includes fields omitted by TranscriptResult's
// artifact JSON. Input/configuration identity and paid-call identity survive restart.
type transcriptionCheckpoint struct {
	OriginJobID        string                     `json:"origin_job_id,omitempty"`
	JobID              string                     `json:"job_id"`
	AudioSHA256        string                     `json:"audio_sha256"`
	ConfiguredProvider string                     `json:"configured_provider"`
	ConfiguredModel    string                     `json:"configured_model"`
	Provider           string                     `json:"provider"`
	Model              string                     `json:"model"`
	Result             *provider.TranscriptResult `json:"result"`
	Usage              provider.TaskUsage         `json:"usage"`
}

// transcribeDurably is shared by source ingestion and personal voice drafts.
// No known response is replayed; a started call without a usable checkpoint is unknown.
func (w *Worker) transcribeDurably(ctx context.Context, job *models.ProcessingJob, p provider.TranscriptionProvider, path string, frozenHashes ...string) (*transcriptionCheckpoint, error) {
	exec, err := w.store.GetJobExecution(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	hash := ""
	if exec.CheckpointJSON != "" && len(frozenHashes) == 1 {
		hash = frozenHashes[0]
	} else {
		hash, err = filehash.SHA256(path)
		if err != nil {
			return nil, err
		}
		if len(frozenHashes) == 1 && frozenHashes[0] != hash {
			return nil, fmt.Errorf("录音指纹不符")
		}
	}
	var cp transcriptionCheckpoint
	if exec.CheckpointJSON != "" {
		if err := json.Unmarshal([]byte(exec.CheckpointJSON), &cp); err != nil || cp.JobID != job.ID || cp.AudioSHA256 != hash || cp.ConfiguredProvider != exec.ConfiguredProvider || cp.ConfiguredModel != exec.ConfiguredModel || cp.Result == nil || cp.Provider == "" {
			return nil, fmt.Errorf("转录断点与冻结音频或配置不一致，需显式恢复")
		}
	} else {
		if exec.RemoteCallStarted {
			if err := w.store.SaveJobResult(ctx, job.ID, "", models.JobResultUnknown); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("转录远端结果未知，请显式重试；自动恢复未再次调用供应商")
		}
		if p == nil {
			return nil, fmt.Errorf("供应商不支持音频转录")
		}
		if err := w.store.MarkJobRemoteCallStarted(ctx, job.ID); err != nil {
			return nil, err
		}
		var result *provider.TranscriptResult
		if contextual, ok := p.(interface {
			TranscribeContext(context.Context, string) (*provider.TranscriptResult, error)
		}); ok {
			result, err = contextual.TranscribeContext(ctx, path)
		} else {
			result, err = p.Transcribe(path)
		}
		if err != nil {
			return nil, fmt.Errorf("转录: %w", err)
		}
		if result == nil {
			return nil, fmt.Errorf("转录响应为空")
		}
		model := result.Model
		if model == "" {
			model = "unknown"
		}
		cp = transcriptionCheckpoint{JobID: job.ID, AudioSHA256: hash, ConfiguredProvider: exec.ConfiguredProvider, ConfiguredModel: exec.ConfiguredModel, Provider: p.Name(), Model: model, Result: result, Usage: result.Usage}
		raw, err := json.Marshal(cp)
		if err != nil {
			return nil, err
		}
		// Preserve the response even when shutdown canceled the request context just
		// after the supplier returned. This bounded write cannot start another call.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := w.store.SaveJobCheckpoint(saveCtx, job.ID, string(raw)); err != nil {
			return nil, fmt.Errorf("持久化转录响应: %w", err)
		}
	}
	cost, known := int64(0), false
	if cp.Provider != "openai" && cp.Provider != "groq" && (cp.Usage.InputUnits > 0 || cp.Usage.OutputUnits > 0) {
		cost, known, err = w.store.ResolveUsageCost(ctx, cp.Provider, cp.Model, cp.Usage.InputUnits, cp.Usage.OutputUnits)
		if err != nil {
			return nil, err
		}
	}
	origin := cp.OriginJobID
	if origin == "" {
		origin = cp.JobID
	}
	if err := w.store.RecordUsageReceipt(ctx, models.UsageReceipt{ReceiptID: origin + ":transcription", AttemptID: origin + ":response", Operation: "transcription", Provider: cp.Provider, Model: cp.Model, InputUnits: cp.Usage.InputUnits, OutputUnits: cp.Usage.OutputUnits, CostCents: cost, CostKnown: known}); err != nil {
		return nil, fmt.Errorf("转录响应记账: %w", err)
	}
	if cp.Provider == "openai" || cp.Provider == "groq" {
		if _, err := w.store.DB.ExecContext(ctx, `UPDATE usage_records SET unit_kind='audio_and_text_tokens' WHERE receipt_id=?`, origin+":transcription"); err != nil {
			return nil, err
		}
	}
	var duration float64
	var audioSnapshot struct {
		DurationSeconds float64              `json:"duration_seconds"`
		AudioEstimate   *store.VoiceASRInput `json:"audio_estimate"`
	}
	if json.Unmarshal([]byte(exec.InputSnapshotJSON), &audioSnapshot) == nil {
		duration = audioSnapshot.DurationSeconds
		if audioSnapshot.AudioEstimate != nil {
			duration = audioSnapshot.AudioEstimate.DurationSeconds
		}
	}
	if duration > 0 {
		if _, err := w.store.DB.ExecContext(ctx, `UPDATE usage_records SET audio_seconds=? WHERE receipt_id=?`, duration, origin+":transcription"); err != nil {
			return nil, err
		}
	}
	if origin != job.ID && known {
		_ = w.store.SettleBudget(ctx, origin, cost)
	}
	cp.Result.Model, cp.Result.Usage = cp.Model, cp.Usage
	return &cp, nil
}
