package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
)

// TestModelSetSwapsBothConfigPointers guards the regression where
// handleModelSet updated only the agent's config pointer (via
// s.agent.SetConfig) and skipped the server's (s.SetConfig). The skipped
// server pointer left /api/model/options, /api/model/list-all, and
// /api/status reporting the stale "active" model — so the dashboard
// picker appeared to stick on the previous model even though the switch
// returned 200. applyReload (the pre-FR-004 path) updated both pointers;
// the FR-004 optimization that replaced it with a direct swap must
// preserve that invariant.
//
// The handler also spawns an async config.SaveAt goroutine. We use a
// manual temp dir (not t.TempDir) and wait for the save to land before
// returning so the goroutine never outlives the test's environment and
// cannot race the cleanup.
func TestModelSetSwapsBothConfigPointers(t *testing.T) {
	// Isolate the config package global cache + on-disk file from the user's
	// real ~/.antares. Reload() inside handleModelSet and the async Save
	// goroutine both touch the package state and the config file.
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	configFile := config.ConfigFile()

	cfg := config.Default()
	// The dashboard-password gate (upstream PR #17) is kept: satisfy it with a
	// set password hash so the handler reaches its actual logic. The bearer
	// token below is belt-and-braces.
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Server.AuthToken = "test-token"
	cfg.Model.Default = "model-a"
	cfg.Model.Provider = "prov-a"

	// Seed the file the way newModelSetServer does: writing via SaveAt (rather
	// than letting Reload's first-run branch create it) keeps the on-disk
	// state explicit and sidesteps the Windows rename-after-first-run quirk.
	if err := config.SaveAt(configFile, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg) // align the agent pointer with the server's

	body := strings.NewReader(`{"model":"model-b","provider":"prov-b"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/model/set", body)
	r.Header.Set("Authorization", "Bearer test-token")
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("handleModelSet status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	// The fix: the SERVER config pointer must reflect the swap so the
	// /api/model/options, list-all, and status handlers (which read
	// s.config()) report the new active model immediately.
	serverCfg := s.config()
	if serverCfg.Model.Default != "model-b" {
		t.Fatalf("server config default = %q, want %q (stale server pointer)",
			serverCfg.Model.Default, "model-b")
	}
	if serverCfg.Model.Provider != "prov-b" {
		t.Fatalf("server config provider = %q, want %q", serverCfg.Model.Provider, "prov-b")
	}

	// The agent pointer (already updated pre-fix) must remain aligned and
	// point at the SAME config struct the server now holds, so a chat turn
	// with no per-turn override resolves to the just-selected model.
	agentCfg := s.agent.Config()
	if agentCfg != serverCfg {
		t.Fatal("agent and server config pointers diverge after model switch")
	}
	if agentCfg.Model.Default != "model-b" {
		t.Fatalf("agent config default = %q, want %q", agentCfg.Model.Default, "model-b")
	}

	// Wait for the async SaveAt goroutine to land the persist at the captured
	// path. This bounds the goroutine's lifetime to the test so it cannot
	// outlive the ANTARES_HOME override (which would clobber the real config)
	// and cannot race the temp-dir cleanup.
	waitForModelSave(t, configFile, "model-b")
}

// ---- handleModelSet edge cases --------------------------

// newModelSetServer seeds an isolated ANTARES_HOME with the given config and
// returns a Server wired exactly as handleModelSet expects: the server and
// agent config pointers start aligned, and the seeded file is what
// config.Reload() inside the handler re-reads from disk.
func newModelSetServer(t *testing.T, seed func(*config.Config)) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)

	cfg := config.Default()
	seed(cfg)
	// Keep the dashboard-password gate (upstream PR #17) satisfied: a set
	// password hash makes requireDashboardPassword pass for these tests.
	cfg.Server.DashboardPasswordHash = "test-hash"
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)
	return s
}

// waitForModelSave polls the config file until the async SaveAt goroutine
// spawned by handleModelSet has landed the new model on disk, then removes the
// file so the (already exited) goroutine cannot race t.TempDir cleanup.
func waitForModelSave(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), want) {
			_ = os.Remove(path)
			_ = os.Remove(path + ".tmp")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("async save did not persist %q to %s", want, path)
}

func TestModelSetEmptyModelRejected(t *testing.T) {
	s := newModelSetServer(t, func(cfg *config.Config) {
		cfg.Model.Default = "model-a"
		cfg.Model.Provider = "prov-a"
	})

	for _, body := range []string{
		`{"model":"","provider":"prov-b"}`,
		`{"model":"   ","provider":"prov-b"}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/model/set", strings.NewReader(body))
		rr := httptest.NewRecorder()
		s.handleModelSet(rr, r)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400 (body=%s)", body, rr.Code, rr.Body.String())
		}
	}
}

