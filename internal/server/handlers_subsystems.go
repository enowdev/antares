package server

import (
	"context"
	"errors"
	"fmt"
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
	mgr := s.currentSkills()
	if mgr == nil {
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
			"skills":    mgr.SearchFiltered(q, skills.Filter{CWE: cwe, Tech: tech, Category: category}, 100),
			"searching": true,
			"library":   mgr.PackCount(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":  mgr.Everyday(),
		"library": mgr.PackCount(),
	})
}

func (s *Server) handleToggleSkill(w http.ResponseWriter, r *http.Request) {

	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	s.skillsConfigMu.Lock()
	defer s.skillsConfigMu.Unlock()

	manager := s.currentSkills()
	if manager == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	if _, ok := manager.Get(body.Name); !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("skill %q not found", body.Name))
		return
	}
	if _, err := config.SetSkillEnabled(body.Name, body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	mgr := s.currentSkills()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	sk, ok := mgr.Get(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": sk, "body": sk.Body})
}

func (s *Server) handleSaveSkill(w http.ResponseWriter, r *http.Request) {
	mgr := s.currentSkills()
	if mgr == nil {
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
	sk, err := mgr.Save(body.Name, body.Description, body.Body, body.Tags)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, skills.ErrReadOnly) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	mgr := s.currentSkills()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, errSkillsOff)
		return
	}
	if err := mgr.Delete(r.PathValue("name")); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, skills.ErrReadOnly) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- cron -------------------------------------------------------------------

func (s *Server) handleCreateCron(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Target   string `json:"target"`
		Timezone string `json:"timezone"`
		// Meta fields the UI or agent may attach. Accepted at the top level
		// for convenience and inside a nested "meta" object for symmetry with
		// the GET response. Empty strings preserve any prior value on upsert.
		Role             string `json:"role"`
		Workspace        string `json:"workspace"`
		ContentProjectID string `json:"content_project_id"`
		ContentStage     string `json:"content_stage"`
		PublishMode      string `json:"publish_mode"`
		Meta             struct {
			Role             string `json:"role"`
			Workspace        string `json:"workspace"`
			ContentProjectID string `json:"content_project_id"`
			ContentStage     string `json:"content_stage"`
			PublishMode      string `json:"publish_mode"`
		} `json:"meta"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	// Meta wins if provided; otherwise the flat fields.
	role := firstNonEmpty(body.Meta.Role, body.Role)
	workspace := firstNonEmpty(body.Meta.Workspace, body.Workspace)
	projectID := firstNonEmpty(body.Meta.ContentProjectID, body.ContentProjectID)
	stage := strings.ToLower(strings.TrimSpace(firstNonEmpty(body.Meta.ContentStage, body.ContentStage)))
	publishMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(body.Meta.PublishMode, body.PublishMode)))
	// A creator-linked job derives its prompt from stage/project at run time
	// when the caller left prompt blank; still require prompt for plain jobs.
	if strings.TrimSpace(body.Prompt) == "" && strings.TrimSpace(role) == "" {
		writeError(w, http.StatusBadRequest, errors.New("prompt is required for jobs without a role"))
		return
	}
	if stage != "" && !validContentStage(stage) {
		writeError(w, http.StatusBadRequest, errors.New("content_stage must be research|plan|produce|publish|full"))
		return
	}
	if publishMode != "" && publishMode != "draft" && publishMode != "auto" {
		writeError(w, http.StatusBadRequest, errors.New("publish_mode must be draft or auto"))
		return
	}
	if (stage != "" || projectID != "" || publishMode != "") && strings.TrimSpace(role) == "" {
		role = "content-creator"
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

	// Upsert path: an explicit id preserves prior Meta and CreatedAt so an
	// edit from the UI does not silently drop unrelated metadata attached by
	// the agent or a prior save.
	var job *store.CronJob
	if id := strings.TrimSpace(body.ID); id != "" {
		if existing, err := s.db.GetCronJob(r.Context(), id); err == nil {
			job = existing
			job.Name = body.Name
			job.Schedule = body.Schedule
			job.Prompt = body.Prompt
			job.Enabled = true
			job.Target = body.Target
			job.Timezone = body.Timezone
		} else {
			job = &store.CronJob{ID: id}
		}
	}
	if job == nil {
		job = &store.CronJob{
			ID: newID("job"), Name: body.Name, Schedule: body.Schedule, Prompt: body.Prompt,
			Enabled: true, Target: body.Target, Timezone: body.Timezone,
		}
	}
	if job.Meta == nil {
		job.Meta = store.Meta{}
	}
	mergeCronMetaFields(job, strings.TrimSpace(role), strings.TrimSpace(workspace), strings.TrimSpace(projectID), stage, publishMode)
	if !next.IsZero() {
		job.NextRun = &next
	}
	if err := s.db.PutCronJob(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// mergeCronMetaFields applies caller-supplied Meta values. Empty strings clear
// their key so the UI can remove a field on edit; role is only overwritten
// when explicitly provided so blank leaves the prior role untouched.
func mergeCronMetaFields(job *store.CronJob, role, workspace, projectID, stage, publishMode string) {
	if job.Meta == nil {
		job.Meta = store.Meta{}
	}
	set := func(key, value string) {
		if value == "" {
			delete(job.Meta, key)
			return
		}
		job.Meta[key] = value
	}
	if role != "" {
		job.Meta["role"] = role
	}
	set("workspace", workspace)
	set("content_project_id", projectID)
	set("content_stage", stage)
	set("publish_mode", publishMode)
}

// validContentStage is duplicated from internal/tools to keep the server free
// of a tool-package import; the enum is short and unlikely to grow.
func validContentStage(s string) bool {
	switch s {
	case "research", "plan", "produce", "publish", "full":
		return true
	}
	return false
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
	refresher := s.mcpRefresh
	if refresher == nil {
		refresher = s.mcp
	}
	s.refreshMCP(w, r, refresher)
}

type mcpRefresher interface {
	Refresh(context.Context, *config.Config) []mcp.ServerStatus
}

func (s *Server) refreshMCPConnections(ctx context.Context) {
	refresher := s.mcpRefresh
	if refresher == nil {
		refresher = s.mcp
	}
	if refresher != nil {
		refresher.Refresh(ctx, s.config())
	}
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
	mgr := s.currentSkills()
	if mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}, "categories": map[string]int{}, "total": 0})
		return
	}
	category := r.URL.Query().Get("category")
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", 50)
	page, total := mgr.Library(category, offset, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":     page,
		"total":      total,
		"offset":     offset,
		"limit":      limit,
		"categories": mgr.Categories(),
	})
}
