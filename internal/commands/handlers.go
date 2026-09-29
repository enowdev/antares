package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// errNoStore and friends keep the "this is not wired up" message uniform.
var (
	errNoStore  = errors.New("no database is attached")
	errNoAgent  = errors.New("no agent is attached")
	errNoSkills = errors.New("skills are not enabled")
	errNoMCP    = errors.New("MCP is not enabled")
)

func cmdHelp(_ context.Context, _ Deps, in Input) (Result, error) {
	surface := in.Surface
	if surface == "" {
		surface = SurfaceWeb
	}
	var b strings.Builder
	b.WriteString("**Commands**\n\n")
	for _, c := range Catalogue(surface) {
		name := "/" + c.Name
		if c.Args != "" {
			name += " " + c.Args
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", name, c.Summary)
	}
	return Result{Output: b.String()}, nil
}

func cmdVersion(_ context.Context, d Deps, _ Input) (Result, error) {
	v := d.Version
	if v == "" {
		v = "unknown"
	}
	return Result{Output: "Antares " + v}, nil
}

func cmdWeb(_ context.Context, d Deps, _ Input) (Result, error) {
	if d.WebURL == "" {
		return Result{Output: "The dashboard address is not known from here."}, nil
	}
	return Result{Output: "Dashboard: " + d.WebURL}, nil
}

func cmdStatus(ctx context.Context, d Deps, _ Input) (Result, error) {
	cfg := d.config()
	var b strings.Builder
	b.WriteString("**Status**\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", orDash(cfg.Model.Default))
	fmt.Fprintf(&b, "- Provider: `%s`\n", orDash(cfg.Model.Provider))
	fmt.Fprintf(&b, "- Toolset: `%s`\n", orDash(cfg.Tools.Toolset))

	if d.Store != nil {
		fmt.Fprintf(&b, "- Database: `%s`\n", d.Store.Driver())
		if st, err := d.Store.Stats(ctx); err == nil {
			fmt.Fprintf(&b, "- Sessions: %d · Messages: %d · Memories: %d\n",
				st.Sessions, st.Messages, st.Memories)
		}
	}
	if d.Agent != nil {
		fmt.Fprintf(&b, "- Tools registered: %d\n", len(d.Agent.Registry().Names()))
		if n := d.Agent.ActiveCount(); n > 0 {
			fmt.Fprintf(&b, "- Turns in flight: %d\n", n)
		}
	}
	if d.Skills != nil {
		fmt.Fprintf(&b, "- Skills: %d\n", d.Skills.Count())
	}
	if cfg.RAG.Enabled {
		fmt.Fprintf(&b, "- Retrieval: `builtin · %s`\n", orDash(cfg.RAG.EmbedModel))
	}
	return Result{Output: b.String()}, nil
}

func cmdModel(_ context.Context, d Deps, in Input) (Result, error) {
	cfg := d.config()
	if in.Args == "" {
		return Result{Output: fmt.Sprintf(
			"Model `%s` on provider `%s`.\n\nChange it with `/model <id>`.",
			orDash(cfg.Model.Default), orDash(cfg.Model.Provider))}, nil
	}
	next, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	next.Model.Default = in.Args
	if err := config.Save(next); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("Model set to `%s`.", in.Args),
		Action: Action{Kind: "config-changed"},
	}, nil
}

func cmdModels(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	provider := in.Args
	if provider == "" {
		provider = d.config().Model.Provider
	}
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := d.Agent.Models(listCtx, provider)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%d model(s) from %s**\n\n", len(models), provider)
	// A provider like OpenRouter lists hundreds; a wall of them buries the
	// instruction that follows.
	const cap = 40
	for i, m := range models {
		if i >= cap {
			fmt.Fprintf(&b, "- … and %d more\n", len(models)-cap)
			break
		}
		fmt.Fprintf(&b, "- `%s`\n", m.ID)
	}
	b.WriteString("\nSelect one with `/model <id>`.")
	return Result{Output: b.String()}, nil
}

