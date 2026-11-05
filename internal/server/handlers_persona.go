package server

import (
	"fmt"
	"net/http"

	"github.com/enowdev/antares/internal/config"
)

// personaFile is one of the global Markdown files that shape the agent:
// SOUL.md (who it is), AGENTS.md (standing instructions) and USER.md (facts
// about the user).
type personaFile struct {
	path func() string
	load func() string
	save func(string) error
}

var personaFiles = map[string]personaFile{
	"soul":   {path: config.SoulPath, load: config.Soul, save: config.SaveSoul},
	"agents": {path: config.AgentsMDPath, load: config.LoadAgentsMD, save: config.SaveAgentsMD},
	"user":   {path: config.UserMDPath, load: config.LoadUserMD, save: config.SaveUserMD},
}

func lookupPersonaFile(w http.ResponseWriter, r *http.Request) (string, personaFile, bool) {
	name := r.PathValue("file")
	f, ok := personaFiles[name]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown persona file %q (want soul, agents or user)", name))
		return "", personaFile{}, false
	}
	return name, f, true
}

func personaResponse(name string, f personaFile) map[string]any {
	out := map[string]any{"content": f.load(), "path": f.path()}
	if name == "soul" {
		out["unset"] = config.SoulIsUnset()
	}
	return out
}

// handleGetPersona returns one persona file as {content, path}. A missing
// AGENTS.md or USER.md is empty content; the soul falls back to its default.
func (s *Server) handleGetPersona(w http.ResponseWriter, r *http.Request) {
	name, f, ok := lookupPersonaFile(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, personaResponse(name, f))
}

// handleSavePersona writes one persona file from {content}. Saving empty
// AGENTS.md or USER.md removes it; an empty soul resets to the unset default.
// Authorization matches POST /api/soul, which this generalises.
func (s *Server) handleSavePersona(w http.ResponseWriter, r *http.Request) {
	name, f, ok := lookupPersonaFile(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := f.save(body.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, personaResponse(name, f))
}
