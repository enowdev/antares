package llm

import (
	"strings"
	"time"
)

type reasoningCapabilityEntry struct {
	model  string
	values []ReasoningValue
}

var openAIReasoningCapabilities = []reasoningCapabilityEntry{
	// https://platform.openai.com/docs/guides/reasoning
	{model: "gpt-5", values: reasoningValuesFor("minimal", "low", "medium", "high")},
}

var codexReasoningCapabilities = []reasoningCapabilityEntry{
	// https://platform.openai.com/docs/models/gpt-5.3-codex
	{model: "gpt-5.3-codex", values: reasoningValuesFor("low", "medium", "high", "xhigh")},
}

var anthropicReasoningCapabilities = []reasoningCapabilityEntry{
	// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking
	{model: "claude-sonnet-5", values: reasoningValuesFor("low", "medium", "high", "xhigh", "max")},
}

var geminiReasoningCapabilities = []reasoningCapabilityEntry{
	// https://ai.google.dev/gemini-api/docs/thinking
	{model: "gemini-3.6-flash", values: reasoningValuesFor("minimal", "low", "medium", "high")},
}

func reasoningValuesFor(values ...string) []ReasoningValue {
	out := make([]ReasoningValue, 0, len(values))
	for _, value := range values {
		out = append(out, ReasoningValue{Value: value, Label: strings.ToUpper(value)})
	}
	return out
}

// StaticReasoningCapability resolves documented model capabilities when a
// provider cannot supply live metadata. It deliberately recognises only direct
// vendor adapters and explicit model families; OpenAI-compatible endpoints
// remain Auto-only because their upstream capabilities are unknowable.
func StaticReasoningCapability(kind, provider, baseURL, model string) *ReasoningCapability {
	var entries []reasoningCapabilityEntry
	switch {
	case kind == "openai" && provider == "openai" && baseURL == "https://api.openai.com/v1":
		entries = openAIReasoningCapabilities
	case kind == "codex" && provider == "openai" && baseURL == "https://api.openai.com/v1":
		entries = codexReasoningCapabilities
	case kind == "anthropic" && provider == "anthropic" && isAnthropicDirectBaseURL(baseURL):
		entries = anthropicReasoningCapabilities
	case kind == "gemini" && provider == "google" && baseURL == "https://generativelanguage.googleapis.com/v1beta":
		entries = geminiReasoningCapabilities
	default:
		return nil
	}

	for _, entry := range entries {
		if exactModelOrDatedSnapshot(entry.model, model) {
			capability, err := NewReasoningCapability(entry.values, entry.values[0].Value, false, ReasoningCapabilityStatic)
			if err != nil {
				panic(err)
			}
			return capability
		}
	}
	return nil
}

// isAnthropicDirectBaseURL recognises both forms of Anthropic's own base URL
// seen at runtime: the bare host (as documented) and the "/v1" form that this
// codebase's provider defaults actually configure. Both are exact, canonical
// Anthropic hosts — this is not a broad prefix match, so a custom or
// Anthropic-compatible endpoint still falls back to Auto-only.
func isAnthropicDirectBaseURL(baseURL string) bool {
	return baseURL == "https://api.anthropic.com" || baseURL == "https://api.anthropic.com/v1"
}

func exactModelOrDatedSnapshot(family, model string) bool {
	if model == family {
		return true
	}
	snapshot, ok := strings.CutPrefix(model, family+"-")
	if !ok {
		return false
	}
	_, err := time.Parse("2006-01-02", snapshot)
	return err == nil
}
