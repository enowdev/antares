package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

func TestResolveReasoningExplicitUnsupportedReturnsError(t *testing.T) {
	a := agentWithConfig(config.Default())
	_, err := a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "google/gemini-3.6-flash",
		Explicit: "max",
	})
	if err == nil || !llm.IsUnsupportedReasoningEffort(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveReasoningUnsupportedStoredValueFallsBackToAuto(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Provider = "google"
	cfg.Model.Default = "gemini-3.6-flash"
	cfg.Agent.ReasoningEffort = "max"
	a := agentWithConfig(cfg)
	got, err := a.resolveReasoning(context.Background(), reasoningInput{ModelRef: cfg.Model.Default})
	if err != nil || got.Value != "" || got.DiscardedLegacy != "max" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestResolveReasoningUsesRoleAgentModelPrecedence(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Provider = "gemini"
	cfg.Model.Default = "gemini-3.6-flash"
	a := agentWithConfig(cfg)

	got, err := a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "gemini/gemini-3.6-flash",
		Role:     "high",
		Agent:    "medium",
		Model:    "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "high" || got.DiscardedLegacy != "" {
		t.Fatalf("got = %+v, want role value high", got)
	}
}

func TestResolveReasoningSkipsUnsupportedStoredValuesInPrecedenceOrder(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Provider = "gemini"
	cfg.Model.Default = "gemini-3.6-flash"
	a := agentWithConfig(cfg)

	got, err := a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "gemini/gemini-3.6-flash",
		Role:     "MAX",
		Agent:    "medium",
		Model:    "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "medium" || got.DiscardedLegacy != "MAX" {
		t.Fatalf("got = %+v, want agent value medium after discarding exact role value MAX", got)
	}
}

func TestResolveReasoningPreservesOpaqueLiveValueAndCase(t *testing.T) {
	srv := newReasoningModelsServer(t, `{
		"data": [
			{"id": "model-a", "reasoning": {"supported_efforts": ["low"], "default_effort": "low"}},
			{"id": "model-b", "reasoning": {"supported_efforts": ["MiXeD"], "default_effort": "MiXeD"}}
		]
	}`)
	cfg := reasoningTestConfig(srv.URL, nil)
	a := agentWithConfig(cfg)

	got, err := a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "router/model-b",
		Explicit: "MiXeD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "MiXeD" || got.Capability == nil || got.Capability.Source != llm.ReasoningCapabilityLive {
		t.Fatalf("got = %+v", got)
	}

	_, err = a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "router/model-b",
		Explicit: "mixed",
	})
	if err == nil || !llm.IsUnsupportedReasoningEffort(err) {
		t.Fatalf("case-changed value err = %v", err)
	}
}

func TestValidateReasoningEffortDoesNotExposeSubmittedValue(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Provider = "gemini"
	a := agentWithConfig(cfg)
	const submitted = "secret-invalid-effort"

	err := a.ValidateReasoningEffort(context.Background(), "gemini/gemini-3.6-flash", submitted)
	if err == nil || !llm.IsUnsupportedReasoningEffort(err) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), submitted) {
		t.Fatalf("error exposes submitted value: %v", err)
	}
}

func TestReasoningCapabilityUsesMatchingModelLiveMetadata(t *testing.T) {
	srv := newReasoningModelsServer(t, `{
		"data": [
			{"id": "model-a", "reasoning": {"supported_efforts": ["low"], "default_effort": "low"}},
			{"id": "model-b", "reasoning": {"supported_efforts": ["MiXeD"], "default_effort": "MiXeD"}}
		]
	}`)
	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))

	capability, err := a.ReasoningCapability(context.Background(), "router/model-b")
	if err != nil {
		t.Fatal(err)
	}
	if capability == nil || capability.Source != llm.ReasoningCapabilityLive {
		t.Fatalf("capability = %#v", capability)
	}
	if len(capability.Values) != 1 || capability.Values[0].Value != "MiXeD" {
		t.Fatalf("values = %#v, want exact model-b metadata", capability.Values)
	}
}

