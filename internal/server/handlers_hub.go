package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/hub"
)

// skillDir is where installed skills land: the first configured directory,
// falling back to the one under the Antares home.
func (s *Server) skillDir() string {
	if dirs := s.config().Skills.Dirs; len(dirs) > 0 && strings.TrimSpace(dirs[0]) != "" {
		return config.Expand(dirs[0])
	}
	return config.Path("skills")
}

// handleHubSkills browses the skill catalogue. A query naming a repository or
// a URL reaches out; anything else searches what ships in the binary.
func (s *Server) handleHubSkills(w http.ResponseWriter, r *http.Request) {
	mgr := s.currentSkills()
	query := r.URL.Query().Get("q")
	found, err := hub.SearchSkills(r.Context(), query)
	if err != nil {
		// A failed remote lookup is information, not a broken page.
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}, "error": err.Error()})
		return
	}

	// Mark what is already on disk so the UI can offer the right action.
	installed := map[string]bool{}
	if mgr != nil {
		for _, sk := range mgr.List() {
			installed[sk.Name] = true
		}
	}
	for i := range found {
		found[i].Installed = installed[found[i].Name]
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": found})
}

// handleHubInstallSkill fetches a skill and writes it into the skills
// directory, then reloads so it is usable on the next turn.
func (s *Server) handleHubInstallSkill(w http.ResponseWriter, r *http.Request) {
	mgr := s.currentSkills()
	var body struct {
		ID string `json:"id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entry, path, err := hub.InstallSkill(r.Context(), body.ID, s.skillDir())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if mgr != nil {
		_ = mgr.Reload()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": entry.Name, "path": path, "summary": entry.Summary,
	})
}

// handleHubPlugins lists the plugins on offer, marking the ones already on
// disk. Everything is bundled, so this never touches the network.
func (s *Server) handleHubPlugins(w http.ResponseWriter, r *http.Request) {
	installed := map[string]bool{}
	if mgr := s.agent.Plugins(); mgr != nil {
		for _, p := range mgr.List() {
			installed[p.Name] = true
		}
	}
	found := hub.SearchPlugins(r.Context(), r.URL.Query().Get("q"), installed)
	writeJSON(w, http.StatusOK, map[string]any{"plugins": found})
}

// handleHubInstallPlugin writes a catalogue plugin to disk and rescans. Because
// a plugin runs code, the dashboard shows its command before this is called —
// the confirmation is the UI's, and the install is the commit.
func (s *Server) handleHubInstallPlugin(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	mgr := s.agent.Plugins()
	if mgr == nil {
		writeError(w, http.StatusBadRequest, errors.New("plugins are switched off"))
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	entry, dest, err := hub.InstallPlugin(body.ID, s.pluginDir())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := mgr.Load(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": entry.Name, "dir": dest})
}

// handleHubMCP lists the MCP servers on offer.
func (s *Server) handleHubMCP(w http.ResponseWriter, r *http.Request) {
	found := hub.SearchMCP(r.Context(), r.URL.Query().Get("q"), s.config())
	writeJSON(w, http.StatusOK, map[string]any{"servers": found})
}

// handleHubInstallMCP registers a catalogue server in the configuration.
func (s *Server) handleHubInstallMCP(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		ID  string            `json:"id"`
		Env map[string]string `json:"env"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	missing, err := hub.InstallMCP(body.ID, cfg, body.Env)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// A server nobody can reach is not worth writing, but a missing key is the
	// user's to fill in — save it and say what is still needed.
	cfg.MCP.Enabled = true
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.refreshMCPConnections(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "missing_keys": missing})
}