func cmdProvider(_ context.Context, d Deps, in Input) (Result, error) {
	cfg := d.config()
	if in.Args == "" {
		names := make([]string, 0, len(cfg.Providers))
		for name := range cfg.Providers {
			names = append(names, name)
		}
		sort.Strings(names)

		var b strings.Builder
		fmt.Fprintf(&b, "Active provider: `%s`\n\n**Configured**\n\n", orDash(cfg.Model.Provider))
		for _, n := range names {
			p := cfg.Providers[n]
			state := "no key"
			if p.APIKey != "" {
				state = "key set"
			}
			marker := ""
			if n == cfg.Model.Provider {
				marker = " ← active"
			}
			fmt.Fprintf(&b, "- `%s` — %s%s\n", n, state, marker)
		}
		b.WriteString("\nSwitch with `/provider <id>`.")
		return Result{Output: b.String()}, nil
	}

	next, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	if _, ok := next.Providers[in.Args]; !ok {
		return Result{}, fmt.Errorf("no provider named %q is configured", in.Args)
	}
	next.Model.Provider = in.Args
	if err := config.Save(next); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("Provider set to `%s`.", in.Args),
		Action: Action{Kind: "config-changed"},
	}, nil
}

func cmdTools(_ context.Context, d Deps, _ Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	cfg := d.config()
	active := d.Agent.Registry().Resolve(cfg.Tools.Toolset, cfg.Tools.Enabled, cfg.Tools.Disabled)

	var b strings.Builder
	fmt.Fprintf(&b, "**%d tool(s) active** (toolset `%s`)\n\n", len(active), orDash(cfg.Tools.Toolset))
	for _, t := range active {
		fmt.Fprintf(&b, "- `%s` — %s\n", t.Name(), firstLine(t.Description()))
	}
	return Result{Output: b.String()}, nil
}

func cmdToolset(_ context.Context, d Deps, in Input) (Result, error) {
	cfg := d.config()
	if in.Args == "" {
		return Result{Output: fmt.Sprintf(
			"Toolset `%s`.\n\nChange it with `/toolset <name>` — `all`, `read-only`, `coding`, or `chat`.",
			orDash(cfg.Tools.Toolset))}, nil
	}
	next, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	next.Tools.Toolset = in.Args
	if err := config.Save(next); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("Toolset set to `%s`.", in.Args),
		Action: Action{Kind: "config-changed"},
	}, nil
}

func cmdSkills(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Agent != nil {
		if mgr := d.Agent.Skills(); mgr != nil {
			d.Skills = mgr
		}
	}
	var projectDir string
	if in.SessionID != "" {
		if d.Store == nil {
			return Result{}, errNoStore
		}
		sess, err := d.Store.GetSession(ctx, in.SessionID)
		if err != nil {
			return Result{}, err
		}
		if sess != nil {
			projectDir, _ = sess.Meta["project_dir"].(string)
		}
	}
	if d.Skills == nil {
		return Result{}, errNoSkills
	}
	var err error
	d.Skills, err = d.Skills.ForProject(projectDir)
	if err != nil {
		slog.Warn("some skills failed to load", "error", err)
	}
	// "/skills search foo" and "/skills install id" reach the hub; anything
	// else lists what is already installed.
	if verb, rest, _ := strings.Cut(in.Args, " "); verb == "search" || verb == "browse" {
		return hubSkillSearch(ctx, d, strings.TrimSpace(rest))
	} else if verb == "install" || verb == "add" {
		return hubSkillInstall(ctx, d, strings.TrimSpace(rest))
	}

	// With no filter, list the everyday catalogue: the bundled security
	// library is thousands strong and would bury it. A filter looks across
	// everything, capped so a broad word does not print the whole library.
	q := strings.ToLower(in.Args)
	list := d.Skills.Everyday()
	if q != "" {
		list = d.Skills.List()
	}
	const maxShown = 50
	var b strings.Builder
	matched := 0
	for _, s := range list {
		if q != "" && !strings.Contains(strings.ToLower(s.Name+" "+s.Description), q) {
			continue
		}
		matched++
		if matched > maxShown {
			continue
		}
		state := ""
		if !s.Enabled {
			state = " _(off)_"
		}
		fmt.Fprintf(&b, "- `%s`%s — %s\n", s.Name, state, s.Description)
	}
	if matched == 0 {
		return Result{Output: "No skills match."}, nil
	}
	if matched > maxShown {
		fmt.Fprintf(&b, "- … and %d more; narrow the filter\n", matched-maxShown)
	}
	b.WriteString("\nFind more with `/skills search <words>`.")
	return Result{Output: fmt.Sprintf("**%d skill(s)**\n\n", matched) + b.String()}, nil
}