func TestModelSetBadJSONRejected(t *testing.T) {
	s := newModelSetServer(t, func(cfg *config.Config) {})

	r := httptest.NewRequest(http.MethodPost, "/api/model/set", strings.NewReader(`{invalid`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestModelSetReloadFailureReturns500(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	// Unparseable config forces config.Reload() inside the handler to fail.
	if err := os.WriteFile(config.ConfigFile(), []byte("{{{{ not yaml\n"), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	cfg := config.Default()
	cfg.Server.DashboardPasswordHash = "test-hash" // satisfy the kept password gate
	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)

	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"model-b","provider":"prov-b"}`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestModelSetProviderChangeClearsInlineCredentials guards the branch that
// wipes legacy top-level model.base_url/api_key whenever the active provider
// changes. Stale inline values from a previous provider would otherwise
// override the new provider's credentials in ResolveProvider and silently
// route chat through the wrong base_url (cascading 401s).
func TestModelSetProviderChangeClearsInlineCredentials(t *testing.T) {
	s := newModelSetServer(t, func(cfg *config.Config) {
		cfg.Model.Default = "model-a"
		cfg.Model.Provider = "prov-a"
		cfg.Model.BaseURL = "http://stale.local/v1"
		cfg.Model.APIKey = "stale-key"
		cfg.Providers["prov-a"] = config.Provider{
			Kind: "openai-compatible", BaseURL: "http://prov-a.local/v1",
			APIKey: "prov-a-key", Enabled: true,
		}
		cfg.Providers["prov-b"] = config.Provider{
			Kind: "openai-compatible", BaseURL: "http://prov-b.local/v1",
			APIKey: "prov-b-key", Enabled: true,
		}
	})

	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"model-b","provider":"prov-b"}`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	got := s.config()
	if got.Model.Default != "model-b" || got.Model.Provider != "prov-b" {
		t.Fatalf("active = %q/%q, want model-b/prov-b", got.Model.Default, got.Model.Provider)
	}
	if got.Model.BaseURL != "" || got.Model.APIKey != "" {
		t.Fatalf("inline creds not cleared on provider change: base_url=%q api_key=%q",
			got.Model.BaseURL, got.Model.APIKey)
	}
	if agentCfg := s.agent.Config(); agentCfg != s.config() {
		t.Fatal("agent and server config pointers diverge after provider switch")
	}
	waitForModelSave(t, config.ConfigFile(), "model-b")
}

// TestModelSetSameProviderClearsLeftoversWhenProviderHasCreds guards the
// else-if branch: even a same-provider set must clear stale inline values when
// the named provider already carries its own credentials.
func TestModelSetSameProviderClearsLeftoversWhenProviderHasCreds(t *testing.T) {
	s := newModelSetServer(t, func(cfg *config.Config) {
		cfg.Model.Default = "model-a"
		cfg.Model.Provider = "prov-a"
		cfg.Model.BaseURL = "http://stale.local/v1"
		cfg.Model.APIKey = "stale-key"
		cfg.Providers["prov-a"] = config.Provider{
			Kind: "openai-compatible", BaseURL: "http://prov-a.local/v1",
			APIKey: "prov-a-key", Enabled: true,
		}
	})

	// No provider in the request -> same-provider else-if branch.
	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"model-c"}`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	got := s.config()
	if got.Model.BaseURL != "" || got.Model.APIKey != "" {
		t.Fatalf("leftover inline creds must clear when the provider carries its own: base_url=%q api_key=%q",
			got.Model.BaseURL, got.Model.APIKey)
	}
	waitForModelSave(t, config.ConfigFile(), "model-c")
}

// TestModelSetSameProviderKeepsInlineWhenProviderLacksCreds is the mirror
// case: when the named provider has no credentials of its own, the inline
// fallback is the only thing ResolveProvider can use, so the handler must NOT
// clear it — doing so would break routing entirely.
func TestModelSetSameProviderKeepsInlineWhenProviderLacksCreds(t *testing.T) {
	s := newModelSetServer(t, func(cfg *config.Config) {
		cfg.Model.Default = "model-a"
		cfg.Model.Provider = "prov-a"
		cfg.Model.BaseURL = "http://stale.local/v1"
		cfg.Model.APIKey = "stale-key"
		cfg.Providers["prov-a"] = config.Provider{
			Kind: "openai-compatible", Enabled: true, // no own creds
		}
	})

	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"model-c"}`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	got := s.config()
	if got.Model.BaseURL != "http://stale.local/v1" || got.Model.APIKey != "stale-key" {
		t.Fatalf("inline creds must survive when the provider carries none: base_url=%q api_key=%q",
			got.Model.BaseURL, got.Model.APIKey)
	}
	waitForModelSave(t, config.ConfigFile(), "model-c")
}

// TestModelSetRequiresDashboardPassword verifies the "keep dashboard-password
// gate" claim: with no password set and no bearer, the swap is refused with
// 428 Precondition Required rather than mutating config.
func TestModelSetRequiresDashboardPassword(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)

	cfg := config.Default()
	cfg.Model.Default = "model-a"
	// No DashboardPasswordHash, no AuthToken → the gate must block.
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)

	r := httptest.NewRequest(http.MethodPost, "/api/model/set",
		strings.NewReader(`{"model":"model-b"}`))
	rr := httptest.NewRecorder()
	s.handleModelSet(rr, r)

	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428 when no dashboard password is set", rr.Code)
	}
	if got := s.config().Model.Default; got != "model-a" {
		t.Fatalf("config was mutated despite the gate: model = %q", got)
	}
}

