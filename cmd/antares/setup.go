package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/server"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

// needsSetup reports whether Antares has enough configuration to answer at all.
func needsSetup(cfg *config.Config) bool {
	if strings.TrimSpace(cfg.Model.Default) == "" {
		return true
	}
	_, p := cfg.ResolveProvider(cfg.Model.Provider)
	// A local endpoint needs no credential; everything else does.
	if p.APIKey == "" && !isLocalEndpoint(p.BaseURL) {
		return true
	}
	return false
}

func isLocalEndpoint(url string) bool {
	l := strings.ToLower(url)
	return strings.Contains(l, "localhost") ||
		strings.Contains(l, "127.0.0.1") ||
		strings.Contains(l, "0.0.0.0")
}

// cmdSetup is the entry point for `antares setup`.
func cmdSetup(args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()

	// An explicit mode skips the picker: `antares setup --web`, `--terminal`.
	for _, a := range args {
		switch strings.TrimLeft(a, "-") {
		case "web", "browser":
			return runWebSetup(ctx, rt)
		case "terminal", "tui", "cli":
			return runTerminalSetup(ctx, rt)
		}
	}
	return runSetupWizard(ctx, rt)
}

// runSetupWizard asks how the user wants to configure Antares, then runs it.
func runSetupWizard(ctx context.Context, rt *runtimeServices) error {
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	if !interactive {
		// Without a terminal there is nothing to prompt with; point at the file.
		fmt.Printf("Antares is not configured yet and there is no terminal to ask on.\n"+
			"Edit %s, or set ANTARES_MODEL and a provider API key.\n", config.ConfigFile())
		return errors.New("setup required")
	}

	printBanner2()
	fmt.Println("  Antares needs a model and an API key before it can answer.")
	fmt.Println("  How would you like to set that up?")
	fmt.Println()
	fmt.Println("    1  Browser   — a guided page in the dashboard")
	fmt.Println("    2  Terminal  — a few questions right here")
	fmt.Println()

	choice := promptLine("  Choose [1/2] (default 1): ", "1")
	switch strings.TrimSpace(choice) {
	case "2", "t", "terminal", "tui":
		return runTerminalSetup(ctx, rt)
	default:
		return runWebSetup(ctx, rt)
	}
}

// ---- browser setup -----------------------------------------------------------

// runWebSetup serves the dashboard and waits until configuration is complete.
func runWebSetup(ctx context.Context, rt *runtimeServices) error {
	port := rt.cfg.Server.Port
	if !portFree(port) {
		// Something already listens there — most likely a running Antares.
		fmt.Printf("\n  Port %d is already in use.\n", port)
		fmt.Printf("  Open http://localhost:%d/setup in your browser to finish.\n\n", port)
		return nil
	}

	srv := server.New(server.Options{
		Config: rt.cfg, Agent: rt.agent, Store: rt.db,
		Dist: server.EmbeddedDist(), Reload: rt.reload,
		Skills: rt.skills, Cron: rt.cron, Gateway: rt.gateway, MCP: rt.mcp,
		Cursor: rt.cursorRunner,
	})

	urls := setupURLs(port)
	fmt.Println()
	fmt.Println("  Open this in your browser to finish setup:")
	for _, u := range urls {
		fmt.Printf("    %s\n", u)
	}
	fmt.Println()
	fmt.Println("  Waiting… press Ctrl+C to cancel.")

	openBrowser(urls[0])

	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(serveCtx) }()

	// Poll the config until the wizard has written a usable model + key.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cfg, err := config.Reload()
			if err != nil {
				continue
			}
			if !needsSetup(cfg) {
				rt.cfg = cfg
				rt.agent.SetConfig(cfg)
				fmt.Printf("\n  Configured: %s (%s)\n\n", cfg.Model.Default, cfg.Model.Provider)
				stopServing()
				<-errCh
				return nil
			}
		}
	}
}

// setupURLs lists every address the setup page is reachable on, so a headless
// box can be finished from a laptop on the same network.
func setupURLs(port int) []string {
	urls := []string{fmt.Sprintf("http://localhost:%d/setup", port)}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		urls = append(urls, fmt.Sprintf("http://%s:%d/setup", ipnet.IP.String(), port))
	}
	return urls
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// openBrowser is best-effort; a headless box simply prints the URL.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return // headless: nothing to open
		}
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// ---- terminal setup ----------------------------------------------------------