func cmdMemory(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	var (
		items []store.Memory
		err   error
	)
	if in.Args == "" {
		items, err = d.Store.ListMemories(ctx, "", "", 20)
	} else {
		items, err = d.Store.SearchMemories(ctx, in.Args, 20)
	}
	if err != nil {
		return Result{}, err
	}
	if len(items) == 0 {
		return Result{Output: "Nothing stored yet. Add one with `/remember <text>`."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%d memor%s**\n\n", len(items), plural(len(items), "y", "ies"))
	for _, m := range items {
		key := m.Key
		if key == "" {
			key = m.ID
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", key, firstLine(m.Content))
	}
	return Result{Output: b.String()}, nil
}

func cmdRemember(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.Args == "" {
		return Result{}, errors.New("usage: /remember <text>")
	}
	// An explicit "key: value" is honoured; anything else is stored keyless.
	key, content := "", in.Args
	if k, rest, ok := strings.Cut(in.Args, ":"); ok && len(k) <= 40 && !strings.Contains(k, " ") {
		key, content = strings.TrimSpace(k), strings.TrimSpace(rest)
	}
	// A fresh id every time: the store upserts on id, so an empty one made
	// each /remember overwrite the previous memory.
	m := &store.Memory{ID: randomID("mem"), Scope: "global", Key: key, Content: content, Source: "command"}
	if err := d.Store.PutMemory(ctx, m); err != nil {
		return Result{}, err
	}
	return Result{Output: "Stored."}, nil
}

func cmdForget(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.Args == "" {
		return Result{}, errors.New("usage: /forget <key>")
	}
	items, err := d.Store.ListMemories(ctx, "", "", 500)
	if err != nil {
		return Result{}, err
	}
	for _, m := range items {
		if m.Key == in.Args || m.ID == in.Args {
			if err := d.Store.DeleteMemory(ctx, m.ID); err != nil {
				return Result{}, err
			}
			return Result{Output: "Forgotten."}, nil
		}
	}
	return Result{}, fmt.Errorf("no memory keyed %q", in.Args)
}

func cmdMCP(ctx context.Context, d Deps, in Input) (Result, error) {
	if verb, rest, _ := strings.Cut(in.Args, " "); verb == "search" || verb == "browse" {
		return hubMCPSearch(ctx, d, strings.TrimSpace(rest))
	} else if verb == "install" || verb == "add" {
		return hubMCPInstall(ctx, d, strings.TrimSpace(rest))
	}
	if d.MCP == nil {
		return Result{}, errNoMCP
	}
	statuses := d.MCP.Status(d.config())
	if len(statuses) == 0 {
		return Result{Output: "No MCP servers are configured."}, nil
	}
	var b strings.Builder
	b.WriteString("**MCP servers**\n\n")
	for _, s := range statuses {
		state := "connected"
		if !s.Connected {
			state = "offline"
			if s.Error != "" {
				state += " — " + s.Error
			}
		}
		fmt.Fprintf(&b, "- `%s` — %s, %d tool(s)\n", s.Name, state, len(s.Tools))
	}
	return Result{Output: b.String()}, nil
}

func cmdConfig(_ context.Context, d Deps, in Input) (Result, error) {
	cfg := d.config()
	if in.Args == "" {
		return Result{Output: "Usage: `/config <path>` to read, `/config <path> <value>` to set. " +
			"Paths look like `model.default` or `agent.toolset`."}, nil
	}
	path, value, hasValue := strings.Cut(in.Args, " ")
	path = strings.TrimSpace(path)
	value = strings.TrimSpace(value)

	if !hasValue || value == "" {
		v, err := cfg.GetPath(path)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: fmt.Sprintf("`%s` = `%v`", path, v)}, nil
	}

	next, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	if err := next.SetPath(path, value); err != nil {
		return Result{}, err
	}
	if err := config.Save(next); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("`%s` set to `%s`.", path, value),
		Action: Action{Kind: "config-changed"},
	}, nil
}

func cmdSessions(ctx context.Context, d Deps, _ Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	list, _, err := d.Store.ListSessions(ctx, store.SessionFilter{Limit: 15})
	if err != nil {
		return Result{}, err
	}
	if len(list) == 0 {
		return Result{Output: "No sessions yet."}, nil
	}
	var b strings.Builder
	b.WriteString("**Recent sessions**\n\n")
	for _, s := range list {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "- `%s` — %s · %d msg · %s\n",
			shortID(s.ID), title, s.MessageCount, s.UpdatedAt.Format("02 Jan 15:04"))
	}
	b.WriteString("\nResume one with `/resume <id>`.")
	return Result{Output: b.String()}, nil
}

