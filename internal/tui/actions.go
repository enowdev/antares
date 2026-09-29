// actions.go carries out what a registry command asks of the terminal. The
// registry answers with markdown for the transcript and, for the things only a
// surface can do — start over, switch session, copy, quit — an Action. The web
// chat translates the same actions in ChatPage.tsx's runCommand; this is the
// terminal's translation.

package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// Messages produced by the background work actions start.
type (
	// sessionLoadedMsg is a session's transcript and per-session state, read
	// off the update loop.
	sessionLoadedMsg struct {
		id         string
		title      string
		role       string
		projectDir string
		msgs       []store.Message
		err        error
		// status is shown once the load lands ("resumed …").
		status string
	}
	// clipboardMsg reports how a copy went.
	clipboardMsg struct{ note string }
	// goalContinueMsg says whether an autonomous goal wants another turn.
	goalContinueMsg struct {
		sessionID string
		goal      string
		ok        bool
	}
)

// applyCommandResult shows a registry command's output and carries out its
// action.
func (m *Model) applyCommandResult(msg cmdResultMsg) (quit bool, cmd tea.Cmd) {
	m.setStatus("")
	if msg.err != nil {
		m.pushSystem(msg.err.Error())
		return false, nil
	}
	out := msg.res.Output
	if msg.name == "status" {
		// The registry reports the runtime; the terminal adds what only it
		// knows about the conversation on screen.
		out = strings.TrimRight(out, "\n") + "\n" + m.sessionStatus()
	}
	if strings.TrimSpace(out) == "" {
		return m.applyAction(msg.res.Action)
	}
	switch msg.res.Action.Kind {
	case "resume", "session-changed":
		// These reload the transcript from the store, which would wipe the
		// output ("Forked into …", "Renamed to …"); it is shown after.
		m.carry = out
		quit, cmd := m.applyAction(msg.res.Action)
		if cmd == nil {
			m.pushCarry() // nothing is reloading after all
		}
		return quit, cmd
	}
	m.pushOutput(out)
	return m.applyAction(msg.res.Action)
}

// pushCarry shows command output held back across a transcript reload.
func (m *Model) pushCarry() {
	if m.carry != "" {
		m.pushOutput(m.carry)
		m.carry = ""
	}
}

// applyAction maps one action kind onto the Model. An unknown kind is ignored:
// a newer registry may name intents this build does not know yet.
func (m *Model) applyAction(a commands.Action) (quit bool, cmd tea.Cmd) {
	switch a.Kind {
	case "new":
		return false, m.newSession()
	case "clear":
		// Clearing the screen keeps the session, like Ctrl+L. /new starts over.
		m.blocks = nil
		m.greet()
		return false, m.welcomeTick()
	case "quit":
		return true, nil
	case "stop":
		if m.busy {
			m.interrupt()
		} else {
			m.setStatus("nothing running")
		}
	case "retry":
		last := m.lastUserText()
		if last == "" {
			m.pushSystem("Nothing to resend yet.")
			return false, nil
		}
		if m.busy {
			m.setStatus("still working — /stop first")
			return false, nil
		}
		return false, m.startTurn(last)
	case "resume":
		if a.Value == "" {
			return false, nil
		}
		if m.busy {
			m.setStatus("still working — /stop first")
			return false, nil
		}
		return false, m.loadSessionCmd(a.Value, "resumed "+shortID(a.Value))
	case "copy":
		text := m.lastAssistantText()
		if text == "" {
			m.pushSystem("Nothing to copy yet.")
			return false, nil
		}
		return false, copyToClipboard(text)
	case "setup":
		m.pushSystem("Setup runs outside the chat: quit, then run `antares setup`.")
	case "compact":
		if m.busy {
			m.setStatus("still working — /stop first")
			return false, nil
		}
		return false, m.startCompact()
	case "session-changed":
		if m.sessionID != "" && !m.busy {
			return false, m.loadSessionCmd(m.sessionID, "")
		}
	case "config-changed":
		m.adoptConfig()
	case "skills-changed":
		m.setStatus("skills updated")
	case "role-changed":
		m.role = a.Value
		if a.Value == "" {
			m.setStatus("role cleared")
		} else {
			m.setStatus("role → " + a.Value)
		}
	case "goal_autostart":
		if m.busy {
			// The running turn ends in the goal check, which picks it up.
			return false, nil
		}
		return false, m.goalCheckCmd()
	}
	return false, nil
}

