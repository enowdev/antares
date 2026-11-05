// undo.go wires the TUI's /undo and /revert commands.
//
// The primitives already exist for the dashboard's "edit message" flow:
//   - agent.PreviewChangesSince(sessionID, marker) → []checkpoint.ChangedSince
//   - agent.RollbackSince(sessionID, marker, skipExternal) → RestoreResult
//   - store.DeleteMessagesFrom(ctx, sessionID, fromID)
//
// Every user message is a checkpoint marker for its turn, so a "marker" is
// just a message id. The dashboard hits these through HTTP; the TUI holds
// the same handles directly on the Model, so no server round-trip is needed.
//
// UX intentionally avoids a modal: /undo pushes an inline system block
// summarising the impact, sets Model.pending, and waits for the next key.
// "y" (with an empty composer) commits, Esc discards, anything else also
// discards and falls through so the keystroke still lands normally. This
// keeps the composer keyboard flow — no modal to dismiss, no separate
// confirm widget — and matches the fragment-heavy terminal aesthetic the
// rest of the TUI already uses.
//
// /revert opens the existing modal picker with the session's recent user
// messages, then feeds the chosen id through the same commit path as /undo.

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/checkpoint"
)

// pendingRevert is a staged rollback waiting for user confirmation. Nil means
// no operation is pending; a pointer keeps the check ergonomic on the hot
// key-handling path (nil vs zero-valued struct).
type pendingRevert struct {
	// marker is the user-message id we roll back to; every message at or
	// after this id will be dropped from the session log, and every file
	// the agent recorded a snapshot for at/after this marker is restored
	// to its state just before the marker fired.
	marker string
	// label is a short human string ("the last turn", "5 turns ago") used
	// in the confirmation block and the post-op notice.
	label string
	// changes is the preview from PreviewChangesSince, cached at build
	// time so the commit block does not fire a second checkpoint scan.
	changes []checkpoint.ChangedSince
}

// cmdUndo stages a rollback of the most recent user turn. The last user
// message is always unambiguous — this is the omp-style /undo, one shot,
// no picker.
func (m *Model) cmdUndo(string) (bool, tea.Cmd) {
	if m.sessionID == "" {
		m.pushSystem("No session yet — /undo needs a turn to undo.")
		return false, nil
	}
	if m.ag == nil || m.db == nil {
		m.pushSystem("Undo unavailable in this build.")
		return false, nil
	}
	marker, err := m.lastUserMessageID()
	if err != nil {
		m.pushSystem("Undo: " + err.Error())
		return false, nil
	}
	if marker == "" {
		m.pushSystem("No user message to undo.")
		return false, nil
	}
	return m.stageRevert(marker, "the last turn")
}

// cmdRevert opens a picker over recent user messages, so the operator can
// revert to any earlier point in the conversation, not just the last turn.
// The picker reuses the existing modal widget that /model and /theme use —
// one keyboard idiom for every selection.
func (m *Model) cmdRevert(args string) (bool, tea.Cmd) {
	if m.sessionID == "" {
		m.pushSystem("No session yet — /revert needs a turn to revert.")
		return false, nil
	}
	if m.ag == nil || m.db == nil {
		m.pushSystem("Revert unavailable in this build.")
		return false, nil
	}
	// Args form: /revert                → picker
	//            /revert <message-id>   → skip picker, stage that marker
	// The id form lets scripts and copy/paste from the dashboard work
	// without a second interactive step.
	if id := strings.TrimSpace(args); id != "" {
		// Validate before staging. An id that is not a user message in this
		// session previews as "no file changes" (PreviewSince matches nothing
		// and reports no error), so without this check the operator would be
		// asked to confirm a rollback that silently does nothing to the files
		// and then fails on the message trim.
		ok, err := m.isUserMessage(id)
		if err != nil {
			m.pushSystem("Revert: " + err.Error())
			return false, nil
		}
		if !ok {
			m.pushSystem("No user message " + shortID(id) + " in this session. Run /revert with no argument to pick one.")
			return false, nil
		}
		return m.stageRevert(id, "message "+shortID(id))
	}
	items, err := m.collectRevertTargets()
	if err != nil {
		m.pushSystem("Revert: " + err.Error())
		return false, nil
	}
	if len(items) == 0 {
		m.pushSystem("No user messages in this session yet.")
		return false, nil
	}
	m.openPicker(picker{
		title:  "Revert to which message?",
		hint:   "everything after is dropped; files are restored",
		footer: "type to filter · ↑↓ move · Enter select · Esc cancel",
		items:  items,
		commit: func(m *Model, it pickerItem) {
			// stageRevert returns (quit, cmd); the picker commit path has
			// nowhere to route them, so we ignore both. In practice
			// stageRevert returns (false, nil) — it only queues a system
			// block and mutates m.pending.
			_, _ = m.stageRevert(it.id, it.label)
		},
	})
	return false, nil
}

