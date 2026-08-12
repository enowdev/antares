package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestOpenRouterReasoningBodySendsExplicitDisable(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "none", Label: "Off", Kind: ReasoningValueDisable},
			{Value: "high", Label: "High"},
		},
		"high", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	c := &openAIClient{opts: Options{BaseURL: "https://openrouter.ai/api/v1"}}
	body := c.buildBody(Request{
		Model: "vendor/model", ReasoningEffort: "none", ReasoningCapability: cap,
	}, false)
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", body["reasoning"])
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("unexpected reasoning_effort alongside reasoning: %#v", body["reasoning_effort"])
	}
}

func TestReasoningAutoOmitsProviderFields(t *testing.T) {
	req := Request{Model: "gpt-5", ReasoningEffort: ""}
	if body := (&openAIClient{}).buildBody(req, false); body["reasoning_effort"] != nil {
		t.Fatalf("OpenAI body = %#v", body)
	}
	if body := (&codexClient{}).buildBody(req, false); body["reasoning"] != nil {
		t.Fatalf("Codex body = %#v", body)
	}
}

func TestOpenAIReasoningEffortBody(t *testing.T) {
	cap := StaticReasoningCapability("openai", "openai", "https://api.openai.com/v1", "gpt-5")
	if cap == nil {
		t.Fatal("expected a static capability for gpt-5")
	}
	c := &openAIClient{opts: Options{BaseURL: "https://api.openai.com/v1", ProviderID: "openai"}, vendor: "openai"}
	body := c.buildBody(Request{Model: "gpt-5", ReasoningEffort: "high", ReasoningCapability: cap}, false)
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("unexpected reasoning field: %#v", body["reasoning"])
	}
}

func TestOpenAIReasoningEffortFallsBackToStaticCapability(t *testing.T) {
	c := &openAIClient{opts: Options{BaseURL: "https://api.openai.com/v1", ProviderID: "openai"}, vendor: "openai"}
	body := c.buildBody(Request{Model: "gpt-5", ReasoningEffort: "minimal"}, false)
	if body["reasoning_effort"] != "minimal" {
		t.Fatalf("reasoning_effort = %#v, want fallback to static capability to validate it", body["reasoning_effort"])
	}
}

func TestCodexReasoningEffortBody(t *testing.T) {
	c := &codexClient{opts: Options{BaseURL: "https://api.openai.com/v1", ProviderID: "openai"}}
	body := c.buildBody(Request{Model: "gpt-5.3-codex", ReasoningEffort: "xhigh"}, false)
	want := map[string]any{"effort": "xhigh"}
	if got := body["reasoning"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("reasoning = %#v, want %#v", got, want)
	}
}

func TestAnthropicAdaptiveThinkingBody(t *testing.T) {
	// The upstream base URL is corrected to the exact static-catalogue key
	// (https://api.anthropic.com) so this capability actually resolves;
	// see task-2-report.md for why the brief's literal "" argument is a
	// vacuous match against the Task 1 catalogue.
	cap := StaticReasoningCapability("anthropic", "anthropic", "https://api.anthropic.com", "claude-sonnet-5")
	if cap == nil {
		t.Fatal("expected a static capability for claude-sonnet-5")
	}
	body := (&anthropicClient{}).buildBody(Request{
		Model: "claude-sonnet-5", ReasoningEffort: "xhigh", ReasoningCapability: cap,
	}, false)
	if got, want := body["thinking"], map[string]any{"type": "adaptive"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thinking = %#v, want %#v", got, want)
	}
	if got, want := body["output_config"], map[string]any{"effort": "xhigh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output_config = %#v, want %#v", got, want)
	}
}

func TestAnthropicLegacyThinkingBudgetBody(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "medium", Label: "Medium"}, {Value: "high", Label: "High"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	body := (&anthropicClient{}).buildBody(Request{
		Model: "claude-3-7-sonnet", ReasoningEffort: "medium", ReasoningCapability: cap,
	}, false)
	if got, want := body["thinking"], map[string]any{"type": "enabled", "budget_tokens": 8192}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thinking = %#v, want %#v", got, want)
	}
	if _, ok := body["output_config"]; ok {
		t.Fatalf("unexpected output_config on a legacy model: %#v", body["output_config"])
	}
}

