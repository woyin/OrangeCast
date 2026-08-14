package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func (srv *Server) handleModelPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	input, err1 := strconv.ParseInt(strings.TrimSpace(r.FormValue("input_cents_per_million")), 10, 64)
	output, err2 := strconv.ParseInt(strings.TrimSpace(r.FormValue("output_cents_per_million")), 10, 64)
	if err1 != nil || err2 != nil || input < 0 || output < 0 {
		http.Error(w, "价格必须是非负整数", http.StatusBadRequest)
		return
	}
	if err := srv.store.SetModelPrice(r.Context(), models.ModelPrice{Provider: r.FormValue("provider"), Model: r.FormValue("model"), InputCentsPerMillion: input, OutputCentsPerMillion: output}); err != nil {
		http.Error(w, "保存模型价格失败："+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/workbench", http.StatusSeeOther)
}

func (srv *Server) checkEditorialBudget(ctx context.Context, profileID string, draftID *string, providerName, model string) error {
	if err := srv.store.CheckEditorialBudget(ctx, profileID, draftID, providerName, model); err != nil {
		return fmt.Errorf("预算门禁拒绝本次调用: %w", err)
	}
	return nil
}

func (srv *Server) recordEditorialUsage(ctx context.Context, profileID string, draftID *string, taskKind, entityType, entityID, providerName, model, promptVersion string, usage provider.TaskUsage) (*int64, error) {
	cost, err := srv.store.CalculateEditorialCost(ctx, providerName, model, usage.InputUnits, usage.OutputUnits)
	if err != nil {
		return nil, fmt.Errorf("计算模型费用: %w", err)
	}
	var fallbackFrom *string
	if usage.FallbackFrom != "" {
		fallbackFrom = &usage.FallbackFrom
	}
	_, err = srv.store.RecordEditorialUsage(ctx, models.EditorialUsageRecord{
		EditorialProfileID: profileID, ArticleDraftID: draftID, TaskKind: taskKind,
		EntityType: entityType, EntityID: entityID, Provider: providerName, Model: model,
		PromptVersion: promptVersion, InputUnits: usage.InputUnits, OutputUnits: usage.OutputUnits,
		CostCents: cost, RetryCount: usage.RetryCount, FallbackFrom: fallbackFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("记录模型费用: %w", err)
	}
	return &cost, nil
}