// stageRevert previews the changes for a marker and, if there is anything
// to do, records the operation as pending and pushes a confirmation block.
// Called by both /undo and /revert with the marker they resolved.
func (m *Model) stageRevert(marker, label string) (bool, tea.Cmd) {
	changes, err := m.ag.PreviewChangesSince(m.sessionID, marker)
	if err != nil {
		m.pushSystem("Preview failed: " + err.Error())
		return false, nil
	}

	m.pending = &pendingRevert{marker: marker, label: label, changes: changes}
	m.pushSystem(m.pendingConfirmMessage())
	return false, nil
}

// pendingConfirmMessage renders the summary shown while waiting for the
// user to confirm. Split from stageRevert so the same string can be re-shown
// after a cancelled key press without re-running the checkpoint scan.
func (m *Model) pendingConfirmMessage() string {
	if m.pending == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Revert to %s?\n", m.pending.label)

	if len(m.pending.changes) == 0 {
		b.WriteString("  No file changes to revert — only the chat history will be trimmed.\n")
	} else {
		revertable := 0
		external := 0
		deletes := 0
		for _, c := range m.pending.changes {
			if c.ExternallyChanged {
				external++
				continue
			}
			revertable++
			if c.WillDelete {
				deletes++
			}
		}
		fmt.Fprintf(&b, "  %d file(s) to restore", revertable)
		if deletes > 0 {
			fmt.Fprintf(&b, " (%d created files will be removed)", deletes)
		}
		b.WriteString(".\n")
		if external > 0 {
			fmt.Fprintf(&b, "  %d file(s) edited outside this session will be skipped.\n", external)
		}
		// Show up to a handful of paths so the operator can spot-check.
		shown := 0
		for _, c := range m.pending.changes {
			if shown >= 6 {
				fmt.Fprintf(&b, "  … and %d more\n", len(m.pending.changes)-shown)
				break
			}
			tag := "revert"
			switch {
			case c.ExternallyChanged:
				tag = "skip  "
			case c.WillDelete:
				tag = "delete"
			}
			fmt.Fprintf(&b, "  [%s] %s\n", tag, c.Path)
			shown++
		}
	}
	b.WriteString("Press y to confirm, any other key to cancel.")
	return b.String()
}

