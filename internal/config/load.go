package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	mu     sync.RWMutex
	loaded *Config
	// saveMu serialises writes to the config file. Concurrent saves (e.g. a
	// rapid run of model switches, or a model switch overlapping a raw config
	// save) would otherwise race on the temp file and the rename, corrupting
	// what lands on disk.
	saveMu sync.Mutex
)

// Load reads defaults, merges the profile YAML file, then applies env overrides.
// It is safe to call repeatedly; the result is cached until Reload or Save.
func Load() (*Config, error) {
	mu.RLock()
	if loaded != nil {
		c := loaded
		mu.RUnlock()
		return c, nil
	}
	mu.RUnlock()
	return Reload()
}

// Reload re-reads configuration from disk, bypassing the cache.
func Reload() (*Config, error) {
	cfg := Default()

	path := ConfigFile()
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// First run: keep defaults, persist them so the file is discoverable.
		if err := writeFile(path, cfg); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	loadDotEnv(Path(".env"))
	applyEnv(cfg)
	normalize(cfg)

	mu.Lock()
	loaded = cfg
	mu.Unlock()
	return cfg, nil
}

// Get returns the cached config, loading it on first use. It never returns nil.
func Get() *Config {
	c, err := Load()
	if err != nil || c == nil {
		return Default()
	}
	return c
}

func SaveAt(path string, cfg *Config) error {
	normalize(cfg)
	return SaveNormalizedAt(path, cfg)
}

// SaveNormalizedAt writes an already-normalized config without mutating it
// further. Callers that hand the same *Config to another goroutine (for
// example, a live model switch that publishes the config to the agent and then
// persists it in the background) MUST normalize synchronously and use this, so
// the background write never mutates a struct another goroutine is reading.
func SaveNormalizedAt(path string, cfg *Config) error {
	if err := writeFile(path, cfg); err != nil {
		return err
	}
	mu.Lock()
	loaded = cfg
	mu.Unlock()
	return nil
}

func Save(cfg *Config) error {
	return SaveAt(ConfigFile(), cfg)
}

// Raw returns the on-disk YAML text for the active profile.
func Raw() (string, error) {
	b, err := os.ReadFile(ConfigFile())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// ParseRaw validates YAML text supplied by the dashboard editor without
// changing either the active config file or the in-memory config cache.
func ParseRaw(text string) (*Config, error) {
	cfg := Default()
	if err := yaml.Unmarshal([]byte(text), cfg); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return cfg, nil
}

// SaveRaw validates then writes YAML text supplied by the dashboard editor.
func SaveRaw(text string) error {
	if _, err := ParseRaw(text); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ConfigFile()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(ConfigFile(), []byte(text), 0o600); err != nil {
		return err
	}
	_, err := Reload()
	return err
}

func writeFile(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# Antares configuration\n# Docs: https://github.com/enowdev/antares\n"

	// Serialise writes so two concurrent saves cannot interleave, and use a
	// unique temp file so a rename can never pick up another writer's partial
	// contents.
	saveMu.Lock()
	defer saveMu.Unlock()

	tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(append([]byte(header), out...)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// loadDotEnv populates process env from a KEY=VALUE file without overwriting
// variables that are already set.
func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}

// applyEnv layers ANTARES_* variables plus a few well-known provider keys.
func applyEnv(c *Config) {
	str := func(env string, dst *string) {
		if v, ok := os.LookupEnv(env); ok && strings.TrimSpace(v) != "" {
			*dst = v
		}
	}
	num := func(env string, dst *int) {
		if v, ok := os.LookupEnv(env); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				*dst = n
			}
		}
	}
	boolean := func(env string, dst *bool) {
		if v, ok := os.LookupEnv(env); ok {
			if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				*dst = b
			}
		}
	}

	str("ANTARES_MODEL", &c.Model.Default)
	str("ANTARES_PROVIDER", &c.Model.Provider)
	str("ANTARES_BASE_URL", &c.Model.BaseURL)
	str("ANTARES_API_KEY", &c.Model.APIKey)
	// Remember that these came from the environment: ResolveProvider lets an
	// env-supplied credential override a named provider's stored one, while a
	// stale value sitting in config.yaml does not.
	if v, ok := os.LookupEnv("ANTARES_BASE_URL"); ok && strings.TrimSpace(v) != "" {
		c.inlineBaseURLFromEnv = true
	}
	if v, ok := os.LookupEnv("ANTARES_API_KEY"); ok && strings.TrimSpace(v) != "" {
		c.inlineAPIKeyFromEnv = true
	}

	str("ANTARES_DB_DRIVER", &c.Database.Driver)
	str("ANTARES_DB_DSN", &c.Database.DSN)
	str("DATABASE_URL", &c.Database.DSN)

	str("ANTARES_HOST", &c.Server.Host)
	num("ANTARES_PORT", &c.Server.Port)
	str("ANTARES_AUTH_TOKEN", &c.Server.AuthToken)
	boolean("ANTARES_AUTH_DISABLED", &c.Server.AuthDisabled)
	str("ANTARES_PUBLIC_URL", &c.Server.PublicURL)

	str("ANTARES_LOG_LEVEL", &c.Logging.Level)
	str("ANTARES_LOG_FILE", &c.Logging.File)
	boolean("ANTARES_LOG_JSON", &c.Logging.JSON)

	str("ANTARES_WORKSPACE", &c.Agent.Workspace)
	num("ANTARES_MAX_TURNS", &c.Agent.MaxTurns)

	boolean("ANTARES_RAG_ENABLED", &c.RAG.Enabled)
	str("ANTARES_RAG_EMBED_MODEL", &c.RAG.EmbedModel)
	str("ANTARES_RAG_RERANK_MODE", &c.RAG.RerankMode)
	str("ANTARES_RAG_RERANK_KEY", &c.RAG.RerankAPIKey)

	str("TELEGRAM_BOT_TOKEN", &c.Gateway.Telegram.BotToken)
	str("DISCORD_BOT_TOKEN", &c.Gateway.Discord.BotToken)

	// Provider API keys: explicit ANTARES_PROVIDER_<NAME>_API_KEY wins, then the
	// provider's declared api_key_env, then conventional vendor variables.
	for name, p := range c.Providers {
		envName := "ANTARES_PROVIDER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
		if v := os.Getenv(envName); v != "" {
			p.APIKey = v
		} else if p.APIKey == "" && p.APIKeyEnv != "" {
			if v := os.Getenv(p.APIKeyEnv); v != "" {
				p.APIKey = v
			}
		}
		urlEnv := "ANTARES_PROVIDER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_BASE_URL"
		if v := os.Getenv(urlEnv); v != "" {
			p.BaseURL = v
		}
		c.Providers[name] = p
	}
}

