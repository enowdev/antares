package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
)

// trackingIPResolver counts LookupIP calls so tests can prove a handler
// actually routed hostname resolution through the injected server resolver,
// rather than bypassing it (e.g. via net.DefaultResolver or an IP literal
// that skips resolution entirely).
type trackingIPResolver struct {
	providerIPResolver
	calls int
}

func (r *trackingIPResolver) LookupIP(
	ctx context.Context, network, host string,
) ([]net.IP, error) {
	r.calls++
	return r.providerIPResolver.LookupIP(ctx, network, host)
}

// hermeticCursorBaseURL is a syntactically valid, public, non-loopback HTTPS
// URL whose host is an IP literal. validateProviderBaseURL parses an IP
// literal directly (net.ParseIP) instead of resolving it via DNS, so tests
// that pass this as base_url never touch the network or depend on the
// resolver — the injected cursorFactory below handles all "connection"
// behavior. Real requests never leave the process either way: production
// code only calls Me/Models through the (fake, in tests) metadata client, and
// never opens a socket to this address.
const hermeticCursorBaseURL = "https://8.8.8.8"

// fakeCursorMetadata is the injectable metadata-client double used across
// these tests. Both calls return the same err, mirroring the brief's shape.
type fakeCursorMetadata struct {
	me     cursor.Me
	models cursor.ModelCatalog
	err    error
}

func (f *fakeCursorMetadata) Me(context.Context) (*cursor.Me, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &f.me, nil
}

func (f *fakeCursorMetadata) Models(context.Context) (*cursor.ModelCatalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &f.models, nil
}

// fakeCursorMetadataSplit lets Me and Models fail independently, so a test can
// pin down the "only save after both calls succeed" guarantee.
type fakeCursorMetadataSplit struct {
	me         cursor.Me
	meErr      error
	models     cursor.ModelCatalog
	modelsErr  error
	meCalls    int
	modelCalls int
}

func (f *fakeCursorMetadataSplit) Me(context.Context) (*cursor.Me, error) {
	f.meCalls++
	if f.meErr != nil {
		return nil, f.meErr
	}
	return &f.me, nil
}

func (f *fakeCursorMetadataSplit) Models(context.Context) (*cursor.ModelCatalog, error) {
	f.modelCalls++
	if f.modelsErr != nil {
		return nil, f.modelsErr
	}
	return &f.models, nil
}

// newCursorTestServer seeds an isolated ANTARES_HOME, saves and reloads cfg
// (so env-derived provider credentials are merged the way production does),
// and returns a Server wired for handler-level tests.
func newCursorTestServer(t *testing.T, seed func(*config.Config)) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	cfg.Server.DashboardPasswordHash = "test-hash"
	if seed != nil {
		seed(cfg)
	}
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	reloaded, err := config.Reload()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	s := &Server{cfg: reloaded, agent: &agent.Agent{}}
	s.agent.SetConfig(reloaded)
	s.reloadFn = func() error { return nil }
	return s
}

func installCursorCatalogRunner(s *Server, httpClient *http.Client) {
	s.cursorRunner = cursorrun.New(cursorrun.Options{
		ResolveClient: func() (cursor.Options, error) {
			_, provider := s.config().ResolveProvider("cursor")
			return cursor.Options{
				BaseURL: provider.BaseURL, APIKey: provider.APIKey, HTTPClient: httpClient,
			}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})
}

func requestCursorModels(s *Server) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/providers/cursor/models", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	r.SetPathValue("id", "cursor")
	rec := httptest.NewRecorder()
	s.handleProviderModels(rec, r)
	return rec
}

