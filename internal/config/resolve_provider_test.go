package config

import "testing"

func TestResolveProviderDoesNotClobberNamedProviderCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.Provider = "antigravity"
	cfg.Model.BaseURL = "http://localhost:8080/v1" // stale CodeBuddy
	cfg.Model.APIKey = "sk-codebuddy-stale"
	cfg.Providers = map[string]Provider{
		"antigravity": {
			Kind: "anthropic", Enabled: true,
			BaseURL: "http://localhost:8080/antigravity/v1",
			APIKey:  "sk-antigravity-real",
		},
		"custom": {
			Kind: "openai-compatible", Enabled: true,
			BaseURL: "http://localhost:8080/v1",
			APIKey:  "sk-codebuddy-stale",
		},
	}

	_, p := cfg.ResolveProvider("antigravity")
	if p.BaseURL != "http://localhost:8080/antigravity/v1" {
		t.Fatalf("BaseURL = %q, want antigravity path (must not inherit model.base_url)", p.BaseURL)
	}
	if p.APIKey != "sk-antigravity-real" {
		t.Fatalf("APIKey clobbered by top-level model.api_key")
	}
}

func TestResolveProviderAllowsInlineWhenProviderHasNoCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.Provider = "inline"
	cfg.Model.BaseURL = "http://localhost:9/v1"
	cfg.Model.APIKey = "sk-inline"
	cfg.Providers = map[string]Provider{
		"inline": {Kind: "openai-compatible", Enabled: true},
	}
	_, p := cfg.ResolveProvider("inline")
	if p.BaseURL != "http://localhost:9/v1" || p.APIKey != "sk-inline" {
		t.Fatalf("expected inline fallback, got base=%q key=%q", p.BaseURL, p.APIKey)
	}
}

func TestClearInlineModelCredentials(t *testing.T) {
	cfg := Default()
	cfg.Model.BaseURL = "http://x"
	cfg.Model.APIKey = "sk-x"
	cfg.ClearInlineModelCredentials()
	if cfg.Model.BaseURL != "" || cfg.Model.APIKey != "" {
		t.Fatalf("not cleared: %+v", cfg.Model)
	}
}