func TestReasoningCapabilityResolvesInlineActiveProviderModelRef(t *testing.T) {
	srv := newReasoningModelsServer(t, `{
		"data": [
			{"id": "model-a", "reasoning": {"supported_efforts": ["Exact"], "default_effort": "Exact"}}
		]
	}`)
	cfg := config.Default()
	cfg.Providers = map[string]config.Provider{}
	cfg.Model.Provider = "inline"
	cfg.Model.Default = "model-a"
	cfg.Model.BaseURL = srv.URL
	a := agentWithConfig(cfg)

	capability, err := a.ReasoningCapability(context.Background(), "inline/model-a")
	if err != nil {
		t.Fatal(err)
	}
	if capability == nil || len(capability.Values) != 1 || capability.Values[0].Value != "Exact" {
		t.Fatalf("capability = %#v", capability)
	}
}

func TestModelsKeepsCuratedWhitelistWhileUsingMatchingLiveCapability(t *testing.T) {
	srv := newReasoningModelsServer(t, `{
		"data": [
			{"id": "listed", "reasoning": {"supported_efforts": ["HIGH"], "default_effort": "HIGH"}},
			{"id": "unlisted", "reasoning": {"supported_efforts": ["low"], "default_effort": "low"}}
		]
	}`)
	cfg := reasoningTestConfig(srv.URL, []string{"listed"})
	a := agentWithConfig(cfg)

	models, err := a.Models(context.Background(), "router")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "listed" {
		t.Fatalf("models = %#v, want only curated model", models)
	}
	capability := models[0].ReasoningCapability
	if capability == nil || capability.Source != llm.ReasoningCapabilityLive ||
		len(capability.Values) != 1 || capability.Values[0].Value != "HIGH" {
		t.Fatalf("capability = %#v", capability)
	}
}

func TestModelsFallsBackToStaticCapabilityForCuratedModel(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["gemini"] = config.Provider{
		Kind:    "gemini",
		Enabled: true,
		Models:  []string{"gemini-3.6-flash"},
	}
	a := agentWithConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	models, err := a.Models(ctx, "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %#v", models)
	}
	capability := models[0].ReasoningCapability
	if capability == nil || capability.Source != llm.ReasoningCapabilityStatic {
		t.Fatalf("capability = %#v, want static fallback", capability)
	}
}

type modelCatalogueRoundTripFunc func(*http.Request) (*http.Response, error)

func (f modelCatalogueRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func installStaticFamilyModelCatalogue(t *testing.T) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = modelCatalogueRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := `{"data":[{"id":"gpt-5"},{"id":"gpt-5.3-codex"}]}`
		if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/models") {
			status = http.StatusNotFound
			body = `{"error":{"message":"not found"}}`
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = previous
	})
}

func assertReasoningValues(t *testing.T, capability *llm.ReasoningCapability, want ...string) {
	t.Helper()
	if capability == nil {
		t.Fatalf("capability = nil, want values %v", want)
	}
	if len(capability.Values) != len(want) {
		t.Fatalf("values = %#v, want %v", capability.Values, want)
	}
	for i, value := range capability.Values {
		if value.Value != want[i] {
			t.Fatalf("values = %#v, want %v", capability.Values, want)
		}
	}
}

func modelInfoByID(t *testing.T, models []llm.ModelInfo, id string) llm.ModelInfo {
	t.Helper()
	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("models = %#v, want %q", models, id)
	return llm.ModelInfo{}
}

