package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

type reasoningInput struct {
	ModelRef string
	Explicit string
	Role     string
	Agent    string
	Model    string
}

type reasoningResolution struct {
	Value           string
	Capability      *llm.ReasoningCapability
	DiscardedLegacy string
}

// ReasoningMetadataUnavailableError distinguishes an unavailable provider
// catalogue from a model that is known not to support a submitted value. Its
// message is deliberately constant and bounded: upstream diagnostics and the
// submitted value are never exposed.
type ReasoningMetadataUnavailableError struct{}

func (*ReasoningMetadataUnavailableError) Error() string {
	return "reasoning metadata is temporarily unavailable; use Auto or retry"
}

func (*ReasoningMetadataUnavailableError) ReasoningMetadataUnavailable() bool { return true }

func IsReasoningMetadataUnavailable(err error) bool {
	var unavailable *ReasoningMetadataUnavailableError
	return errors.As(err, &unavailable)
}

type reasoningTarget struct {
	providerID string
	model      string
	provider   config.Provider
}

// ReasoningCapability returns the best model-specific reasoning metadata the
// configured provider can supply. Documented direct-provider metadata avoids a
// network dependency; dynamic providers use the Agent-owned cached catalogue.
func (a *Agent) ReasoningCapability(ctx context.Context, modelRef string) (*llm.ReasoningCapability, error) {
	return a.reasoningCapabilityForConfig(ctx, a.config(), modelRef)
}

func (a *Agent) reasoningCapabilityForConfig(
	ctx context.Context,
	cfg *config.Config,
	modelRef string,
) (*llm.ReasoningCapability, error) {
	target := reasoningTargetForConfig(cfg, modelRef)
	if capability := staticReasoningCapability(target); capability != nil {
		return capability, nil
	}

	models, err := a.modelsForProvider(ctx, target.providerID, target.provider)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ReasoningMetadataUnavailableError{}
	}
	for _, model := range models {
		if model.ID == target.model && model.ReasoningCapability != nil {
			return model.ReasoningCapability, nil
		}
	}
	return nil, nil
}

// ValidateReasoningEffort validates an explicit value without including the
// submitted value in any error.
func (a *Agent) ValidateReasoningEffort(ctx context.Context, modelRef, effort string) error {
	return a.ValidateReasoningEffortForConfig(ctx, a.config(), modelRef, effort)
}

// ValidateReasoningEffortForConfig validates against an immutable candidate
// config while retaining the Agent-owned provider catalogue cache. It does not
// publish the candidate as the live Agent configuration.
func (a *Agent) ValidateReasoningEffortForConfig(
	ctx context.Context,
	cfg *config.Config,
	modelRef string,
	effort string,
) error {
	if effort == "" {
		return nil
	}
	capability, err := a.reasoningCapabilityForConfig(ctx, cfg, modelRef)
	if err != nil {
		return err
	}
	return llm.ValidateReasoningEffort(reasoningTargetForConfig(cfg, modelRef).model, capability, effort)
}

// resolveReasoning distinguishes a new explicit override from stored legacy
// values. An invalid explicit override is an error; invalid stored values are
// skipped in role, agent, model order so old configuration degrades to Auto.
func (a *Agent) resolveReasoning(ctx context.Context, in reasoningInput) (reasoningResolution, error) {
	agentValue := in.Agent
	if agentValue == "" {
		agentValue = a.config().Agent.ReasoningEffort
	}
	modelValue := in.Model
	if modelValue == "" {
		modelValue = a.config().Model.ReasoningEffort
	}
	storedValues := []string{in.Role, agentValue, modelValue}

	capability, err := a.ReasoningCapability(ctx, in.ModelRef)
	if err != nil {
		if IsReasoningMetadataUnavailable(err) && in.Explicit == "" {
			resolution := reasoningResolution{}
			for _, stored := range storedValues {
				if stored != "" {
					resolution.DiscardedLegacy = stored
					break
				}
			}
			return resolution, nil
		}
		return reasoningResolution{}, err
	}
	resolution := reasoningResolution{Capability: capability}
	model := a.reasoningTarget(in.ModelRef).model

	if in.Explicit != "" {
		if err := llm.ValidateReasoningEffort(model, capability, in.Explicit); err != nil {
			return reasoningResolution{}, err
		}
		resolution.Value = in.Explicit
		return resolution, nil
	}

	for _, stored := range storedValues {
		if stored == "" {
			continue
		}
		if err := llm.ValidateReasoningEffort(model, capability, stored); err == nil {
			resolution.Value = stored
			return resolution, nil
		}
		if resolution.DiscardedLegacy == "" {
			resolution.DiscardedLegacy = stored
		}
	}
	return resolution, nil
}

func (a *Agent) reasoningTarget(modelRef string) reasoningTarget {
	cfg := a.config()
	return reasoningTargetForConfig(cfg, modelRef)
}

func reasoningTargetForConfig(cfg *config.Config, modelRef string) reasoningTarget {
	if cfg == nil {
		return reasoningTarget{model: modelRef}
	}
	providerID := cfg.Model.Provider
	model := modelRef
	if model == "" {
		model = cfg.Model.Default
	}
	if modelRef != "" {
		if candidate, rest, ok := strings.Cut(modelRef, "/"); ok && rest != "" {
			if _, configured := cfg.Providers[candidate]; configured {
				providerID, model = candidate, rest
			} else if candidate == cfg.Model.Provider || candidate == "google" {
				// "google/model" is the canonical direct-Gemini reference even
				// though the shipped provider map uses the key "gemini".
				providerID, model = candidate, rest
			}
		}
	}

	id, provider := cfg.ResolveProvider(providerID)
	if id == "google" {
		if _, configured := cfg.Providers[id]; !configured {
			provider = config.Provider{
				Kind:    "gemini",
				BaseURL: "https://generativelanguage.googleapis.com/v1beta",
				Enabled: true,
			}
		}
	}
	return reasoningTarget{providerID: id, model: model, provider: provider}
}

func staticReasoningCapability(target reasoningTarget) *llm.ReasoningCapability {
	kind := strings.ToLower(strings.TrimSpace(target.provider.Kind))
	switch kind {
	case "google":
		kind = "gemini"
	case "claude":
		kind = "anthropic"
	case "responses", "openai-responses":
		kind = "codex"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(target.provider.BaseURL), "/")
	if baseURL == "" {
		switch kind {
		case "openai", "codex":
			baseURL = "https://api.openai.com/v1"
		case "anthropic":
			baseURL = "https://api.anthropic.com/v1"
		case "gemini":
			baseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
	}
	return llm.StaticReasoningCapability(
		kind,
		target.providerID,
		baseURL,
		target.model,
	)
}
