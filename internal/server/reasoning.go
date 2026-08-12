package server

import (
	"context"

	"github.com/enowdev/antares/internal/config"
)

// validateExplicitReasoning rejects a newly submitted override before the
// caller opens a stream or mutates persistent state. Empty means Auto and is
// valid for every model.
func (s *Server) validateExplicitReasoning(
	ctx context.Context,
	cfg *config.Config,
	modelRef string,
	effort string,
) error {
	if effort == "" {
		return nil
	}
	if modelRef == "" {
		modelRef = reasoningModelRef(cfg)
	}
	return s.agent.ValidateReasoningEffort(ctx, modelRef, effort)
}

// validateChangedReasoning validates only values changed by a mutation.
// Persisted values from older releases remain loadable and are handled as
// legacy values by the agent at runtime.
func (s *Server) validateChangedReasoning(
	ctx context.Context,
	before *config.Config,
	after *config.Config,
) error {
	modelRef := reasoningModelRef(after)
	if before.Agent.ReasoningEffort != after.Agent.ReasoningEffort {
		if err := s.validateExplicitReasoning(ctx, after, modelRef, after.Agent.ReasoningEffort); err != nil {
			return err
		}
	}
	if before.Model.ReasoningEffort != after.Model.ReasoningEffort {
		if err := s.validateExplicitReasoning(ctx, after, modelRef, after.Model.ReasoningEffort); err != nil {
			return err
		}
	}
	return nil
}

func reasoningModelRef(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.Model.Provider == "" || cfg.Model.Default == "" {
		return cfg.Model.Default
	}
	// Qualifying with the configured provider preserves aggregator model ids:
	// openrouter + anthropic/claude becomes openrouter/anthropic/claude, which
	// Agent resolves back to provider=openrouter and model=anthropic/claude.
	return cfg.Model.Provider + "/" + cfg.Model.Default
}
