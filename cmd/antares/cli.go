package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/roles"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
	"github.com/enowdev/antares/internal/version"
)

// cliEnv is the light runtime a shell subcommand works against: configuration
// and the database, plus the skill library or an agent only when a command
// asks for them. Unlike bootstrap it never connects MCP servers, starts the
// scheduler, or unpacks bundled files, so a read-only listing stays fast and
// spawns no processes.
type cliEnv struct {
	ctx    context.Context
	cfg    *config.Config
	db     store.Store
	skills *skills.Manager
	agent  *agent.Agent
}

func openCLIEnv(ctx context.Context) (*cliEnv, error) {
	if err := config.EnsureHome(); err != nil {
		return nil, fmt.Errorf("preparing %s: %w", config.Home(), err)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	cfg, err = migrateSkillState(cfg)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN,
		cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	return &cliEnv{ctx: ctx, cfg: cfg, db: db}, nil
}

func (e *cliEnv) close() {
	if e.agent != nil {
		e.agent.Shell().CloseAll()
	}
	_ = e.db.Close()
}

// skillManager loads the skill library the same way bootstrap does, minus the
// background refresh and the one-time seeding of bundled skills.
func (e *cliEnv) skillManager() *skills.Manager {
	if e.skills != nil {
		return e.skills
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	mgr := skills.NewManager(skills.Options{
		Dirs: expandAll(e.cfg.Skills.Dirs), PackDirs: []string{config.Path("security-skills")},
		UserHome: home, ProjectDir: cwd,
	})
	if err := mgr.Reload(); err != nil {
		slog.Warn("some skills failed to load", "error", err)
	}
	mgr.SetDisabled(e.cfg.Skills.Disabled)
	e.skills = mgr
	return mgr
}

// lightAgent is an agent with roles and skills but no MCP servers, plugins, or
// retrieval: enough for commands that list models, roles, or role performance.
func (e *cliEnv) lightAgent() *agent.Agent {
	if e.agent != nil {
		return e.agent
	}
	ag := agent.New(e.cfg, e.db, tools.Default(), tools.NewShellManager(e.cfg.Terminal), nil)
	ag.SetSkills(e.skillManager())
	reg := roles.NewRegistry(expandAll(e.cfg.Roles.Dirs))
	_ = reg.Reload()
	ag.SetRoles(reg)
	e.agent = ag
	return ag
}

// cliNeeds says which services a passthrough command uses.
type cliNeeds int

const (
	needStore  cliNeeds = 0
	needSkills cliNeeds = 1 << iota
	needAgent
	// needRuntime means the full bootstrap: the command reports on MCP servers
	// or the complete tool registry, which only exist once servers connect.
	needRuntime
)

// passthrough lists the registry commands that make sense from a shell and what
// each needs. The rest either need a live conversation (/title, /undo, /goal,
// /role, …), are carried out by a screen (/new, /clear, /copy, …), already have
// a dedicated subcommand that wins (config, model, provider, status, version,
// help), or belong to the security modules (/findings, /report, /engagement).
var passthrough = map[string]cliNeeds{
	"models":    needAgent,
	"tools":     needRuntime,
	"toolset":   needStore,
	"skills":    needSkills,
	"memory":    needStore,
	"remember":  needStore,
	"forget":    needStore,
	"mcp":       needRuntime,
	"usage":     needStore,
	"cost":      needStore,
	"web":       needStore,
	"reasoning": needStore,
	"panel":     needAgent,
	"roles":     needAgent,
	"team":      needAgent,
}

// passthroughNeeds refines the table for commands whose needs depend on the
// arguments: `mcp search` and `mcp install` read the catalogue, not the servers.
func passthroughNeeds(name, args string) cliNeeds {
	n := passthrough[name]
	if name == "mcp" {
		verb, _, _ := strings.Cut(strings.TrimSpace(args), " ")
		switch verb {
		case "search", "browse", "install", "add":
			return needStore
		}
	}
	return n
}

// runPassthrough runs a registry command from the shell. The second return is
// false when name is not a command the shell exposes.
func runPassthrough(name string, args []string) (bool, error) {
	name = strings.ToLower(name)
	if _, ok := passthrough[name]; !ok {
		if spec, known := commands.Lookup(name); known {
			return true, fmt.Errorf("/%s works inside a conversation — use it in `antares tui` or the dashboard chat (%s)",
				spec.Name, spec.Summary)
		}
		return false, nil
	}
	return true, runRegistryCommand(name, strings.TrimSpace(strings.Join(args, " ")))
}

func runRegistryCommand(name, args string) error {
	ctx := context.Background()
	deps := commands.Deps{Version: version.Version}
	needs := passthroughNeeds(name, args)

	if needs&needRuntime != 0 {
		quietBootstrapLogs()
		rt, err := bootstrap(ctx)
		if err != nil {
			return err
		}
		defer rt.close()
		deps = rt.commandDeps()
		deps.WebURL = daemonURL(rt.cfg)
		// The shell has no live runtime to rebuild; the daemon, if any, is told
		// to restart below.
		deps.Reload = nil
	} else {
		env, err := openCLIEnv(ctx)
		if err != nil {
			return err
		}
		defer env.close()
		cfg := env.cfg
		deps.Config = func() *config.Config { return cfg }
		deps.Store = env.db
		deps.WebURL = daemonURL(cfg)
		if needs&needSkills != 0 {
			deps.Skills = env.skillManager()
		}
		if needs&needAgent != 0 {
			deps.Agent = env.lightAgent()
		}
	}

	// The shell is a terminal, and every command it exposes is offered on the
	// TUI surface, so it reuses that surface rather than adding a new one: the
	// registry only branches on surface to filter /help, which the shell does
	// not expose.
	res, err := commands.Run(ctx, deps, commands.Input{Name: name, Args: args, Surface: commands.SurfaceTUI})
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Output) != "" {
		fmt.Print(renderForTerminal(shellHints(res.Output), stdoutIsTTY()))
	}
	switch res.Action.Kind {
	case "config-changed", "skills-changed":
		noteDaemonRestart()
	}
	return nil
}

var slashHint = regexp.MustCompile("`/([a-z]+)")

// shellHints rewrites the registry's "try `/usage 30`" hints into the shell
// spelling, for the commands whose arguments read the same in both places.
func shellHints(md string) string {
	return slashHint.ReplaceAllStringFunc(md, func(m string) string {
		name := m[2:]
		if _, ok := passthrough[name]; ok || name == "skills" || name == "memory" || name == "model" {
			return "`antares " + name
		}
		return m
	})
}

// noteDaemonRestart tells the user a running server still has the old
// configuration: the shell writes config.yaml, but only the daemon's own
// reload path applies it live.
func noteDaemonRestart() {
	state, live, err := currentDaemon()
	if err != nil || !live {
		return
	}
	fmt.Fprintf(os.Stderr, "note: the running server (pid %d) picks this up on restart: antares stop && antares\n", state.PID)
}

// quietBootstrapLogs keeps the full runtime's info-level startup chatter
// ("mcp tools registered", …) out of a shell command's stderr. An explicit
// ANTARES_LOG_LEVEL still wins.
func quietBootstrapLogs() {
	if strings.TrimSpace(os.Getenv("ANTARES_LOG_LEVEL")) == "" {
		_ = os.Setenv("ANTARES_LOG_LEVEL", "warn")
	}
}
