package server

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/providers"
	"github.com/enowdev/antares/internal/tools"
)

func configPath() string { return config.ConfigFile() }

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"values":  s.config().Redacted(),
		"schema":  config.Schema(),
		"profile": config.ActiveProfile(),
		"path":    configPath(),
	})
}

func (s *Server) handleConfigSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schema": config.Schema()})
}

// handleUpdateConfig applies dotted-path updates and reloads dependent services.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Updates map[string]any `json:"updates"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Updates) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no changes supplied"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Apply in a deterministic order so an error message names the same field
	// on every retry.
	paths := make([]string, 0, len(body.Updates))
	for p := range body.Updates {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		value := body.Updates[path]
		// A redacted secret coming back unchanged means "leave it alone".
		if str, ok := value.(string); ok && strings.Contains(str, "••••") {
			continue
		}
		if err := cfg.SetPath(path, value); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		// Changing (or clearing) the dashboard password must not leave old
		// logins valid.
		if path == "server.dashboard_password_hash" {
			s.invalidateDashSessions()
		}
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": len(paths), "values": s.config().Redacted()})
}

func (s *Server) handleGetRawConfig(w http.ResponseWriter, r *http.Request) {
	text, err := config.Raw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"yaml": text, "path": configPath()})
}

func (s *Server) handleSaveRawConfig(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveRaw(body.YAML); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// applyReload rebuilds services that depend on configuration.
func (s *Server) applyReload() error {
	if s.reloadFn == nil {
		cfg, err := config.Reload()
		if err != nil {
			return err
		}
		s.SetConfig(cfg)
		if s.agent != nil {
			s.agent.SetConfig(cfg)
		}
		if manager := s.currentSkills(); manager != nil {
			manager.SetDisabled(cfg.Skills.Disabled)
		}
		if s.gateway != nil {
			s.gateway.SetConfig(cfg)
		}
		return nil
	}
	if err := s.reloadFn(); err != nil {
		return err
	}
	cfg := config.Get()
	s.SetConfig(cfg)
	if manager := s.currentSkills(); manager != nil {
		manager.SetDisabled(cfg.Skills.Disabled)
	}
	return nil
}

// ---- models -----------------------------------------------------------------

func (s *Server) handleModelOptions(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()

	type providerInfo struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Kind    string `json:"kind"`
		Enabled bool   `json:"enabled"`
		HasKey  bool   `json:"has_key"`
		// Local endpoints need no credential, but that is not the same as being
		// ready: nothing may be listening. The UI marks them differently.
		Local   bool   `json:"local"`
		BaseURL string `json:"base_url"`
		Active  bool   `json:"active"`
		// Setup metadata, so the connect form can render the right fields.
		Hint            string            `json:"hint,omitempty"`
		KeyHint         string            `json:"key_hint,omitempty"`
		KeyURL          string            `json:"key_url,omitempty"`
		KeyLabel        string            `json:"key_label,omitempty"`
		Note            string            `json:"note,omitempty"`
		NeedsRegion     bool              `json:"needs_region,omitempty"`
		NeedsAPIVersion bool              `json:"needs_api_version,omitempty"`
		NeedsBaseURL    bool              `json:"needs_base_url,omitempty"`
		TimeoutSecs     int               `json:"timeout_seconds,omitempty"`
		Headers         map[string]string `json:"headers,omitempty"`
		// Custom marks a user-defined provider. Customs always group under
		// "API key" — even a localhost endpoint is a configured service, not
		// one of the built-in local runtimes.
		Custom bool `json:"custom,omitempty"`
	}

	// Every provider from the catalogue (configured or not), so the new kinds
	// can be set up from here — then any custom providers only in the config.
	// The catalogue's "custom" entry belongs to the first-run wizard, but older
	// installations may still use that id for their real custom provider.
	seen := map[string]bool{}
	providerList := make([]providerInfo, 0)
	exposeHeaders := cfg.Server.DashboardLocked() || s.bearerAuthorizedOrQuery(r)
	for _, sp := range setupProviderCatalogue(cfg) {
		p := cfg.Providers[sp.ID]
		if sp.Custom && !legacyCustomProviderInUse(cfg, p) {
			seen[sp.ID] = true
			continue
		}
		label, kind := sp.Label, sp.Kind
		if sp.Custom {
			label = firstNonEmpty(p.Label, sp.Label)
			kind = firstNonEmpty(p.Kind, sp.Kind)
		}
		info := providerInfo{
			ID: sp.ID, Label: label, Kind: kind,
			Enabled: p.Enabled, HasKey: p.APIKey != "", Local: sp.Local,
			BaseURL: firstNonEmpty(p.BaseURL, sp.BaseURL), Active: sp.ID == cfg.Model.Provider,
			Hint: sp.Hint, KeyHint: sp.KeyHint, KeyURL: sp.KeyURL, KeyLabel: sp.KeyLabel,
			Note: sp.Note, NeedsRegion: sp.NeedsRegion, NeedsAPIVersion: sp.NeedsAPIVersion,
			NeedsBaseURL: sp.NeedsBaseURL, TimeoutSecs: p.TimeoutSecs, Custom: sp.Custom,
		}
		if sp.Custom && exposeHeaders {
			info.Headers = p.Headers
		}
		providerList = append(providerList, info)
		seen[sp.ID] = true
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := cfg.Providers[name]
		info := providerInfo{
			ID: name, Label: firstNonEmpty(p.Label, name), Kind: p.Kind, Enabled: p.Enabled,
			HasKey: p.APIKey != "", BaseURL: p.BaseURL,
			Active: name == cfg.Model.Provider, TimeoutSecs: p.TimeoutSecs,
			Custom: true, NeedsBaseURL: true,
		}
		if exposeHeaders {
			info.Headers = p.Headers
		}
		providerList = append(providerList, info)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active":    map[string]string{"model": cfg.Model.Default, "provider": cfg.Model.Provider},
		"providers": providerList,
	})
}

