package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/providers"
)

// handleProviderModelInfo tries to discover a model's context window from the
// provider's live model list, so the UI can auto-fill it when adding a model.
// Returns { found, context_window }. A model the provider does not report is
// found:false — the caller then asks the user for the value.
func (s *Server) handleProviderModelInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		// Keep accepting the earlier dashboard parameter while model is rolled
		// out; model is the canonical API name.
		modelID = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if modelID == "" {
		writeError(w, http.StatusBadRequest, errors.New("a model id is required"))
		return
	}
	// Agent integrations (Cursor) are not chat-model providers: fail before
	// the generic agent.Models -> llm.New path. This handler's contract is a
	// silent fallback (found:false), so no network call is needed either way.
	if providers.CapabilityOf(s.config(), id) == providers.CapabilityAgent {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
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
				"found":                true,
				"id":                   m.ID,
				"context_window":       m.ContextWindow,
				"name":                 m.Name,
				"reasoning":            m.Reasoning,
				"reasoning_capability": m.ReasoningCapability,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": false})
}

// handleProviderModels returns the live model catalogue for an
// agent-capability provider (Cursor). It never touches cfg.Model and never
// aggregates into /api/model/list-all — that isolation is what lets Cursor
// carry its own model picker without disturbing the active chat model.
func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	id := r.PathValue("id")
	cfg := s.config()
	if providers.CapabilityOf(cfg, id) != providers.CapabilityAgent {
		writeError(w, http.StatusBadRequest,
			errors.New("this provider does not expose a dedicated model endpoint"))
		return
	}

	_, p := cfg.ResolveProvider(id)
	key := strings.TrimSpace(p.APIKey)
	if key == "" {
		// No resolved credential: report the need without making a network call.
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}, "needs_key": true})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if s.cursorRunner == nil {
		writeError(w, http.StatusBadGateway, errors.New("Cursor is unavailable"))
		return
	}
	catalog, err := s.cursorRunner.Catalog(ctx, false)
	if err != nil {
		if cursor.IsAuthError(err) {
			writeJSON(w, http.StatusOK, map[string]any{"models": []any{}, "error": err.Error()})
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}

	type modelOut struct {
		ID          string                  `json:"id"`
		Name        string                  `json:"name"`
		Description string                  `json:"description,omitempty"`
		Aliases     []string                `json:"aliases"`
		Parameters  []cursor.ModelParameter `json:"parameters"`
		Variants    []cursor.ModelVariant   `json:"variants"`
	}
	out := make([]modelOut, 0, len(catalog.Items))
	for _, m := range catalog.Items {
		aliases := append([]string{}, m.Aliases...)
		parameters := append([]cursor.ModelParameter{}, m.Parameters...)
		for i := range parameters {
			parameters[i].Values = append([]cursor.ModelParameterValue{}, parameters[i].Values...)
		}
		variants := append([]cursor.ModelVariant{}, m.Variants...)
		for i := range variants {
			variants[i].Params = append([]cursor.ModelParameterSelection{}, variants[i].Params...)
		}
		out = append(out, modelOut{
			ID:          m.ID,
			Name:        m.DisplayName,
			Description: m.Description,
			Aliases:     aliases,
			Parameters:  parameters,
			Variants:    variants,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// handleContextWindow reports the active model's token budget, so the composer's
// context gauge can show "0 / <window>" before the first turn (usage events
// carry the window once a turn runs, but not before one has). Mirrors the
// agent's own resolution: per-model provider meta, then the configured window,
// then a sane default.
func (s *Server) handleContextWindow(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	model := cfg.Model.Default
	// Same precedence as the agent's contextWindowFor: per-model meta override,
	// then the provider catalogue (real windows for known models like glm-5.2's
	// 1M), then the configured window, then a sane default.
	window := 128000
	if w := providers.ContextWindow(model); w > 0 {
		window = w
	} else if cfg.Model.ContextWindow > 0 {
		window = cfg.Model.ContextWindow
	}
	for _, p := range cfg.Providers {
		if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
			window = m.ContextWindow
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"context_window": window, "model": model})
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
	// Agent integrations (Cursor) do not curate a manual model whitelist —
	// their catalogue is discovered live via GET /api/providers/{id}/models.
	// Reject before any config mutation, matching the same boundary as
	// /api/model/set and /api/model/list.
	if providers.CapabilityOf(cfg, id) == providers.CapabilityAgent {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"%s is an agent integration; its models are discovered via GET /api/providers/%s/models", id, id))
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
	// Same boundary as handleAddProviderModel: Cursor has no manual model
	// whitelist to delete from.
	if providers.CapabilityOf(cfg, id) == providers.CapabilityAgent {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"%s is an agent integration; its models are discovered via GET /api/providers/%s/models", id, id))
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
	if body.BaseURL != nil {
		baseURL := strings.TrimSpace(*body.BaseURL)
		allowLocal := false
		for _, provider := range setupProviderCatalogue(cfg) {
			if provider.ID == id {
				allowLocal = provider.Local
				break
			}
		}
		if baseURL != "" {
			if err := s.validateProviderBaseURL(r.Context(), baseURL, allowLocal); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		p.BaseURL = baseURL
	}
	if body.TimeoutSecs != nil {
		p.TimeoutSecs = *body.TimeoutSecs
	}
	if body.Headers != nil {
		p.Headers = body.Headers
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