func TestModelsUsesResolvedOpenAIAdapterFamilyForDirectOpenAI(t *testing.T) {
	installStaticFamilyModelCatalogue(t)
	for _, configuredKind := range []string{"openai", "openai-compatible"} {
		t.Run(configuredKind, func(t *testing.T) {
			cfg := config.Default()
			cfg.Model.Provider = "openai"
			cfg.Model.Default = "gpt-5"
			cfg.Providers = map[string]config.Provider{
				"openai": {
					Kind:    configuredKind,
					BaseURL: "https://api.openai.com/v1",
					Enabled: true,
				},
			}

			a := agentWithConfig(cfg)
			models, err := a.Models(context.Background(), "openai")
			if err != nil {
				t.Fatal(err)
			}
			model := modelInfoByID(t, models, "gpt-5")
			assertReasoningValues(t, model.ReasoningCapability, "minimal", "low", "medium", "high")
			capability, err := a.ReasoningCapability(context.Background(), "openai/gpt-5")
			if err != nil {
				t.Fatal(err)
			}
			assertReasoningValues(t, capability, "minimal", "low", "medium", "high")
		})
	}
}

func TestModelsKeepsExplicitCodexFamilyForResponsesAlias(t *testing.T) {
	installStaticFamilyModelCatalogue(t)
	for _, configuredKind := range []string{"codex", "responses", "openai-responses"} {
		t.Run(configuredKind, func(t *testing.T) {
			cfg := config.Default()
			cfg.Model.Provider = "openai"
			cfg.Model.Default = "gpt-5.3-codex"
			cfg.Providers = map[string]config.Provider{
				"openai": {
					Kind:    configuredKind,
					BaseURL: "https://api.openai.com/v1",
					Enabled: true,
					Models:  []string{"gpt-5.3-codex", "gpt-5"},
				},
			}

			a := agentWithConfig(cfg)
			models, err := a.Models(context.Background(), "openai")
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 2 {
				t.Fatalf("models = %#v, want two curated models", models)
			}
			codex := modelInfoByID(t, models, "gpt-5.3-codex")
			assertReasoningValues(t, codex.ReasoningCapability, "low", "medium", "high", "xhigh")
			openAI := modelInfoByID(t, models, "gpt-5")
			if openAI.ReasoningCapability != nil {
				t.Fatalf("%s alias broadened gpt-5 to OpenAI capability: %#v", configuredKind, openAI.ReasoningCapability)
			}
			capability, err := a.ReasoningCapability(context.Background(), "openai/gpt-5.3-codex")
			if err != nil {
				t.Fatal(err)
			}
			assertReasoningValues(t, capability, "low", "medium", "high", "xhigh")
		})
	}
}

