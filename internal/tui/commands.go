package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/version"
)

// Command is one slash command offered in the palette. Native commands carry a
// Run; the rest come from the shared registry in internal/commands and are
// dispatched through commands.Run.
type Command struct {
	Name    string
	Args    string
	Summary string
	Run     func(m *Model, args string) (quit bool, cmd tea.Cmd)
}

// native lists the commands the terminal carries out itself. Most are here
// because the terminal can do more than print text — a picker, a staged
// confirmation, a clipboard — and a few because they read or change state only
// this Model holds (the reasoning display, the next turn's options). Everything
// else is the registry's, so it behaves as it does in the web chat.
var native []Command

func init() {
	native = []Command{
		{"help", "", "Show every command", (*Model).cmdHelp},
		{"model", "[id]", "Pick the active model", (*Model).cmdModel},
		{"provider", "[id]", "Connect or switch a provider", (*Model).cmdProvider},
		{"theme", "[name]", "Pick the colour theme (Ctrl+T)", (*Model).cmdTheme},
		{"reasoning", "", "Toggle reasoning display (Ctrl+R)", (*Model).cmdReasoning},
		{"revert", "[message-id]", "Revert to an earlier turn (picker)", (*Model).cmdRevert},
		{"undo", "", "Undo the last turn, restoring files", (*Model).cmdUndo},
		{"web", "", "Print the dashboard URL", (*Model).cmdWeb},
		{"quit", "", "Leave the TUI", func(*Model, string) (bool, tea.Cmd) { return true, nil }},
		{"effort", "[level]", "Pick the reasoning effort for the next turns", (*Model).cmdEffort},
		{"project", "[dir|clear]", "Bind a folder to the new session", (*Model).cmdProject},
		{"attach", "[path|clear]", "Attach a file or image to the next message", (*Model).cmdAttach},
		{"search", "<text>", "Search past messages and resume one", (*Model).cmdSearch},
		{"delete", "", "Delete this conversation", (*Model).cmdDelete},
		{"answer", "<text>", "Answer the question the agent is waiting on", (*Model).cmdAnswer},
	}
	sort.Slice(native, func(i, j int) bool { return native[i].Name < native[j].Name })
}

