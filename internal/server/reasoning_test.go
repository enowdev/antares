package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/roles"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

func TestHandleModelListAllIncludesReasoningCapability(t *testing.T) {
	catalog := newServerReasoningCatalog(t, nil)
	s, _, _ := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Model.Provider = "router"
		cfg.Model.Default = "model-a"
		cfg.Providers = map[string]config.Provider{
			"router": {
				Kind:    "openai-compatible",
				BaseURL: catalog.URL,
				Enabled: true,
			},
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/model/list-all", nil)
	rec := httptest.NewRecorder()
	s.handleModelListAll(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Models []llm.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 1 || body.Models[0].ID != "model-a" {
		t.Fatalf("models = %#v", body.Models)
	}
	assertServerReasoningCapability(t, body.Models[0])
}

func TestHandleProviderModelInfoReadsModelQueryAndIncludesCapability(t *testing.T) {
	catalog := newServerReasoningCatalog(t, nil)
	s, _, _ := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"router": {
				Kind:    "openai-compatible",
				BaseURL: catalog.URL,
				Enabled: true,
			},
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/providers/router/model-info?model=model-a", nil)
	req.SetPathValue("id", "router")
	rec := httptest.NewRecorder()
	s.handleProviderModelInfo(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Found               bool                     `json:"found"`
		ID                  string                   `json:"id"`
		Reasoning           bool                     `json:"reasoning"`
		ReasoningCapability *llm.ReasoningCapability `json:"reasoning_capability"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Found || body.ID != "model-a" {
		t.Fatalf("response = %#v", body)
	}
	assertServerReasoningCapability(t, llm.ModelInfo{
		ID:                  body.ID,
		Reasoning:           body.Reasoning,
		ReasoningCapability: body.ReasoningCapability,
	})
}

func TestHandleChatRejectsUnsupportedReasoningBeforeChatRequest(t *testing.T) {
	var chatRequests atomic.Int32
	catalog := newServerReasoningCatalog(t, &chatRequests)
	s, _, _ := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Model.Provider = "router"
		cfg.Model.Default = "model-a"
		cfg.Providers = map[string]config.Provider{
			"router": {
				Kind:    "openai-compatible",
				BaseURL: catalog.URL,
				Enabled: true,
			},
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(`{"message":"hello","reasoning_effort":"unsupported"}`))
	rec := httptest.NewRecorder()
	s.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unsupported request opened SSE: %q", rec.Header().Get("Content-Type"))
	}
	if got := chatRequests.Load(); got != 0 {
		t.Fatalf("chat requests = %d, want 0", got)
	}
}

func TestHandleUpdateConfigRejectsChangedUnsupportedReasoningWithoutSaving(t *testing.T) {
	s, _, configPath := newReasoningBoundaryServer(t, nil)
	before := mustReadServerFile(t, configPath)

	req := httptest.NewRequest(http.MethodPost, "/api/config",
		strings.NewReader(`{"updates":{"model.reasoning_effort":"unsupported"}}`))
	rec := httptest.NewRecorder()
	s.handleUpdateConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if after := mustReadServerFile(t, configPath); !bytes.Equal(after, before) {
		t.Fatal("rejected config update changed the config file")
	}
	if got := s.config().Model.ReasoningEffort; got != "" {
		t.Fatalf("rejected config update mutated in-memory effort to %q", got)
	}
}

func TestHandleUpdateConfigAllowsUnrelatedEditWithLegacyUnsupportedReasoning(t *testing.T) {
	s, _, _ := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Model.ReasoningEffort = "legacy-unsupported"
	})

	req := httptest.NewRequest(http.MethodPost, "/api/config",
		strings.NewReader(`{"updates":{"display.theme":"dark"}}`))
	rec := httptest.NewRecorder()
	s.handleUpdateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.ReasoningEffort != "legacy-unsupported" {
		t.Fatalf("legacy reasoning effort = %q", saved.Model.ReasoningEffort)
	}
	if saved.Display.Theme != "dark" {
		t.Fatalf("theme = %q, want dark", saved.Display.Theme)
	}
}

func TestHandleSaveRawConfigRejectsNewUnsupportedReasoningWithoutSaving(t *testing.T) {
	s, _, configPath := newReasoningBoundaryServer(t, nil)
	before := mustReadServerFile(t, configPath)
	raw := "model:\n  provider: openai\n  default: gpt-5\n  reasoning_effort: unsupported\n"
	body, err := json.Marshal(map[string]string{"yaml": raw})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveRawConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if after := mustReadServerFile(t, configPath); !bytes.Equal(after, before) {
		t.Fatal("rejected raw config changed the config file")
	}
}

func TestHandleSaveRawConfigRepairsMalformedExistingYAML(t *testing.T) {
	s, _, configPath := newReasoningBoundaryServer(t, nil)
	if err := os.WriteFile(configPath, []byte("model: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := "model:\n  provider: openai\n  default: gpt-5\n"
	body, err := json.Marshal(map[string]string{"yaml": raw})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveRawConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if after := mustReadServerFile(t, configPath); string(after) != raw {
		t.Fatalf("saved config = %q, want submitted repair %q", after, raw)
	}
}

func TestHandleSaveRawConfigRejectedSubmissionDoesNotCreateMissingFile(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "malformed YAML", raw: "model: [\n"},
		{
			name: "unsupported reasoning",
			raw:  "model:\n  provider: openai\n  default: gpt-5\n  reasoning_effort: unsupported\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, a, configPath := newReasoningBoundaryServer(t, nil)
			if err := os.Remove(configPath); err != nil {
				t.Fatal(err)
			}
			serverBefore := s.config()
			agentBefore := a.Config()
			body, err := json.Marshal(map[string]string{"yaml": tt.raw})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			s.handleSaveRawConfig(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(configPath); !os.IsNotExist(err) {
				t.Fatalf("rejected raw save created %s: %v", configPath, err)
			}
			if s.config() != serverBefore {
				t.Fatal("rejected raw save replaced the live server config")
			}
			if a.Config() != agentBefore {
				t.Fatal("rejected raw save replaced the live agent config")
			}
		})
	}
}

func TestHandleSaveRawConfigValidatesNewProviderAgainstCandidateConfig(t *testing.T) {
	var oldFetches, candidateFetches atomic.Int32
	oldCatalog := newServerCatalogFixture(t, http.StatusOK, `{
		"data": [{
			"id": "model-old",
			"reasoning": {"supported_efforts": ["low"], "default_effort": "low"}
		}]
	}`, &oldFetches)
	candidateCatalog := newServerCatalogFixture(t, http.StatusOK, `{
		"data": [{
			"id": "model-a",
			"reasoning": {"supported_efforts": ["MiXeD"], "default_effort": "MiXeD"}
		}]
	}`, &candidateFetches)
	s, a, configPath := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Model.Provider = "old-router"
		cfg.Model.Default = "model-old"
		cfg.Providers = map[string]config.Provider{
			"old-router": {
				Kind:    "openai-compatible",
				BaseURL: oldCatalog.URL,
				Enabled: true,
			},
		}
	})
	raw := "model:\n" +
		"  provider: candidate-router\n" +
		"  default: model-a\n" +
		"  reasoning_effort: MiXeD\n" +
		"providers:\n" +
		"  candidate-router:\n" +
		"    kind: openai-compatible\n" +
		"    base_url: " + candidateCatalog.URL + "\n" +
		"    enabled: true\n"
	body, err := json.Marshal(map[string]string{"yaml": raw})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveRawConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := oldFetches.Load(); got != 0 {
		t.Fatalf("old live provider catalogue fetches = %d, want 0", got)
	}
	if got := candidateFetches.Load(); got != 1 {
		t.Fatalf("candidate provider catalogue fetches = %d, want 1", got)
	}
	if after := mustReadServerFile(t, configPath); string(after) != raw {
		t.Fatalf("saved config = %q, want candidate text %q", after, raw)
	}
	if got := s.config(); got.Model.Provider != "candidate-router" ||
		got.Model.Default != "model-a" || got.Model.ReasoningEffort != "MiXeD" {
		t.Fatalf("live server config = %+v", got.Model)
	}
	if got := a.Config(); got.Model.Provider != "candidate-router" ||
		got.Model.Default != "model-a" || got.Model.ReasoningEffort != "MiXeD" {
		t.Fatalf("live agent config = %+v", got.Model)
	}
}

func TestHandleSaveRawConfigRejectsInvalidEffortAgainstCandidateWithoutMutation(t *testing.T) {
	var oldFetches, candidateFetches atomic.Int32
	oldCatalog := newServerCatalogFixture(t, http.StatusOK, `{
		"data": [{
			"id": "model-old",
			"reasoning": {"supported_efforts": ["low"], "default_effort": "low"}
		}]
	}`, &oldFetches)
	candidateCatalog := newServerCatalogFixture(t, http.StatusOK, `{
		"data": [{
			"id": "model-a",
			"reasoning": {"supported_efforts": ["MiXeD"], "default_effort": "MiXeD"}
		}]
	}`, &candidateFetches)
	s, a, configPath := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		cfg.Model.Provider = "old-router"
		cfg.Model.Default = "model-old"
		cfg.Providers = map[string]config.Provider{
			"old-router": {
				Kind:    "openai-compatible",
				BaseURL: oldCatalog.URL,
				Enabled: true,
			},
		}
	})
	fileBefore := mustReadServerFile(t, configPath)
	serverBefore := s.config()
	agentBefore := a.Config()
	raw := "model:\n" +
		"  provider: candidate-router\n" +
		"  default: model-a\n" +
		"  reasoning_effort: unsupported\n" +
		"providers:\n" +
		"  candidate-router:\n" +
		"    kind: openai-compatible\n" +
		"    base_url: " + candidateCatalog.URL + "\n" +
		"    enabled: true\n"
	body, err := json.Marshal(map[string]string{"yaml": raw})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveRawConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := oldFetches.Load(); got != 0 {
		t.Fatalf("old live provider catalogue fetches = %d, want 0", got)
	}
	if got := candidateFetches.Load(); got != 1 {
		t.Fatalf("candidate provider catalogue fetches = %d, want 1", got)
	}
	if after := mustReadServerFile(t, configPath); !bytes.Equal(after, fileBefore) {
		t.Fatal("rejected candidate config changed the config file")
	}
	if s.config() != serverBefore {
		t.Fatal("rejected candidate config replaced the live server config")
	}
	if a.Config() != agentBefore {
		t.Fatal("rejected candidate config replaced the live agent config")
	}
}

func TestHandleSaveRawConfigAppliesCandidateProviderEnvWithoutPersistingSecrets(t *testing.T) {
	const secret = "round2-candidate-secret"
	tests := []struct {
		name           string
		declaredEnv    string
		providerEnv    string
		wantCredential string
	}{
		{
			name:           "api_key_env",
			declaredEnv:    secret,
			wantCredential: secret,
		},
		{
			name:           "provider-specific API key",
			declaredEnv:    "wrong-fallback-secret",
			providerEnv:    secret,
			wantCredential: secret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var accepted, rejected, wrongEndpoint atomic.Int32
			catalog := newAuthenticatedServerCatalogFixture(
				t, secret, &accepted, &rejected,
			)
			wrongCatalog := newServerCatalogFixture(
				t,
				http.StatusServiceUnavailable,
				`{"error":{"message":"wrong raw endpoint"}}`,
				&wrongEndpoint,
			)
			s, a, configPath := newReasoningBoundaryServer(t, nil)
			t.Setenv("ROUND2_CANDIDATE_API_KEY", tt.declaredEnv)
			t.Setenv("ANTARES_PROVIDER_CANDIDATE_ROUTER_API_KEY", tt.providerEnv)
			t.Setenv("ANTARES_PROVIDER_CANDIDATE_ROUTER_BASE_URL", catalog.URL)
			raw := "model:\n" +
				"  provider: candidate-router\n" +
				"  default: model-a\n" +
				"  reasoning_effort: MiXeD\n" +
				"providers:\n" +
				"  candidate-router:\n" +
				"    kind: openai-compatible\n" +
				"    base_url: " + wrongCatalog.URL + "\n" +
				"    api_key_env: ROUND2_CANDIDATE_API_KEY\n" +
				"    enabled: true\n"
			body, err := json.Marshal(map[string]string{"yaml": raw})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			s.handleSaveRawConfig(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := accepted.Load(); got != 1 {
				t.Fatalf("authenticated catalogue requests = %d, want 1", got)
			}
			if got := rejected.Load(); got != 0 {
				t.Fatalf("rejected catalogue requests = %d, want 0", got)
			}
			if got := wrongEndpoint.Load(); got != 0 {
				t.Fatalf("raw endpoint requests = %d, want 0", got)
			}
			saved := mustReadServerFile(t, configPath)
			if string(saved) != raw {
				t.Fatalf("saved config = %q, want submitted text %q", saved, raw)
			}
			if bytes.Contains(saved, []byte(secret)) ||
				bytes.Contains(saved, []byte(catalog.URL)) {
				t.Fatal("saved config contains environment-derived provider data")
			}
			if provider := s.config().Providers["candidate-router"]; provider.APIKey != tt.wantCredential ||
				provider.BaseURL != catalog.URL {
				t.Fatalf("live server provider = %+v", provider)
			}
			if provider := a.Config().Providers["candidate-router"]; provider.APIKey != tt.wantCredential ||
				provider.BaseURL != catalog.URL {
				t.Fatalf("live agent provider = %+v", provider)
			}
		})
	}
}

func TestHandleSaveRawConfigRejectsMissingOrWrongCandidateProviderEnvWithoutMutation(t *testing.T) {
	const secret = "round2-candidate-secret"
	tests := []struct {
		name        string
		declaredEnv string
	}{
		{name: "missing credential"},
		{name: "wrong credential", declaredEnv: "wrong-secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var accepted, rejected, wrongEndpoint atomic.Int32
			catalog := newAuthenticatedServerCatalogFixture(
				t, secret, &accepted, &rejected,
			)
			wrongCatalog := newServerCatalogFixture(
				t,
				http.StatusServiceUnavailable,
				`{"error":{"message":"wrong raw endpoint"}}`,
				&wrongEndpoint,
			)
			s, a, configPath := newReasoningBoundaryServer(t, nil)
			t.Setenv("ROUND2_CANDIDATE_API_KEY", tt.declaredEnv)
			t.Setenv("ANTARES_PROVIDER_CANDIDATE_ROUTER_API_KEY", "")
			t.Setenv("ANTARES_PROVIDER_CANDIDATE_ROUTER_BASE_URL", catalog.URL)
			fileBefore := mustReadServerFile(t, configPath)
			serverBefore := s.config()
			agentBefore := a.Config()
			raw := "model:\n" +
				"  provider: candidate-router\n" +
				"  default: model-a\n" +
				"  reasoning_effort: MiXeD\n" +
				"providers:\n" +
				"  candidate-router:\n" +
				"    kind: openai-compatible\n" +
				"    base_url: " + wrongCatalog.URL + "\n" +
				"    api_key_env: ROUND2_CANDIDATE_API_KEY\n" +
				"    enabled: true\n"
			body, err := json.Marshal(map[string]string{"yaml": raw})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			s.handleSaveRawConfig(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := accepted.Load(); got != 0 {
				t.Fatalf("authenticated catalogue requests = %d, want 0", got)
			}
			if got := rejected.Load(); got != 1 {
				t.Fatalf("rejected catalogue requests = %d, want 1", got)
			}
			if got := wrongEndpoint.Load(); got != 0 {
				t.Fatalf("raw endpoint requests = %d, want 0", got)
			}
			if after := mustReadServerFile(t, configPath); !bytes.Equal(after, fileBefore) {
				t.Fatal("rejected environment candidate changed the config file")
			}
			if s.config() != serverBefore {
				t.Fatal("rejected environment candidate replaced the live server config")
			}
			if a.Config() != agentBefore {
				t.Fatal("rejected environment candidate replaced the live agent config")
			}
		})
	}
}

func TestHandleSaveRawConfigDistinguishesUnavailableMetadataFromAutoOnlyModel(t *testing.T) {
	tests := []struct {
		name           string
		catalogStatus  int
		catalogBody    string
		wantStatus     int
		wantError      string
		forbiddenError string
	}{
		{
			name:          "known Auto-only model",
			catalogStatus: http.StatusOK,
			catalogBody:   `{"data":[{"id":"model-a"}]}`,
			wantStatus:    http.StatusBadRequest,
			wantError:     "unsupported reasoning override",
		},
		{
			name:           "metadata unavailable",
			catalogStatus:  http.StatusServiceUnavailable,
			catalogBody:    `{"error":{"message":"SECRET-UPSTREAM-DIAGNOSTIC"}}`,
			wantStatus:     http.StatusServiceUnavailable,
			wantError:      "use Auto or retry",
			forbiddenError: "SECRET-UPSTREAM-DIAGNOSTIC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fetches atomic.Int32
			catalog := newServerCatalogFixture(
				t, tt.catalogStatus, tt.catalogBody, &fetches,
			)
			s, a, configPath := newReasoningBoundaryServer(t, nil)
			fileBefore := mustReadServerFile(t, configPath)
			serverBefore := s.config()
			agentBefore := a.Config()
			raw := "model:\n" +
				"  provider: candidate-router\n" +
				"  default: model-a\n" +
				"  reasoning_effort: high\n" +
				"providers:\n" +
				"  candidate-router:\n" +
				"    kind: openai-compatible\n" +
				"    base_url: " + catalog.URL + "\n" +
				"    enabled: true\n"
			body, err := json.Marshal(map[string]string{"yaml": raw})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config/raw", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			s.handleSaveRawConfig(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantError) {
				t.Fatalf("body = %s, want %q", rec.Body.String(), tt.wantError)
			}
			if tt.forbiddenError != "" && strings.Contains(rec.Body.String(), tt.forbiddenError) {
				t.Fatalf("response leaked upstream diagnostic: %s", rec.Body.String())
			}
			if got := fetches.Load(); got != 1 {
				t.Fatalf("catalogue fetches = %d, want 1", got)
			}
			if after := mustReadServerFile(t, configPath); !bytes.Equal(after, fileBefore) {
				t.Fatal("rejected config changed the config file")
			}
			if s.config() != serverBefore {
				t.Fatal("rejected config replaced the live server config")
			}
			if a.Config() != agentBefore {
				t.Fatal("rejected config replaced the live agent config")
			}
		})
	}
}

func TestHandleSaveRoleRejectsExplicitUnsupportedReasoning(t *testing.T) {
	var roleDir string
	s, a, _ := newReasoningBoundaryServer(t, func(cfg *config.Config) {
		roleDir = filepath.Join(config.Home(), "roles")
		cfg.Roles.Dirs = []string{roleDir}
	})
	reg := roles.NewRegistry([]string{roleDir})
	if _, err := reg.Save(roles.Role{
		Name: "custom-reviewer", Title: "Custom Reviewer", Model: "openai/gpt-5",
		Effort: "low", Prompt: "before",
	}); err != nil {
		t.Fatal(err)
	}
	a.SetRoles(reg)
	rolePath := filepath.Join(roleDir, "custom-reviewer.md")
	before := mustReadServerFile(t, rolePath)

	req := httptest.NewRequest(http.MethodPost, "/api/roles", strings.NewReader(
		`{"name":"custom-reviewer","title":"Custom Reviewer","model":"openai/gpt-5",`+
			`"effort":"unsupported","body":"after"}`,
	))
	rec := httptest.NewRecorder()
	s.handleSaveRole(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if after := mustReadServerFile(t, rolePath); !bytes.Equal(after, before) {
		t.Fatal("rejected role save changed the role file")
	}
	if role, ok := reg.Get("custom-reviewer"); !ok || role.Effort != "low" {
		t.Fatalf("rejected role save mutated registry: %#v, found = %v", role, ok)
	}
}

func newReasoningBoundaryServer(
	t *testing.T,
	seed func(*config.Config),
) (*Server, *agent.Agent, string) {
	t.Helper()
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_MODEL", "")
	t.Setenv("ANTARES_PROVIDER", "")
	t.Setenv("ANTARES_BASE_URL", "")
	t.Setenv("ANTARES_API_KEY", "")
	for _, key := range []string{
		"OPENROUTER_API_KEY",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GEMINI_API_KEY",
		"CURSOR_API_KEY",
	} {
		t.Setenv(key, "")
	}

	cfg := config.Default()
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Model.Provider = "openai"
	cfg.Model.Default = "gpt-5"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:    "openai",
			BaseURL: "https://api.openai.com/v1",
			Enabled: true,
		},
	}
	if seed != nil {
		seed(cfg)
	}
	configPath := config.ConfigFile()
	if err := config.SaveAt(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	// Reload overlays YAML onto fresh defaults, including default provider map
	// entries absent from the test fixture. Keep only the explicitly seeded
	// providers so list-all can never probe real or developer-local endpoints.
	reloaded.Providers = make(map[string]config.Provider, len(cfg.Providers))
	for id, provider := range cfg.Providers {
		reloaded.Providers[id] = provider
	}
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := agent.New(reloaded, db, tools.NewRegistry(), nil, nil)
	s := New(Options{
		Config: reloaded,
		Agent:  a,
		Store:  db,
		Reload: func() error {
			next, err := config.Reload()
			if err == nil {
				a.SetConfig(next)
			}
			return err
		},
	})
	return s, a, configPath
}

func newServerReasoningCatalog(t *testing.T, chatRequests *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": [{
					"id": "model-a",
					"name": "Model A",
					"context_length": 128000,
					"reasoning": {
						"supported_efforts": ["low"],
						"default_effort": "low"
					}
				}]
			}`))
		case r.Method == http.MethodPost:
			if chatRequests != nil {
				chatRequests.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"unexpected"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newServerCatalogFixture(
	t *testing.T,
	status int,
	response string,
	fetches *atomic.Int32,
) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if fetches != nil {
			fetches.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newAuthenticatedServerCatalogFixture(
	t *testing.T,
	apiKey string,
	accepted *atomic.Int32,
	rejected *atomic.Int32,
) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			if rejected != nil {
				rejected.Add(1)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid synthetic credential"}}`))
			return
		}
		if accepted != nil {
			accepted.Add(1)
		}
		_, _ = w.Write([]byte(`{
			"data": [{
				"id": "model-a",
				"reasoning": {
					"supported_efforts": ["MiXeD"],
					"default_effort": "MiXeD"
				}
			}]
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertServerReasoningCapability(t *testing.T, model llm.ModelInfo) {
	t.Helper()
	if !model.Reasoning {
		t.Fatal("legacy reasoning flag = false")
	}
	capability := model.ReasoningCapability
	if capability == nil || capability.Source != llm.ReasoningCapabilityLive {
		t.Fatalf("reasoning capability = %#v", capability)
	}
	if capability.Default != "low" || len(capability.Values) != 1 ||
		capability.Values[0].Value != "low" {
		t.Fatalf("reasoning capability = %#v", capability)
	}
}

func mustReadServerFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
