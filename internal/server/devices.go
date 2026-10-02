package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// Desktop contract (docs/plans/2026-10-02-desktop-contract.md).
const (
	desktopContractVersion = 1
	minDesktopVersion      = "0.1.0"

	// deviceCacheTTL bounds how long a device lookup (and so a revocation
	// made outside this process, e.g. by the CLI) may be served from memory.
	deviceCacheTTL = 30 * time.Second
	// deviceTouchEvery throttles last_seen_at writes per device.
	deviceTouchEvery = time.Minute

	handoffTTL    = 60 * time.Second
	handoffPrefix = "ahc_"
	handoffMaxLen = 2048

	// authFailLimit failed password attempts per authFailWindow per client
	// address before login and pairing answer 429.
	authFailLimit  = 10
	authFailWindow = 5 * time.Minute
)

// deviceAuthState holds the in-memory parts of device auth. The zero value is
// ready to use, so a Server built as a literal in tests works.
type deviceAuthState struct {
	mu sync.Mutex
	// byHash caches token-hash lookups: the device id ("" = unknown or
	// revoked) and when the entry expires.
	byHash map[string]deviceCacheEntry
	// byID caches whether a device id is still live, for sessions minted by a
	// device's handoff.
	byID map[string]deviceCacheEntry
	// touched is when last_seen_at was last written per device id.
	touched map[string]time.Time

	handoffs map[string]handoffCode
	fails    map[string][]time.Time

	// touches tracks in-flight last_seen_at writes (tests wait on it).
	touches sync.WaitGroup
}

type deviceCacheEntry struct {
	id  string
	exp time.Time
}

type handoffCode struct {
	exp      time.Time
	deviceID string
}

// ---- authorization ----------------------------------------------------------