// TestConnectCursorPreservesActiveModel guards the primary model boundary:
// connecting Cursor must never touch cfg.Model, even on success.
func TestConnectCursorPreservesActiveModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Model.Provider = "openrouter"
	cfg.Model.Default = "openai/gpt-5"
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: cfg, agent: &agent.Agent{}}
	s.agent.SetConfig(cfg)
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return &fakeCursorMetadata{
			me:     cursor.Me{APIKeyName: "test"},
			models: cursor.ModelCatalog{Items: []cursor.Model{{ID: "composer-2"}}},
		}, nil
	}
	s.reloadFn = func() error { return nil }
	resolver := &trackingIPResolver{
		providerIPResolver: dns64Resolver(
			t, "api.cursor.com", net.ParseIP("54.158.233.194")),
	}
	s.providerResolver = resolver

	req := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key"}`))
	req.SetPathValue("id", "cursor")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resolver.calls == 0 {
		t.Fatal("Cursor connection bypassed the server provider resolver")
	}
	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Provider != "openrouter" || saved.Model.Default != "openai/gpt-5" {
		t.Fatalf("active model changed: %+v", saved.Model)
	}
	if saved.Providers["cursor"].APIKey != "synthetic-key" {
		t.Fatalf("cursor credential was not saved: %+v", saved.Providers["cursor"])
	}
}

