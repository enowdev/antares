// Package tui is antares' terminal UI: a colourful, responsive, web-like chat
// built on Bubble Tea — sidebar, scrollable transcript with Markdown, a bordered
// input box, a slash-command palette, and toggleable reasoning. The Bubble Tea
// renderer owns the screen, so resizing and redraws never corrupt the layout.
package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/mcp"
	"github.com/enowdev/antares/internal/store"
)

// blockKind distinguishes transcript entries so each renders in its own voice.
type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockReasoning
	blockTool
	blockNotice
	blockError
	blockSystem
)

// block is one entry in the scrollback.
type block struct {
	kind      blockKind
	title     string
	text      string
	streaming bool
	done      bool
	isError   bool
	// markdown renders a system block through Glamour — command output.
	markdown bool
	// callID ties a tool block to its call, so progress and the result land on
	// the right block when a turn runs several tools at once.
	callID string
}

// agent-event bridge messages.
type (
	evMsg          struct{ e agent.Event }
	doneMsg        struct{ err error }
	welcomeTickMsg struct{}
)

// Model is the Bubble Tea model.
type Model struct {
	ag  *agent.Agent
	cfg *config.Config
	db  store.Store

	st       styles
	renderer *glamour.TermRenderer

	width, height int
	ready         bool

	vp   viewport.Model
	ta   textarea.Model
	spin spinner.Model

	blocks    []block
	sessionID string
	title     string

	busy   bool
	cancel context.CancelFunc
	msgCh  chan tea.Msg

	tokensIn, tokensOut int
	ctxUsed, ctxWindow  int
	showReasoning       bool
	status              string
	themeName           string

	palette    []Command
	paletteSel int

	picker picker
	input  inputModal

	// pending is a staged /undo or /revert awaiting the operator's "y" to
	// commit. Nil when nothing is pending; a pointer keeps the hot-path
	// check cheap (nil vs zero-valued struct).
	pending *pendingRevert

	// confirm is any other staged yes/no (/delete), handled like pending.
	confirm *pendingConfirm

	// ask and approvals are a running turn paused on the person: an
	// ask_user question, and tool calls waiting for approval (oldest first).
	ask       *pendingAsk
	approvals []pendingApproval

	// Per-turn options the web composer sends too. role is the session's
	// specialist (stored per session by /role; held here until a new
	// session exists), effort the reasoning effort ("" is auto), projectDir
	// the folder bound on a session's first turn, attachments the files
	// that go with the next message.
	role        string
	effort      string
	projectDir  string
	attachments []attachment

	// after is a command a picker's commit queued; the picker path has no
	// other way to return one. Drained by modalCmd.
	after tea.Cmd

	// carry is command output held back while a transcript reload it
	// triggered is in flight, so the reload does not wipe it.
	carry string

	cache map[string]string // memoised block renders, keyed by content+width

	welcomeFrame int // animation frame for the home screen's opening

	// chrome is the layout's own state: side column, tabs, per-session
	// counters, and where things were drawn (chrome.go, sidebar.go).
	chrome chromeState

	demo bool

	// reload rebuilds runtime-owned services (shell, RAG, skills, plugins,
	// roles, gateway, MCP) from the on-disk desired config. Wired by the
	// runtime via SetReload; nil in the demo and in tests.
	reload func() error

	// mcp is the runtime's MCP manager, for /mcp. Wired by SetMCP; nil
	// makes /mcp say MCP is not enabled.
	mcp *mcp.Manager
}

// New builds a TUI bound to a running agent.
func New(ag *agent.Agent, cfg *config.Config, db store.Store) *Model {
	ta := textarea.New()
	ta.Placeholder = "Message antares…  (/ for commands, Enter to send, Ctrl+J for newline)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.Focus()
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends; Ctrl+J adds a newline.
	// Hidden caret while empty so the placeholder shows no reverse-video block.
	ta.Cursor.SetMode(cursor.CursorHide)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	name := defaultTheme
	if cfg != nil {
		if t := cfg.Display.Theme; t != "" && t != "system" && t != "default" {
			if _, ok := themes[t]; ok {
				name = t
			}
		}
	}
	// A steady accent caret when the field has text.
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(themeByName(name).Accent)

	return &Model{
		ag: ag, cfg: cfg, db: db,
		themeName:     name,
		st:            newStyles(themeByName(name)),
		ta:            ta,
		spin:          sp,
		showReasoning: cfg != nil && cfg.Display.ShowReasoning,
	}
}

