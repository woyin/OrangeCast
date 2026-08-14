package server

import (
	"context"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

const (
	editorialRoleWriter   = "writer"
	editorialRoleScout    = "scout"
	editorialRoleCurator  = "curator"
	editorialRoleEvidence = "evidence_reviewer"
	editorialRoleStyle    = "style_editor"
)

// editorialTaskConfig resolves one editorial role's optional override. Each
// field falls back independently to analysis, allowing users to override only
// a model or only a provider.
func editorialTaskConfig(settings *models.Settings, role string) provider.TaskConfig {
	providerOverride, modelOverride := (*string)(nil), (*string)(nil)
	switch role {
	case editorialRoleWriter:
		providerOverride, modelOverride = settings.WriterProvider, settings.WriterModel
	case editorialRoleScout:
		providerOverride, modelOverride = settings.ScoutProvider, settings.ScoutModel
	case editorialRoleCurator:
		providerOverride, modelOverride = settings.CuratorProvider, settings.CuratorModel
	case editorialRoleEvidence:
		providerOverride, modelOverride = settings.EvidenceReviewerProvider, settings.EvidenceReviewerModel
	case editorialRoleStyle:
		providerOverride, modelOverride = settings.StyleEditorProvider, settings.StyleEditorModel
	}
	return taskConfigFrom(fallbackSetting(providerOverride, settings.AnalysisProvider), fallbackSetting(modelOverride, settings.AnalysisModel))
}

func fallbackSetting(override, fallback *string) *string {
	if override != nil && strings.TrimSpace(*override) != "" {
		return override
	}
	return fallback
}

func (srv *Server) editorialFallbackConfig(ctx context.Context, role string) (provider.TaskConfig, bool) {
	route, err := srv.store.GetEditorialRoleFallback(ctx, role)
	if err != nil {
		return provider.TaskConfig{}, false
	}
	providerName, model := route.Provider, route.Model
	return taskConfigFrom(&providerName, &model), true
}