// TestSetupStatusOmitsCursorCapability keeps first-run onboarding limited to
// chat-model providers: Cursor must never appear in the setup picker.
func TestSetupStatusOmitsCursorCapability(t *testing.T) {
	s := newCursorTestServer(t, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	s.handleSetupStatus(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Providers []setupProvider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, p := range body.Providers {
		if p.ID == "cursor" || p.Capability == "agent" {
			t.Fatalf("setup status exposed an agent-capability provider: %+v", p)
		}
	}
}

func TestSetupProviderCatalogueDoesNotResolveAbsentProviderThroughLegacyModel(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_API_KEY", "synthetic-legacy-key")
	t.Setenv("ANTARES_BASE_URL", "https://legacy.example/v1")
	t.Setenv("CURSOR_API_KEY", "synthetic-cursor-key")

	cfg, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["copilot"]; ok {
		t.Fatal("test requires copilot to be absent from cfg.Providers")
	}

	var copilot, cursorProvider *setupProvider
	catalogue := setupProviderCatalogue(cfg)
	for i := range catalogue {
		switch catalogue[i].ID {
		case "copilot":
			copilot = &catalogue[i]
		case "cursor":
			cursorProvider = &catalogue[i]
		}
	}
	if copilot == nil || cursorProvider == nil {
		t.Fatalf("catalogue missing providers: copilot=%v cursor=%v", copilot != nil, cursorProvider != nil)
	}
	if copilot.HasKey {
		t.Fatal("absent copilot provider inherited the legacy ANTARES_API_KEY")
	}
	if copilot.BaseURL != "" {
		t.Fatalf("absent copilot base URL = %q, want catalogue default", copilot.BaseURL)
	}
	if !cursorProvider.HasKey {
		t.Fatal("configured default cursor provider did not resolve CURSOR_API_KEY")
	}
	if cursorProvider.BaseURL != "https://api.cursor.com" {
		t.Fatalf("cursor base URL = %q, want configured default", cursorProvider.BaseURL)
	}
}

// TestSetupCompleteRejectsCursorProvider guards onboarding: the initial setup
// flow must never be able to activate an agent-capability provider.
func TestSetupCompleteRejectsCursorProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, agent: &agent.Agent{}}
	s.agent.SetConfig(cfg)
	s.reloadFn = func() error { return nil }

	body := `{"provider":"cursor","model":"composer-2","api_key":"synthetic-key"}`
	r := httptest.NewRequest(http.MethodPost, "/api/setup/complete", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleSetupComplete(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Provider == "cursor" {
		t.Fatal("setup complete activated the cursor provider")
	}
	if saved.Providers["cursor"].APIKey == "synthetic-key" {
		t.Fatal("setup complete stored a cursor credential")
	}
}

// TestModelOptionsReportsCursorAgentCapabilityAndEnvKey covers resolved
// environment credentials and the capability field surfaced to the dashboard.
func TestModelOptionsReportsCursorAgentCapabilityAndEnvKey(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "env-cursor-key")
	s := newCursorTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/model/options", nil)
	rec := httptest.NewRecorder()
	s.handleModelOptions(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Providers []struct {
			ID         string `json:"id"`
			Capability string `json:"capability"`
			HasKey     bool   `json:"has_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range body.Providers {
		if p.ID != "cursor" {
			continue
		}
		found = true
		if p.Capability != "agent" {
			t.Fatalf("cursor capability = %q, want agent", p.Capability)
		}
		if !p.HasKey {
			t.Fatal("cursor has_key = false despite CURSOR_API_KEY being set")
		}
	}
	if !found {
		t.Fatal("cursor provider missing from /api/model/options")
	}
}

// TestProviderModelsReturnsCursorCatalog covers the provider-specific model
// endpoint's complete response shape and its shared five-minute cache.
func TestProviderModelsReturnsCursorCatalog(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(cursor.ModelCatalog{Items: []cursor.Model{{
			ID:          "composer-2",
			DisplayName: "Composer 2",
			Description: "Cloud agent",
			Aliases:     []string{"composer"},
			Parameters: []cursor.ModelParameter{{
				ID: "effort",
				Values: []cursor.ModelParameterValue{
					{Value: "high", DisplayName: "High"},
				},
			}},
			Variants: []cursor.ModelVariant{{
				Params:      []cursor.ModelParameterSelection{{ID: "effort", Value: "high"}},
				DisplayName: "High effort",
				IsDefault:   true,
			}},
		}}})
	}))
	t.Cleanup(upstream.Close)

	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = "synthetic-key"
		p.BaseURL = upstream.URL
		cfg.Providers["cursor"] = p
	})
	installCursorCatalogRunner(s, upstream.Client())

	rec := requestCursorModels(s)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Models []struct {
			ID          string                  `json:"id"`
			Name        string                  `json:"name"`
			Description string                  `json:"description"`
			Aliases     []string                `json:"aliases"`
			Parameters  []cursor.ModelParameter `json:"parameters"`
			Variants    []cursor.ModelVariant   `json:"variants"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 1 || body.Models[0].ID != "composer-2" ||
		body.Models[0].Name != "Composer 2" ||
		!reflect.DeepEqual(body.Models[0].Aliases, []string{"composer"}) ||
		len(body.Models[0].Parameters) != 1 ||
		len(body.Models[0].Variants) != 1 {
		t.Fatalf("unexpected models: %+v", body.Models)
	}

	second := requestCursorModels(s)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider endpoint bypassed shared cache: requests=%d", got)
	}

	s.SetConfig(s.config())
	third := requestCursorModels(s)
	if third.Code != http.StatusOK {
		t.Fatalf("third status=%d body=%s", third.Code, third.Body.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("SetConfig did not invalidate shared cache: requests=%d", got)
	}
}

// TestProviderModelsNeedsKeyWithoutNetworkCall covers the "no resolved key ->
// no network access" guarantee.
func TestCursorProviderModelsNeedsKeyWithoutNetworkCall(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = ""
		p.APIKeyEnv = ""
		cfg.Providers["cursor"] = p
	})
	var called atomic.Bool
	s.cursorRunner = cursorrun.New(cursorrun.Options{
		ResolveClient: func() (cursor.Options, error) {
			called.Store(true)
			return cursor.Options{}, errors.New("resolver must not be called")
		},
	})

	rec := requestCursorModels(s)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if called.Load() {
		t.Fatal("handleProviderModels reached the network without a resolved key")
	}

	var body struct {
		NeedsKey bool `json:"needs_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.NeedsKey {
		t.Fatal("needs_key = false with no resolved credential")
	}
}

func TestCursorProviderModelsAuthErrorRemainsSafeFallback(t *testing.T) {
	secret := "provider-auth-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"rejected ` + secret + `"}}`))
	}))
	t.Cleanup(upstream.Close)

	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = secret
		p.BaseURL = upstream.URL
		cfg.Providers["cursor"] = p
	})
	installCursorCatalogRunner(s, upstream.Client())

	rec := requestCursorModels(s)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Models []any  `json:"models"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 0 || body.Error == "" {
		t.Fatalf("unexpected auth fallback: %+v", body)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("auth fallback leaked credential: %s", rec.Body.String())
	}
}

// TestModelListAllExcludesCursor guards model isolation: list-all must never
// call or include Cursor, even when it has a usable (env) credential.
func TestModelListAllExcludesCursor(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "env-cursor-key")
	s := newCursorTestServer(t, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/model/list-all", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleModelListAll(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"provider":"cursor"`) {
		t.Fatalf("list-all touched the cursor provider: %s", rec.Body.String())
	}
}