// SetReload wires the runtime's reload closure so config changes made from
// the TUI (model switch, provider connect, theme, /config …) rebuild the
// same services a dashboard save would. Call once after New; nil is fine
// (the TUI falls back to a config-only refresh and notes what a restart
// would apply).
func (m *Model) SetReload(fn func() error) { m.reload = fn }

// SetMCP wires the runtime's MCP manager so /mcp can list server state. Call
// once after New; nil is fine.
func (m *Model) SetMCP(mgr *mcp.Manager) { m.mcp = mgr }

// Run takes over the terminal until the user quits.
func (m *Model) Run(ctx context.Context) error {
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	m.greet()
	return tea.Batch(textarea.Blink, m.spin.Tick, m.welcomeTick())
}

// syncCursor hides the caret while the field is empty — so the placeholder has
// no reverse-video block — and shows a steady accent caret once the user types.
func (m *Model) syncCursor() {
	if strings.TrimSpace(m.ta.Value()) == "" {
		m.ta.Cursor.SetMode(cursor.CursorHide)
	} else {
		m.ta.Cursor.SetMode(cursor.CursorStatic)
	}
}

// welcomeTick drives the empty-state splash animation. It re-arms itself only
// while the splash is showing, so it costs nothing once a conversation starts.
func (m *Model) welcomeTick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return welcomeTickMsg{} })
}

