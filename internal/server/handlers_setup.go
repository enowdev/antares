package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
)

// setupProvider is one option in the onboarding picker. The catalogue lives
// here rather than in the frontend so the terminal wizard and the web wizard
// stay in step.
type setupProvider struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Hint    string   `json:"hint"`
	KeyHint string   `json:"key_hint,omitempty"`
	KeyURL  string   `json:"key_url,omitempty"`
	BaseURL string   `json:"base_url,omitempty"`
	Local   bool     `json:"local"`
	Models  []string `json:"models,omitempty"`
	HasKey  bool     `json:"has_key"`
	// KeyLabel overrides the "API key" label (e.g. "Service account JSON").
	KeyLabel string `json:"key_label,omitempty"`
	// Note is an extra line shown under the form (e.g. how creds are supplied).
	Note string `json:"note,omitempty"`
	// NeedsRegion / NeedsAPIVersion request the extra inputs those kinds use.
	NeedsRegion     bool `json:"needs_region,omitempty"`
	NeedsAPIVersion bool `json:"needs_api_version,omitempty"`
	// NeedsBaseURL forces the endpoint field (e.g. the Azure resource URL).
	NeedsBaseURL bool `json:"needs_base_url,omitempty"`
	// Custom marks a user-defined provider: the user names it and points
	// Antares at any OpenAI-compatible endpoint, local or remote.
	Custom bool `json:"custom,omitempty"`
}

func setupProviderCatalogue(cfg *config.Config) []setupProvider {
	out := []setupProvider{
		{
			ID: "openrouter", Label: "OpenRouter", Kind: "openai-compatible",
			Hint:    "One key, most models — the easiest start.",
			KeyHint: "sk-or-v1-…", KeyURL: "https://openrouter.ai/keys",
			BaseURL: "https://openrouter.ai/api/v1",
			Models: []string{
				"anthropic/claude-sonnet-4.5", "openai/gpt-5",
				"google/gemini-2.5-pro", "deepseek/deepseek-chat",
			},
		},
		{
			ID: "anthropic", Label: "Anthropic", Kind: "anthropic",
			Hint:    "Claude, direct. Extended thinking and prompt caching.",
			KeyHint: "sk-ant-…", KeyURL: "https://console.anthropic.com/settings/keys",
			BaseURL: "https://api.anthropic.com/v1",
			Models:  []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"},
		},
		{
			ID: "openai", Label: "OpenAI", Kind: "openai",
			Hint:    "GPT, direct.",
			KeyHint: "sk-…", KeyURL: "https://platform.openai.com/api-keys",
			BaseURL: "https://api.openai.com/v1",
			Models:  []string{"gpt-5", "gpt-5-mini", "gpt-4.1"},
		},
		{
			ID: "gemini", Label: "Google Gemini", Kind: "gemini",
			Hint:    "Gemini, direct.",
			KeyURL:  "https://aistudio.google.com/apikey",
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			Models:  []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		},
		{
			ID: "zai", Label: "Z.ai GLM (Coding Plan)", Kind: "anthropic",
			Hint:    "GLM models on your Z.ai coding plan.",
			KeyHint: "your Z.ai API key", KeyURL: "https://z.ai/manage-apikey/apikey-list",
			BaseURL: "https://api.z.ai/api/anthropic/v1",
			Models:  []string{"glm-5.2", "glm-4.7", "glm-4.6"},
		},
		{
			ID: "opencode", Label: "OpenCode Go (Zen)", Kind: "opencode",
			Hint:    "GLM, Kimi, DeepSeek, MiMo, MiniMax and Qwen on an OpenCode subscription.",
			KeyHint: "your OpenCode API key", KeyURL: "https://opencode.ai/auth",
			BaseURL: "https://opencode.ai/zen/go/v1",
			Note:    "One endpoint, two wire formats: MiniMax and Qwen use the Anthropic API, the rest use OpenAI. Antares routes each model automatically.",
			Models: []string{
				"glm-5.2", "kimi-k3", "deepseek-v4-pro",
				"minimax-m3", "qwen3.8-max",
			},
		},
		{
			ID: "ollama", Label: "Ollama", Kind: "openai-compatible",
			Hint:    "Runs on this machine. No key needed.",
			BaseURL: "http://127.0.0.1:11434/v1", Local: true,
		},
		{
			ID: "lmstudio", Label: "LM Studio", Kind: "openai-compatible",
			Hint:    "Runs on this machine. No key needed.",
			BaseURL: "http://127.0.0.1:1234/v1", Local: true,
		},
		{
			ID: "azure", Label: "Azure OpenAI", Kind: "azure",
			Hint:    "GPT on your Azure resource.",
			KeyHint: "your Azure API key", KeyURL: "https://portal.azure.com",
			NeedsBaseURL: true, NeedsAPIVersion: true,
			Note: "Base URL is the resource endpoint (https://<resource>.openai.azure.com). The model is your deployment name.",
		},
		{
			ID: "bedrock", Label: "AWS Bedrock", Kind: "bedrock",
			Hint:        "Claude on AWS Bedrock.",
			NeedsRegion: true,
			Note:        "Credentials come from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY in the environment. Model is a Bedrock id, e.g. anthropic.claude-3-5-sonnet-20241022-v2:0.",
		},
		{
			ID: "vertex", Label: "Google Vertex AI", Kind: "vertex",
			Hint:     "Gemini on GCP.",
			KeyLabel: "Service account JSON (or path)", NeedsRegion: true,
			KeyURL: "https://console.cloud.google.com/vertex-ai",
			Note:   "Paste the service-account key JSON (or a path to it). Project comes from the key or GOOGLE_CLOUD_PROJECT.",
		},
		{
			ID: "copilot", Label: "GitHub Copilot", Kind: "copilot",
			Hint:     "Copilot models via GitHub.",
			KeyLabel: "GitHub token",
			Note:     "Run `antares auth copilot` in a terminal to sign in, then paste the token it prints.",
		},
		{
			ID: "codex", Label: "OpenAI Responses (Codex)", Kind: "codex",
			Hint:    "The Responses API and reasoning models.",
			KeyHint: "sk-…", KeyURL: "https://platform.openai.com/api-keys",
			BaseURL: "https://api.openai.com/v1",
		},
		{
			ID: "custom", Label: "Custom provider", Kind: "openai-compatible",
			Hint:         "Any OpenAI-compatible endpoint — name it yourself.",
			NeedsBaseURL: true, Custom: true,
		},
	}
	for i := range out {
		// Resolve only configured providers. ResolveProvider intentionally
		// treats an unknown name as the legacy inline model provider, which
		// would otherwise make absent catalogue entries inherit ANTARES_API_KEY
		// and ANTARES_BASE_URL.
		if _, configured := cfg.Providers[out[i].ID]; configured {
			_, resolved := cfg.ResolveProvider(out[i].ID)
			out[i].HasKey = strings.TrimSpace(resolved.APIKey) != ""
			if resolved.BaseURL != "" {
				out[i].BaseURL = resolved.BaseURL
			}
		}
	}
	return out
}

