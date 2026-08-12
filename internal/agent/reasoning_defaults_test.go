package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

// TestDefaultConfigReasoningEffortReachesOpenRouterCompatibleProvider guards
// the exact regression a review found in Task 2's provider-adapter validation:
// with the old "medium" default and no attached llm.ReasoningCapability, every
// default chat through an openai-compatible provider (OpenRouter's kind) was
// rejected before any network call, because the static catalog deliberately
// returns nil for unknown compatible endpoints. It reproduces the precedence
// agent.go's Run loop uses (firstNonEmpty(explicit, agent, model)) with the
// real default config against a real openAIClient.
func TestDefaultConfigReasoningEffortReachesOpenRouterCompatibleProvider(t *testing.T) {
	cfg := config.Default()
	effort := firstNonEmpty("", cfg.Agent.ReasoningEffort, cfg.Model.ReasoningEffort)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	client, err := llm.New(llm.Options{Kind: "openai-compatible", BaseURL: srv.URL, HTTPClient: srv.Client(), ProviderID: "openrouter"})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}

	a := &Agent{}
	_, err = a.callModel(context.Background(), client, llm.Request{
		Model:           "vendor/model",
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		ReasoningEffort: effort,
	}, false, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("default config reasoning effort blocked an OpenRouter-shaped chat before it reached the network: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("requests reaching the provider = %d, want 1", got)
	}
}

// TestDefaultConfigReasoningEffortReachesDirectAnthropicProvider is the direct
// Anthropic half of the same finding: even a model the static catalog knows
// (claude-sonnet-5) was previously rejected pre-request because the runtime
// default base URL ("https://api.anthropic.com/v1") didn't match the
// catalog's bare-host check.
func TestDefaultConfigReasoningEffortReachesDirectAnthropicProvider(t *testing.T) {
	cfg := config.Default()
	effort := firstNonEmpty("", cfg.Agent.ReasoningEffort, cfg.Model.ReasoningEffort)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	client, err := llm.New(llm.Options{Kind: "anthropic", BaseURL: srv.URL, HTTPClient: srv.Client(), ProviderID: "anthropic"})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}

	a := &Agent{}
	_, err = a.callModel(context.Background(), client, llm.Request{
		Model:           "claude-sonnet-5",
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		ReasoningEffort: effort,
	}, false, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("default config reasoning effort blocked a direct-Anthropic chat before it reached the network: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("requests reaching the provider = %d, want 1", got)
	}
}