func legacyCustomProviderInUse(cfg *config.Config, p config.Provider) bool {
	return cfg.Model.Provider == "custom" ||
		p.Enabled ||
		strings.TrimSpace(p.BaseURL) != "" ||
		strings.TrimSpace(p.APIKey) != "" ||
		strings.TrimSpace(p.APIKeyEnv) != "" ||
		strings.TrimSpace(p.APIVersion) != "" ||
		strings.TrimSpace(p.Region) != "" ||
		len(p.Headers) > 0 ||
		len(p.Models) > 0 ||
		len(p.ModelMeta) > 0
}

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	cfg := s.config()

	// Calling a provider we know has no credential just turns a known state
	// into an opaque 401. Report the missing key instead.
	id, p := cfg.ResolveProvider(provider)
	if p.APIKey == "" && !isLocalEndpoint(p.BaseURL) && !customProviderHasHeaders(cfg, id, p) {
		writeJSON(w, http.StatusOK, map[string]any{
			"models": []any{}, "needs_key": true, "provider": id,
		})
		return
	}

	models, err := s.agent.Models(r.Context(), provider)
	if err != nil {
		// Soft errors so the page can still render the tab. "Nothing is running
		// there" is a different situation from "the provider said no", and the
		// UI advises differently on each.
		out := map[string]any{"models": []any{}, "error": err.Error(), "provider": id}
		if llm.IsUnreachable(err) {
			out["unreachable"] = true
			out["base_url"] = p.BaseURL
			out["local"] = isLocalEndpoint(p.BaseURL)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	for i := range models {
		models[i] = applyUserOverride(p, enrichModelInfo(id, models[i], p.Kind))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleModelListAll fetches models from every provider that has a usable
// credential (or is a reachable local endpoint) and returns them as one list,
// each tagged with the provider it came from. This is what lets the Models page
// show everything at once — pick any model and /model/set switches provider and
// model together, so there is no separate "switch provider" step.
func (s *Server) handleModelListAll(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	cfg := s.config()

	// Which providers are worth calling: a stored key, a set key-env, local, or
	// a configured custom provider with explicit request headers.
	type target struct {
		id, label string
	}
	var targets []target
	seen := map[string]bool{}
	add := func(id, label, kind string) {
		if seen[id] {
			return
		}
		p := cfg.Providers[id]
		keyed := p.APIKey != "" || (p.APIKeyEnv != "" && os.Getenv(p.APIKeyEnv) != "")
		if keyed || isLocalEndpoint(p.BaseURL) || customProviderHasHeaders(cfg, id, p) {
			targets = append(targets, target{id: id, label: firstNonEmpty(p.Label, label, id)})
			seen[id] = true
		}
	}
	for _, sp := range setupProviderCatalogue(cfg) {
		add(sp.ID, sp.Label, sp.Kind)
	}
	for name := range cfg.Providers {
		add(name, cfg.Providers[name].Label, cfg.Providers[name].Kind)
	}

	type row struct {
		llm.ModelInfo
		Provider      string `json:"provider"`
		ProviderLabel string `json:"provider_label"`
	}
	type provErr struct {
		Provider string `json:"provider"`
		Label    string `json:"label"`
		Error    string `json:"error"`
	}

	var (
		mu     sync.Mutex
		models []row
		errs   []provErr
		wg     sync.WaitGroup
	)
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			list, err := s.agent.Models(r.Context(), t.id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, provErr{Provider: t.id, Label: t.label, Error: err.Error()})
				return
			}
			p := cfg.Providers[t.id]
			for _, m := range list {
				m = applyUserOverride(p, enrichModelInfo(t.id, m, p.Kind))
				m = withOfficialReasoning(p.Kind, p.BaseURL, m)
				models = append(models, row{ModelInfo: m, Provider: t.id, ProviderLabel: t.label})
			}
		}(t)
	}
	wg.Wait()

	sort.Slice(models, func(i, j int) bool {
		if models[i].ProviderLabel != models[j].ProviderLabel {
			return models[i].ProviderLabel < models[j].ProviderLabel
		}
		return models[i].ID < models[j].ID
	})
	sort.Slice(errs, func(i, j int) bool { return errs[i].Label < errs[j].Label })

	writeJSON(w, http.StatusOK, map[string]any{
		"active": map[string]string{"model": cfg.Model.Default, "provider": cfg.Model.Provider},
		"models": models,
		"errors": errs,
	})
}