// providerChoice is one option in the provider picker.
type providerChoice struct {
	id      string
	label   string
	hint    string
	keyHint string
	// suggested models shown after the key is entered
	models []string
}

var providerChoices = []providerChoice{
	{
		id: "openrouter", label: "OpenRouter",
		hint:    "one key, most models — the easiest start",
		keyHint: "sk-or-v1-…  from openrouter.ai/keys",
		models: []string{
			"anthropic/claude-sonnet-4.5",
			"openai/gpt-5",
			"google/gemini-2.5-pro",
			"deepseek/deepseek-chat",
		},
	},
	{
		id: "anthropic", label: "Anthropic",
		hint:    "Claude, direct",
		keyHint: "sk-ant-…  from console.anthropic.com",
		models:  []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"},
	},
	{
		id: "openai", label: "OpenAI",
		hint:    "GPT, direct",
		keyHint: "sk-…  from platform.openai.com/api-keys",
		models:  []string{"gpt-5", "gpt-5-mini", "gpt-4.1"},
	},
	{
		id: "gemini", label: "Google Gemini",
		hint:    "Gemini, direct",
		keyHint: "from aistudio.google.com/apikey",
		models:  []string{"gemini-2.5-pro", "gemini-2.5-flash"},
	},
	{
		id: "ollama", label: "Ollama (local)",
		hint:   "runs on this machine, no key needed",
		models: []string{"llama3.3", "qwen2.5-coder"},
	},
	{
		id: "lmstudio", label: "LM Studio (local)",
		hint: "runs on this machine, no key needed",
	},
	{
		id: "custom", label: "Something else",
		hint: "any OpenAI-compatible endpoint",
	},
}

