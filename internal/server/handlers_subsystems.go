package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cron"
	"github.com/enowdev/antares/internal/mcp"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
)

var (
	errSkillsOff = errors.New("the skill library is not available")
	errCronOff   = errors.New("the scheduler is not running")
)

// ---- skills -----------------------------------------------------------------

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}
	// A search query looks across the whole library, including the thousands in
	// the bundled security pack — capped, so a broad query does not ship all of
	// them. No query returns just the everyday skills, which keeps the page from
	// trying to render seven thousand cards.
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cwe := strings.TrimSpace(r.URL.Query().Get("cwe"))
	tech := strings.TrimSpace(r.URL.Query().Get("tech"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if q != "" || cwe != "" || tech != "" || category != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"skills":    s.skills.SearchFiltered(q, skills.Filter{CWE: cwe, Tech: tech, Category: category}, 100),
			"searching": true,
			"library":   s.skills.PackCount(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":  s.skills.Everyday(),
		"library": s.skills.PackCount(),
	})
}

func (s *Server) handleToggleSkill(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
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
	if err := s.skills.SetEnabled(body.Name, body.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	sk, ok := s.skills.Get(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": sk, "body": sk.Body})
}

func (s *Server) handleSaveSkill(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Body        string   `json:"body"`
		Tags        []string `json:"tags"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sk, err := s.skills.Save(body.Name, body.Description, body.Body, body.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	if err := s.skills.Delete(r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- cron -------------------------------------------------------------------

func (s *Server) handleCreateCron(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Target   string `json:"target"`
		Timezone string `json:"timezone"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Prompt) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name and prompt are required"))
		return
	}
	loc := time.Local
	if s.cron != nil {
		loc = s.cron.Location()
	}
	next, err := cron.Validate(body.Schedule, loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	job := &store.CronJob{
		ID: newID("job"), Name: body.Name, Schedule: body.Schedule, Prompt: body.Prompt,
		Enabled: true, Target: body.Target, Timezone: body.Timezone, Meta: store.Meta{},
	}
	if !next.IsZero() {
		job.NextRun = &next
	}
	if err := s.db.PutCronJob(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleToggleCron(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.db.GetCronJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	job.Enabled = body.Enabled
	if !body.Enabled {
		job.NextRun = nil
		if err := s.db.PutCronJob(r.Context(), job); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	} else if s.cron != nil {
		if err := s.cron.Recompute(r.Context(), job); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	} else if err := s.db.PutCronJob(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleRunCron(w http.ResponseWriter, r *http.Request) {
	if s.cron == nil {
		writeError(w, http.StatusServiceUnavailable, errCronOff)
		return
	}
	if err := s.cron.RunNow(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true})
}

func (s *Server) handleCronRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.ListCronRuns(r.Context(), r.PathValue("id"), queryInt(r, "limit", 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// handleValidateCron previews the next activations for an expression.
func (s *Server) handleValidateCron(w http.ResponseWriter, r *http.Request) {
	expr := r.URL.Query().Get("schedule")
	loc := time.Local
	if s.cron != nil {
		loc = s.cron.Location()
	}
	next, err := cron.Validate(expr, loc)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	// Show a few upcoming runs so the user can sanity-check the expression.
	sched, _ := cron.Parse(expr)
	upcoming := []time.Time{}
	t := time.Now().In(loc)
	for i := 0; i < 5 && sched != nil; i++ {
		t = sched.Next(t)
		if t.IsZero() {
			break
		}
		upcoming = append(upcoming, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "next": next, "upcoming": upcoming})
}

// ---- channels & pairing -----------------------------------------------------

func (s *Server) handleToggleChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
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
	id := r.PathValue("id")
	spec, ok := channelSpecByID(id)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("unknown channel"))
		return
	}
	if body.Enabled && !channelConfigured(cfg, spec) {
		writeError(w, http.StatusBadRequest, errors.New("configure "+spec.Label+" before enabling it"))
		return
	}
	setChannelEnabled(cfg, id, body.Enabled)
	// Enabling any channel implies the gateway itself runs.
	if body.Enabled {
		cfg.Gateway.Enabled = true
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string]any{"ok": true}
	// Reconnect right away rather than making someone restart the process for a
	// switch they just flipped.
	if s.gateway != nil {
		if err := s.gateway.Sync(id); err != nil {
			out["restart_required"] = true
			out["note"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleApprovePairing(w http.ResponseWriter, r *http.Request) {
	s.setPairingStatus(w, r, "approved")
}

func (s *Server) handleRevokePairing(w http.ResponseWriter, r *http.Request) {
	s.setPairingStatus(w, r, "revoked")
}

func (s *Server) setPairingStatus(w http.ResponseWriter, r *http.Request, status string) {
	var body struct {
		ID         string `json:"id"`
		Platform   string `json:"platform"`
		ExternalID string `json:"external_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	pairings, err := s.db.ListPairings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var target *store.Pairing
	for i := range pairings {
		p := &pairings[i]
		if (body.ID != "" && p.ID == body.ID) ||
			(body.Platform != "" && p.Platform == body.Platform && p.ExternalID == body.ExternalID) {
			target = p
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	target.Status = status
	if err := s.db.PutPairing(r.Context(), target); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, target)
}

// ---- cron listing & deletion ------------------------------------------------

func (s *Server) handleListCron(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.ListCronJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleDeleteCron(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeleteCronJob(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- mcp --------------------------------------------------------------------

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if s.mcp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "servers": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": cfg.MCP.Enabled,
		"servers": s.mcp.Status(cfg),
	})
}

func (s *Server) handleMCPRefresh(w http.ResponseWriter, r *http.Request) {
	s.refreshMCP(w, r, s.mcp)
}

type mcpRefresher interface {
	Refresh(context.Context, *config.Config) []mcp.ServerStatus
}

func (s *Server) refreshMCP(w http.ResponseWriter, r *http.Request, refresher mcpRefresher) {
	cfg := s.config()
	if refresher == nil || !cfg.MCP.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"enabled": false,
			"servers": []any{},
		})
		return
	}
	servers := refresher.Refresh(r.Context(), cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"servers": servers,
	})
}

// handleSkillLibrary browses the bundled security skill library — paged, by
// category — so thousands of skills are explorable without searching blind.
func (s *Server) handleSkillLibrary(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}, "categories": map[string]int{}, "total": 0})
		return
	}
	category := r.URL.Query().Get("category")
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", 50)
	page, total := s.skills.Library(category, offset, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":     page,
		"total":      total,
		"offset":     offset,
		"limit":      limit,
		"categories": s.skills.Categories(),
	})
}