// greet resets to the empty state; the animated welcomeView is what actually
// fills the viewport (see renderBlocks), so there is nothing to seed here.
func (m *Model) greet() {
	m.welcomeFrame = 0
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true
		m.refreshTranscript()
		return m, nil

	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case modelsFetchedMsg:
		m.mergeFetchedModels(msg.refs)
		return m, nil

	case welcomeTickMsg:
		if !m.ready || !m.showWelcome() {
			return m, nil // splash gone — stop animating
		}
		m.welcomeFrame++
		m.refreshTranscript()
		return m, m.welcomeTick()

	case evMsg:
		m.observe(msg.e) // the side column's counters
		m.applyEvent(msg.e)
		m.refreshTranscript()
		return m, m.listen()

	case doneMsg:
		m.busy = false
		m.cancel = nil
		m.closeStreaming()
		m.clearPauses()
		var next tea.Cmd
		if msg.err != nil && !strings.Contains(msg.err.Error(), "context canceled") {
			m.blocks = append(m.blocks, block{kind: blockError, text: msg.err.Error()})
		} else if msg.err == nil {
			// An autonomous goal drives the next turn itself; the terminal
			// is its host, so ask whether it wants one.
			next = m.goalCheckCmd()
		}
		m.refreshTranscript()
		return m, next

	case cmdResultMsg:
		quit, cmd := m.applyCommandResult(msg)
		m.refreshTranscript()
		if quit {
			return m, tea.Quit
		}
		return m, cmd

	case sessionLoadedMsg:
		m.adoptSession(msg)
		m.refreshTranscript()
		m.vp.GotoBottom()
		return m, nil

	case clipboardMsg:
		m.setStatus(msg.note)
		return m, nil

	case goalContinueMsg:
		cmd := m.continueGoal(msg)
		m.refreshTranscript()
		return m, cmd

	case attachedMsg:
		if msg.err != nil {
			m.pushSystem("Attach: " + msg.err.Error())
		} else {
			m.attachments = append(m.attachments, msg.att)
			m.setStatus("attached " + msg.att.name)
		}
		m.refreshTranscript()
		return m, nil

	case searchResultMsg:
		m.showSearchResults(msg)
		m.refreshTranscript()
		return m, nil

	case sessionDeletedMsg:
		if msg.err != nil {
			m.pushSystem("Delete failed: " + msg.err.Error())
			m.refreshTranscript()
			return m, nil
		}
		cmd := m.newSession()
		m.setStatus("deleted " + shortID(msg.id))
		m.refreshTranscript()
		return m, cmd

	case tea.KeyMsg:
		return m.onKey(msg)

	case tea.MouseMsg:
		if m.input.active {
			return m, nil // keyboard-only
		}
		if m.picker.active {
			m.picker.onMouse(m, msg)
			return m, m.modalCmd()
		}
		// The side column takes the wheel and clicks over it.
		if m.chromeMouse(msg) {
			return m, nil
		}
		// Mouse wheel scrolls the transcript; the input keeps focus.
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Forward anything else to the components.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Modals capture all keys while open.
	if m.input.active {
		return m, m.input.onKey(m, msg)
	}
	if m.picker.active {
		m.picker.onKey(m, msg)
		return m, m.modalCmd()
	}

	// A tool waiting for approval takes "y" / "n" / Esc on an empty
	// composer. Other keys pass through: the request stays queued (it times
	// out on its own) and typing is never swallowed.
	if len(m.approvals) > 0 && m.ta.Value() == "" {
		r := msg.Runes
		if len(r) == 1 && (r[0] == 'y' || r[0] == 'Y') {
			m.decideApproval(true)
			m.refreshTranscript()
			return m, nil
		}
		if (len(r) == 1 && (r[0] == 'n' || r[0] == 'N')) || msg.Type == tea.KeyEsc {
			m.decideApproval(false)
			m.refreshTranscript()
			return m, nil
		}
	}

	// A staged /delete works like the staged /undo below: "y" on an empty
	// composer commits, anything else cancels and still lands.
	if m.confirm != nil {
		c := m.confirm
		m.confirm = nil
		if r := msg.Runes; m.ta.Value() == "" && len(r) == 1 && (r[0] == 'y' || r[0] == 'Y') {
			cmd := c.onYes(m)
			m.refreshTranscript()
			return m, cmd
		}
		m.pushSystem(c.cancelled)
		m.refreshTranscript()
		if msg.Type == tea.KeyEsc {
			return m, nil
		}
	}

	// An open question: Esc on an empty composer puts it aside.
	if m.ask != nil && !m.ask.aside && msg.Type == tea.KeyEsc && m.ta.Value() == "" && len(m.palette) == 0 {
		m.putAskAside()
		m.refreshTranscript()
		return m, nil
	}

	// A staged /undo or /revert intercepts the next keystroke: "y" (with
	// an empty composer) commits, Esc discards, everything else discards
	// and falls through so the keystroke still lands normally. Placed
	// above the palette check so a stray key while a rollback is pending
	// never silently disappears into palette navigation.
	if m.pending != nil {
		if m.ta.Value() == "" {
			if r := msg.Runes; len(r) == 1 && (r[0] == 'y' || r[0] == 'Y') {
				m.commitPending()
				m.refreshTranscript()
				return m, nil
			}
			if msg.Type == tea.KeyEsc {
				m.cancelPending()
				m.refreshTranscript()
				return m, nil
			}
		}
		m.cancelPending()
		m.refreshTranscript()
		// fall through so the keystroke still routes as normal input
	}

	// The layout's own keys: Ctrl+B, F1–F4 / Alt+1–4, the QUESTION box.
	if m.chromeKey(msg) {
		return m, nil
	}

	// Palette navigation takes priority.
	if len(m.palette) > 0 {
		switch msg.Type {
		case tea.KeyUp, tea.KeyCtrlP:
			m.paletteSel = (m.paletteSel - 1 + len(m.palette)) % len(m.palette)
			return m, nil
		case tea.KeyDown, tea.KeyCtrlN:
			m.paletteSel = (m.paletteSel + 1) % len(m.palette)
			return m, nil
		case tea.KeyTab:
			m.acceptCompletion()
			m.refreshTranscript()
			return m, nil
		case tea.KeyEsc:
			m.palette = nil
			m.refreshTranscript()
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if m.busy {
			m.interrupt()
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyCtrlD:
		return m, tea.Quit

	case tea.KeyCtrlL:
		m.blocks = nil
		m.greet()
		m.refreshTranscript()
		return m, m.welcomeTick()

	case tea.KeyCtrlR:
		m.showReasoning = !m.showReasoning
		m.setStatus("reasoning " + onOff(m.showReasoning))
		m.refreshTranscript()
		return m, nil

	case tea.KeyCtrlT:
		m.openThemePicker()
		return m, nil

	case tea.KeyPgUp:
		m.vp.HalfViewUp()
		return m, nil
	case tea.KeyPgDown:
		m.vp.HalfViewDown()
		return m, nil

	case tea.KeyEnter:
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			if len(m.attachments) == 0 || m.busy || m.demo {
				return m, nil
			}
			// Attachments alone are a message, as in the web composer.
			return m, m.startTurn("")
		}
		if len(m.palette) > 0 {
			// Enter runs the highlighted command straight away — no second
			// Enter — unless it needs an argument, which it then waits for.
			c := m.palette[m.paletteSel]
			if strings.HasPrefix(c.Args, "<") {
				m.acceptCompletion()
				m.refreshTranscript()
				return m, nil
			}
			name := c.Name
			m.palette = nil
			m.ta.Reset()
			m.syncCursor()
			quit, cmd := m.runCommand("/" + name)
			m.refreshTranscript()
			if quit {
				return m, tea.Quit
			}
			return m, cmd
		}
		// Commands run even mid-turn — /stop, /steer and /answer are for
		// exactly then.
		if _, _, isCmd := commands.Parse(text); isCmd {
			m.ta.Reset()
			m.syncCursor()
			m.vp.GotoBottom()
			quit, cmd := m.runCommand(text)
			m.refreshTranscript()
			if quit {
				return m, tea.Quit
			}
			return m, cmd
		}
		if m.ask != nil && !m.ask.aside {
			m.ta.Reset()
			m.syncCursor()
			m.answerAsk(text)
			m.refreshTranscript()
			m.vp.GotoBottom()
			return m, nil
		}
		if m.busy {
			m.setStatus("still working — Ctrl+C to interrupt")
			return m, nil
		}
		m.ta.Reset()
		m.syncCursor()
		m.vp.GotoBottom()
		if m.demo {
			m.ta.Reset()
			m.demoReply(text)
			m.refreshTranscript()
			m.vp.GotoBottom()
			return m, nil
		}
		return m, m.startTurn(text)
	}

	// Several runes in one message are text that arrived together — fast
	// typing while a render was slow, or a paste without bracketed paste.
	// Insert them as text: handed to the textarea as a key, "up" or "left"
	// would match its cursor bindings by name and vanish.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Alt {
		m.ta.InsertString(string(msg.Runes))
		m.updatePalette()
		m.syncCursor()
		m.refreshTranscript()
		return m, nil
	}

	// Normal editing. Re-flow the transcript so the viewport resizes when the
	// palette opens or closes — the input and sidebar stay put.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.updatePalette()
	m.syncCursor()
	m.refreshTranscript()
	return m, cmd
}

