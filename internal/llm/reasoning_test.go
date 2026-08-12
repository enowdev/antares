package llm

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReasoningCapabilityRejectsInconsistentDisableMetadata(t *testing.T) {
	_, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "none", Label: "Off", Kind: ReasoningValueDisable}},
		"", true, ReasoningCapabilityStatic,
	)
	if err == nil || !strings.Contains(err.Error(), "mandatory") {
		t.Fatalf("err = %v, want mandatory/disable conflict", err)
	}
}

func TestValidateReasoningEffortPreservesOpaqueValues(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "extra-high", Label: "Extra High"},
			{Value: "xhigh", Label: "Extra High (new)"},
		},
		"extra-high", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReasoningEffort("gpt-example", cap, "extra-high"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReasoningEffort("gpt-example", cap, "EXTRA-HIGH"); err == nil {
		t.Fatal("case-normalized value was accepted")
	}
}

func TestValidateReasoningEffortAcceptsAuto(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReasoningEffort("gpt-example", cap, ""); err != nil {
		t.Fatalf("err = %v, want Auto accepted", err)
	}
}

func TestValidateReasoningEffortRejectsUnknownOverride(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateReasoningEffort("gpt-example", cap, "high")
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
	if unsupported.Model != "gpt-example" || !slices.Equal(unsupported.Allowed, []string{"low"}) {
		t.Fatalf("error = %#v", unsupported)
	}
}

func TestValidateReasoningEffortDoesNotExposeSubmittedOverride(t *testing.T) {
	const submitted = "reasoning-override-secret"
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateReasoningEffort("gpt-example", cap, submitted)
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
	if strings.Contains(err.Error(), submitted) {
		t.Fatalf("error leaks submitted override: %q", err)
	}
	if _, found := reflect.TypeOf(*unsupported).FieldByName("Effort"); found {
		t.Fatal("UnsupportedReasoningEffortError exposes the submitted override")
	}
}

func TestModelInfoWithReasoningCapabilityEnablesReasoning(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	info := (ModelInfo{ID: "gpt-example"}).WithReasoningCapability(cap)
	if !info.Reasoning {
		t.Fatal("Reasoning = false, want true when capability is attached")
	}
	if info.ReasoningCapability != cap {
		t.Fatalf("ReasoningCapability = %#v, want %#v", info.ReasoningCapability, cap)
	}
}

func TestReasoningCapabilityRequiresUniqueValues(t *testing.T) {
	_, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "low", Label: "Low again"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want duplicate value error", err)
	}
}

func TestReasoningCapabilityDefaultMustBeAllowed(t *testing.T) {
	_, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}},
		"high", false, ReasoningCapabilityStatic,
	)
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("err = %v, want unsupported default error", err)
	}
}

func TestStaticReasoningCapabilityRepresentativeFamilies(t *testing.T) {
	tests := []struct {
		kind, provider, baseURL, model string
		want                           []string
		disable                        bool
	}{
		{"openai", "openai", "https://api.openai.com/v1", "gpt-5", []string{"minimal", "low", "medium", "high"}, false},
		{"codex", "openai", "https://api.openai.com/v1", "gpt-5.3-codex", []string{"low", "medium", "high", "xhigh"}, false},
		{"anthropic", "anthropic", "https://api.anthropic.com", "claude-sonnet-5", []string{"low", "medium", "high", "xhigh", "max"}, false},
		{"gemini", "google", "https://generativelanguage.googleapis.com/v1beta", "gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}, false},
	}
	for _, tt := range tests {
		cap := StaticReasoningCapability(tt.kind, tt.provider, tt.baseURL, tt.model)
		if got := reasoningValues(cap); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.model, got, tt.want)
		}
		if cap == nil {
			t.Errorf("%s: got nil capability", tt.model)
		} else if cap.CanDisable != tt.disable {
			t.Errorf("%s: can_disable=%v", tt.model, cap.CanDisable)
		}
	}
}

// TestStaticReasoningCapabilityAcceptsRuntimeAnthropicBaseURL guards against a
// regression where every default-config, direct-Anthropic chat request failed
// pre-request validation: the provider default base URL configured in
// internal/config/defaults.go is "https://api.anthropic.com/v1", but the
// catalog originally matched only the bare "https://api.anthropic.com" host.
func TestStaticReasoningCapabilityAcceptsRuntimeAnthropicBaseURL(t *testing.T) {
	cap := StaticReasoningCapability("anthropic", "anthropic", "https://api.anthropic.com/v1", "claude-sonnet-5")
	if cap == nil {
		t.Fatal("got nil capability for the runtime default Anthropic base URL (with /v1)")
	}
}

func TestStaticReasoningCapabilityDoesNotGuessUnknownCompatibleModels(t *testing.T) {
	if got := StaticReasoningCapability("openai-compatible", "custom", "https://example.test/v1", "gpt-5"); got != nil {
		t.Fatalf("got %#v, want Auto-only", got)
	}
}

func reasoningValues(cap *ReasoningCapability) []string {
	if cap == nil {
		return nil
	}
	out := make([]string, 0, len(cap.Values))
	for _, value := range cap.Values {
		out = append(out, value.Value)
	}
	return out
}
