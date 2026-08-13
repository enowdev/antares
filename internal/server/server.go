// Package server exposes the Antares HTTP API and serves the built dashboard.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cron"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/gateway"
	"github.com/enowdev/antares/internal/mcp"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/socialbrowser"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// cursorMetadataClient is the narrow surface Server needs from a Cursor
// client: identity/quota verification and the model catalogue. Tests inject a
// fake through cursorFactory; production always goes through cursor.New.
type cursorMetadataClient interface {
	Me(context.Context) (*cursor.Me, error)
	Models(context.Context) (*cursor.ModelCatalog, error)
}

// cursorClientFactory builds a cursorMetadataClient from connection options.
type cursorClientFactory func(cursor.Options) (cursorMetadataClient, error)

// Server wires the API handlers to the agent and store.
type Server struct {
	cfg     *config.Config
	agent   *agent.Agent
	db      store.Store
	skills  *skills.Manager
	cron    *cron.Runner
	gateway *gateway.Manager
	mcp     *mcp.Manager
	social  *socialbrowser.Manager
	mux     *http.ServeMux
	hub     *liveHub
	wake    *wakeQueue
	started time.Time

	// distFS holds the embedded dashboard build, when present.
	distFS fs.FS

	// cursorFactory overrides how a Cursor metadata client is constructed.
	// Only tests set this; production callers get cursor.New via
	// newCursorMetadataClient.
	cursorFactory cursorClientFactory

	// cursorRunner owns the shared Cursor model catalogue cache and remote-run
	// lifecycle. It resolves the current provider config before each operation.
	cursorRunner cursorrun.Runner

	// providerResolver overrides provider hostname resolution in handler tests.
	// Production uses net.DefaultResolver.
	providerResolver providerIPResolver

	mu       sync.RWMutex
	reloadFn func() error

	// dashSessions holds active dashboard login session tokens (cookie value →
	// expiry). Guarded by its own mutex; cleared when the password changes.
	dashMu       sync.Mutex
	dashSessions map[string]time.Time
	// setupMu serializes the one-time setup write, preventing two first-run
	// requests from both passing the setup check before either saves.
	setupMu sync.Mutex
	// passwordMu serializes first-password creation and password replacement.
	passwordMu sync.Mutex
}

// Options configures a Server.
type Options struct {
	Config *config.Config
	Agent  *agent.Agent
	Store  store.Store
	// Dist is the built dashboard to serve at "/". May be nil in dev, where
	// Vite serves the UI and proxies /api here.
	Dist fs.FS
	// Reload re-reads configuration from disk and rebuilds dependent services.
	Reload func() error
	// Skills, Cron, and Gateway are optional; their endpoints report
	// unavailability when nil.
	Skills  *skills.Manager
	Cron    *cron.Runner
	Gateway *gateway.Manager
	MCP     *mcp.Manager
	Social  *socialbrowser.Manager
	// Cursor is the runtime-scoped catalogue and remote-run service shared with
	// the Agent. When nil, New constructs a compatibility fallback.
	Cursor cursorrun.Runner
}

// New builds the HTTP server and registers every route.
func New(o Options) *Server {
	s := &Server{
		cfg:          o.Config,
		agent:        o.Agent,
		db:           o.Store,
		skills:       o.Skills,
		cron:         o.Cron,
		gateway:      o.Gateway,
		mcp:          o.MCP,
		social:       o.Social,
		mux:          http.NewServeMux(),
		hub:          newLiveHub(),
		wake:         newWakeQueue(),
		started:      time.Now(),
		distFS:       o.Dist,
		reloadFn:     o.Reload,
		cursorRunner: o.Cursor,

		dashSessions: map[string]time.Time{},
	}
	if s.cursorRunner == nil {
		s.cursorRunner = cursorrun.New(cursorrun.Options{
			ResolveClient: func() (cursor.Options, error) {
				_, provider := s.config().ResolveProvider("cursor")
				provider.APIKey = strings.TrimSpace(provider.APIKey)
				options := cursor.Options{
					BaseURL: provider.BaseURL,
					APIKey:  provider.APIKey,
				}
				if !provider.Enabled || provider.APIKey == "" {
					return options, cursorrun.ErrNotConfigured
				}
				return options, nil
			},
			Now:        time.Now,
			CatalogTTL: 5 * time.Minute,
		})
	}
	// Restore dashboard logins so a daemon restart does not break EventSource
	// reattach (/api/chat/attach) for browsers that still hold a valid cookie.
	s.loadDashSessions()
	// Finished background sub-agents resume (or wake) the delegating session
	// instead of the main agent polling for them.
	if s.agent != nil {
		s.agent.OnBackgroundDone(s.onBackgroundDone)
	}
	s.routes()
	return s
}