// runTerminalSetup walks the questions that matter and writes the config.
func runTerminalSetup(ctx context.Context, rt *runtimeServices) error {
	cfg, err := config.Reload()
	if err != nil {
		return err
	}

	printBanner2()

	// 1. Provider
	fmt.Println("  Which provider should Antares talk to?")
	fmt.Println()
	for i, p := range providerChoices {
		fmt.Printf("    %d  %-18s %s\n", i+1, p.label, dim(p.hint))
	}
	fmt.Println()
	idx := promptIndex("  Choose [1-"+strconv.Itoa(len(providerChoices))+"] (default 1): ", len(providerChoices), 1)
	chosen := providerChoices[idx]
	cfg.Model.Provider = chosen.id

	entry := cfg.Providers[chosen.id]
	if entry.Kind == "" {
		entry.Kind = "openai-compatible"
	}
	entry.Enabled = true

	// 2. Endpoint for custom / local providers
	switch chosen.id {
	case "custom":
		entry.BaseURL = promptLine("\n  Base URL (e.g. https://api.example.com/v1): ", entry.BaseURL)
		if entry.BaseURL == "" {
			return errors.New("a base URL is required for a custom provider")
		}
	case "ollama":
		entry.BaseURL = promptLine("\n  Ollama URL (default http://127.0.0.1:11434/v1): ", "http://127.0.0.1:11434/v1")
	case "lmstudio":
		entry.BaseURL = promptLine("\n  LM Studio URL (default http://127.0.0.1:1234/v1): ", "http://127.0.0.1:1234/v1")
	}

	// 3. API key, unless the endpoint is local
	if !isLocalEndpoint(entry.BaseURL) {
		fmt.Println()
		if chosen.keyHint != "" {
			fmt.Printf("  API key  %s\n", dim(chosen.keyHint))
		}
		key := promptSecret("  Paste it here (input hidden): ")
		if key == "" && entry.APIKey == "" {
			fmt.Println("\n  " + warn("No key entered — Antares will not be able to answer until one is set."))
		} else if key != "" {
			entry.APIKey = key
		}
	}
	cfg.Providers[chosen.id] = entry

	// 4. Model, verified against the provider when possible
	fmt.Println()
	model := pickModel(ctx, cfg, chosen)
	if model == "" {
		return errors.New("a model is required")
	}
	cfg.Model.Default = model

	// 5. Workspace — only record the path here; the directory is created after
	// the wizard finishes (see below), so an abandoned wizard leaves nothing.
	fmt.Println()
	ws := promptLine(fmt.Sprintf("  Workspace directory (default %s): ", cfg.Agent.Workspace), cfg.Agent.Workspace)
	cfg.Agent.Workspace = config.Expand(ws)

	// 5b. Storage. SQLite is the default and needs nothing; Postgres asks for a
	// DSN and is verified now so a bad string surfaces here, not on next start.
	fmt.Println()
	if promptYesNo("  Use PostgreSQL instead of the default SQLite?", false) {
		dsn := promptLine("  Connection string (postgres://user:pass@host:5432/db?sslmode=disable): ", "")
		dsn = strings.TrimSpace(dsn)
		if dsn == "" {
			fmt.Println("    " + dim("no DSN given — keeping SQLite."))
		} else {
			fmt.Print("  Checking the connection… ")
			probe, err := store.Open(ctx, "postgres", dsn, 2, 5000, false)
			if err != nil {
				fmt.Println(warn("failed"))
				fmt.Printf("    %s\n", dim(err.Error()))
				fmt.Println("    " + dim("keeping SQLite. Set it later with `antares setup` or in the dashboard."))
			} else {
				probe.Close()
				fmt.Println(good("ok"))
				cfg.Database.Driver = "postgres"
				cfg.Database.DSN = dsn
			}
		}
	}

	// 6. Optional extras
	fmt.Println()
	if promptYesNo("  Enable semantic search (RAG)?", false) {
		cfg.RAG.Enabled = true
		fmt.Println("    1  voyage   " + dim("Voyage AI embeddings (voyage-4)"))
		fmt.Println("    2  openai   " + dim("OpenAI-compatible /v1/embeddings"))
		fmt.Println("    3  custom   " + dim("your own OpenAI-compatible endpoint"))
		switch promptIndex("  Embedding provider [1-3] (default 1): ", 3, 1) {
		case 2:
			cfg.RAG.EmbedProvider = "openai"
			cfg.RAG.EmbedModel = promptLine("  Embedding model (default text-embedding-3-small): ", "text-embedding-3-small")
		case 3:
			cfg.RAG.EmbedProvider = "custom"
			cfg.RAG.EmbedBaseURL = promptLine("  Embeddings endpoint URL: ", "")
			cfg.RAG.EmbedModel = promptLine("  Embedding model: ", "")
			cfg.RAG.EmbedAPIKey = promptSecret("  API key (blank if none): ")
		default:
			cfg.RAG.EmbedProvider = "voyage"
			cfg.RAG.EmbedModel = promptLine("  Embedding model (default voyage-4): ", "voyage-4")
			cfg.RAG.EmbedAPIKey = promptSecret("  Voyage API key: ")
		}
	}

	fmt.Println()
	if promptYesNo("  Connect a Telegram bot?", false) {
		token := promptSecret("  Bot token from @BotFather: ")
		if token != "" {
			cfg.Gateway.Enabled = true
			cfg.Gateway.Telegram.Enabled = true
			cfg.Gateway.Telegram.BotToken = token
		}
	}

	// 6b. Dashboard password (web only). The TUI never asks for it.
	fmt.Println()
	if promptYesNo("  Protect the web dashboard with a password?", false) {
		pw := promptSecret("  Dashboard password (input hidden): ")
		if pw != "" {
			hash, err := config.HashPassword(pw)
			if err != nil {
				return fmt.Errorf("hashing dashboard password: %w", err)
			}
			cfg.Server.DashboardPasswordHash = hash
		}
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("saving configuration: %w", err)
	}
	// Now that setup completed, create the chosen workspace directory.
	if err := os.MkdirAll(cfg.Agent.Workspace, 0o755); err != nil {
		return fmt.Errorf("creating workspace: %w", err)
	}
	rt.cfg = cfg
	rt.agent.SetConfig(cfg)

	// 7. Verify against the provider so a bad key surfaces now, not later.
	fmt.Println()
	fmt.Print("  Checking the provider… ")
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if ok, detail := rt.agent.Probe(probeCtx); ok {
		fmt.Println(good("ok") + "  " + dim(detail))
	} else {
		fmt.Println(warn("failed"))
		fmt.Printf("    %s\n", dim(detail))
		fmt.Println("    " + dim("Fix it later with `antares setup` or in the dashboard."))
	}

	fmt.Println()
	fmt.Printf("  Saved to %s\n", config.ConfigFile())
	fmt.Println()
	fmt.Println("  " + bold("Next"))
	fmt.Println("    antares          start chatting in the terminal")
	fmt.Printf("    antares serve    dashboard on http://localhost:%d\n", cfg.Server.Port)
	fmt.Println()
	return nil
}

