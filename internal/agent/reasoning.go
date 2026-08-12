package agent

import (
	"context"
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

type reasoningTarget struct {
	providerID string
	model      string
	provider   config.Provider
}

// ReasoningCapability returns the best model-specific reasoning metadata the
// configured provider can supply. Documented direct-provider metadata avoids a
// network dependency; dynamic providers are queried through Agent.Models.
func (a *Agent) ReasoningCapability(ctx context.Context, modelRef string) (*llm.ReasoningCapability, error) {
	target := a.reasoningTarget(modelRef)
	if capability := staticReasoningCapability(target); capability != nil {
		return capability, nil
	}

	models, err := a.Models(ctx, target.providerID)
	if err != nil {
		// Model catalogues are optional. Unknown metadata means Auto-only; it
		// must not make an otherwise valid chat depend on a /models endpoint.
		return nil, nil
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
	capability, err := a.ReasoningCapability(ctx, modelRef)
	if err != nil {
		return err
	}
	return llm.ValidateReasoningEffort(a.reasoningTarget(modelRef).model, capability, effort)
}

// resolveReasoning distinguishes a new explicit override from stored legacy
// values. An invalid explicit override is an error; invalid stored values are
// skipped in role, agent, model order so old configuration degrades to Auto.
func (a *Agent) resolveReasoning(ctx context.Context, in reasoningInput) (reasoningResolution, error) {
	capability, err := a.ReasoningCapability(ctx, in.ModelRef)
	if err != nil {
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

	agentValue := in.Agent
	if agentValue == "" {
		agentValue = a.config().Agent.ReasoningEffort
	}
	modelValue := in.Model
	if modelValue == "" {
		modelValue = a.config().Model.ReasoningEffort
	}
	for _, stored := range []string{in.Role, agentValue, modelValue} {
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