// startTurn runs one agent turn, bridging its events onto the Bubble Tea loop.
// The turn carries the options the web composer sends: role, reasoning effort,
// the project folder (first turn only), and any attachments.
func (m *Model) startTurn(text string) tea.Cmd {
	req := m.turnRequest(text)
	shown := text
	if len(m.attachments) > 0 {
		names := make([]string, len(m.attachments))
		for i, a := range m.attachments {
			names[i] = a.name
		}
		shown = strings.TrimSpace(text + "\n[attached: " + strings.Join(names, ", ") + "]")
	}
	m.attachments = nil
	m.blocks = append(m.blocks, block{kind: blockUser, text: shown})

	ag := m.ag
	return m.runStream(func(ctx context.Context, emit agent.Emit) error {
		_, err := ag.Run(ctx, req, emit)
		return err
	})
}

// turnRequest builds the request for a message typed now.
func (m *Model) turnRequest(text string) agent.Request {
	message, images := buildMessage(text, m.attachments)
	req := agent.Request{
		SessionID:       m.sessionID,
		Message:         message,
		Images:          images,
		Platform:        "tui",
		Role:            m.role,
		ReasoningEffort: m.effort,
	}
	if m.sessionID == "" {
		// Only a new session takes a binding; an existing one carries its own.
		req.ProjectDir = m.projectDir
	}
	return req
}

