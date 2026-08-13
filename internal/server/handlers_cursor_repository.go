package server

import (
	"net/http"

	"github.com/enowdev/antares/internal/cursorrun"
)

// handleCursorRepository performs the local-only repository preflight used by
// the Cursor options UI. It is protected before resolving or inspecting any
// caller-selected path.
func (s *Server) handleCursorRepository(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}
	info, err := cursorrun.InspectRepository(r.Context(), dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
