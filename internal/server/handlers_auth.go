package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// dashCookie is the name of the dashboard login session cookie.
const dashCookie = "antares_dash"

// dashSessionTTL is how long a dashboard login stays valid.
const dashSessionTTL = 30 * 24 * time.Hour

// withDashboardAuth gates the web dashboard behind the login password when one
// is configured. It is web-only: it never applies to the TUI or gateways
// (those talk to the agent in-process, not over HTTP), and any client that
// presents the configured server.auth_token as a bearer (or, for EventSource
// allowlisted paths, ?token=) bypasses it — so the CLI and scripted API
// callers keep working. Requests without a valid session cookie get 401 on
// /api/* (except the auth and health endpoints the login page itself needs),
// which the dashboard turns into a redirect to /login.
func (s *Server) withDashboardAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		if !cfg.Server.DashboardLocked() {
			next.ServeHTTP(w, r)
			return
		}
		// Only guard the API surface; the SPA shell and static assets load so
		// the login page can render.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Endpoints the login flow and liveness/readiness checks must reach
		// unauthenticated. /api/auth/password does its own authorization (it must
		// accept the current password to change it). /api/status and
		// /api/setup/status are non-sensitive readiness checks the setup gate and
		// status pill read before a login can happen.
		switch r.URL.Path {
		case "/api/health", "/api/status",
			"/api/auth/login", "/api/auth/logout", "/api/auth/status", "/api/auth/password",
			"/api/setup/status":
			next.ServeHTTP(w, r)
			return
		}
		// Bearer auth_token (header or allowlisted ?token= for EventSource).
		if s.bearerAuthorizedOrQuery(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.dashSessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, errors.New("dashboard login required"))
	})
}

// requireDashboardPassword refuses an operation when no dashboard password is
// set, returning 428 Precondition Required with a machine-readable code the
// dashboard turns into a "set a password first" prompt. It gates features that
// store or use sensitive credentials (SSH keys, proxy passwords, session
// cookies): those must not live on a box whose web UI has no password at all.
// It returns true when the caller should stop (already responded).
//
// A configured bearer auth_token counts as protection too — a scripted/CLI
// caller presenting it is trusted, so the gate only bites the unprotected web.
func (s *Server) requireDashboardPassword(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.config()
	if cfg.Server.DashboardLocked() {
		return false
	}
	if s.bearerAuthorizedOrQuery(r) {
		return false
	}
	writeJSON(w, http.StatusPreconditionRequired, map[string]any{
		"error": "dashboard_password_required",
		"message": "Set a dashboard password first — this stores or uses sensitive " +
			"credentials and must not be left on an unprotected dashboard.",
	})
	return true
}

// dashSessionValid reports whether the request carries a live dashboard session
// cookie, pruning it if expired.
func (s *Server) dashSessionValid(r *http.Request) bool {
	c, err := r.Cookie(dashCookie)
	if err != nil || c.Value == "" {
		return false
	}
	s.dashMu.Lock()
	defer s.dashMu.Unlock()
	exp, ok := s.dashSessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.dashSessions, c.Value)
		return false
	}
	return true
}

// handleAuthStatus tells the dashboard whether a login is required and whether
// the current request is already authenticated.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	locked := cfg.Server.DashboardLocked()
	writeJSON(w, http.StatusOK, map[string]any{
		"password_required": locked,
		"authenticated":     !locked || s.dashSessionValid(r),
	})
}

