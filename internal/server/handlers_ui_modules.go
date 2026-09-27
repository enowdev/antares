package server

import (
	"errors"
	"net/http"

	"github.com/enowdev/antares/internal/config"
)

// modulesResponse is the contract in web/src/lib/useModules.ts. Modules is
// null when display.modules is absent from config.yaml, which the dashboard
// treats as every module on.
type modulesResponse struct {
	Modules *[]string `json:"modules"`
	Preset  string    `json:"preset"`
}

func newModulesResponse(modules *[]string, preset string) modulesResponse {
	// An install that predates modules shows everything, which is the Full
	// preset. A stored list without a label is left unlabelled; the dashboard
	// derives the label from the list either way.
	if modules == nil && preset == "" {
		preset = "full"
	}
	return modulesResponse{Modules: modules, Preset: preset}
}

// handleGetModules reports the persisted dashboard module set.
func (s *Server) handleGetModules(w http.ResponseWriter, r *http.Request) {
	modules, preset, err := config.PersistedModules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, newModulesResponse(modules, preset))
}

// handleSetModules persists a module set chosen in Settings.
func (s *Server) handleSetModules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Modules *[]string `json:"modules"`
		Preset  string    `json:"preset"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Modules == nil {
		writeError(w, http.StatusBadRequest, errors.New("modules is required"))
		return
	}
	if err := config.ValidateModules(*body.Modules); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := config.ValidatePreset(body.Preset); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := config.SetModules(*body.Modules, body.Preset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, newModulesResponse(cfg.Display.Modules, cfg.Display.Preset))
}
