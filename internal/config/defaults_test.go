package config

import "testing"

// TestDefaultReasoningEffortIsAuto guards against a regression where the
// fresh-install default carried a non-empty reasoning effort ("medium").
// Reasoning effort is now a provider/model-specific opaque value validated
// pre-request; OpenRouter alone can route to thousands of backing models
// with unknown or absent reasoning ladders, so any non-empty default here
// would fail validation before every default chat reached the network.
// Auto ("") is the only value guaranteed to be valid everywhere.
func TestDefaultReasoningEffortIsAuto(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ReasoningEffort != "" {
		t.Fatalf("Agent.ReasoningEffort = %q, want empty (Auto)", cfg.Agent.ReasoningEffort)
	}
	if cfg.Model.ReasoningEffort != "" {
		t.Fatalf("Model.ReasoningEffort = %q, want empty (Auto)", cfg.Model.ReasoningEffort)
	}
}