// pickModel offers the provider's catalogue when it can be listed, and falls
// back to the curated suggestions when it cannot.
func pickModel(ctx context.Context, cfg *config.Config, chosen providerChoice) string {
	listCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	id, p := cfg.ResolveProvider(chosen.id)
	var live []llm.ModelInfo
	if client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: p.Headers, ProviderID: id, Timeout: 20 * time.Second,
	}); err == nil {
		fmt.Print("  Fetching the model list… ")
		if models, err := client.Models(listCtx); err == nil && len(models) > 0 {
			live = models
			fmt.Println(good(fmt.Sprintf("%d found", len(models))))
		} else {
			fmt.Println(dim("unavailable"))
		}
	}

	suggestions := chosen.models
	if len(live) > 0 {
		// Keep the curated order where those models exist, then fill the rest.
		have := map[string]bool{}
		for _, m := range live {
			have[m.ID] = true
		}
		var ordered []string
		for _, s := range suggestions {
			if have[s] {
				ordered = append(ordered, s)
				delete(have, s)
			}
		}
		rest := make([]string, 0, len(have))
		for id := range have {
			rest = append(rest, id)
		}
		sort.Strings(rest)
		suggestions = append(ordered, rest...)
	}

	if len(suggestions) == 0 {
		return promptLine("  Model id: ", "")
	}

	fmt.Println()
	fmt.Println("  Pick a model:")
	shown := suggestions
	if len(shown) > 12 {
		shown = shown[:12]
	}
	for i, s := range shown {
		fmt.Printf("    %2d  %s\n", i+1, s)
	}
	fmt.Println("     0  " + dim("type an id myself"))
	fmt.Println()

	idx := promptIndex(fmt.Sprintf("  Choose [0-%d] (default 1): ", len(shown)), len(shown), 1)
	if idx < 0 {
		return promptLine("  Model id: ", "")
	}
	return shown[idx]
}

// ---- prompt helpers ----------------------------------------------------------

var stdinReader = bufio.NewReader(os.Stdin)

func promptLine(question, def string) string {
	fmt.Print(question)
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// promptSecret reads without echoing, falling back to a visible read when the
// terminal cannot be put into no-echo mode.
func promptSecret(question string) string {
	fmt.Print(question)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return promptLine("", "")
	}
	b, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// promptIndex reads a 1-based choice and returns it 0-based; 0 returns -1.
func promptIndex(question string, count, def int) int {
	for {
		line := promptLine(question, strconv.Itoa(def))
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			fmt.Println("  " + warn("Enter a number."))
			continue
		}
		if n == 0 {
			return -1
		}
		if n < 1 || n > count {
			fmt.Printf("  %s\n", warn(fmt.Sprintf("Choose between 1 and %d.", count)))
			continue
		}
		return n - 1
	}
}

func promptYesNo(question string, def bool) bool {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	line := strings.ToLower(promptLine(question+suffix, ""))
	switch line {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// ---- presentation ------------------------------------------------------------

func colourEnabled() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""
}

func paint(code, s string) string {
	if !colourEnabled() {
		return s
	}
	return code + s + "\x1b[0m"
}

func dim(s string) string  { return paint("\x1b[2m", s) }
func bold(s string) string { return paint("\x1b[1m", s) }
func good(s string) string { return paint("\x1b[38;5;114m", s) }
func warn(s string) string { return paint("\x1b[38;5;179m", s) }

func printBanner2() {
	accent := "\x1b[38;5;203m"
	if !colourEnabled() {
		accent = ""
	}
	reset := "\x1b[0m"
	if !colourEnabled() {
		reset = ""
	}
	fmt.Printf("\n  %s✳%s  %s %s\n\n", accent, reset, bold(version.Display), dim(version.Version))
}