// TestAnthropicLegacyUnmappableEffortFailsBeforeRequest guards a silent-drop
// bug: a value can pass validation against an *attached* capability (e.g. a
// hypothetical live capability advertising more values than this codebase's
// hardcoded legacy budget table knows) yet have no entry in
// anthropicLegacyThinkingBudgets for that model family. buildBody previously
// just omitted "thinking" in that case, silently downgrading the turn to
// Auto. Chat/Stream must now fail before any request is sent.
func TestAnthropicLegacyUnmappableEffortFailsBeforeRequest(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "xhigh", Label: "Extra High"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := &anthropicClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}}
	_, err = c.Chat(context.Background(), Request{
		Model: "claude-3-7-sonnet", ReasoningEffort: "xhigh", ReasoningCapability: cap,
	})
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("requests sent = %d, want 0", got)
	}

	// buildBody alone (bypassing Chat) must also stay silent-safe: it must not
	// fabricate a "thinking" override it cannot actually honor.
	body := (&anthropicClient{}).buildBody(Request{
		Model: "claude-3-7-sonnet", ReasoningEffort: "xhigh", ReasoningCapability: cap,
	}, false)
	if _, ok := body["thinking"]; ok {
		t.Fatalf("buildBody emitted a thinking override for an unmappable legacy effort: %#v", body["thinking"])
	}
}

func TestAnthropicAdaptiveDisableBody(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "off", Label: "Off", Kind: ReasoningValueDisable},
			{Value: "low", Label: "Low"},
		},
		"low", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	body := (&anthropicClient{}).buildBody(Request{
		Model: "claude-sonnet-5", ReasoningEffort: "off", ReasoningCapability: cap,
	}, false)
	if got, want := body["thinking"], map[string]any{"type": "disabled"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thinking = %#v, want %#v", got, want)
	}
	if _, ok := body["output_config"]; ok {
		t.Fatalf("unexpected output_config for a disabled turn: %#v", body["output_config"])
	}
}

func TestReasoningValidationBlocksRequestBeforeNetworkIO(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cap, err := NewReasoningCapability([]ReasoningValue{{Value: "low", Label: "Low"}}, "low", false, ReasoningCapabilityStatic)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "test-model", ReasoningEffort: "max", ReasoningCapability: cap}

	clients := map[string]Client{
		"openai":    &openAIClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}, vendor: "openai"},
		"codex":     &codexClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}},
		"anthropic": &anthropicClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}},
		"gemini":    &geminiClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}},
	}
	for name, c := range clients {
		_, err := c.Chat(context.Background(), req)
		var unsupported *UnsupportedReasoningEffortError
		if !errors.As(err, &unsupported) {
			t.Fatalf("%s: err = %v, want UnsupportedReasoningEffortError", name, err)
		}
	}
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Fatalf("requests sent = %d, want 0", got)
	}
}

func TestOpenRouterModelsBuildsLiveReasoningCapability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model","reasoning":{"supported_efforts":["none","low","high"],"default_effort":"high","mandatory":false,"supports_max_tokens":false}}]}`))
	}))
	defer srv.Close()
	c := &openAIClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}}
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	cap := models[0].ReasoningCapability
	if cap == nil {
		t.Fatal("expected a live reasoning capability")
	}
	if cap.Source != ReasoningCapabilityLive || cap.Default != "high" {
		t.Fatalf("capability = %#v", cap)
	}
	if !models[0].Reasoning {
		t.Fatal("expected legacy Reasoning boolean to be set")
	}
	var disableFound bool
	for _, v := range cap.Values {
		if v.Value == "none" {
			disableFound = v.Kind == ReasoningValueDisable
		}
	}
	if !disableFound {
		t.Fatal(`expected "none" marked as disable`)
	}
}

func TestOpenRouterModelsRejectsContradictoryReasoningMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model","reasoning":{"supported_efforts":["none","low"],"default_effort":"low","mandatory":true}}]}`))
	}))
	defer srv.Close()
	c := &openAIClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}}
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	if got := models[0].ReasoningCapability; got != nil {
		t.Fatalf("expected no capability for contradictory metadata, got %#v", got)
	}
}
