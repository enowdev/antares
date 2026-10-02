package server

// Migration from another assistant agent (Hermes, OpenClaw, …). Contract:
// docs/plans/2026-10-03-migrate-contract.md. Plans carry secrets in their
// payloads, so they stay server-side under a plan_id for ten minutes; the
// client only ever sees items without payloads.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/migrate"
)

// migratePlanTTL is how long a plan stays applicable.
const migratePlanTTL = 10 * time.Minute

type cachedPlan struct {
	plan    migrate.Plan
	expires time.Time
}

// migratePlans caches plans by id. Package-level so it survives a reload of
// the server's config; ids are random, so servers in tests cannot collide.
var migratePlans = struct {
	sync.Mutex
	m   map[string]cachedPlan
	now func() time.Time
}{m: map[string]cachedPlan{}, now: time.Now}

// migrateMu serialises apply and undo: both rewrite config.yaml and files.
var migrateMu sync.Mutex

func storeMigratePlan(p migrate.Plan) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := "plan_" + hex.EncodeToString(b)
	migratePlans.Lock()
	defer migratePlans.Unlock()
	now := migratePlans.now()
	for k, v := range migratePlans.m {
		if now.After(v.expires) {
			delete(migratePlans.m, k)
		}
	}
	migratePlans.m[id] = cachedPlan{plan: p, expires: now.Add(migratePlanTTL)}
	return id
}

func takeMigratePlan(id string) (migrate.Plan, bool) {
	migratePlans.Lock()
	defer migratePlans.Unlock()
	c, ok := migratePlans.m[id]
	if !ok {
		return migrate.Plan{}, false
	}
	if migratePlans.now().After(c.expires) {
		delete(migratePlans.m, id)
		return migrate.Plan{}, false
	}
	return c.plan, true
}

func dropMigratePlan(id string) {
	migratePlans.Lock()
	delete(migratePlans.m, id)
	migratePlans.Unlock()
}

// migrateGate guards plan/apply/undo, which read and write credentials.
// During first-run setup there is no dashboard password yet, so the setup
// trust applies instead (loopback or the bearer token, as for
// /api/setup/complete); afterwards the dashboard password is required.
// It returns true when the caller should stop (already responded).
func (s *Server) migrateGate(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.config()
	if cfg != nil && NeedsSetup(cfg) {
		if requestIsLoopback(r) || s.bearerAuthorized(r) || cfg.Server.DashboardLocked() {
			return false
		}
		writeError(w, http.StatusForbidden, errors.New("importing during setup is available only from loopback or with a configured bearer token"))
		return true
	}
	return s.requireDashboardPassword(w, r)
}

func (s *Server) migrateDeps() migrate.Deps {
	d := migrate.Deps{Store: s.db}
	if s.agent != nil {
		d.RAG = s.agent.RAG()
	}
	return d
}

// GET /api/migrate/sources
func (s *Server) handleMigrateSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sources": migrate.DetectAll(r.Context())})
}

// GET /api/migrate/backups — past migrations, newest first, for Undo.
func (s *Server) handleMigrateBackups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"backups": migrate.ListBackups()})
}

// POST /api/migrate/plan {source, root, profile} → Plan + plan_id.
func (s *Server) handleMigratePlan(w http.ResponseWriter, r *http.Request) {
	if s.migrateGate(w, r) {
		return
	}
	var body struct {
		Source  string `json:"source"`
		Root    string `json:"root"`
		Profile string `json:"profile"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := migrate.Lookup(strings.TrimSpace(body.Source)); !ok {
		writeError(w, http.StatusBadRequest, errors.New("unknown source"))
		return
	}
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	plan, err := migrate.BuildPlan(r.Context(), strings.TrimSpace(body.Source), strings.TrimSpace(body.Root),
		strings.TrimSpace(body.Profile), migrate.NewEnv(cfg))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, migrate.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	if plan.Items == nil {
		plan.Items = []migrate.Item{}
	}
	writeJSON(w, http.StatusOK, struct {
		migrate.Plan
		PlanID string `json:"plan_id"`
	}{plan, storeMigratePlan(plan)})
}

// POST /api/migrate/apply {plan_id, items:[Choice]} → Report. 410 when the
// plan expired; the client plans again.
func (s *Server) handleMigrateApply(w http.ResponseWriter, r *http.Request) {
	if s.migrateGate(w, r) {
		return
	}
	var body struct {
		PlanID string           `json:"plan_id"`
		Items  []migrate.Choice `json:"items"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, ok := takeMigratePlan(body.PlanID)
	if !ok {
		writeError(w, http.StatusGone, errors.New("the plan expired; plan the import again"))
		return
	}
	migrateMu.Lock()
	defer migrateMu.Unlock()
	report, err := migrate.Apply(r.Context(), plan, body.Items, s.migrateDeps())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The plan's conflict marks are stale now; a second apply must re-plan.
	dropMigratePlan(body.PlanID)
	if err := s.applyReload(); err != nil {
		slog.Warn("migrate: reload after apply failed", "error", err)
	}
	writeJSON(w, http.StatusOK, report)
}

// POST /api/migrate/undo {backup: "<dir name>"} → {ok:true}.
func (s *Server) handleMigrateUndo(w http.ResponseWriter, r *http.Request) {
	if s.migrateGate(w, r) {
		return
	}
	var body struct {
		Backup string `json:"backup"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := migrate.ResolveBackup(body.Backup); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	migrateMu.Lock()
	defer migrateMu.Unlock()
	if err := migrate.Undo(r.Context(), body.Backup, s.migrateDeps()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		slog.Warn("migrate: reload after undo failed", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// afterMigrateWindow is how long after an import the setup wizard may still
// finish its remaining steps with /api/setup/complete {after_migrate:true}.
const afterMigrateWindow = time.Hour

// requireAfterMigrateAccess admits /api/setup/complete with after_migrate
// only from the setup trust (loopback, bearer token, or a dashboard session
// when one is configured) and only while the newest migration, not undone,
// is under an hour old. It returns true when the caller should stop.
func (s *Server) requireAfterMigrateAccess(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.config()
	trusted := requestIsLoopback(r) || s.bearerAuthorized(r) || (cfg.Server.DashboardLocked() && s.dashSessionValid(r))
	if !trusted {
		writeError(w, http.StatusForbidden, errors.New("initial setup is available only from loopback or with a configured bearer token"))
		return true
	}
	backups := migrate.ListBackups()
	if len(backups) == 0 || backups[0].Undone || time.Since(backups[0].CreatedAt) > afterMigrateWindow {
		writeError(w, http.StatusConflict, errors.New("initial setup has already been completed"))
		return true
	}
	return false
}