// newSession forgets the conversation and every per-session option, leaving a
// fresh composer.
func (m *Model) newSession() tea.Cmd {
	if m.busy {
		m.interrupt()
	}
	m.sessionID = ""
	m.title = ""
	m.role = ""
	m.projectDir = ""
	m.attachments = nil
	m.ask = nil
	m.approvals = nil
	m.tokensIn, m.tokensOut = 0, 0
	m.ctxUsed, m.ctxWindow = 0, 0
	m.blocks = nil
	m.greet()
	m.setStatus("new session")
	return m.welcomeTick()
}

// adoptConfig picks up a config the registry already saved. When the runtime
// reload is wired the registry ran it, so only the TUI's desired view needs
// refreshing; otherwise fall back to the config-only refresh.
func (m *Model) adoptConfig() {
	if m.reload != nil {
		m.cfg = config.Get()
		return
	}
	m.reloadConfig()
}

// sessionStatus is the conversation half of /status.
func (m *Model) sessionStatus() string {
	var b strings.Builder
	b.WriteString("\n**This conversation**\n\n")
	sess := m.sessionID
	if sess == "" {
		sess = "(new)"
	}
	fmt.Fprintf(&b, "- Session: `%s`\n", sess)
	fmt.Fprintf(&b, "- Role: %s\n", firstNon(m.role, "general assistant"))
	fmt.Fprintf(&b, "- Reasoning effort: %s\n", firstNon(m.effort, "auto"))
	if m.projectDir != "" {
		fmt.Fprintf(&b, "- Project: `%s`\n", m.projectDir)
	}
	if m.ctxWindow > 0 {
		fmt.Fprintf(&b, "- Context: %d / %d\n", m.ctxUsed, m.ctxWindow)
	}
	fmt.Fprintf(&b, "- Tokens: %d in / %d out\n", m.tokensIn, m.tokensOut)
	return b.String()
}

func (m *Model) lastUserText() string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockUser {
			return m.blocks[i].text
		}
	}
	return ""
}

func (m *Model) lastAssistantText() string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockAssistant && strings.TrimSpace(m.blocks[i].text) != "" {
			return m.blocks[i].text
		}
	}
	return ""
}

// ---- sessions ------------------------------------------------------------------

// loadSessionCmd reads a session's transcript, stored role, and project binding
// off the update loop.
func (m *Model) loadSessionCmd(id, status string) tea.Cmd {
	db := m.db
	if db == nil {
		m.pushSystem("No store available.")
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out := sessionLoadedMsg{id: id, status: status}
		sess, err := db.GetSession(ctx, id)
		if err != nil {
			out.err = err
			return out
		}
		if sess != nil {
			out.title = sess.Title
			out.projectDir, _ = sess.Meta["project_dir"].(string)
		}
		out.role, _ = db.GetKV(ctx, "role:"+id)
		out.msgs, out.err = db.ListMessages(ctx, id, 0, 0)
		return out
	}
}

// adoptSession swaps the transcript for a loaded session.
func (m *Model) adoptSession(msg sessionLoadedMsg) {
	if msg.err != nil {
		m.pushCarry()
		m.pushSystem("Could not load the session: " + msg.err.Error())
		return
	}
	if msg.id != m.sessionID {
		// A different conversation: its turn options are its own.
		m.tokensIn, m.tokensOut = 0, 0
		m.ctxUsed, m.ctxWindow = 0, 0
		m.attachments = nil
	}
	m.sessionID = msg.id
	m.title = msg.title
	m.role = msg.role
	m.projectDir = msg.projectDir
	m.blocks = blocksFromMessages(msg.msgs, m.showReasoning)
	m.pushCarry()
	if msg.status != "" {
		m.setStatus(msg.status)
	}
}

