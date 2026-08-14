package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/woyin/orangecast/internal/provider"
)

// callEditorialWithFallback is the provider-selection seam shared by paid
// editorial jobs. The workflow owns request construction and budget/policy
// checks; this module owns only retry routing and provenance.
func callEditorialWithFallback[T any](
	srv *Server,
	ctx context.Context,
	role string,
	primaryConfig provider.TaskConfig,
	primaryName string,
	primaryCall func() (*T, error),
	fallbackCall func(provider.TaskConfig, *provider.ProviderBundle) (*T, string, error),
	markFallback func(*T, string),
	primaryLabel, fallbackLabel string,
) (*T, string, string, error) {
	primaryModel := provider.EffectiveTaskModel(primaryConfig)
	result, err := primaryCall()
	if err == nil {
		if result == nil {
			return nil, "", "", errors.New(primaryLabel + ": provider returned an empty result")
		}
		return result, primaryName, primaryModel, nil
	}
	fallbackConfig, configured := srv.editorialFallbackConfig(ctx, role)
	fallbackBundle, bundleErr := srv.bundleFor(fallbackConfig)
	if !configured || bundleErr != nil || fallbackBundle == nil {
		return nil, "", "", fmt.Errorf("%s: %w", primaryLabel, err)
	}
	fallbackResult, fallbackName, fallbackErr := fallbackCall(fallbackConfig, fallbackBundle)
	if fallbackErr != nil {
		return nil, "", "", fmt.Errorf("%s: %w", fallbackLabel, fallbackErr)
	}
	if fallbackResult == nil {
		return nil, "", "", errors.New(fallbackLabel + ": provider returned an empty result")
	}
	markFallback(fallbackResult, primaryName+"/"+primaryModel)
	return fallbackResult, fallbackName, provider.EffectiveTaskModel(fallbackConfig), nil
}
