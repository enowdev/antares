package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func newModulesServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_CONFIG", "")
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)
	return s
}

func decodeModules(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return got
}

func getModules(s *Server) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.handleGetModules(rr, httptest.NewRequest(http.MethodGet, "/api/ui/modules", nil))
	return rr
}

func postModules(s *Server, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.handleSetModules(rr, providerJSONRequest(http.MethodPost, "/api/ui/modules", body))
	return rr
}

func TestGetModulesAbsentIsNull(t *testing.T) {
	s := newModulesServer(t)
	rr := getModules(s)
	got := decodeModules(t, rr)
	modules, present := got["modules"]
	if !present || modules != nil {
		t.Fatalf("modules = %#v (present=%v), want explicit null; body = %s", modules, present, rr.Body.String())
	}
	if got["preset"] != "full" {
		t.Fatalf("preset = %#v, want full for an install without modules", got["preset"])
	}
}

func TestPostModulesPersistsAndReturnsNormalizedList(t *testing.T) {
	s := newModulesServer(t)
	got := decodeModules(t, postModules(s, `{"modules":["studio","automation"],"preset":"creator"}`))
	if want := []any{"automation", "studio"}; !reflect.DeepEqual(got["modules"], want) {
		t.Fatalf("response modules = %#v, want %#v", got["modules"], want)
	}
	if got["preset"] != "creator" {
		t.Fatalf("response preset = %#v, want creator", got["preset"])
	}

	reloaded, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Display.Modules == nil || !reflect.DeepEqual(*reloaded.Display.Modules, []string{"automation", "studio"}) {
		t.Fatalf("persisted modules = %v", reloaded.Display.Modules)
	}
	if live := s.config().Display.Modules; live == nil || len(*live) != 2 {
		t.Fatalf("server config not refreshed after save: %v", live)
	}

	got = decodeModules(t, getModules(s))
	if want := []any{"automation", "studio"}; !reflect.DeepEqual(got["modules"], want) || got["preset"] != "creator" {
		t.Fatalf("GET after POST = %#v", got)
	}

	// The General preset round-trips as an explicit empty list, not null.
	decodeModules(t, postModules(s, `{"modules":[],"preset":"general"}`))
	rr := getModules(s)
	if !strings.Contains(rr.Body.String(), `"modules":[]`) {
		t.Fatalf("GET after General = %s, want an empty list", rr.Body.String())
	}
}

func TestPostModulesRejectsInvalidInput(t *testing.T) {
	s := newModulesServer(t)
	for _, body := range []string{
		`{"modules":["automation","warp-drive"],"preset":"custom"}`,
		`{"modules":["studio","studio"],"preset":"custom"}`,
		`{"modules":["studio"],"preset":"bogus"}`,
		`{"modules":["studio"]}`,
		`{"preset":"full"}`,
		`{"modules":null,"preset":"full"}`,
		`not json`,
	} {
		if rr := postModules(s, body); rr.Code != http.StatusBadRequest {
			t.Errorf("POST %s: status = %d, want 400 (body=%s)", body, rr.Code, rr.Body.String())
		}
	}
	modules, _, err := config.PersistedModules()
	if err != nil {
		t.Fatal(err)
	}
	if modules != nil {
		t.Fatalf("rejected POSTs persisted modules: %q", *modules)
	}
}

// setupCompleteModules runs setup complete against a fresh install with the
// given extra JSON fields and returns the status plus the persisted config.
func setupCompleteModules(t *testing.T, extra string) (*httptest.ResponseRecorder, *config.Config) {
	t.Helper()
	fixture, _ := headerProviderFixture(t)
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_CONFIG", "")
	cfg := config.Default()
	cfg.Model.Default = ""
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), "sqlite", filepath.Join(t.TempDir(), "modules.db"), 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &Server{cfg: cfg, db: db, agent: agent.New(cfg, db, nil, nil, nil)}

	body := `{"provider":"custom","name":"Modules Host","base_url":"` + fixture.URL + `/v1","headers":{"X-Tenant":"team=a=b"},"model":"header-model","workspace":"` + filepath.Join(t.TempDir(), "ws") + `"` + extra + `}`
	r := providerJSONRequest(http.MethodPost, "/api/setup/complete", body)
	r.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	s.handleSetupComplete(rr, r)

	reloaded, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	return rr, reloaded
}

func TestSetupCompleteWritesChosenModules(t *testing.T) {
	rr, cfg := setupCompleteModules(t, `,"modules":["security","automation"],"preset":"security"`)
	assertProviderOK(t, rr)
	if cfg.Display.Modules == nil || !reflect.DeepEqual(*cfg.Display.Modules, []string{"automation", "security"}) {
		t.Fatalf("persisted modules = %v", cfg.Display.Modules)
	}
	if cfg.Display.Preset != "security" {
		t.Fatalf("persisted preset = %q", cfg.Display.Preset)
	}
}

func TestSetupCompleteWritesGeneralAsEmptyList(t *testing.T) {
	rr, cfg := setupCompleteModules(t, `,"modules":[],"preset":"general"`)
	assertProviderOK(t, rr)
	if cfg.Display.Modules == nil || len(*cfg.Display.Modules) != 0 {
		t.Fatalf("persisted modules = %v, want explicit empty list", cfg.Display.Modules)
	}
}

func TestSetupCompleteWithoutModulesLeavesKeyAbsent(t *testing.T) {
	rr, cfg := setupCompleteModules(t, "")
	assertProviderOK(t, rr)
	if cfg.Display.Modules != nil {
		t.Fatalf("persisted modules = %q, want absent", *cfg.Display.Modules)
	}
	raw, err := os.ReadFile(config.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "modules:") {
		t.Fatalf("config.yaml gained a modules key:\n%s", raw)
	}
}

func TestSetupCompleteRejectsInvalidModules(t *testing.T) {
	for _, extra := range []string{
		`,"modules":["automation","nope"],"preset":"custom"`,
		`,"modules":["automation"],"preset":"bogus"`,
		`,"preset":"full"`,
	} {
		rr, cfg := setupCompleteModules(t, extra)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("setup with %s: status = %d, want 400 (body=%s)", extra, rr.Code, rr.Body.String())
		}
		if cfg.Model.Default != "" {
			t.Errorf("setup with %s wrote the config despite rejecting it", extra)
		}
	}
}

// The endpoints sit behind the same auth as GET /api/config.
func TestModulesRoutesRequireAuth(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_CONFIG", "")
	cfg := config.Default()
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Server.AuthToken = "modules-test-token"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Config: cfg})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, providerJSONRequest(method, "/api/ui/modules", `{"modules":[],"preset":"general"}`))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status = %d, want 401", method, rr.Code)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/api/ui/modules", nil)
	r.Header.Set("Authorization", "Bearer modules-test-token")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, r)
	if got := decodeModules(t, rr); got["modules"] != nil {
		t.Fatalf("authorized GET = %#v, want modules null", got)
	}
}
