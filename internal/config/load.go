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

// Save writes the config to the active profile file and refreshes the cache.
func Save(cfg *Config) error {
	normalize(cfg)
	if err := writeFile(ConfigFile(), cfg); err != nil {
		return err
	}
	mu.Lock()
	loaded = cfg
	mu.Unlock()
	return nil
}

// Raw returns the on-disk YAML text for the active profile.
func Raw() (string, error) {
	b, err := os.ReadFile(ConfigFile())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// SaveRaw validates then writes YAML text supplied by the dashboard editor.
func SaveRaw(text string) error {
	cfg := Default()
	if err := yaml.Unmarshal([]byte(text), cfg); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# Antares configuration\n# Docs: https://github.com/enowdev/antares\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), out...), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
func (c *Config) ResolveProvider(name string) (string, Provider) {
	if name == "" {
		name = c.Model.Provider
	}
	if p, ok := c.Providers[name]; ok {
		if c.Model.BaseURL != "" && name == c.Model.Provider {
			p.BaseURL = c.Model.BaseURL
		}
		if c.Model.APIKey != "" && name == c.Model.Provider {
			p.APIKey = c.Model.APIKey
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