func withOfficialReasoning(kind, baseURL string, m llm.ModelInfo) llm.ModelInfo {
	cap := llm.OfficialReasoning(kind, baseURL, m.ID)
	if len(cap.Values) == 0 {
		return m
	}
	m.Reasoning = true
	m.ReasoningCap = &cap
	return m
}

// enrichModelInfo fills in llm.ModelInfo fields that the provider's own
// /models endpoint left blank (context window, pricing, capability flags) by
// consulting the bundled models.dev snapshot. Provider-reported values win
// when they are non-zero, so a live API is always trusted over a stale
// snapshot. Called before withOfficialReasoning so the reasoning ladder
// layer can still overwrite the capability flag for official providers.
func enrichModelInfo(providerID string, m llm.ModelInfo, kind string) llm.ModelInfo {
	meta, ok := providers.MetaByProvider(providerID, m.ID)
	if !ok {
		// OpenAI-compatible proxies (EnxAPI, custom endpoints, LiteLLM, …)
		// re-serve official models under their real ids. When the strict
		// provider/id lookup misses, fall back to searching every provider
		// so the picker still shows correct context/cost — but only for
		// these proxies, so a first-party provider that genuinely does not
		// carry a model never inherits some unrelated entry.
		if kind == "openai-compatible" || kind == "custom" {
			meta, ok = providers.MetaByAnyProvider(m.ID)
		}
	}
	if !ok {
		return m
	}
	if m.Name == "" || m.Name == m.ID {
		if meta.Name != "" {
			m.Name = meta.Name
		}
	}
	if m.ContextWindow == 0 && meta.ContextWindow > 0 {
		m.ContextWindow = meta.ContextWindow
	}
	if m.MaxOutput == 0 && meta.MaxOutput > 0 {
		m.MaxOutput = meta.MaxOutput
	}
	if m.InputCost == 0 && meta.Cost.Input > 0 {
		m.InputCost = meta.Cost.Input
	}
	if m.OutputCost == 0 && meta.Cost.Output > 0 {
		m.OutputCost = meta.Cost.Output
	}
	// Vision is a strict input-modality claim ("this model reads image
	// bytes"). meta.Attachment is broader — it flips on for PDF/audio/video
	// too — so folding it into Vision would mark an audio-only model as
	// vision-capable and let the picker feed it images it will reject.
	if !m.Vision && meta.Vision {
		m.Vision = true
	}
	if !m.Tools && meta.ToolCall {
		m.Tools = true
	}
	if !m.Reasoning && meta.Reasoning {
		m.Reasoning = true
	}
	return m
}

// applyUserOverride swaps in fields the user pinned in cfg.Providers[id].ModelMeta.
// Called after enrichModelInfo so the override always wins over live+generated.
func applyUserOverride(p config.Provider, m llm.ModelInfo) llm.ModelInfo {
	override, ok := p.ModelMeta[m.ID]
	if !ok {
		return m
	}
	if override.ContextWindow > 0 {
		m.ContextWindow = override.ContextWindow
	}
	return m
}

