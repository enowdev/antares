// Package mockserver is a small in-memory Antares implementing the desktop
// contract's server endpoints, for the shell's tests (and manual runs via
// cmd/mockantares) before or without the real server.
package mockserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Options shape the mock.
type Options struct {
	Password  string // dashboard password ("" = none)
	AuthToken string // server.auth_token ("" = none)
	// Old makes the server predate the desktop contract: /api/version and
	// the device endpoints answer 404.
	Old        bool
	Contract   int    // default 1
	MinDesktop string // default "0.1.0"
}

type device struct {
	ID, Name, Platform string
	Hash               string
	Created            time.Time
	Revoked            *time.Time
}

// Server is the mock; Handler serves it, URL is set by Start.
type Server struct {
	opt Options

	mu       sync.Mutex
	devices  []*device
	codes    map[string]string // code -> next
	sessions map[string]bool
	Healthy  bool
	Handoffs int

	URL string
	ts  *httptest.Server
}

// New builds a mock (not listening).
func New(opt Options) *Server {
	if opt.Contract == 0 {
		opt.Contract = 1
	}
	if opt.MinDesktop == "" {
		opt.MinDesktop = "0.1.0"
	}
	return &Server{opt: opt, codes: map[string]string{}, sessions: map[string]bool{}, Healthy: true}
}

// Start serves on a random loopback port.
func Start(opt Options) *Server {
	s := New(opt)
	s.ts = httptest.NewServer(s.Handler())
	s.URL = s.ts.URL
	return s
}

// Close stops a started server.
func (s *Server) Close() {
	if s.ts != nil {
		s.ts.Close()
	}
}

// SetHealthy flips /api/health.
func (s *Server) SetHealthy(ok bool) {
	s.mu.Lock()
	s.Healthy = ok
	s.mu.Unlock()
}

// RevokeAll revokes every device (as the dashboard's Devices section would).
func (s *Server) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, d := range s.devices {
		d.Revoked = &now
	}
}

// ActiveDevices counts non-revoked devices.
func (s *Server) ActiveDevices() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.devices {
		if d.Revoked == nil {
			n++
		}
	}
	return n
}

// Handler is the mock's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.Healthy
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "version": "v0.0.0-mock"})
	})
	if !s.opt.Old {
		mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"version": "v0.0.0-mock", "contract": s.opt.Contract, "min_desktop": s.opt.MinDesktop})
		})
		mux.HandleFunc("POST /api/devices/pair", s.pair)
		mux.HandleFunc("GET /api/devices", s.authed(s.listDevices))
		mux.HandleFunc("DELETE /api/devices/{id}", s.authed(s.revoke))
		mux.HandleFunc("POST /api/auth/handoff", s.authed(s.handoff))
		mux.HandleFunc("GET /auth/handoff", s.consume)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if c, err := r.Cookie("antares_dash"); err != nil || !s.hasSession(c.Value) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Mock Antares</title><body style="background:#08090a;color:#eee;font-family:sans-serif"><h1>Mock dashboard</h1><p id=path></p><p><a href="https://example.com/">external link</a></p><script>document.getElementById('path').textContent=location.pathname</script>`))
	})
	return mux
}

func (s *Server) hasSession(v string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[v]
}

// authorizedDevice: the server token, a live device token, or a session.
func (s *Server) authorized(r *http.Request) (*device, bool) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer != "" && bearer != r.Header.Get("Authorization") {
		if s.opt.AuthToken != "" && bearer == s.opt.AuthToken {
			return nil, true
		}
		h := hash(bearer)
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, d := range s.devices {
			if d.Hash == h && d.Revoked == nil {
				return d, true
			}
		}
		return nil, false
	}
	if c, err := r.Cookie("antares_dash"); err == nil && s.hasSession(c.Value) {
		return nil, true
	}
	return nil, s.opt.AuthToken == "" && s.opt.Password == ""
}

func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, ok := s.authorized(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r, d)
	}
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Platform, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if _, ok := s.authorized(r); !ok {
		switch {
		case s.opt.Password != "" && in.Password != s.opt.Password:
			writeJSON(w, 401, map[string]string{"error": "invalid password"})
			return
		case s.opt.Password == "" && s.opt.AuthToken != "":
			writeJSON(w, 401, map[string]string{"error": "this server has no dashboard password; pair with the server token or `antares device pair`"})
			return
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		writeJSON(w, 400, map[string]string{"error": "name must be 1-64 characters"})
		return
	}
	switch in.Platform {
	case "desktop-macos", "desktop-windows", "desktop-linux", "cli", "other":
	default:
		in.Platform = "other"
	}
	token := "atd_" + randHex(24)
	d := &device{ID: "dev_" + randHex(8), Name: name, Platform: in.Platform, Hash: hash(token), Created: time.Now().UTC()}
	s.mu.Lock()
	s.devices = append(s.devices, d)
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"device": deviceJSON(d, false), "token": token})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, cur *device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{}
	for _, d := range s.devices {
		out = append(out, deviceJSON(d, cur != nil && d.ID == cur.ID))
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request, _ *device) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.ID == id {
			now := time.Now()
			d.Revoked = &now
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "unknown device"})
}

func (s *Server) handoff(w http.ResponseWriter, r *http.Request, _ *device) {
	var in struct {
		Next string `json:"next"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	next := safeNext(in.Next)
	code := "ahc_" + randHex(16)
	s.mu.Lock()
	s.codes[code] = next
	s.Handoffs++
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"code": code, "expires_in": 60,
		"url": "/auth/handoff?code=" + code + "&next=" + url.QueryEscape(next),
	})
}

func (s *Server) consume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	code := r.URL.Query().Get("code")
	s.mu.Lock()
	_, ok := s.codes[code]
	delete(s.codes, code)
	sess := ""
	if ok {
		sess = randHex(16)
		s.sessions[sess] = true
	}
	s.mu.Unlock()
	if !ok {
		http.Redirect(w, r, "/login?handoff=expired", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "antares_dash", Value: sess, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusFound)
}

func safeNext(n string) string {
	if !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.Contains(n, `\`) || strings.Contains(n, "://") {
		return "/"
	}
	return n
}

func deviceJSON(d *device, current bool) map[string]any {
	m := map[string]any{"id": d.ID, "name": d.Name, "platform": d.Platform, "created_at": d.Created, "last_seen_at": nil, "revoked_at": d.Revoked, "current": current}
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}