func cmdResume(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	if in.Args == "" {
		return Result{}, errors.New("usage: /resume <session id>")
	}
	list, _, err := d.Store.ListSessions(ctx, store.SessionFilter{Limit: 200})
	if err != nil {
		return Result{}, err
	}
	// Accept a prefix, since /sessions prints shortened ids.
	for _, s := range list {
		if strings.HasPrefix(s.ID, in.Args) {
			return Result{
				Output: "Resuming " + shortID(s.ID) + ".",
				Action: Action{Kind: "resume", Value: s.ID},
			}, nil
		}
	}
	return Result{}, fmt.Errorf("no session matches %q", in.Args)
}

func cmdStop(_ context.Context, d Deps, in Input) (Result, error) {
	if d.Agent == nil {
		return Result{}, errNoAgent
	}
	if in.SessionID != "" && d.Agent.Interrupt(in.SessionID) {
		return Result{Output: "Interrupted.", Action: Action{Kind: "stop"}}, nil
	}
	return Result{Output: "Nothing is running.", Action: Action{Kind: "stop"}}, nil
}

func cmdCompact(_ context.Context, _ Deps, in Input) (Result, error) {
	if in.SessionID == "" {
		return Result{}, errors.New("there is no session to compact yet")
	}
	// The agent compacts on its own thresholds; forcing it here would mean
	// running a turn purely to summarise. Marking it is enough — the next turn
	// picks the summary up.
	return Result{
		Output: "The next turn will start from a summary of this session.",
		Action: Action{Kind: "compact", Value: in.SessionID},
	}, nil
}

func cmdReasoning(_ context.Context, d Deps, in Input) (Result, error) {
	next, err := config.Reload()
	if err != nil {
		return Result{}, err
	}
	on := !next.Agent.Verbose
	switch strings.ToLower(in.Args) {
	case "on", "true", "1":
		on = true
	case "off", "false", "0":
		on = false
	}
	next.Agent.Verbose = on
	if err := config.Save(next); err != nil {
		return Result{}, err
	}
	if err := d.reload(); err != nil {
		return Result{}, err
	}
	state := "hidden"
	if on {
		state = "shown"
	}
	return Result{
		Output: "Verbose reasoning is now " + state + ".",
		Action: Action{Kind: "config-changed"},
	}, nil
}

func cmdUsage(ctx context.Context, d Deps, in Input) (Result, error) {
	if d.Store == nil {
		return Result{}, errNoStore
	}
	days := 7
	if in.Args != "" {
		if n, err := strconv.Atoi(strings.TrimSuffix(in.Args, "d")); err == nil && n > 0 {
			days = n
		}
	}
	since := time.Now().AddDate(0, 0, -days)
	rows, err := d.Store.UsageByModel(ctx, since)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{Output: fmt.Sprintf("No usage recorded in the last %d day(s).", days)}, nil
	}

	var inTok, outTok int64
	var cost float64
	var b strings.Builder
	fmt.Fprintf(&b, "**Usage, last %d day(s)**\n\n", days)
	for _, r := range rows {
		inTok += r.TokensIn
		outTok += r.TokensOut
		cost += r.Cost
		fmt.Fprintf(&b, "- `%s` — %s in / %s out · $%.4f\n",
			r.Model, humanCount(r.TokensIn), humanCount(r.TokensOut), r.Cost)
	}
	fmt.Fprintf(&b, "\n**Total** %s in / %s out · $%.4f",
		humanCount(inTok), humanCount(outTok), cost)
	return Result{Output: b.String()}, nil
}

// ---- helpers ----------------------------------------------------------------

// config returns the live configuration, never nil.
func (d Deps) config() *config.Config {
	if d.Config != nil {
		if c := d.Config(); c != nil {
			return c
		}
	}
	return config.Get()
}

// reload re-reads configuration into the running services. A surface that did
// not supply one has nothing to refresh, which is not an error.
func (d Deps) reload() error {
	if d.Reload == nil {
		return nil
	}
	return d.Reload()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}

func shortID(id string) string {
	if len(id) > 14 {
		return id[:14]
	}
	return id
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}