// blocksFromMessages rebuilds the visible transcript from the message log.
// Hidden messages — context the agent injected for itself — stay hidden, as
// they do in the web chat.
func blocksFromMessages(msgs []store.Message, showReasoning bool) []block {
	out := make([]block, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Hidden {
			continue
		}
		switch msg.Role {
		case store.RoleUser:
			out = append(out, block{kind: blockUser, text: msg.Content})
		case store.RoleAssistant:
			if showReasoning && strings.TrimSpace(msg.Reasoning) != "" {
				out = append(out, block{kind: blockReasoning, text: msg.Reasoning, done: true})
			}
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, block{kind: blockAssistant, text: msg.Content, done: true})
			}
		case store.RoleTool:
			out = append(out, block{kind: blockTool, title: msg.ToolName, text: msg.Content, done: true})
		}
	}
	return out
}

// ---- compact and goals -----------------------------------------------------------

// startCompact summarises the session now, streaming its progress like a turn.
func (m *Model) startCompact() tea.Cmd {
	if m.ag == nil || m.sessionID == "" {
		m.pushSystem("There is no session to compact yet.")
		return nil
	}
	sess := m.sessionID
	ag := m.ag
	return m.runStream(func(ctx context.Context, emit agent.Emit) error {
		return ag.CompactNow(ctx, sess, emit)
	})
}

// goalCheckCmd asks whether the session's autonomous goal wants another turn.
// The check reads the store, so it runs off the update loop.
func (m *Model) goalCheckCmd() tea.Cmd {
	ag, sess := m.ag, m.sessionID
	if ag == nil || sess == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !ag.ShouldAutoContinueGoal(ctx, sess) {
			return goalContinueMsg{sessionID: sess}
		}
		g, _ := ag.GetGoal(ctx, sess)
		text := ""
		if g != nil {
			text = g.Text
		}
		return goalContinueMsg{sessionID: sess, goal: text, ok: true}
	}
}

// continueGoal starts the next autonomous iteration. The terminal is the host
// here, doing what a gateway's turn-end driver does: it feeds the goal back as
// hidden context so the transcript shows the agent carrying on, not a message
// the user never typed.
func (m *Model) continueGoal(msg goalContinueMsg) tea.Cmd {
	if !msg.ok || msg.sessionID != m.sessionID || m.busy || m.ag == nil {
		return nil
	}
	note := "Continue working on the standing goal"
	if strings.TrimSpace(msg.goal) != "" {
		note += ": " + msg.goal
	}
	note += "\n\nTake the next concrete step; stop when it is met or only the user can unblock it."
	m.pushSystem("Continuing the autonomous goal… (/goal pause to hold it, /stop to interrupt)")
	req := agent.Request{SessionID: m.sessionID, ContextInject: note, Platform: "tui"}
	ag := m.ag
	return m.runStream(func(ctx context.Context, emit agent.Emit) error {
		_, err := ag.Run(ctx, req, emit)
		return err
	})
}

// ---- clipboard ---------------------------------------------------------------------

// copyToClipboard writes text through OSC 52, which reaches the local clipboard
// even over SSH when the terminal allows it, and also through the platform's
// clipboard tool when one is installed, since not every terminal honours
// OSC 52. Both run off the update loop.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		_, _ = fmt.Fprint(os.Stdout, osc52(text))
		if tool := clipboardTool(); tool != nil {
			cmd := exec.Command(tool[0], tool[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err == nil {
				return clipboardMsg{note: "copied the last reply"}
			}
		}
		// OSC 52 cannot report back, so say what was tried.
		return clipboardMsg{note: "sent the last reply to the terminal clipboard (OSC 52)"}
	}
}

// osc52 is the escape sequence that asks the terminal to set the clipboard.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// clipboardTool returns the first clipboard writer found on PATH, or nil.
func clipboardTool() []string {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip"}}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		candidates = append(candidates, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
}
