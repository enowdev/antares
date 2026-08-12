package llm

import "strings"

// reasoningValue resolves and validates the effort value to send upstream.
// Auto (an empty ReasoningEffort) is always valid and produces no override. A
// non-empty value is validated against the request's attached capability,
// falling back to the static catalogue when the caller did not attach one.
// Callers wired through the agent layer normally attach an already-resolved
// capability; the fallback is defense in depth for direct/test callers.
func reasoningValue(req Request, kind, providerID, baseURL string) (string, error) {
	value := req.ReasoningEffort
	if value == "" {
		return "", nil
	}
	capability := req.ReasoningCapability
	if capability == nil {
		capability = StaticReasoningCapability(kind, providerID, baseURL, req.Model)
	}
	if err := ValidateReasoningEffort(req.Model, capability, value); err != nil {
		return "", err
	}
	return value, nil
}

// resolvedReasoningCapability mirrors reasoningValue's capability resolution
// so adapters can inspect capability metadata (such as the marked disable
// value) once a value has already been validated.
func resolvedReasoningCapability(req Request, kind, providerID, baseURL string) *ReasoningCapability {
	if req.ReasoningCapability != nil {
		return req.ReasoningCapability
	}
	return StaticReasoningCapability(kind, providerID, baseURL, req.Model)
}

// reasoningDisableValue returns the capability's marked disable value, or ""
// when the capability cannot disable reasoning.
func reasoningDisableValue(capability *ReasoningCapability) string {
	if capability == nil {
		return ""
	}
	for _, v := range capability.Values {
		if v.Kind == ReasoningValueDisable {
			return v.Value
		}
	}
	return ""
}

// openRouterReasoningMetadata is OpenRouter's per-model reasoning metadata
// returned from GET /models.
// https://openrouter.ai/docs/guides/best-practices/reasoning-tokens
type openRouterReasoningMetadata struct {
	SupportedEfforts  []string `json:"supported_efforts"`
	DefaultEffort     string   `json:"default_effort"`
	DefaultEnabled    *bool    `json:"default_enabled"`
	Mandatory         bool     `json:"mandatory"`
	SupportsMaxTokens bool     `json:"supports_max_tokens"`
}

// openRouterReasoningCapability builds a live capability from OpenRouter's
// documented reasoning metadata. Only the documented "none" value is marked
// as a disable choice. Contradictory metadata — a mandatory model that also
// lists "none", or a shape NewReasoningCapability otherwise rejects — yields
// no capability rather than being silently repaired.
func openRouterReasoningCapability(meta *openRouterReasoningMetadata) *ReasoningCapability {
	if meta == nil || len(meta.SupportedEfforts) == 0 {
		return nil
	}
	if meta.Mandatory {
		for _, effort := range meta.SupportedEfforts {
			if effort == "none" {
				return nil
			}
		}
	}
	values := make([]ReasoningValue, 0, len(meta.SupportedEfforts))
	for _, effort := range meta.SupportedEfforts {
		value := ReasoningValue{Value: effort, Label: strings.ToUpper(effort)}
		if effort == "none" {
			value.Kind = ReasoningValueDisable
		}
		values = append(values, value)
	}
	def := meta.DefaultEffort
	if def == "" {
		def = meta.SupportedEfforts[0]
	}
	capability, err := NewReasoningCapability(values, def, meta.Mandatory, ReasoningCapabilityLive)
	if err != nil {
		return nil
	}
	return capability
}