// commitPending runs the staged rollback: restore files, drop the tail of
// the message log, and clear any persisted context summary (its through_seq
// now points past deleted rows). Fires only when the user typed `y` on an
// empty composer after a stage.
func (m *Model) commitPending() {
	if m.pending == nil {
		return
	}
	p := m.pending
	m.pending = nil

	// Files first: if the file restore fails we surface an error and stop,
	// leaving the message log intact. A half-done state where messages are
	// dropped but files are still at their post-turn contents is worse
	// than "nothing happened, try again".
	// skipExternal files are the ones the confirm block warned about. They are
	// counted from the staged preview, not from the result: RestoreSince drops
	// them with a bare `continue` and never records them in Failed, so
	// len(res.Failed) would report 0 for exactly the case worth reporting.
	skipped := 0
	for _, c := range p.changes {
		if c.ExternallyChanged {
			skipped++
		}
	}
	var restored, deleted, failed int
	res, err := m.ag.RollbackSince(m.sessionID, p.marker, true)
	if err != nil {
		m.pushSystem("Rollback failed: " + err.Error())
		return
	}
	if res != nil {
		restored = len(res.Restored)
		deleted = len(res.Deleted)
		failed = len(res.Failed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.db.DeleteMessagesFrom(ctx, m.sessionID, p.marker); err != nil {
		m.pushSystem("Rollback partial: files restored, but trimming messages failed: " + err.Error())
		return
	}

	// Drop the persisted context summary — its through_seq points past rows
	// that no longer exist, so a next turn would either replay the stale
	// summary or hide live messages behind it. The dashboard does the same
	// thing in handleEditMessage.
	if sess, err := m.db.GetSession(ctx, m.sessionID); err == nil && sess.Meta != nil {
		if _, ok := sess.Meta["context_compact"]; ok {
			delete(sess.Meta, "context_compact")
			_ = m.db.UpdateSession(ctx, sess)
		}
	}

	// Rehydrate the transcript so the composer reflects the trimmed state.
	// The message log is authoritative — clear blocks and re-render from
	// storage, otherwise the user still sees the assistant reply we just
	// deleted server-side.
	m.reloadTranscript(ctx)

	var summary strings.Builder
	fmt.Fprintf(&summary, "Reverted %s.", p.label)
	if restored+deleted > 0 {
		fmt.Fprintf(&summary, " %d file(s) restored", restored)
		if deleted > 0 {
			fmt.Fprintf(&summary, ", %d removed", deleted)
		}
		summary.WriteString(".")
	}
	if skipped > 0 {
		fmt.Fprintf(&summary, " %d skipped (edited outside the session).", skipped)
	}
	if failed > 0 {
		fmt.Fprintf(&summary, " %d could not be restored.", failed)
	}
	m.pushSystem(summary.String())
}

// cancelPending discards a staged rollback without touching files or
// messages. Called when the operator types anything other than `y` after
// staging, or hits Esc.
func (m *Model) cancelPending() {
	if m.pending == nil {
		return
	}
	m.pending = nil
	m.pushSystem("Revert cancelled.")
}

// lastUserMessageID returns the id of the most recent user message in the
// current session, or "" if there is none. Used by /undo.
func (m *Model) lastUserMessageID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msgs, err := m.db.ListMessages(ctx, m.sessionID, 0, 0)
	if err != nil {
		return "", err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].ID, nil
		}
	}
	return "", nil
}

// isUserMessage reports whether id names a user message in the current
// session. Guards the `/revert <message-id>` form: a marker the checkpoint
// store has never seen is indistinguishable from "this turn changed no
// files", so it has to be rejected before anything is staged.
func (m *Model) isUserMessage(id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msgs, err := m.db.ListMessages(ctx, m.sessionID, 0, 0)
	if err != nil {
		return false, err
	}
	for _, msg := range msgs {
		if msg.ID == id && msg.Role == "user" {
			return true, nil
		}
	}
	return false, nil
}

// collectRevertTargets builds the picker items for /revert: every user
// message in the session, newest first, with a short label showing the
// text so the operator can pick by content rather than by opaque id.
func (m *Model) collectRevertTargets() ([]pickerItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msgs, err := m.db.ListMessages(ctx, m.sessionID, 0, 0)
	if err != nil {
		return nil, err
	}
	var items []pickerItem
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg.Role != "user" {
			continue
		}
		seen++
		label := previewText(msg.Content, 60)
		right := ""
		if seen == 1 {
			right = "latest"
		} else {
			right = fmt.Sprintf("%d turns ago", seen-1)
		}
		items = append(items, pickerItem{
			id:    msg.ID,
			label: label,
			right: right,
		})
	}
	return items, nil
}

// reloadTranscript re-reads the message log and rebuilds the visible blocks.
// Called after a rollback so the composer no longer shows deleted turns.
func (m *Model) reloadTranscript(ctx context.Context) {
	msgs, err := m.db.ListMessages(ctx, m.sessionID, 0, 0)
	if err != nil {
		return
	}
	m.blocks = blocksFromMessages(msgs, m.showReasoning)
	m.refreshTranscript()
}

// previewText returns a single-line summary of a message, elided at maxLen.
// Newlines become spaces so the picker row stays a clean single line.
func previewText(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > maxLen {
		return string([]rune(s)[:maxLen-1]) + "…"
	}
	if s == "" {
		return "(empty message)"
	}
	return s
}
