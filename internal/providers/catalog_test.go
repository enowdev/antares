package providers

import (
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestCursorConnectDoesNotChangeActiveModel(t *testing.T) {
	cfg := config.Default()
	beforeProvider, beforeModel := cfg.Model.Provider, cfg.Model.Default

	info, known := Connect(cfg, "cursor", "synthetic-key")
	if !known || info.Capability() != CapabilityAgent {
		t.Fatalf("cursor info = %+v, known=%v", info, known)
	}
	if activated := Activate(cfg, "cursor", ""); activated {
		t.Fatal("agent provider was activated as an LLM")
	}
	if cfg.Model.Provider != beforeProvider || cfg.Model.Default != beforeModel {
		t.Fatalf("model changed to %s/%s", cfg.Model.Provider, cfg.Model.Default)
	}
	if p := cfg.Providers["cursor"]; !p.Enabled || p.APIKey != "synthetic-key" {
		t.Fatalf("cursor provider not connected: %+v", p)
	}
}

func TestDefaultCursorProviderUsesEnvironmentKey(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("CURSOR_API_KEY", "synthetic-env-key")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, p := cfg.ResolveProvider("cursor")
	if p.APIKey != "synthetic-env-key" || p.Kind != "cursor-agent" {
		t.Fatalf("cursor provider = %+v", p)
	}
	// The env key is picked up, but the provider still ships disabled: having
	// CURSOR_API_KEY in the environment must not by itself let the model spawn
	// billable cloud agents. Enabling is the user's explicit act.
	if p.Enabled {
		t.Error("cursor must default to disabled even when its env key is present")
	}
}
