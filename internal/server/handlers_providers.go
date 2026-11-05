package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/providers"
)

// handleProviderModelInfo tries to discover a model's context window from the
// provider's live model list, so the UI can auto-fill it when adding a model.
// Returns { found, context_window }. A model the provider does not report is
// found:false — the caller then asks the user for the value.
func (s *Server) handleProviderModelInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	modelID := strings.TrimSpace(r.URL.Query().Get("id"))
	if modelID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model id is required"))
		return
	}
	models, err := s.agent.Models(r.Context(), id)
	if err != nil {
		// Fetch failed — not fatal, the UI falls back to manual entry.
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	for _, m := range models {
		if m.ID == modelID {
			writeJSON(w, http.StatusOK, map[string]any{
				"found":          true,
				"context_window": m.ContextWindow,
				"name":           m.Name,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": false})
}

// handleContextWindow reports the active model's token budget, so the composer's
// context gauge can show "0 / <window>" before the first turn (usage events
// carry the window once a turn runs, but not before one has). Mirrors the
// agent's own resolution: per-model provider meta, then the configured window,
// then a sane default.
func (s *Server) handleContextWindow(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	// The dashboard ModelPicker sends "provider/model-id" (e.g. "enx/kimi-k2.6").
	// Split it so resolveContextWindow can prefer the caller-named provider
	// and match against its Models/ModelMeta lists.
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" && strings.Contains(model, "/") {
		provider, model, _ = strings.Cut(model, "/")
	}
	if model == "" {
		model = cfg.Model.Default
	}
	if provider == "" {
		provider = cfg.Model.Provider
	}
	window := resolveContextWindowFor(cfg, provider, model)
	writeJSON(w, http.StatusOK, map[string]any{"context_window": window, "model": model, "provider": provider})
}

// resolveContextWindowFor is resolveContextWindow with an explicit provider
// hint, so a caller who names the provider (dashboard picker) skips the
// heuristic that walks every provider config. Falls through to the general
// cascade when the hint is empty.
func resolveContextWindowFor(cfg *config.Config, provider, model string) int {
	if cfg == nil || model == "" {
		return 128000
	}
	if provider != "" {
		p := cfg.Providers[provider]
		if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
			return m.ContextWindow
		}
		if meta, ok := providers.MetaByProvider(provider, model); ok && meta.ContextWindow > 0 {
			return meta.ContextWindow
		}
		if p.Kind == "openai-compatible" || p.Kind == "custom" {
			if meta, ok := providers.MetaByAnyProvider(model); ok && meta.ContextWindow > 0 {
				return meta.ContextWindow
			}
		}
	}
	return resolveContextWindow(cfg, model)
}

// resolveContextWindow mirrors agent.contextWindowFor so the dashboard's
// gauge and the compaction threshold cannot disagree. Cascade:
//
//  1. Per-provider user override in cfg.Providers[…].ModelMeta — but only
//     for the provider that actually declares the model, not every one.
//  2. Strict providers.MetaByProvider — for the model's owning provider.
//  3. Proxy fallback: for openai-compatible / custom providers only,
//     search the whole models.dev snapshot by bare id.
//  4. Global fallback cfg.Model.ContextWindow — the Settings > Model page's
//     "Context Window" field. 0 (default) means "not set" so we skip.
//  5. 128000 safety net.
//
// The earlier version iterated every provider in the config and returned the
// first hit, which caused providers.MetaByProvider's loose fallback to attribute
// the wrong context/cost to any unrelated provider — e.g. reading Anthropic's
// entry for a request against a Kimi model.
func resolveContextWindow(cfg *config.Config, model string) int {
	if cfg == nil || model == "" {
		return 128000
	}
	candidates := candidateProviders(cfg, model)
	// First pass: strict provider/id lookup for each candidate.
	for _, providerID := range candidates {
		p := cfg.Providers[providerID]
		if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
			return m.ContextWindow
		}
		if meta, ok := providers.MetaByProvider(providerID, model); ok && meta.ContextWindow > 0 {
			return meta.ContextWindow
		}
	}
	// Second pass: proxy fallback. If any candidate provider is an
	// openai-compatible proxy (EnxAPI, LiteLLM, custom endpoint), consult
	// the bare-id lookup — those proxies re-serve official models under
	// real ids without cataloguing them anywhere models.dev can see.
	for _, providerID := range candidates {
		if !isProxyKind(cfg.Providers[providerID].Kind) {
			continue
		}
		if meta, ok := providers.MetaByAnyProvider(model); ok && meta.ContextWindow > 0 {
			return meta.ContextWindow
		}
	}
	if cfg.Model.ContextWindow > 0 {
		return cfg.Model.ContextWindow
	}
	return 128000
}

// isProxyKind reports whether a provider is an OpenAI-compatible passthrough
// that re-serves official models under real ids. Only such providers opt in
// to the bare-id metadata fallback; first-party providers (anthropic, openai,
// gemini, cursor-agent, opencode) always resolve by strict provider/id.
func isProxyKind(kind string) bool {
	switch kind {
	case "openai-compatible", "custom":
		return true
	default:
		return false
	}
}

// candidateProviders returns provider ids to check for a model, in priority
// order. The active provider wins so /api/context-window (which reads the
// default model) sees the same answer as an active turn. After that, any
// provider whose Models list *or* ModelMeta keys mention the id — a provider
// that carries a per-model meta override has clearly claimed the model.
// Result is de-duplicated and never empty when a provider is configured.
func candidateProviders(cfg *config.Config, model string) []string {
	seen := map[string]bool{}
	var out []string
	push := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	push(cfg.Model.Provider)
	for providerID, p := range cfg.Providers {
		if _, ok := p.ModelMeta[model]; ok {
			push(providerID)
			continue
		}
		for _, m := range p.Models {
			if m == model {
				push(providerID)
				break
			}
		}
	}
	return out
}

// handleAddProviderModel adds a model id to providers.<id>.models, with an
// optional context window stored in model_meta. Manually added models then
// appear in the model list alongside auto-discovered ones (see agent.Models).
func (s *Server) handleAddProviderModel(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Model         string `json:"model"`
		ContextWindow int    `json:"context_window"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	modelID := strings.TrimSpace(body.Model)
	if modelID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model id is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.Provider{}
	}
	p := cfg.Providers[id]
	// Append unless already present.
	exists := false
	for _, m := range p.Models {
		if m == modelID {
			exists = true
			break
		}
	}
	if !exists {
		p.Models = append(p.Models, modelID)
	}
	if body.ContextWindow > 0 {
		if p.ModelMeta == nil {
			p.ModelMeta = map[string]config.ModelMeta{}
		}
		p.ModelMeta[modelID] = config.ModelMeta{ContextWindow: body.ContextWindow}
	}
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteProviderModel removes a manually added model id (and its meta).
func (s *Server) handleDeleteProviderModel(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	modelID := r.PathValue("model")

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p := cfg.Providers[id]
	out := p.Models[:0]
	for _, m := range p.Models {
		if m != modelID {
			out = append(out, m)
		}
	}
	p.Models = out
	delete(p.ModelMeta, modelID)
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleProviderSettings saves per-provider settings: base URL, request
// timeout, and custom headers. Credentials go through the key endpoint; this is
// everything else a provider entry carries.
func (s *Server) handleProviderSettings(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		BaseURL     *string           `json:"base_url"`
		Kind        *string           `json:"kind"`
		Label       *string           `json:"label"`
		TimeoutSecs *int              `json:"timeout_seconds"`
		Headers     map[string]string `json:"headers"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	p := cfg.Providers[id]
	if body.Headers != nil {
		headers, err := config.NormalizeProviderHeaders(body.Headers)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.Headers = headers
	}
	// Custom providers (user-named entries, plus the legacy "custom" slot) may
	// point at loopback or LAN addresses; built-ins keep their catalogue rule.
	sp := lookupSetupProvider(cfg, id)
	custom := sp == nil || sp.Custom
	local := sp != nil && sp.Local
	if body.BaseURL != nil {
		baseURL := strings.TrimSpace(*body.BaseURL)
		if baseURL != "" {
			if err := s.validateChosenBaseURL(r.Context(), baseURL, custom, local); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		p.BaseURL = baseURL
	}
	if body.Kind != nil {
		// A built-in's wire format is part of what it is; only a provider
		// the user defined may switch, e.g. after picking the wrong format.
		if !custom {
			writeError(w, http.StatusBadRequest, errors.New("the API format of a built-in provider cannot be changed"))
			return
		}
		kind, err := customProviderKind(*body.Kind)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.Kind = kind
	}
	if body.Label != nil {
		if label := strings.TrimSpace(*body.Label); label != "" {
			p.Label = label
		}
	}
	if body.TimeoutSecs != nil {
		p.TimeoutSecs = *body.TimeoutSecs
	}
	cfg.Providers[id] = p

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// customProviderKinds are the wire formats a user-defined provider may speak.
// Each takes a custom base URL, so a gateway or proxy that re-serves one of
// these APIs can be added as its own provider. The empty kind means OpenAI
// chat completions, the one nearly every gateway serves.
var customProviderKinds = map[string]bool{
	"openai-compatible": true,
	"anthropic":         true,
	"gemini":            true,
	"codex":             true,
}

// customProviderKind validates the kind a user picked for a custom provider.
func customProviderKind(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "custom" {
		return "openai-compatible", nil
	}
	if !customProviderKinds[kind] {
		return "", fmt.Errorf("unsupported API format %q: use openai-compatible, anthropic, gemini or codex", kind)
	}
	return kind, nil
}

// handleCreateProvider adds a user-defined provider: a name, a base URL in
// one of the customProviderKinds formats, and an optional key. Any number may
// exist side by side, each under its own id, and
// loopback/LAN endpoints are accepted — the user is pointing Antares at their
// own service.
func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Name    string            `json:"name"`
		Kind    string            `json:"kind"`
		BaseURL string            `json:"base_url"`
		APIKey  string            `json:"api_key"`
		Headers map[string]string `json:"headers"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	kind, err := customProviderKind(body.Kind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	headers, err := config.NormalizeProviderHeaders(body.Headers)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("a name is required"))
		return
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("a base URL is required"))
		return
	}
	if err := s.validateCustomProviderBaseURL(r.Context(), baseURL); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := CustomProviderID(cfg, name)

	// Verify the pair now so a bad endpoint or key surfaces at creation time
	// rather than on the first turn. A keyless service is allowed.
	key := strings.TrimSpace(body.APIKey)
	if key != "" || len(headers) > 0 {
		client, err := llm.New(llm.Options{
			Kind: kind, BaseURL: baseURL, APIKey: key, Headers: headers,
			ProviderID: id, Timeout: 30 * time.Second,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if _, err := client.Models(ctx); err != nil {
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
	}

	cfg.Providers[id] = config.Provider{
		Kind: kind, BaseURL: baseURL, APIKey: key, Headers: headers,
		Enabled: true, Label: name,
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// handleDeleteProvider removes a user-defined provider. Built-in catalogue
// entries and the active provider are refused.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	// The legacy "custom" slot behaves like any user-defined provider: it can
	// be deleted. Other built-ins cannot.
	if isCatalogueProviderID(id) && id != "custom" {
		writeError(w, http.StatusBadRequest, errors.New("built-in providers cannot be deleted"))
		return
	}
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, ok := cfg.Providers[id]; !ok {
		writeError(w, http.StatusNotFound, errors.New("unknown provider"))
		return
	}
	if cfg.Model.Provider == id {
		writeError(w, http.StatusBadRequest, errors.New("this provider is active — pick another model before deleting it"))
		return
	}
	delete(cfg.Providers, id)
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