// presentedBearer returns the Authorization bearer value, falling back to
// ?token= only on the narrow EventSource/media allowlist when allowQuery.
func presentedBearer(r *http.Request, allowQuery bool) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if v := strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")); v != "" {
			return v
		}
	}
	if allowQuery && queryTokenAllowed(r.URL.Path) {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

// authTokenMatches compares a presented bearer with server.auth_token in
// constant time. An empty or disabled token never matches.
func (s *Server) authTokenMatches(presented string) bool {
	cfg := s.config()
	if cfg == nil || cfg.Server.AuthDisabled || presented == "" {
		return false
	}
	token := strings.TrimSpace(cfg.Server.AuthToken)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

// bearerClient reports whether the presented bearer is server.auth_token or a
// live device token, and the device id in the latter case.
func (s *Server) bearerClient(r *http.Request, allowQuery bool) (bool, string) {
	presented := presentedBearer(r, allowQuery)
	if presented == "" {
		return false, ""
	}
	if s.authTokenMatches(presented) {
		return true, ""
	}
	if id := s.deviceForToken(r.Context(), presented); id != "" {
		return true, id
	}
	return false, ""
}

// clientAuthorized is the contract's authorization rule: auth_token bearer,
// a live device bearer, or a valid dashboard session cookie. The device id is
// set when the credential belongs to a device (directly, or a session minted
// by that device's handoff).
func (s *Server) clientAuthorized(r *http.Request, allowQuery bool) (bool, string) {
	if ok, id := s.bearerClient(r, allowQuery); ok {
		return true, id
	}
	if ok, id := s.dashSession(r); ok {
		return true, id
	}
	return false, ""
}

// deviceForToken resolves a device token to its id when the device exists and
// is not revoked. Anything not shaped like a device token is refused without
// a lookup. Results (positive and negative) are cached for deviceCacheTTL.
func (s *Server) deviceForToken(ctx context.Context, token string) string {
	if s.db == nil || !store.LooksLikeDeviceToken(token) {
		return ""
	}
	hash := store.HashDeviceToken(token)
	now := time.Now()
	st := &s.devAuth
	st.mu.Lock()
	if e, ok := st.byHash[hash]; ok && now.Before(e.exp) {
		st.mu.Unlock()
		if e.id != "" {
			s.touchDevice(e.id)
		}
		return e.id
	}
	st.mu.Unlock()

	id := ""
	d, err := s.db.DeviceByTokenHash(ctx, hash)
	switch {
	case err == nil && !d.Revoked():
		id = d.ID
	case err != nil && !errors.Is(err, store.ErrNotFound):
		// A database error is not cached: the next request retries.
		slog.Warn("device lookup failed", "error", err)
		return ""
	}
	st.mu.Lock()
	if st.byHash == nil {
		st.byHash = map[string]deviceCacheEntry{}
	}
	if len(st.byHash) > 4096 {
		for k, e := range st.byHash {
			if now.After(e.exp) {
				delete(st.byHash, k)
			}
		}
	}
	st.byHash[hash] = deviceCacheEntry{id: id, exp: now.Add(deviceCacheTTL)}
	st.mu.Unlock()
	if id != "" {
		s.touchDevice(id)
	}
	return id
}

// deviceLive reports whether a device id exists and is not revoked (cached).
func (s *Server) deviceLive(ctx context.Context, id string) bool {
	if s.db == nil || id == "" {
		return false
	}
	now := time.Now()
	st := &s.devAuth
	st.mu.Lock()
	if e, ok := st.byID[id]; ok && now.Before(e.exp) {
		st.mu.Unlock()
		return e.id != ""
	}
	st.mu.Unlock()
	d, err := s.db.GetDevice(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Warn("device lookup failed", "error", err)
		return false
	}
	live := err == nil && !d.Revoked()
	st.mu.Lock()
	if st.byID == nil {
		st.byID = map[string]deviceCacheEntry{}
	}
	e := deviceCacheEntry{exp: now.Add(deviceCacheTTL)}
	if live {
		e.id = id
	}
	st.byID[id] = e
	st.mu.Unlock()
	return live
}

// forgetDevice drops every cached entry for a device, so a revoke through
// this server takes effect at once.
func (s *Server) forgetDevice(id string) {
	st := &s.devAuth
	st.mu.Lock()
	for k, e := range st.byHash {
		if e.id == id {
			delete(st.byHash, k)
		}
	}
	delete(st.byID, id)
	delete(st.touched, id)
	st.mu.Unlock()
}

// touchDevice writes last_seen_at at most once a minute per device, off the
// request path.
func (s *Server) touchDevice(id string) {
	now := time.Now()
	st := &s.devAuth
	st.mu.Lock()
	if last, ok := st.touched[id]; ok && now.Sub(last) < deviceTouchEvery {
		st.mu.Unlock()
		return
	}
	if st.touched == nil {
		st.touched = map[string]time.Time{}
	}
	st.touched[id] = now
	st.mu.Unlock()
	db := s.db
	st.touches.Add(1)
	go func() {
		defer st.touches.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.TouchDevice(ctx, id, now); err != nil {
			slog.Debug("device touch failed", "device", id, "error", err)
		}
	}()
}

// ---- rate limiting -----------------------------------------------------------

// clientAddr is the peer address used for rate limiting. X-Forwarded-For is
// honoured only when server.trust_proxy is set.
func (s *Server) clientAddr(r *http.Request) string {
	if cfg := s.config(); cfg != nil && cfg.Server.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if v := strings.TrimSpace(first); v != "" {
				return v
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authThrottled reports whether this client has used up its failed attempts.
func (s *Server) authThrottled(r *http.Request) bool {
	key := s.clientAddr(r)
	cutoff := time.Now().Add(-authFailWindow)
	st := &s.devAuth
	st.mu.Lock()
	defer st.mu.Unlock()
	recent := st.fails[key][:0]
	for _, t := range st.fails[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(st.fails, key)
	} else {
		st.fails[key] = recent
	}
	return len(recent) >= authFailLimit
}

// authFailed records a failed password attempt for this client.
func (s *Server) authFailed(r *http.Request) {
	key := s.clientAddr(r)
	st := &s.devAuth
	st.mu.Lock()
	if st.fails == nil {
		st.fails = map[string][]time.Time{}
	}
	if len(st.fails) > 10000 {
		st.fails = map[string][]time.Time{}
	}
	st.fails[key] = append(st.fails[key], time.Now())
	st.mu.Unlock()
}

func writeThrottled(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "300")
	writeError(w, http.StatusTooManyRequests, errors.New("too many failed attempts; try again later"))
}

// ---- handlers ---------------------------------------------------------------

// deviceView is a device as the API shows it.
type deviceView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Platform   string  `json:"platform"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt *string `json:"last_seen_at"`
	RevokedAt  *string `json:"revoked_at"`
	Current    bool    `json:"current"`
}

func isoPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339)
	return &v
}

// handlePairDevice creates a device and returns its token once. It
// authenticates itself (see the contract's pairing rules).
func (s *Server) handlePairDevice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("database unavailable"))
		return
	}
	var body struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.config()
	if ok, _ := s.clientAuthorized(r, false); !ok {
		switch {
		case cfg.Server.DashboardLocked():
			if s.authThrottled(r) {
				writeThrottled(w)
				return
			}
			if !config.CheckPassword(cfg.Server.DashboardPasswordHash, body.Password) {
				s.authFailed(r)
				writeError(w, http.StatusUnauthorized, errors.New("incorrect password"))
				return
			}
		case strings.TrimSpace(cfg.Server.AuthToken) != "" && !cfg.Server.AuthDisabled:
			writeError(w, http.StatusUnauthorized, errors.New("this server has no dashboard password: pair with the auth token as bearer, or run `antares device pair` on the server"))
			return
		}
		// Neither a password nor a token: an open server pairs freely.
	}
	name, err := store.NormalizeDeviceName(body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	d, token, err := store.PairDevice(r.Context(), s.db, name, body.Platform)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("device paired", "device", d.ID, "platform", d.Platform)
	writeJSON(w, http.StatusOK, map[string]any{
		"device": map[string]any{
			"id": d.ID, "name": d.Name, "platform": d.Platform,
			"created_at": d.CreatedAt.UTC().Format(time.RFC3339),
		},
		"token": token,
	})
}

// handleListDevices lists every device; `current` marks the caller's.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("database unavailable"))
		return
	}
	_, current := s.clientAuthorized(r, false)
	devs, err := s.db.ListDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]deviceView, 0, len(devs))
	for _, d := range devs {
		out = append(out, deviceView{
			ID: d.ID, Name: d.Name, Platform: d.Platform,
			CreatedAt:  d.CreatedAt.UTC().Format(time.RFC3339),
			LastSeenAt: isoPtr(d.LastSeenAt),
			RevokedAt:  isoPtr(d.RevokedAt),
			Current:    current != "" && d.ID == current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// handleRevokeDevice revokes a device and every dashboard session its
// handoffs minted.
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("database unavailable"))
		return
	}
	id := r.PathValue("id")
	if err := s.db.RevokeDevice(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errors.New("device not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.forgetDevice(id)
	s.dropDeviceSessions(id)
	slog.Info("device revoked", "device", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleVersion reports the server version and the desktop contract it speaks.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     version.Version,
		"contract":    desktopContractVersion,
		"min_desktop": minDesktopVersion,
	})
}

// safeNextPath returns next when it is a same-origin path, else "/". A path
// must start with one "/", carry no scheme, host or backslash, and no control
// characters (browsers drop tabs/newlines, which would turn "/\t/x" into
// "//x").
func safeNextPath(next string) string {
	if next == "" || len(next) > handoffMaxLen || next[0] != '/' {
		return "/"
	}
	if strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "/"
	}
	for i := 0; i < len(next); i++ {
		if c := next[i]; c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "/"
	}
	return next
}

// handleHandoffCreate mints a single-use code that /auth/handoff exchanges for
// a dashboard session, so a token never has to sit in a URL.
func (s *Server) handleHandoffCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Next string `json:"next"`
	}
	// The body is optional: an empty one means next = "/".
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
	}
	next := safeNextPath(strings.TrimSpace(body.Next))
	_, deviceID := s.clientAuthorized(r, false)

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	code := handoffPrefix + hex.EncodeToString(b)
	now := time.Now()
	st := &s.devAuth
	st.mu.Lock()
	if st.handoffs == nil {
		st.handoffs = map[string]handoffCode{}
	}
	for k, c := range st.handoffs {
		if now.After(c.exp) {
			delete(st.handoffs, k)
		}
	}
	st.handoffs[code] = handoffCode{exp: now.Add(handoffTTL), deviceID: deviceID}
	st.mu.Unlock()

	q := url.Values{}
	q.Set("code", code)
	q.Set("next", next)
	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"url":        "/auth/handoff?" + q.Encode(),
		"expires_in": int(handoffTTL / time.Second),
	})
}

// handleHandoffExchange consumes a handoff code, mints a dashboard session and
// redirects to `next`. Outside /api: it is a browser navigation.
func (s *Server) handleHandoffExchange(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")

	code := r.URL.Query().Get("code")
	next := safeNextPath(r.URL.Query().Get("next"))

	now := time.Now()
	st := &s.devAuth
	st.mu.Lock()
	c, ok := st.handoffs[code]
	if ok {
		delete(st.handoffs, code) // single use, even when expired
	}
	st.mu.Unlock()

	if !ok || now.After(c.exp) || (c.deviceID != "" && !s.deviceLive(r.Context(), c.deviceID)) {
		h.Set("Location", "/login?handoff=expired")
		w.WriteHeader(http.StatusFound)
		return
	}
	s.mintDashSession(w, r, c.deviceID)
	h.Set("Location", next)
	w.WriteHeader(http.StatusFound)
}

// ---- session ↔ device links ---------------------------------------------------

// dashSessionDevicesFile maps a dashboard session to the device whose handoff
// minted it, so revoking the device ends those sessions too.
func dashSessionDevicesFile() string {
	return config.Path("dash_session_devices.json")
}

func (s *Server) loadDashSessionDevices() {
	data, err := os.ReadFile(dashSessionDevicesFile())
	if err != nil {
		return
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	s.dashMu.Lock()
	s.sessionDevice = map[string]string{}
	for tok, dev := range raw {
		if _, ok := s.dashSessions[tok]; ok {
			s.sessionDevice[tok] = dev
		}
	}
	s.dashMu.Unlock()
}

// persistSessionDevicesLocked writes the links. Caller holds dashMu.
func (s *Server) persistSessionDevicesLocked() {
	raw := map[string]string{}
	for tok, dev := range s.sessionDevice {
		if _, ok := s.dashSessions[tok]; ok {
			raw[tok] = dev
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if err := os.WriteFile(dashSessionDevicesFile(), data, 0o600); err != nil {
		slog.Warn("dash sessions: could not persist device links", "error", err)
	}
}

// dropDeviceSessions ends every dashboard session minted by a device.
func (s *Server) dropDeviceSessions(id string) {
	s.dashMu.Lock()
	defer s.dashMu.Unlock()
	changed := false
	for tok, dev := range s.sessionDevice {
		if dev == id {
			delete(s.sessionDevice, tok)
			delete(s.dashSessions, tok)
			changed = true
		}
	}
	if changed {
		s.persistDashSessionsLocked()
		s.persistSessionDevicesLocked()
	}
}