// runStream runs fn — a turn, a compaction — on its own goroutine, bridging
// the events it emits onto the Bubble Tea loop.
func (m *Model) runStream(fn func(ctx context.Context, emit agent.Emit) error) tea.Cmd {
	m.busy = true
	m.refreshTranscript()

	runCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.msgCh = make(chan tea.Msg, 128)
	ch := m.msgCh

	go func() {
		err := fn(runCtx, func(e agent.Event) error {
			select {
			case ch <- evMsg{e}:
			case <-runCtx.Done():
				return runCtx.Err()
			}
			return nil
		})
		ch <- doneMsg{err: err}
	}()
	return tea.Batch(m.listen(), m.spin.Tick)
}

// listen pulls the next bridged agent message.
func (m *Model) listen() tea.Cmd {
	ch := m.msgCh
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		return <-ch
	}
}

func (m *Model) interrupt() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.sessionID != "" && m.ag != nil {
		m.ag.Interrupt(m.sessionID)
	}
	m.clearPauses()
	m.setStatus("interrupted")
}

// applyEvent folds one agent event into the transcript.
func (m *Model) applyEvent(e agent.Event) {
	switch e.Type {
	case agent.EventSession:
		if e.ID != "" && e.ID != m.sessionID {
			if m.sessionID == "" && m.role != "" {
				// A role picked before the first turn is remembered against
				// the new session, as the web does, so later turns keep it.
				m.persistRole(e.ID, m.role)
			}
			m.sessionID = e.ID
		}
		if e.Title != "" {
			m.title = e.Title
		}
	case agent.EventText:
		m.appendTo(blockAssistant, e.Delta)
	case agent.EventReasoning:
		m.appendTo(blockReasoning, e.Delta)
	case agent.EventToolCall:
		m.closeStreaming()
		m.blocks = append(m.blocks, block{kind: blockTool, title: toolHeadline(e.Name, e.Arguments), streaming: true, callID: e.ID})
	case agent.EventToolProgress:
		if i := m.toolBlock(e.ID); i >= 0 {
			m.blocks[i].text += e.Chunk
		} else {
			m.appendTo(blockTool, e.Chunk)
		}
	case agent.EventToolResult:
		if i := m.toolBlock(e.ID); i >= 0 {
			m.blocks[i].text = e.Content
			m.blocks[i].done = true
			m.blocks[i].isError = e.IsError
			m.blocks[i].streaming = false
		}
	case agent.EventAsk:
		m.closeStreaming()
		if a := askFromEvent(e); a != nil {
			m.ask = a
			m.pushSystem(askPrompt(a))
			m.setStatus("waiting for your answer")
		}
	case agent.EventApproval:
		m.closeStreaming()
		m.queueApproval(approvalFromEvent(e))
	case agent.EventNotice:
		m.blocks = append(m.blocks, block{kind: blockNotice, text: e.Message})
	case agent.EventReset:
		// A retry after a provider glitch: drop the partial reply we streamed so
		// the retry does not stack on top of it.
		for len(m.blocks) > 0 && m.blocks[len(m.blocks)-1].streaming {
			m.blocks = m.blocks[:len(m.blocks)-1]
		}
	case agent.EventError:
		m.blocks = append(m.blocks, block{kind: blockError, text: e.Err})
	case agent.EventUsage:
		m.tokensIn, m.tokensOut = e.InputTokens, e.OutputTokens
		if e.ContextTokens > 0 {
			m.ctxUsed = e.ContextTokens
		}
		if e.ContextWindow > 0 {
			m.ctxWindow = e.ContextWindow
		}
	}
}

// toolBlock finds the unfinished tool block for a call. Tools in one batch
// run in parallel and report in any order, so the call id decides; a result
// without an id falls back to the newest unfinished tool block.
func (m *Model) toolBlock(callID string) int {
	fallback := -1
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := m.blocks[i]
		if b.kind != blockTool || b.done {
			continue
		}
		if callID != "" && b.callID == callID {
			return i
		}
		if fallback < 0 {
			fallback = i
		}
	}
	if callID != "" {
		for _, b := range m.blocks {
			if b.kind == blockTool && b.callID == callID {
				return -1 // already finished; a late duplicate changes nothing
			}
		}
	}
	return fallback
}

