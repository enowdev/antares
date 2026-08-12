package llm

import (
	"fmt"
)

// ReasoningValueKind describes special behavior associated with a reasoning
// effort value.
type ReasoningValueKind string

const ReasoningValueDisable ReasoningValueKind = "disable"

// ReasoningValue is one provider-defined reasoning effort. Value is opaque:
// callers must retain its spelling and case when sending it to a provider.
type ReasoningValue struct {
	Value string             `json:"value"`
	Label string             `json:"label"`
	Kind  ReasoningValueKind `json:"kind,omitempty"`
}

// ReasoningCapabilitySource identifies how the capability was obtained.
type ReasoningCapabilitySource string

const (
	ReasoningCapabilityLive   ReasoningCapabilitySource = "live"
	ReasoningCapabilityStatic ReasoningCapabilitySource = "static"
)

// ReasoningCapability describes the effort values supported by one model.
type ReasoningCapability struct {
	Values     []ReasoningValue          `json:"values"`
	Default    string                    `json:"default,omitempty"`
	Mandatory  bool                      `json:"mandatory"`
	CanDisable bool                      `json:"can_disable"`
	Source     ReasoningCapabilitySource `json:"source"`
}

// NewReasoningCapability constructs a valid immutable-by-convention capability.
func NewReasoningCapability(values []ReasoningValue, defaultValue string, mandatory bool, source ReasoningCapabilitySource) (*ReasoningCapability, error) {
	allowed := make(map[string]struct{}, len(values))
	disableCount := 0
	for _, value := range values {
		if value.Value == "" {
			return nil, fmt.Errorf("reasoning capability contains an empty value")
		}
		if _, exists := allowed[value.Value]; exists {
			return nil, fmt.Errorf("reasoning capability contains duplicate value %q", value.Value)
		}
		allowed[value.Value] = struct{}{}
		switch value.Kind {
		case "":
		case ReasoningValueDisable:
			disableCount++
		default:
			return nil, fmt.Errorf("reasoning capability value %q has unknown kind %q", value.Value, value.Kind)
		}
	}
	if disableCount > 1 {
		return nil, fmt.Errorf("reasoning capability contains multiple disable values")
	}
	if mandatory && disableCount != 0 {
		return nil, fmt.Errorf("mandatory reasoning capability cannot include a disable value")
	}
	if defaultValue == "" {
		return nil, fmt.Errorf("reasoning capability default is required")
	}
	if _, ok := allowed[defaultValue]; !ok {
		return nil, fmt.Errorf("reasoning capability default %q is not allowed", defaultValue)
	}

	return &ReasoningCapability{
		Values:     append([]ReasoningValue(nil), values...),
		Default:    defaultValue,
		Mandatory:  mandatory,
		CanDisable: disableCount == 1,
		Source:     source,
	}, nil
}

// UnsupportedReasoningEffortError reports an override not advertised by a model.
type UnsupportedReasoningEffortError struct {
	Model   string
	Allowed []string
}

func (e *UnsupportedReasoningEffortError) Error() string {
	return fmt.Sprintf("unsupported reasoning override for model %q (allowed: %v)", e.Model, e.Allowed)
}

// ValidateReasoningEffort accepts Auto (an empty effort) for every model and
// otherwise requires an exact, advertised opaque value.
func ValidateReasoningEffort(model string, capability *ReasoningCapability, effort string) error {
	if effort == "" {
		return nil
	}
	allowed := make([]string, 0)
	if capability != nil {
		for _, value := range capability.Values {
			allowed = append(allowed, value.Value)
			if effort == value.Value {
				return nil
			}
		}
	}
	return &UnsupportedReasoningEffortError{Model: model, Allowed: allowed}
}