// Handler returns the root handler with middleware applied.
func (s *Server) Handler() http.Handler {
	return s.withRecovery(s.withLogging(s.withCORS(s.withAuth(s.withDashboardAuth(s.mux)))))
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	host := s.cfg.Server.Host
	if host == "" {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, strconv.Itoa(s.cfg.Server.Port))
}

// SetConfig swaps the live configuration after a reload.
func (s *Server) SetConfig(cfg *config.Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	if s.cursorRunner != nil {
		s.cursorRunner.InvalidateCatalog()
	}
}

func (s *Server) config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// newCursorMetadataClient builds a Cursor metadata client, honouring an
// injected test factory when one is set. No production caller injects one.
func (s *Server) newCursorMetadataClient(o cursor.Options) (cursorMetadataClient, error) {
	if s.cursorFactory != nil {
		return s.cursorFactory(o)
	}
	return cursor.New(o)
}

//go:embed all:dist
var embeddedDist embed.FS

// EmbeddedDist returns the compiled dashboard, or nil when the binary was built
// without one (the dev flow).
func EmbeddedDist() fs.FS {
	sub, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// ---- middleware -------------------------------------------------------------

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, fmt.Errorf("internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the response code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.wrote += n
	return n, err
}

// Flush forwards to the underlying writer so SSE keeps streaming.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		level := slog.LevelDebug
		if sw.status >= 500 {
			level = slog.LevelError
		} else if sw.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "http",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins := s.config().Server.CORSOrigins
		origin := r.Header.Get("Origin")
		if origin != "" && len(origins) > 0 {
			allowed := ""
			for _, o := range origins {
				if o == "*" {
					allowed = "*"
					break
				}
				if strings.EqualFold(o, origin) {
					allowed = origin
					break
				}
			}
			if allowed != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowed)
				if allowed == "*" {
					// Wildcard origins and credentialed requests are incompatible.
					// Keep an explicit wildcard useful without reflecting an
					// attacker-controlled origin.
					w.Header().Set("Vary", "Origin")
				} else {
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withAuth enforces the bearer token when one is configured. An empty token
// means the dashboard is open, which is the expected setup behind a private
// network such as Tailscale.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.config()
		token := strings.TrimSpace(cfg.Server.AuthToken)
		if token == "" || cfg.Server.AuthDisabled || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Health stays reachable so the UI can report "needs token". The dashboard
		// auth endpoints are also exempt: a browser authenticates with the
		// dashboard PASSWORD (a cookie), not the bearer token, so it must reach
		// login/status/logout without already holding the token. The password
		// login then gates the rest of the dashboard via withDashboardAuth.
		switch r.URL.Path {
		case "/api/health", "/api/auth/status", "/api/auth/login", "/api/auth/logout", "/api/auth/password":
			next.ServeHTTP(w, r)
			return
		}

		presented := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			presented = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
		if presented == "" && queryTokenAllowed(r.URL.Path) {
			// EventSource and media elements cannot set headers, so only the
			// narrow stream/media allowlist accepts a query capability.
			presented = r.URL.Query().Get("token")
		}
		// Constant-time compare so the token isn't a timing oracle — this gate now
		// also fronts SSH command execution via /api/vps.
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("invalid token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----------------------------------------------------------------

// newID mints a prefixed random identifier for records created via the API.
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// sseWriter streams server-sent events with flushing after every frame.
type sseWriter struct {
	w  http.ResponseWriter
	f  http.Flusher
	mu sync.Mutex
}

func newSSE(w http.ResponseWriter) (*sseWriter, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming is not supported")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}, nil
}

func (s *sseWriter) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

func (s *sseWriter) comment(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.f.Flush()
}

// Serve runs the HTTP server until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Addr(),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: chat responses stream for as long as the model runs.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("antares listening",
			"addr", srv.Addr, "version", version.Version, "database", s.db.Driver())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