// TestSetProviderKeyCursorAuthErrorDoesNotLeakKey guards secret-safety: an
// auth rejection must never echo the supplied key back to the caller, and the
// key must not be persisted.
func TestSetProviderKeyCursorAuthErrorDoesNotLeakKey(t *testing.T) {
	s := newCursorTestServer(t, nil)
	secret := "super-secret-key-value"
	s.cursorFactory = func(o cursor.Options) (cursorMetadataClient, error) {
		if o.APIKey != secret {
			t.Fatalf("factory received unexpected api key: %q", o.APIKey)
		}
		return &fakeCursorMetadata{err: &cursor.APIError{
			Status: http.StatusUnauthorized, Message: "unauthorized",
		}}, nil
	}

	body := `{"api_key":"` + secret + `","base_url":"` + hermeticCursorBaseURL + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key", strings.NewReader(body))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var respBody struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatal(err)
	}
	if respBody.OK {
		t.Fatal("expected ok=false for an auth error")
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked the supplied api key: %s", rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Providers["cursor"].APIKey == secret {
		t.Fatal("rejected credential was saved")
	}
}

// TestSetProviderKeyCursorTransportErrorMapsTo502 covers the transport /
// invalid-response mapping distinct from the auth-rejection 200/ok:false path.
func TestSetProviderKeyCursorTransportErrorMapsTo502(t *testing.T) {
	s := newCursorTestServer(t, nil)
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return &fakeCursorMetadata{err: errors.New("connection reset")}, nil
	}

	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key","base_url":"`+hermeticCursorBaseURL+`"}`))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want 502", rec.Code, rec.Body.String())
	}
}

// TestSetProviderKeyCursorSavesOnlyAfterBothCallsSucceed pins the
// verifyCursorProvider contract: a catalog fetch failure after a successful
// identity check must not persist the credential.
func TestSetProviderKeyCursorSavesOnlyAfterBothCallsSucceed(t *testing.T) {
	s := newCursorTestServer(t, nil)
	fake := &fakeCursorMetadataSplit{modelsErr: errors.New("catalog unavailable")}
	s.cursorFactory = func(cursor.Options) (cursorMetadataClient, error) {
		return fake, nil
	}

	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/key",
		strings.NewReader(`{"api_key":"synthetic-key","base_url":"`+hermeticCursorBaseURL+`"}`))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleSetProviderKey(rec, r)
	if rec.Code == http.StatusOK {
		var respBody struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
			t.Fatal(err)
		}
		if respBody.OK {
			t.Fatal("provider reported ok=true despite a failed model-catalog fetch")
		}
	}
	if fake.meCalls == 0 {
		t.Fatal("Me was never called")
	}
	if fake.modelCalls == 0 {
		t.Fatal("Models was never called")
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Providers["cursor"].APIKey == "synthetic-key" {
		t.Fatal("credential was saved despite the models call failing")
	}
}

// ---- Fix round 1: additional isolation guards ------------------------------