// TestModelSetConcurrentWithConfigReads runs model switches concurrently with
// readers of the agent's live config, under -race. It pins the fix for the
// pointer-swap / async-normalize data race: the swap must publish an atomic
// pointer and the background save must not mutate the shared struct.
func TestModelSetConcurrentWithConfigReads(t *testing.T) {
	// A manual temp dir, not t.TempDir: handleModelSet saves in a detached
	// goroutine, and twenty overlapping saves are still writing when the test
	// body returns. t.TempDir's RemoveAll would race them and fail the test
	// with "directory not empty". We wait for the writers to drain and then
	// clean up ourselves.
	home, err := os.MkdirTemp("", "antares-model-set-concurrent")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer func() {
		waitForSavesToDrain(t, home)
		_ = os.RemoveAll(home)
	}()
	t.Setenv("ANTARES_HOME", home)

	cfg := config.Default()
	cfg.Server.DashboardPasswordHash = "test-hash"
	cfg.Model.Default = "model-0"
	cfg.Model.Provider = "prov"
	cfg.Providers["prov"] = config.Provider{Kind: "openai-compatible", Enabled: true}
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	s := &Server{cfg: cfg}
	s.agent = &agent.Agent{}
	s.agent.SetConfig(cfg)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers: hammer the agent's live config the way a turn does.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c := s.agent.Config()
					_ = c.Model.Default
					for name := range c.Providers {
						_ = name
					}
				}
			}
		}()
	}

	// Writers: switch the model repeatedly.
	for i := 0; i < 20; i++ {
		body := strings.NewReader(`{"model":"model-` + strconv.Itoa(i) + `"}`)
		r := httptest.NewRequest(http.MethodPost, "/api/model/set", body)
		rr := httptest.NewRecorder()
		s.handleModelSet(rr, r)
		if rr.Code != http.StatusOK {
			close(stop)
			wg.Wait()
			t.Fatalf("swap %d: status = %d (body=%s)", i, rr.Code, rr.Body.String())
		}
	}
	close(stop)
	wg.Wait()

	// The in-memory swap IS ordered, so the live config must show the last
	// switch. The on-disk state is not: handleModelSet saves in a detached
	// goroutine, so twenty overlapping saves can land in any order and the
	// file legitimately ends up holding an earlier model. Asserting
	// "model-19 reached disk" made this test fail whenever the scheduler
	// happened to finish the savers out of order.
	if got := s.agent.Config().Model.Default; got != "model-19" {
		t.Fatalf("live config = %q, want the last switch model-19", got)
	}
	if got := s.config().Model.Default; got != "model-19" {
		t.Fatalf("server config = %q, want the last switch model-19", got)
	}
	// Some save must still land: whichever model wins the race, the file has
	// to hold one of the models we set.
	waitForAnyModelSave(t, config.ConfigFile(), 20)
}

// waitForAnyModelSave polls until a detached save has landed a config carrying
// any of the models the concurrent switches set. It deliberately does not care
// WHICH model won — the saves are unordered, so demanding the last one is a
// guarantee the handler never made.
func waitForAnyModelSave(t *testing.T, path string, models int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			for i := range models {
				if strings.Contains(string(b), "model-"+strconv.Itoa(i)) {
					return
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no concurrent save landed a model in %s", path)
}

// waitForSavesToDrain waits until no temporary config file is left in dir, so
// every detached save goroutine has finished writing before the caller removes
// the directory. SaveNormalizedAt writes to ".config-*.yaml.tmp" and renames.
func waitForSavesToDrain(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		pending := false
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml.tmp") {
				pending = true
				break
			}
		}
		if !pending {
			// One more short settle: a goroutine may be between CreateTemp and
			// its first write. Two consecutive clean reads is enough in
			// practice, and a leftover temp file is harmless to RemoveAll.
			time.Sleep(20 * time.Millisecond)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Log("config saves did not drain within 5s; cleaning up anyway")
}
