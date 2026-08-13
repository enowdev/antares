package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

func TestProviderAddAndUseCursorPreserveActiveModel(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	output := captureProviderStdout(t, func() {
		if err := cmdProvider([]string{"add", "cursor", "synthetic-key"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "connected cursor agent integration") {
		t.Fatalf("add output = %q", output)
	}
	connected, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if connected.Model.Provider != beforeProvider || connected.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", connected.Model.Provider, connected.Model.Default)
	}
	if p := connected.Providers["cursor"]; !p.Enabled || p.APIKey != "synthetic-key" || p.Kind != "cursor-agent" {
		t.Fatalf("cursor provider = %+v", p)
	}

	err = cmdProvider([]string{"use", "cursor"})
	if err == nil || !strings.Contains(err.Error(), "cursor_agent") {
		t.Fatalf("use cursor error = %v", err)
	}
	afterUse, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if afterUse.Model.Provider != beforeProvider || afterUse.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", afterUse.Model.Provider, afterUse.Model.Default)
	}
}

func TestRuntimeCursorRunnerUsesAtomicConfigAndInvalidatesOnReload(t *testing.T) {
	var calls atomic.Int32
	var version atomic.Int32
	version.Store(1)
	var authMu sync.Mutex
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		authMu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		authMu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{map[string]any{
				"id": "model-" + string(rune('0'+version.Load())),
			}},
		})
	}))
	defer upstream.Close()

	cfg := config.Default()
	provider := cfg.Providers["cursor"]
	provider.Enabled = true
	provider.APIKey = "runtime-key-one"
	provider.BaseURL = upstream.URL
	cfg.Providers["cursor"] = provider
	ag := agent.New(cfg, nil, tools.NewRegistry(), nil, nil)
	rt := &runtimeServices{cfg: cfg, agent: ag}
	runner := newRuntimeCursorRunner(ag)
	rt.setCursorRunner(runner)
	if rt.cursorRunner != runner {
		t.Fatal("runtimeServices did not retain the installed Cursor runner")
	}

	first, err := runner.Catalog(context.Background(), false)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "model-1" {
		t.Fatalf("first catalogue = %+v, %v", first, err)
	}
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cached catalogue requests = %d, want 1", got)
	}

	version.Store(2)
	reloaded := *cfg
	reloaded.Providers = make(map[string]config.Provider, len(cfg.Providers))
	for id, configured := range cfg.Providers {
		reloaded.Providers[id] = configured
	}
	ag.SetConfig(&reloaded)
	second, err := runner.Catalog(context.Background(), false)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "model-2" {
		t.Fatalf("reloaded catalogue = %+v, %v", second, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("catalogue requests after same-key reload = %d, want 2", got)
	}

	changedKey := reloaded
	changedKey.Providers = make(map[string]config.Provider, len(reloaded.Providers))
	for id, configured := range reloaded.Providers {
		changedKey.Providers[id] = configured
	}
	provider = changedKey.Providers["cursor"]
	provider.APIKey = "runtime-key-two"
	changedKey.Providers["cursor"] = provider
	ag.SetConfig(&changedKey)
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	authMu.Lock()
	gotAuthorizations := append([]string(nil), authorizations...)
	authMu.Unlock()
	if len(gotAuthorizations) != 3 ||
		gotAuthorizations[0] != "Bearer runtime-key-one" ||
		gotAuthorizations[1] != "Bearer runtime-key-one" ||
		gotAuthorizations[2] != "Bearer runtime-key-two" {
		t.Fatalf("resolved authorizations = %v", gotAuthorizations)
	}
}

func captureProviderStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