// TestModelSetRejectsCursorProvider guards /api/model/set: an agent
// integration (Cursor) can never become the active chat model, in memory or
// on disk, regardless of which config value (model or provider) triggers it.
func TestModelSetRejectsCursorProvider(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		cfg.Model.Provider = "openrouter"
		cfg.Model.Default = "openai/gpt-5"
	})

	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"composer-2","provider":"cursor"}`))
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleModelSet(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	// Both the in-memory pointer and the on-disk file must be untouched.
	memCfg := s.config()
	if memCfg.Model.Provider != "openrouter" || memCfg.Model.Default != "openai/gpt-5" {
		t.Fatalf("in-memory config mutated: %+v", memCfg.Model)
	}
	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Provider != "openrouter" || saved.Model.Default != "openai/gpt-5" {
		t.Fatalf("on-disk config mutated: %+v", saved.Model)
	}
}

// TestSetupTestRejectsCursorProvider guards POST /api/setup/test: it must
// fail before the generic llm.New call, with an actionable message pointing
// at the dedicated Cursor connection flow rather than llm.New's own
// "cursor-agent is an agent integration" guard text.
func TestSetupTestRejectsCursorProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	cfg := config.Default()
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg}

	body := `{"provider":"cursor","api_key":"synthetic-key"}`
	r := httptest.NewRequest(http.MethodPost, "/api/setup/test", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleSetupTest(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/api/providers/cursor/key") {
		t.Fatalf("response missing actionable pointer to the Cursor connection flow: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "cursor_agent tool") {
		t.Fatal("handleSetupTest reached the generic llm.New guard instead of failing earlier")
	}
}

// TestModelListRejectsCursorProvider guards GET /api/model/list: it must fail
// before the generic agent.Models -> llm.New path, even when Cursor has a
// resolved key (which would otherwise pass the existing needs_key check and
// reach the generic path).
func TestModelListRejectsCursorProvider(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = "synthetic-key"
		cfg.Providers["cursor"] = p
	})

	r := httptest.NewRequest(http.MethodGet, "/api/model/list?provider=cursor", nil)
	rec := httptest.NewRecorder()
	s.handleModelList(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Models     []any  `json:"models"`
		Capability string `json:"capability"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Capability != "agent" {
		t.Fatalf("capability = %q, want agent (body=%s)", body.Capability, rec.Body.String())
	}
	if len(body.Models) != 0 {
		t.Fatalf("models = %+v, want empty", body.Models)
	}
	if !strings.Contains(body.Error, "/api/providers/cursor/models") {
		t.Fatalf("error = %q, missing actionable pointer", body.Error)
	}
	if strings.Contains(rec.Body.String(), "cursor_agent tool") {
		t.Fatal("handleModelList reached the generic agent.Models -> llm.New path")
	}
}

// TestProviderModelInfoSkipsCursorProvider guards GET
// /api/providers/{id}/model-info: it must fail before agent.Models. A curated
// Models whitelist proves this deterministically — if the generic path ran,
// agent.Models would return the whitelist entry (no live call needed) and
// this handler would report found:true; the capability guard must prevent
// that regardless of the whitelist's contents.
func TestProviderModelInfoSkipsCursorProvider(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.APIKey = "synthetic-key"
		p.Models = []string{"composer-2"}
		cfg.Providers["cursor"] = p
	})

	r := httptest.NewRequest(http.MethodGet, "/api/providers/cursor/model-info?id=composer-2", nil)
	r.SetPathValue("id", "cursor")
	rec := httptest.NewRecorder()
	s.handleProviderModelInfo(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Found bool `json:"found"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Found {
		t.Fatal("handleProviderModelInfo reached the generic agent.Models path (curated whitelist matched)")
	}
}

// TestAddProviderModelRejectsCursorProvider guards POST
// /api/providers/{id}/model: Cursor has no manual model whitelist to append
// to — its catalogue is discovered live via /api/providers/{id}/models.
func TestAddProviderModelRejectsCursorProvider(t *testing.T) {
	s := newCursorTestServer(t, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/providers/cursor/model",
		strings.NewReader(`{"model":"composer-2"}`))
	r.SetPathValue("id", "cursor")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleAddProviderModel(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Providers["cursor"].Models) != 0 {
		t.Fatalf("cursor gained a manual model entry: %+v", saved.Providers["cursor"].Models)
	}
}

// TestDeleteProviderModelRejectsCursorProvider mirrors
// TestAddProviderModelRejectsCursorProvider for the delete path.
func TestDeleteProviderModelRejectsCursorProvider(t *testing.T) {
	s := newCursorTestServer(t, func(cfg *config.Config) {
		p := cfg.Providers["cursor"]
		p.Models = []string{"composer-2"}
		cfg.Providers["cursor"] = p
	})

	r := httptest.NewRequest(http.MethodDelete, "/api/providers/cursor/model/composer-2", nil)
	r.SetPathValue("id", "cursor")
	r.SetPathValue("model", "composer-2")
	r.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.handleDeleteProviderModel(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}

	saved, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Providers["cursor"].Models) != 1 {
		t.Fatalf("cursor's manual model entry was mutated despite the guard: %+v", saved.Providers["cursor"].Models)
	}
}
