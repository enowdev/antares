// smokefixture seeds an isolated ANTARES_HOME so `bin/antares --foreground`
// can boot for smoke testing without touching the developer's
// real ~/.antares, without dialling any provider, and with the exact session
// row the dashboard route-walk expects at /c/<id>.
//
// Every field it writes is deliberate: onboarding must be considered *done*
// (Model.Default + a provider with a key or a local BaseURL — see
// server.NeedsSetup), auth must be enforced with a known password so the
// smoke script can log in against the real /api/auth/login flow, and every
// background subsystem that would otherwise reach the network (RAG embedder,
// gateways, plugins, cron, browser, MCP, smart titles, verify-replies,
// code-execution sandbox) must be off.
//
// The helper is intentionally its own binary so the real `antares` command
// never grows a "prepare smoke fixture" mode. It reuses config.Default,
// config.SaveAt, and store.Open so schema drift is caught here the same day
// it lands in production.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "smokefixture:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		home      = flag.String("home", "", "ANTARES_HOME to seed (required; must already be set as an env var too)")
		host      = flag.String("host", "127.0.0.1", "server host to bind (loopback keeps setup /complete gated to local requests)")
		port      = flag.Int("port", 0, "server port to write into config (required)")
		password  = flag.String("password", "", "dashboard password to hash (required)")
		authToken = flag.String("token", "", "server.auth_token bearer to accept (required)")
		modelBase = flag.String("model-base", "", "openai-compatible base URL for the smoke provider stub (required, e.g. http://127.0.0.1:PORT/v1)")
		modelName = flag.String("model", "smoke/gpt-smoke", "model id to record as the active chat model")
		sessionID = flag.String("session-id", "smoke-session", "session row to insert so /c/<id> resolves")
		workspace = flag.String("workspace", "", "agent workspace directory (defaults to <home>/workspace)")
	)
	flag.Parse()

	if *home == "" || *port == 0 || *password == "" || *authToken == "" || *modelBase == "" {
		flag.Usage()
		return errors.New("--home, --port, --password, --token, and --model-base are required")
	}
	// config.Path/Home read ANTARES_HOME lazily; if the parent forgot to set it
	// the write would silently land in the real ~/.antares. Refuse.
	if got := os.Getenv("ANTARES_HOME"); got != *home {
		return fmt.Errorf("ANTARES_HOME env (%q) does not match --home (%q); set it before invoking so config.Path resolves to the isolated tree", got, *home)
	}
	ws := *workspace
	if ws == "" {
		ws = filepath.Join(*home, "workspace")
	}
	for _, d := range []string{*home, ws} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	if err := config.EnsureHome(); err != nil {
		return fmt.Errorf("ensure home tree: %w", err)
	}

	cfg := config.Default()

	// Onboarding satisfied: NeedsSetup checks Model.Default plus a resolved
	// provider that either has an APIKey or a local (loopback) BaseURL. The
	// stub is loopback, so the synthetic key is decorative — but we set both
	// because the LLM client always sends whatever key is stored.
	cfg.Model.Default = *modelName
	cfg.Model.Provider = "smoke"
	cfg.Model.MaxTokens = 512
	cfg.Model.ContextWindow = 8192
	// Wipe the shipped provider list so no ambient env var (OPENAI_API_KEY,
	// ANTHROPIC_API_KEY, OPENROUTER_API_KEY) can accidentally light up a real
	// provider through config.applyEnv. The smoke stub is the only reachable
	// route to a model.
	cfg.Providers = map[string]config.Provider{
		"smoke": {
			Kind:        "openai-compatible",
			Label:       "Smoke Stub",
			Enabled:     true,
			BaseURL:     *modelBase,
			APIKey:      "sk-smoke-synthetic",
			TimeoutSecs: 30,
		},
	}
	cfg.ClearInlineModelCredentials()

	// Server: isolated port, loopback, real auth. AuthDisabled must stay false
	// so /api/auth/login is the code path being exercised.
	cfg.Server.Host = *host
	cfg.Server.Port = *port
	cfg.Server.AuthToken = *authToken
	cfg.Server.AuthDisabled = false
	cfg.Server.CORSOrigins = nil
	cfg.Server.PublicURL = ""
	hash, err := config.HashPassword(*password)
	if err != nil {
		return fmt.Errorf("hash dashboard password: %w", err)
	}
	cfg.Server.DashboardPasswordHash = hash

	// Database: sqlite inside the isolated home so nothing survives cleanup.
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(*home, "antares.db")
	cfg.Database.MaxConns = 4
	cfg.Database.WAL = true

	// Agent: no auxiliary model calls, deterministic titles, no verify loop,
	// no background reset. Workspace inside the fixture so the terminal tool
	// cannot escape into $HOME even if the smoke walk exercised it.
	cfg.Agent.Workspace = ws
	cfg.Agent.SmartTitles = false
	cfg.Agent.VerifyReplies = false
	cfg.Agent.IdleTimeoutSecs = 3600

	// Everything that would otherwise reach the network or spawn helper
	// binaries is off. The route walk only paints the dashboard; nothing here
	// is supposed to execute a tool.
	cfg.RAG.Enabled = false
	cfg.Gateway.Enabled = false
	cfg.Gateway.Telegram.Enabled = false
	cfg.Gateway.Discord.Enabled = false
	cfg.Plugins.Enabled = false
	cfg.Cron.Enabled = false
	cfg.Tools.Browser.Enabled = false
	cfg.MCP.Enabled = false
	cfg.MCP.Servers = map[string]config.MCPServer{}
	cfg.CodeExecution.Enabled = false
	cfg.Streaming.Enabled = true // dashboard reads /api/status; leave the flag as-is
	cfg.Memory.Enabled = false
	cfg.Memory.UserProfileEnabled = false
	cfg.Skills.Enabled = false
	cfg.Skills.Dirs = []string{filepath.Join(*home, "skills")}
	cfg.Plugins.Dirs = []string{filepath.Join(*home, "plugins")}

	// Logging inside the fixture home; the smoke driver tails /tmp separately.
	cfg.Logging.File = filepath.Join(*home, "logs", "antares.log")

	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Insert the fixture session through the real store so the schema is the
	// same one the server will read from — a hand-crafted INSERT would drift
	// the moment a migration adds a column.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN, cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	existing, err := st.GetSession(ctx, *sessionID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("lookup fixture session: %w", err)
	}
	if existing == nil {
		sess := &store.Session{
			ID:        *sessionID,
			Title:     "Smoke fixture session",
			Platform:  "web",
			ChannelID: "smoke",
			UserID:    "smoke",
			Model:     cfg.Model.Default,
			Provider:  cfg.Model.Provider,
			Workspace: ws,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Meta:      store.Meta{"fixture": true},
		}
		if err := st.CreateSession(ctx, sess); err != nil {
			return fmt.Errorf("create fixture session: %w", err)
		}
	}
	fmt.Printf("smokefixture ready home=%s port=%d model=%s session=%s\n", *home, *port, cfg.Model.Default, *sessionID)
	return nil
}