// lookupSetupProvider finds a catalogue entry by id, or nil for ids that are
// not built-ins (user-defined providers, typos).
func lookupSetupProvider(cfg *config.Config, id string) *setupProvider {
	catalogue := setupProviderCatalogue(cfg)
	for i := range catalogue {
		if catalogue[i].ID == id {
			return &catalogue[i]
		}
	}
	return nil
}

// NeedsSetup reports whether Antares can answer at all yet.
func NeedsSetup(cfg *config.Config) bool {
	if strings.TrimSpace(cfg.Model.Default) == "" {
		return true
	}
	id, p := cfg.ResolveProvider(cfg.Model.Provider)
	return p.APIKey == "" && !isLocalEndpoint(p.BaseURL) && !customProviderHasHeaders(cfg, id, p)
}

func customProviderHasHeaders(cfg *config.Config, id string, p config.Provider) bool {
	if strings.TrimSpace(p.BaseURL) == "" || len(p.Headers) == 0 {
		return false
	}
	provider := lookupSetupProvider(cfg, id)
	return provider == nil || provider.Custom
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	home, _ := os.UserHomeDir()
	visible := setupProviderCatalogue(cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup": NeedsSetup(cfg),
		"model":       cfg.Model.Default,
		"provider":    cfg.Model.Provider,
		"workspace":   cfg.Agent.Workspace,
		"home":        home,
		"config_path": configPath(),
		"providers":   visible,
		"database":    cfg.Database.Driver,
	})
}