// handleOfficialReasoningCapability returns the native reasoning ladder for
// one official provider+model. Custom endpoints return an empty values list.
func (s *Server) handleOfficialReasoningCapability(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	cfg := s.config()
	provider := r.URL.Query().Get("provider")
	model := r.URL.Query().Get("model")
	if provider == "" && strings.Contains(model, "/") {
		provider, model, _ = strings.Cut(model, "/")
	}
	if provider == "" {
		provider = cfg.Model.Provider
	}
	if model == "" {
		model = cfg.Model.Default
	}
	p := cfg.Providers[provider]
	cap := llm.OfficialReasoning(p.Kind, p.BaseURL, model)
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":             provider,
		"model":                model,
		"reasoning_capability": cap,
	})
}

func (s *Server) handleModelSet(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prevProvider := cfg.Model.Provider
	cfg.Model.Default = body.Model
	if body.Provider != "" {
		cfg.Model.Provider = body.Provider
	}
	// Drop legacy top-level model.base_url/api_key whenever the active provider
	// changes (or on every set as a safety net). Stale CodeBuddy values there
	// used to override antigravity/gemini credentials in ResolveProvider.
	if body.Provider != "" && body.Provider != prevProvider {
		cfg.ClearInlineModelCredentials()
	} else if strings.TrimSpace(cfg.Model.BaseURL) != "" || strings.TrimSpace(cfg.Model.APIKey) != "" {
		// Even same-provider sets clear leftovers so a prior manual edit cannot
		// keep routing through the wrong base_url after the user fixed providers.*.
		// Only clear when the named provider already carries its own credentials.
		if p, ok := cfg.Providers[cfg.Model.Provider]; ok &&
			(strings.TrimSpace(p.BaseURL) != "" || strings.TrimSpace(p.APIKey) != "") {
			cfg.ClearInlineModelCredentials()
		}
	}
	// Normalize synchronously BEFORE publishing the pointer to the agent, so the
	// background save never mutates a struct the agent is concurrently reading
	// during a live turn. The goroutine then only marshals and writes bytes.
	config.Normalize(cfg)
	s.agent.SetConfig(cfg)
	s.SetConfig(cfg)
	savePath := config.ConfigFile()
	go func(c *config.Config, path string) {
		if err := config.SaveNormalizedAt(path, c); err != nil {
			slog.Warn("async config save failed after model switch", "error", err)
		}
	}(cfg, savePath)
	writeJSON(w, http.StatusOK, map[string]string{"model": body.Model, "provider": cfg.Model.Provider})
}

// ---- tools ------------------------------------------------------------------

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	active := map[string]bool{}
	for _, t := range s.agent.Registry().Resolve(cfg.Tools.Toolset, cfg.Tools.Enabled, cfg.Tools.Disabled) {
		active[t.Name()] = true
	}

	type toolInfo struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Enabled          bool     `json:"enabled"`
		RequiresApproval bool     `json:"requires_approval"`
		Toolsets         []string `json:"toolsets"`
	}
	all := s.agent.Registry().All()
	out := make([]toolInfo, 0, len(all))
	for _, t := range all {
		sets := tools.ToolsetsFor(t.Name())
		sort.Strings(sets)
		out = append(out, toolInfo{
			Name: t.Name(), Description: t.Description(), Enabled: active[t.Name()],
			RequiresApproval: tools.NeedsApproval(t), Toolsets: sets,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"toolset":  cfg.Tools.Toolset,
		"toolsets": tools.ToolsetNames(),
		"tools":    out,
	})
}

func (s *Server) handleToggleTool(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
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
	cfg.Tools.Enabled = removeString(cfg.Tools.Enabled, body.Name)
	cfg.Tools.Disabled = removeString(cfg.Tools.Disabled, body.Name)
	if body.Enabled {
		cfg.Tools.Enabled = append(cfg.Tools.Enabled, body.Name)
	} else {
		cfg.Tools.Disabled = append(cfg.Tools.Disabled, body.Name)
	}
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

func (s *Server) handleSetToolset(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Toolset string `json:"toolset"`
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
	cfg.Tools.Toolset = body.Toolset
	// Switching preset clears per-tool overrides so the choice is predictable.
	cfg.Tools.Enabled = nil
	cfg.Tools.Disabled = nil
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

func removeString(list []string, want string) []string {
	out := list[:0]
	for _, v := range list {
		if v != want {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