// Normalize applies the same defaulting and cleanup that a save would, without
// writing anything. Callers that publish a config to other goroutines and then
// persist it in the background use this to normalize up front, so the async
// write does not mutate a shared struct. See SaveNormalizedAt.
func Normalize(c *Config) { normalize(c) }

func normalize(c *Config) {
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	if c.MCP.Servers == nil {
		c.MCP.Servers = map[string]MCPServer{}
	}
	if c.Tools.Timeouts == nil {
		c.Tools.Timeouts = map[string]int{}
	}
	if c.Tools.Platform == nil {
		c.Tools.Platform = map[string]string{}
	}
	c.Agent.Workspace = Expand(c.Agent.Workspace)
	c.Terminal.CWD = Expand(c.Terminal.CWD)
	c.Logging.File = Expand(c.Logging.File)
	if c.Database.Driver == "sqlite" {
		c.Database.DSN = Expand(c.Database.DSN)
	}
	for i, d := range c.Skills.Dirs {
		c.Skills.Dirs[i] = Expand(d)
	}
	if c.Server.Port <= 0 {
		c.Server.Port = 8787
	}
	if c.Agent.MaxTurns <= 0 {
		c.Agent.MaxTurns = 200
	}
	// Negative is meaningless; treat as default cap. 0 stays unlimited.
	if c.Display.MaxLiveReasoningChars < 0 {
		c.Display.MaxLiveReasoningChars = 48_000
	}
}

// ResolveProvider returns the provider entry used for a model call, falling back
// to the inline model.* fields when no named provider matches.
//
// Top-level model.base_url / model.api_key are legacy "inline provider" overrides.
// A value left in the *config file* must NOT clobber a named provider that
// already has its own base_url or api_key — otherwise switching the UI to
// antigravity/gemini while stale CodeBuddy values remain in model.* silently
// routes Claude to /v1 with the wrong key (cascading 401s).
//
// ANTARES_BASE_URL / ANTARES_API_KEY are the documented exception: an env var is
// an explicit, per-run instruction from the operator, so it still wins over the
// named provider's stored credentials.
func (c *Config) ResolveProvider(name string) (string, Provider) {
	if name == "" {
		name = c.Model.Provider
	}
	if p, ok := c.Providers[name]; ok {
		if name == c.Model.Provider {
			if v := strings.TrimSpace(c.Model.BaseURL); v != "" &&
				(c.inlineBaseURLFromEnv || strings.TrimSpace(p.BaseURL) == "") {
				p.BaseURL = c.Model.BaseURL
			}
			if v := strings.TrimSpace(c.Model.APIKey); v != "" &&
				(c.inlineAPIKeyFromEnv || strings.TrimSpace(p.APIKey) == "") {
				p.APIKey = c.Model.APIKey
			}
		}
		return name, p
	}
	return name, Provider{
		Kind:        "openai-compatible",
		BaseURL:     c.Model.BaseURL,
		APIKey:      c.Model.APIKey,
		Enabled:     true,
		Label:       name,
		TimeoutSecs: 300,
	}
}

// ClearInlineModelCredentials wipes top-level model.base_url and model.api_key.
// Call this whenever the active provider changes so a previous provider's
// credentials cannot leak into ResolveProvider for the next one.
func (c *Config) ClearInlineModelCredentials() {
	c.Model.BaseURL = ""
	c.Model.APIKey = ""
}