// handleSetupTest verifies a credential and returns the model list in one
// round trip, so the wizard can move straight to picking a model.
func (s *Server) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	if s.requireSetupAccess(w, r) {
		return
	}
	var body struct {
		Provider string            `json:"provider"`
		BaseURL  string            `json:"base_url"`
		APIKey   string            `json:"api_key"`
		Headers  map[string]string `json:"headers"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg := s.config()
	catalogue := setupProviderCatalogue(cfg)
	var chosen *setupProvider
	for i := range catalogue {
		if catalogue[i].ID == body.Provider {
			chosen = &catalogue[i]
			break
		}
	}
	if chosen == nil {
		writeError(w, http.StatusBadRequest, errors.New("unknown provider"))
		return
	}
	baseURL := firstNonEmpty(body.BaseURL, chosen.BaseURL)
	if chosen.Custom && baseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": "A base URL is required for a custom provider.",
		})
		return
	}
	if baseURL != "" {
		if err := s.validateChosenBaseURL(r.Context(), baseURL, chosen.Custom, chosen.Local); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	apiKey := body.APIKey
	// A redacted value means "keep what is stored".
	if strings.Contains(apiKey, "••••") || apiKey == "" {
		if p, ok := cfg.Providers[body.Provider]; ok {
			apiKey = p.APIKey
		}
	}
	// Headers are wizard-scoped: a custom setup mints a fresh provider id at
	// save time, so any headers stored under the legacy "custom" slot belong
	// to a different target and must not be forwarded here — that would leak
	// unrelated credentials to whatever base URL the user just typed in. For
	// built-in providers the slot key matches the stored entry, so reusing
	// its recorded headers on a reconnect is safe.
	var headers map[string]string
	if chosen.Custom {
		if body.Headers != nil {
			normalized, err := config.NormalizeProviderHeaders(body.Headers)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			headers = normalized
		}
	} else {
		headers = cfg.Providers[body.Provider].Headers
	}
	// A keyless custom service on a LAN is legitimate; everything else needs
	// a credential unless the endpoint is local.
	if apiKey == "" && !chosen.Custom && !isLocalEndpoint(baseURL) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": "An API key is required for this provider.",
		})
		return
	}

	client, err := llm.New(llm.Options{
		Kind: chosen.Kind, BaseURL: baseURL, APIKey: apiKey, Headers: headers,
		ProviderID: body.Provider, Timeout: 30 * time.Second,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	models, err := client.Models(ctx)
	if err != nil {
		if llm.IsAuthError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "error": "The provider rejected this API key.",
			})
			return
		}
		if !llm.IsUnsupported(err) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"ok": false, "error": "The provider could not be reached or returned an invalid response: " + err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "models": []any{}, "suggested": chosen.Models,
			"note": "Connected, but this provider does not publish a model list.",
		})
		return
	}

	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "models": ids, "suggested": chosen.Models,
	})
}

// handleSetupComplete writes everything the wizard collected in one save.
func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.requireSetupAccess(w, r) {
		return
	}

	var body struct {
		Provider  string            `json:"provider"`
		Name      string            `json:"name"`
		BaseURL   string            `json:"base_url"`
		APIKey    string            `json:"api_key"`
		Headers   map[string]string `json:"headers"`
		Model     string            `json:"model"`
		Workspace string            `json:"workspace"`
		Database  struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
		} `json:"database"`
		RAG struct {
			Enabled       bool   `json:"enabled"`
			EmbedProvider string `json:"embed_provider"`
			EmbedModel    string `json:"embed_model"`
			EmbedAPIKey   string `json:"embed_api_key"`
		} `json:"rag"`
		Telegram          string `json:"telegram_token"`
		Discord           string `json:"discord_token"`
		Language          string `json:"language"`
		DashboardPassword string `json:"dashboard_password"`
		// Modules is a pointer so an omitted field (leave display.modules
		// absent: every module on) differs from [] (the General preset).
		Modules *[]string `json:"modules"`
		Preset  string    `json:"preset"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Modules != nil {
		if err := config.ValidateModules(*body.Modules); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if body.Preset != "" {
		if body.Modules == nil {
			writeError(w, http.StatusBadRequest, errors.New("preset requires modules"))
			return
		}
		if err := config.ValidatePreset(body.Preset); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if strings.TrimSpace(body.Model) == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !NeedsSetup(cfg) {
		writeError(w, http.StatusConflict, errors.New("initial setup has already been completed"))
		return
	}

	chosen := lookupSetupProvider(cfg, body.Provider)
	if chosen == nil {
		writeError(w, http.StatusBadRequest, errors.New("unknown provider"))
		return
	}
	// A custom provider is stored under an id minted from the user's name, so
	// more than one can exist. An unnamed one defaults to "custom-provider"
	// with the catalogue label — a visible, manageable provider either way.
	providerID := body.Provider
	if chosen.Custom {
		providerID = CustomProviderID(cfg, body.Name)
	}
	baseURL := firstNonEmpty(body.BaseURL, chosen.BaseURL)
	if chosen.Custom && baseURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("a base URL is required for a custom provider"))
		return
	}
	if baseURL != "" {
		if err := s.validateChosenBaseURL(r.Context(), baseURL, chosen.Custom, chosen.Local); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	entry := cfg.Providers[providerID]
	entry.Kind = chosen.Kind
	entry.Enabled = true
	entry.Label = chosen.Label
	if name := strings.TrimSpace(body.Name); chosen.Custom && name != "" {
		entry.Label = name
	}
	if baseURL != "" {
		entry.BaseURL = baseURL
	}
	if chosen.Custom {
		// A named custom provider mints its own id (providerID), separate from
		// the legacy "custom" slot the wizard picker uses (body.Provider).
		// The freshly minted entry starts with no stored headers; only headers
		// explicitly submitted for this provider are persisted. Falling back
		// to the legacy slot here silently attached whatever credential lived
		// under "custom" to an unrelated host. Editing an existing entry with
		// omitted headers still keeps its own recorded headers, since we read
		// them from cfg.Providers[providerID] above.
		if body.Headers != nil {
			headers, err := config.NormalizeProviderHeaders(body.Headers)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			entry.Headers = headers
		}
	}
	if key := strings.TrimSpace(body.APIKey); key != "" && !strings.Contains(key, "••••") {
		entry.APIKey = key
	}
	cfg.Providers[providerID] = entry

	cfg.Model.Provider = providerID
	cfg.Model.Default = strings.TrimSpace(body.Model)

	if ws := strings.TrimSpace(body.Workspace); ws != "" {
		cfg.Agent.Workspace = config.Expand(ws)
		if err := os.MkdirAll(cfg.Agent.Workspace, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	if d := strings.TrimSpace(body.Database.Driver); d != "" {
		dsn := strings.TrimSpace(body.Database.DSN)
		// Verify a postgres connection now so a bad DSN is caught during
		// onboarding rather than on the next restart. Open pings and migrates.
		if d == "postgres" {
			if dsn == "" {
				writeError(w, http.StatusBadRequest, errors.New("a connection string is required for postgres"))
				return
			}
			probe, err := store.Open(r.Context(), d, dsn, 2, 5000, false)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("could not connect to postgres: %w", err))
				return
			}
			probe.Close()
		}
		cfg.Database.Driver = d
		if dsn != "" {
			cfg.Database.DSN = dsn
		}
	}

	cfg.RAG.Enabled = body.RAG.Enabled
	if body.RAG.Enabled {
		if p := strings.TrimSpace(body.RAG.EmbedProvider); p != "" {
			cfg.RAG.EmbedProvider = p
		} else if cfg.RAG.EmbedProvider == "" {
			cfg.RAG.EmbedProvider = body.Provider
		}
		if m := strings.TrimSpace(body.RAG.EmbedModel); m != "" {
			cfg.RAG.EmbedModel = m
		}
		if k := strings.TrimSpace(body.RAG.EmbedAPIKey); k != "" {
			cfg.RAG.EmbedAPIKey = k
		}
	}

	if tk := strings.TrimSpace(body.Telegram); tk != "" {
		cfg.Gateway.Enabled = true
		cfg.Gateway.Telegram.Enabled = true
		cfg.Gateway.Telegram.BotToken = tk
	}
	if tk := strings.TrimSpace(body.Discord); tk != "" {
		cfg.Gateway.Enabled = true
		cfg.Gateway.Discord.Enabled = true
		cfg.Gateway.Discord.BotToken = tk
	}
	if lang := strings.TrimSpace(body.Language); lang != "" {
		cfg.Display.Language = lang
	}
	if body.Modules != nil {
		modules := config.NormalizeModules(*body.Modules)
		cfg.Display.Modules = &modules
		cfg.Display.Preset = body.Preset
	}
	// An optional dashboard password locks the web UI behind a login. It is
	// stored hashed; the plaintext never touches config.
	if pw := strings.TrimSpace(body.DashboardPassword); pw != "" {
		hash, err := config.HashPassword(pw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		cfg.Server.DashboardPasswordHash = hash
		s.invalidateDashSessions()
	}

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"model":    cfg.Model.Default,
		"provider": cfg.Model.Provider,
		"restart_required": cfg.Database.Driver != s.db.Driver() ||
			cfg.Gateway.Telegram.Enabled || cfg.Gateway.Discord.Enabled,
	})
}