func (m *Model) appendTo(kind blockKind, text string) {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == kind && m.blocks[n-1].streaming {
		m.blocks[n-1].text += text
		return
	}
	m.blocks = append(m.blocks, block{kind: kind, text: text, streaming: true})
}

func (m *Model) closeStreaming() {
	for i := range m.blocks {
		m.blocks[i].streaming = false
	}
}

func (m *Model) setStatus(s string) { m.status = s }

// applyTheme switches the active theme live (styles, caret) without
// persisting it — used for both the /theme command and modal previews.
func (m *Model) applyTheme(name string) {
	if _, ok := themes[name]; !ok {
		return
	}
	m.themeName = name
	m.st = newStyles(themeByName(name))
	m.cache = nil
	m.ta.Cursor.Style = lipgloss.NewStyle().Foreground(themeByName(name).Accent)
}

// persistTheme writes the chosen theme back to config so it survives restarts.
func (m *Model) persistTheme(name string) {
	if m.cfg == nil {
		return
	}
	m.cfg.Display.Theme = name
	m.saveConfig()
}

// reloadConfig re-reads config from disk so the TUI reflects changes made
// elsewhere (the dashboard, the CLI, a hand-edited file) without a restart.
// When SetReload is wired, delegate to the runtime so the same services a
// dashboard save rebuilds (shell, RAG, skills, plugins, roles, gateway,
// reconciled MCP) come up on the desired config. The agent gets a distinct
// pointer either way, so a subsequent TUI mutation of m.cfg cannot race the
// running services.
func (m *Model) reloadConfig() {
	if m.ag == nil {
		return // no live runtime (tests / demo) — keep the in-memory config
	}
	if m.reload != nil {
		if err := m.reload(); err != nil {
			m.setStatus("reload failed: " + err.Error())
			return
		}
		// After rt.reload the desired config is on disk and the agent holds
		// the effective view; hand the TUI its own desired clone.
		m.cfg = config.Get()
		return
	}
	fresh, err := config.Reload()
	if err != nil || fresh == nil {
		return
	}
	m.cfg = fresh
	// Hand the agent an effective clone so it keeps its boot-time
	// restart-required values; a later TUI mutation of m.cfg must not race
	// the running services.
	effective, _ := config.Effective(m.ag.Config(), fresh)
	m.ag.SetConfig(effective)
}

// saveConfig writes the desired config to disk, then rebuilds runtime
// services. When SetReload is wired the runtime owns the whole reconcile
// (shell, RAG, skills, plugins, roles, gateway, reconciled MCP) — reloading
// picks up the just-saved desired. Without SetReload we fall back to handing
// the agent an effective clone so restart-required fields survive; the user
// sees which ones needed a restart to fully apply.
func (m *Model) saveConfig() {
	if m.cfg == nil {
		return
	}
	if err := config.Save(m.cfg); err != nil {
		m.setStatus("save failed: " + err.Error())
		return
	}
	if m.reload != nil {
		if err := m.reload(); err != nil {
			m.setStatus("reload failed: " + err.Error())
			return
		}
		// Keep m.cfg as the desired clone so the next mutation edits a
		// TUI-owned pointer, not the agent's live view.
		m.cfg = config.Get()
		return
	}
	if m.ag != nil {
		effective, pending := config.Effective(m.ag.Config(), m.cfg)
		m.ag.SetConfig(effective)
		if len(pending) > 0 {
			m.setStatus("saved; restart to apply: " + strings.Join(pending, ", "))
		}
	}
}

// modalCmd keeps the input modal's caret blinking while it is open, and hands
// back any command a picker commit queued.
func (m *Model) modalCmd() tea.Cmd {
	var cmds []tea.Cmd
	if m.after != nil {
		cmds = append(cmds, m.after)
		m.after = nil
	}
	if m.input.active {
		cmds = append(cmds, textinput.Blink)
	}
	return tea.Batch(cmds...)
}

// persistRole stores a session's role off the update loop.
func (m *Model) persistRole(sessionID, role string) {
	db := m.db
	if db == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.SetKV(ctx, "role:"+sessionID, role)
	}()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
