package server

import (
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/version"
)

// commandDeps hands the shared command layer everything the server has wired.
func (s *Server) commandDeps() commands.Deps {
	mgr := s.currentSkills()
	return commands.Deps{
		Config:  s.config,
		Agent:   s.agent,
		Store:   s.db,
		Skills:  mgr,
		MCP:     s.mcp,
		Reload:  s.applyReload,
		Version: version.Version,
		WebURL:  s.config().Server.PublicURL,
	}
}

// handleCommandList feeds the chat composer's slash palette.
func (s *Server) handleCommandList(w http.ResponseWriter, r *http.Request) {
	surface := commands.Surface(r.URL.Query().Get("surface"))
	if surface == "" {
		surface = commands.SurfaceWeb
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": commands.Catalogue(surface)})
}

// handleCommandRun executes one slash command and returns markdown to show in
// the transcript. Commands that only the client can carry out come back with an
// action and no output.
func (s *Server) handleCommandRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input     string `json:"input"`
		SessionID string `json:"session_id"`
		Surface   string `json:"surface"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	name, args, ok := commands.Parse(body.Input)
	if !ok {
		// Accept a bare name too, so callers do not have to re-add the slash.
		name, args, ok = commands.Parse("/" + strings.TrimSpace(body.Input))
		if !ok {
			writeError(w, http.StatusBadRequest, errNotFound)
			return
		}
	}

	surface := commands.Surface(body.Surface)
	if surface == "" {
		surface = commands.SurfaceWeb
	}

	res, err := commands.Run(r.Context(), s.commandDeps(), commands.Input{
		Name:      name,
		Args:      args,
		SessionID: body.SessionID,
		Surface:   surface,
	})
	if err != nil {
		// A command that fails is a normal outcome to show in the transcript,
		// not a transport error the UI should render as a red banner.
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"output": res.Output,
		"action": res.Action,
	})
}