// handleSetProviderKey verifies a credential and stores it in one step, so a
// provider can be connected from wherever the user noticed it was missing
// rather than sending them to hunt through Settings.
//
// It exists because config.SetPath cannot write into the providers map: map
// values are not addressable through reflection.
func (s *Server) handleSetProviderKey(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		APIKey     string `json:"api_key"`
		BaseURL    string `json:"base_url"`
		Region     string `json:"region"`
		APIVersion string `json:"api_version"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	id := r.PathValue("id")
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chosen := lookupSetupProvider(cfg, id)
	entry, exists := cfg.Providers[id]
	if chosen == nil {
		// Not in the catalogue: still manageable when it is a user-defined
		// custom provider already present in the config.
		if !exists {
			writeError(w, http.StatusBadRequest, errors.New("unknown provider"))
			return
		}
	}
	custom := chosen == nil || chosen.Custom
	local := chosen != nil && chosen.Local
	var catalogueBaseURL string
	if chosen != nil {
		catalogueBaseURL = chosen.BaseURL
	}
	if entry.Kind == "" {
		if chosen != nil {
			entry.Kind = chosen.Kind
		} else {
			entry.Kind = "openai-compatible"
		}
	}
	baseURL := firstNonEmpty(body.BaseURL, entry.BaseURL, catalogueBaseURL)
	if baseURL != "" {
		if err := s.validateChosenBaseURL(r.Context(), baseURL, custom, local); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	region := firstNonEmpty(body.Region, entry.Region)
	apiVersion := firstNonEmpty(body.APIVersion, entry.APIVersion)
	// A blank or redacted key means "keep what is stored" (same convention as
	// the setup wizard): reconnecting to update the endpoint must not silently
	// wipe the saved credential. The connection test runs with the kept key.
	key := strings.TrimSpace(body.APIKey)
	if key == "" || strings.Contains(key, "••••") {
		key = strings.TrimSpace(entry.APIKey)
	}
	// Bedrock takes its credentials from the AWS environment, so no key here.
	// Custom providers may be keyless services on a LAN, so no key is forced.
	if key == "" && !custom && entry.Kind != "bedrock" && !isLocalEndpoint(baseURL) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "An API key is required."})
		return
	}

	// Reject a bad key here rather than saving it and failing on the next turn.
	client, err := llm.New(llm.Options{
		Kind: entry.Kind, BaseURL: baseURL, APIKey: key, Headers: entry.Headers,
		Region: region, APIVersion: apiVersion,
		ProviderID: id, Timeout: 30 * time.Second,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	models, err := client.Models(ctx)
	if err != nil {
		if llm.IsAuthError(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !llm.IsUnsupported(err) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"ok": false, "error": "The provider could not be reached or returned an invalid response: " + err.Error(),
			})
			return
		}
	}

	entry.APIKey = key
	entry.BaseURL = baseURL
	entry.Region = region
	entry.APIVersion = apiVersion
	entry.Enabled = true
	if entry.Label == "" {
		if chosen != nil {
			entry.Label = chosen.Label
		} else {
			entry.Label = id
		}
	}
	cfg.Providers[id] = entry

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": len(models)})
}

// slugifyProviderName turns a display name into a config id: lowercase
// alphanumerics with dashes for everything else.
func slugifyProviderName(name string) string {
	var b strings.Builder
	prevDash := true // suppresses a leading dash
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// isCatalogueProviderID reports whether id names a built-in provider.
func isCatalogueProviderID(id string) bool {
	return lookupSetupProvider(&config.Config{}, id) != nil
}

// CustomProviderID mints a unique config id for a user-named provider. A name
// that slugs to nothing (or to "custom" itself) becomes "custom-provider"
// — never the legacy "custom" slot, which no longer renders on the providers
// page, so a nameless setup still lands on a visible, manageable provider.
// The id avoids every built-in catalogue id and any provider already in the
// config; it is the single minter shared by the web API and both wizards.
func CustomProviderID(cfg *config.Config, name string) string {
	slug := slugifyProviderName(name)
	if slug == "" || slug == "custom" {
		slug = "custom-provider"
	}
	base := slug
	for i := 2; ; i++ {
		if _, taken := cfg.Providers[slug]; !taken && !isCatalogueProviderID(slug) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}