// handleAuthLogin verifies the dashboard password and, on success, mints a
// session cookie.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	if !cfg.Server.DashboardLocked() {
		// Nothing to log into; report success so the UI proceeds.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !config.CheckPassword(cfg.Server.DashboardPasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, errors.New("incorrect password"))
		return
	}

	tok := newSessionToken()
	exp := time.Now().Add(dashSessionTTL)
	s.dashMu.Lock()
	s.dashSessions[tok] = exp
	s.persistDashSessionsLocked()
	s.dashMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     dashCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(dashSessionTTL / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthLogout drops the caller's dashboard session.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(dashCookie); err == nil && c.Value != "" {
		s.dashMu.Lock()
		delete(s.dashSessions, c.Value)
		s.persistDashSessionsLocked()
		s.dashMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthSetPassword sets or changes the dashboard password (or clears it).
// Setting the FIRST password is allowed while the dashboard is still open. Once
// a password exists, changing or clearing it requires proving you already hold
// it — either a valid login session or the current password — so a drive-by
// request cannot take over or unlock a protected dashboard.
func (s *Server) handleAuthSetPassword(w http.ResponseWriter, r *http.Request) {
	s.passwordMu.Lock()
	defer s.passwordMu.Unlock()

	var body struct {
		Current  string `json:"current"`  // required once a password is already set
		Password string `json:"password"` // new password; empty clears (removes) it
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	if cfg.Server.DashboardLocked() {
		authorized := s.dashSessionValid(r) || config.CheckPassword(cfg.Server.DashboardPasswordHash, body.Current)
		if !authorized {
			writeError(w, http.StatusUnauthorized, errors.New("current password required"))
			return
		}
	} else if !requestIsLoopback(r) && !s.bearerAuthorized(r) {
		// The first password is a bootstrap capability. Without this check the
		// first network client wins a race and can lock out the owner (or claim
		// the dashboard when no bearer token is configured).
		writeError(w, http.StatusForbidden, errors.New("the first dashboard password must be set from loopback or with a configured bearer token"))
		return
	}

	next := strings.TrimSpace(body.Password)
	if next == "" {
		cfg.Server.DashboardPasswordHash = ""
	} else {
		hash, err := config.HashPassword(next)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		cfg.Server.DashboardPasswordHash = hash
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Any old login must not outlive a password change; the caller re-logs in.
	s.invalidateDashSessions()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "locked": cfg.Server.DashboardLocked()})
}

// invalidateDashSessions drops every active dashboard session. Call after the
// password changes so old logins cannot outlive it.
func (s *Server) invalidateDashSessions() {
	s.dashMu.Lock()
	s.dashSessions = map[string]time.Time{}
	s.persistDashSessionsLocked()
	s.dashMu.Unlock()
}

func newSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// dashSessionsFile is where login sessions are written so a daemon restart
// does not force every browser to re-login. Without this, EventSource
// reattach (/api/chat/attach) returns 401 after every restart while the
// cookie still looks valid, and the UI sits on "Working…" without live events.
func dashSessionsFile() string {
	return config.Path("dash_sessions.json")
}

// loadDashSessions restores sessions from disk (if any), dropping expired ones.
// Called once at server construction.
func (s *Server) loadDashSessions() {
	path := dashSessionsFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("dash sessions: corrupt file, ignoring", "path", path, "error", err)
		return
	}
	now := time.Now()
	out := make(map[string]time.Time, len(raw))
	for tok, expMS := range raw {
		exp := time.UnixMilli(expMS)
		if exp.After(now) && strings.TrimSpace(tok) != "" {
			out[tok] = exp
		}
	}
	s.dashMu.Lock()
	s.dashSessions = out
	// Rewrite if we pruned expired entries.
	if len(out) != len(raw) {
		s.persistDashSessionsLocked()
	}
	s.dashMu.Unlock()
}

// persistDashSessionsLocked writes the in-memory map to disk. Caller must hold
// dashMu. Failures are logged and non-fatal — login still works in-process.
func (s *Server) persistDashSessionsLocked() {
	raw := make(map[string]int64, len(s.dashSessions))
	now := time.Now()
	for tok, exp := range s.dashSessions {
		if exp.After(now) {
			raw[tok] = exp.UnixMilli()
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	path := dashSessionsFile()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Warn("dash sessions: could not persist", "path", path, "error", err)
	}
}