// paletteCommands is the union shown in the palette and by /help: every native
// command, then every registry command offered in the terminal that no native
// one already covers. Sorted by name.
func paletteCommands() []Command {
	out := make([]Command, 0, len(native)+64)
	seen := make(map[string]bool, len(native))
	for _, c := range native {
		out = append(out, c)
		seen[c.Name] = true
	}
	for _, s := range commands.Catalogue(commands.SurfaceTUI) {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, Command{Name: s.Name, Args: s.Args, Summary: s.Summary})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func nativeCommand(name string) (Command, bool) {
	for _, c := range native {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func commandExists(name string) bool {
	for _, c := range paletteCommands() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// updatePalette recomputes the command suggestions from the current input.
func (m *Model) updatePalette() {
	text := m.ta.Value()
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n") {
		m.palette = nil
		return
	}
	prefix := strings.ToLower(strings.TrimPrefix(text, "/"))
	var out []Command
	for _, c := range paletteCommands() {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	m.palette = out
	if m.paletteSel >= len(out) {
		m.paletteSel = 0
	}
}

func (m *Model) acceptCompletion() {
	if len(m.palette) == 0 {
		return
	}
	c := m.palette[m.paletteSel]
	m.ta.SetValue("/" + c.Name + " ")
	m.ta.CursorEnd()
	m.syncCursor()
	m.palette = nil
}

// runCommand parses and runs a slash command: a native one in place, anything
// else through the shared registry on a background command.
func (m *Model) runCommand(line string) (bool, tea.Cmd) {
	name, args, ok := commands.Parse(line)
	if !ok {
		m.pushSystem("Not a command. Try /help.")
		return false, nil
	}
	if c, ok := nativeCommand(name); ok {
		return c.Run(m, args)
	}
	if name == "role" && args != "" && m.sessionID == "" {
		// The registry attaches a role to an existing session; before the
		// first turn there is none, so hold it and send it with that turn —
		// the same thing the web's role picker does.
		return false, m.stageRole(args)
	}
	if _, ok := commands.Lookup(name); !ok {
		m.pushSystem("Unknown command /" + name + ". Try /help.")
		return false, nil
	}
	return false, m.runRegistry(name, args)
}

// cmdResultMsg carries a registry command's outcome back to the update loop.
type cmdResultMsg struct {
	name string
	res  commands.Result
	err  error
}

// runRegistry runs one registry command off the update loop — /models, /learn
// and /panel call a model and can take a while.
func (m *Model) runRegistry(name, args string) tea.Cmd {
	deps := m.commandDeps()
	in := commands.Input{
		Name:      name,
		Args:      args,
		SessionID: m.sessionID,
		Surface:   commands.SurfaceTUI,
		Platform:  "tui",
	}
	m.setStatus("/" + name + "…")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := commands.Run(ctx, deps, in)
		return cmdResultMsg{name: name, res: res, err: err}
	}
}

// commandDeps hands the registry what the terminal holds. The config is
// captured by value so the command goroutine never reads m.cfg while the
// update loop swaps it.
func (m *Model) commandDeps() commands.Deps {
	cfg := m.cfg
	d := commands.Deps{
		Config:  func() *config.Config { return cfg },
		Agent:   m.ag,
		Store:   m.db,
		MCP:     m.mcp,
		Reload:  m.reload,
		Version: version.Version,
		WebURL:  m.webURL(),
	}
	if m.ag != nil {
		d.Skills = m.ag.Skills()
	}
	return d
}

func (m *Model) pushSystem(text string) {
	m.blocks = append(m.blocks, block{kind: blockSystem, text: text})
}

// pushOutput shows command output, which the registry writes as markdown.
func (m *Model) pushOutput(text string) {
	m.blocks = append(m.blocks, block{kind: blockSystem, text: text, markdown: true})
}

// ---- command implementations ------------------------------------------------

func (m *Model) cmdHelp(string) (bool, tea.Cmd) {
	var b strings.Builder
	b.WriteString("**Commands**\n\n")
	for _, c := range paletteCommands() {
		name := "/" + c.Name
		if c.Args != "" {
			name += " " + c.Args
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", name, c.Summary)
	}
	m.pushOutput(b.String())
	return false, nil
}

func (m *Model) cmdTheme(args string) (bool, tea.Cmd) {
	if args == "" {
		m.openThemePicker() // interactive, clickable picker
		return false, nil
	}
	if _, ok := themes[args]; !ok {
		m.pushSystem("Unknown theme " + args + ". Try /theme to pick.")
		return false, nil
	}
	m.applyTheme(args)
	m.persistTheme(args)
	m.setStatus("theme → " + args)
	return false, nil
}

// cmdReasoning stays native: the registry's /reasoning flips agent.verbose in
// the config, which this display does not read. Here it shows or hides the
// thinking blocks, the same toggle as Ctrl+R.
func (m *Model) cmdReasoning(string) (bool, tea.Cmd) {
	m.showReasoning = !m.showReasoning
	m.setStatus("reasoning " + onOff(m.showReasoning))
	return false, nil
}

func (m *Model) webURL() string {
	if m.cfg != nil && strings.TrimSpace(m.cfg.Server.PublicURL) != "" {
		return m.cfg.Server.PublicURL
	}
	port := 8787
	if m.cfg != nil && m.cfg.Server.Port > 0 {
		port = m.cfg.Server.Port
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

func (m *Model) cmdWeb(string) (bool, tea.Cmd) {
	m.pushSystem("Dashboard: " + m.webURL())
	return false, nil
}

func (m *Model) cmdModel(args string) (bool, tea.Cmd) {
	if args == "" {
		return false, m.openModelPicker()
	}
	if m.cfg == nil {
		return false, nil
	}
	m.cfg.Model.Default = args
	m.saveConfig()
	m.setStatus("model → " + args)
	m.pushSystem("Model set to " + args)
	return false, nil
}

func (m *Model) cmdProvider(args string) (bool, tea.Cmd) {
	if args == "" {
		m.openProviderPicker()
		return false, nil
	}
	if m.cfg == nil {
		return false, nil
	}
	m.selectProvider(args)
	return false, nil
}

// ---- tool headline ----------------------------------------------------------

func toolHeadline(name, args string) string {
	summary := summarizeArgs(args)
	if summary == "" {
		return name
	}
	return name + "  " + summary
}

// summarizeArgs renders a one-line hint from a tool's JSON arguments.
func summarizeArgs(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	for _, k := range []string{"path", "command", "query", "pattern", "url", "target", "domain", "ip", "action"} {
		if v, ok := m[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
