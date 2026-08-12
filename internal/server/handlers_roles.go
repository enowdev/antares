package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/roles"
	"github.com/enowdev/antares/internal/tools"
)

// handleRoles lists the specialist roles for the dashboard.
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"roles": []any{}})
		return
	}
	out := map[string]any{
		"roles":  reg.List(),
		"scope":  s.config().Security.Scope,
		"active": s.agent.ActiveAgents(),
		// Option lists for the editor's dropdowns.
		"toolsets":   tools.ToolsetNames(),
		"categories": []string{"general", "engineering", "research", "writing", "security"},
	}
	if perf := s.agent.RolePerformance(); perf != nil {
		out["performance"] = perf.List()
	}
	writeJSON(w, http.StatusOK, out)
}

// roleBody is the editable shape of a role sent from the dashboard. Body is the
// role's prompt (Markdown), everything else is front matter.
type roleBody struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Category string   `json:"category"`
	Toolset  string   `json:"toolset"`
	Model    string   `json:"model"`
	Effort   string   `json:"effort"`
	MaxTurns int      `json:"max_turns"`
	Tags     []string `json:"tags"`
	Danger   bool     `json:"danger"`
	Subrole  bool     `json:"subrole"`
	Parent   string   `json:"parent"`
	Body     string   `json:"body"`
}

// handleGetRole returns one role including its prompt body, which the list
// endpoint omits — the editor needs it to edit without wiping the prompt.
func (s *Server) handleGetRole(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("roles are not available"))
		return
	}
	role, ok := reg.Get(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": role.Name, "title": role.Title, "summary": role.Summary,
		"category": role.Category, "toolset": role.Toolset, "model": role.Model,
		"effort": role.Effort, "max_turns": role.MaxTurns, "tags": role.Tags,
		"danger": role.Danger, "subrole": role.Subrole, "parent": role.Parent,
		"source": role.Source, "body": role.Prompt,
	})
}

// handleSaveRole creates or updates a local (custom) role. Builtin roles are
// read-only; the registry refuses to shadow one.
func (s *Server) handleSaveRole(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("roles are not available"))
		return
	}
	var b roleBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	effort := strings.TrimSpace(b.Effort)
	previousEffort := ""
	if previous, ok := reg.Get(b.Name); ok {
		previousEffort = previous.Effort
	}
	if effort != previousEffort {
		if err := s.validateExplicitReasoning(
			r.Context(), s.config(), strings.TrimSpace(b.Model), effort,
		); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	saved, err := reg.Save(roles.Role{
		Name: b.Name, Title: b.Title, Summary: b.Summary, Category: b.Category,
		Toolset: b.Toolset, Model: b.Model, Effort: effort, MaxTurns: b.MaxTurns,
		Tags: b.Tags, Danger: b.Danger, Subrole: b.Subrole, Parent: b.Parent,
		Prompt: b.Body,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// handleDeleteRole removes a local role. Builtin roles cannot be deleted.
func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.Roles()
	if reg == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("roles are not available"))
		return
	}
	if err := reg.Delete(r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleSwarm reports the sub-agents running right now, for a live panel.
func (s *Server) handleSwarm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"active": s.agent.ActiveAgents()})
}

// handleSessionRole reports which specialist a session runs as, so the chat
// picker can reflect it after a reload.
func (s *Server) handleSessionRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := ""
	if s.db != nil && id != "" {
		role, _ = s.db.GetKV(r.Context(), "role:"+id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": role})
}