func TestModelsKeepsUnknownCompatibleEndpointAutoOnly(t *testing.T) {
	installStaticFamilyModelCatalogue(t)
	cfg := config.Default()
	cfg.Model.Provider = "custom"
	cfg.Model.Default = "gpt-5"
	cfg.Providers = map[string]config.Provider{
		"custom": {
			Kind:    "custom",
			BaseURL: "https://gateway.example.test/v1",
			Enabled: true,
		},
	}

	a := agentWithConfig(cfg)
	models, err := a.Models(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	model := modelInfoByID(t, models, "gpt-5")
	if model.ReasoningCapability != nil {
		t.Fatalf("capability = %#v, want Auto-only", model.ReasoningCapability)
	}
	capability, err := a.ReasoningCapability(context.Background(), "custom/gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if capability != nil {
		t.Fatalf("resolved capability = %#v, want Auto-only", capability)
	}
}

func TestRunResolvesStoredRoleReasoningOnceBeforeTurnLoop(t *testing.T) {
	var (
		mu         sync.Mutex
		chatBodies []map[string]any
		chatCalls  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "model-a", "reasoning": {"supported_efforts": ["low"], "default_effort": "low"}}
				]
			}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode chat body: %v", err)
			}
			mu.Lock()
			chatBodies = append(chatBodies, body)
			chatCalls++
			call := chatCalls
			mu.Unlock()
			if call == 1 {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := reasoningTestConfig(srv.URL, nil)
	cfg.Agent.ReasoningEffort = "low"
	cfg.Model.ReasoningEffort = "low"
	cfg.Streaming.Enabled = false
	a := newReasoningRunAgent(t, cfg)

	var discardedNotices int
	result, err := a.Run(context.Background(), Request{
		Message:  "test",
		Role:     "reviewer",
		Quiet:    true,
		MaxTurns: 2,
	}, func(event Event) error {
		if event.Type == EventNotice && strings.Contains(event.Message, "high") {
			discardedNotices++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "ok" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if discardedNotices != 1 {
		t.Fatalf("discarded-role notices = %d, want one", discardedNotices)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chatBodies) != 2 {
		t.Fatalf("chat calls = %d, want two", len(chatBodies))
	}
	for i, body := range chatBodies {
		if body["reasoning_effort"] != "low" {
			t.Fatalf("chat body %d reasoning_effort = %#v, want low", i+1, body["reasoning_effort"])
		}
	}
}

func TestRunCarriesMatchingReasoningCapabilityThroughFallbackEntries(t *testing.T) {
	var (
		primaryModels atomic.Int32
		primaryChats  atomic.Int32
	)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			primaryModels.Add(1)
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "primary-model", "reasoning": {"supported_efforts": ["HIGH"], "default_effort": "HIGH"}}
				]
			}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			primaryChats.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"primary unavailable"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer primary.Close()

	var (
		mu             sync.Mutex
		fallbackEffort any
		fallbackModels atomic.Int32
	)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			fallbackModels.Add(1)
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "fallback-model", "reasoning": {"supported_efforts": ["HIGH"], "default_effort": "HIGH"}}
				]
			}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode fallback body: %v", err)
			}
			mu.Lock()
			fallbackEffort = body["reasoning_effort"]
			mu.Unlock()
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fallback ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fallback.Close()

	cfg := config.Default()
	cfg.Model.Provider = "primary"
	cfg.Model.Default = "primary-model"
	cfg.Model.Fallback = []string{"backup/fallback-model"}
	cfg.Model.MaxRetries = -1
	cfg.Model.ReasoningEffort = ""
	cfg.Agent.ReasoningEffort = "HIGH"
	cfg.Streaming.Enabled = false
	cfg.Providers = map[string]config.Provider{
		"primary": {
			Kind:    "openai-compatible",
			BaseURL: primary.URL,
			Enabled: true,
		},
		"backup": {
			Kind:    "openai-compatible",
			BaseURL: fallback.URL,
			Enabled: true,
		},
	}
	a := newReasoningRunAgent(t, cfg)

	result, err := a.Run(context.Background(), Request{
		Message:  "test fallback",
		Quiet:    true,
		MaxTurns: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "fallback ok" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if got := primaryChats.Load(); got != 1 {
		t.Fatalf("primary chat calls = %d, want one", got)
	}
	if got := primaryModels.Load(); got != 1 {
		t.Fatalf("primary catalogue fetches = %d, want one shared by fallback setup and run resolution", got)
	}
	if got := fallbackModels.Load(); got != 1 {
		t.Fatalf("fallback catalogue fetches = %d, want one", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if fallbackEffort != "HIGH" {
		t.Fatalf("fallback reasoning_effort = %#v, want exact live value HIGH", fallbackEffort)
	}
}

func reasoningTestConfig(baseURL string, curated []string) *config.Config {
	cfg := config.Default()
	cfg.Model.Provider = "router"
	cfg.Model.Default = "model-a"
	cfg.Model.MaxRetries = -1
	cfg.Model.ReasoningEffort = ""
	cfg.Agent.ReasoningEffort = ""
	cfg.Providers = map[string]config.Provider{
		"router": {
			Kind:    "openai-compatible",
			BaseURL: baseURL,
			Enabled: true,
			Models:  curated,
		},
	}
	return cfg
}

func newReasoningModelsServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
}

func newReasoningRunAgent(t *testing.T, cfg *config.Config) *Agent {
	t.Helper()
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(cfg, db, tools.NewRegistry(), nil, nil)
}
