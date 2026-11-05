package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/enowdev/antares/internal/checkpoint"
	"github.com/enowdev/antares/internal/config"
)

// TestPreviewText covers the picker-label helper: newline flattening, ellipsis
// at maxLen, and the "(empty message)" placeholder that the picker shows when
// the last user turn was image-only or accidentally blank. Kept as a small
// unit test because the /revert picker's readability depends on it — a broken
// previewText silently ships unreadable rows.
func TestPreviewText(t *testing.T) {
	cases := []struct {
		name, in, want string
		max            int
	}{
		{"empty", "", "(empty message)", 40},
		{"whitespace only", "   \n\t  ", "(empty message)", 40},
		{"fits", "hello world", "hello world", 40},
		{"newlines collapse", "line one\nline two\n\nline three", "line one line two line three", 60},
		{"ellipsis", strings.Repeat("x", 100), strings.Repeat("x", 9) + "…", 10},
	}
	for _, tc := range cases {
		got := previewText(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("%s: previewText(%q, %d) = %q, want %q", tc.name, tc.in, tc.max, got, tc.want)
		}
	}
}

// TestPendingConfirmMessage checks the confirmation block's exit points:
//  1. no pending → empty string (defensive; onKey should never reach here)
//  2. pending with zero changes → "only chat history will be trimmed" copy
//  3. pending with mixed changes → counts revertable / external / deletes,
//     shows up to a handful of paths, and always ends on the "y to confirm"
//     line so the user always sees the escape hatch.
//
// A regression here would ship a confusing prompt to real users; the block
// is the entire UX of the feature, so it earns a test.
func TestPendingConfirmMessage(t *testing.T) {
	cfg := &config.Config{}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)

	if got := m.pendingConfirmMessage(); got != "" {
		t.Fatalf("no pending should render empty, got %q", got)
	}

	m.pending = &pendingRevert{marker: "abc", label: "the last turn"}
	got := m.pendingConfirmMessage()
	if !strings.Contains(got, "the last turn") {
		t.Errorf("expected label in output, got %q", got)
	}
	if !strings.Contains(got, "only the chat history will be trimmed") {
		t.Errorf("zero-change branch missing, got %q", got)
	}
	if !strings.HasSuffix(got, "any other key to cancel.") {
		t.Errorf("prompt must always end with the cancel hint, got %q", got)
	}

	m.pending = &pendingRevert{
		marker: "abc",
		label:  "5 turns ago",
		changes: []checkpoint.ChangedSince{
			{Path: "a.go"},
			{Path: "b.go", WillDelete: true},
			{Path: "c.go", ExternallyChanged: true},
			{Path: "d.go"},
		},
	}
	got = m.pendingConfirmMessage()
	if !strings.Contains(got, "3 file(s) to restore") {
		t.Errorf("expected 3 revertable, got %q", got)
	}
	if !strings.Contains(got, "1 created files will be removed") {
		t.Errorf("expected delete count, got %q", got)
	}
	if !strings.Contains(got, "1 file(s) edited outside") {
		t.Errorf("expected external count, got %q", got)
	}
	for _, want := range []string{"[revert]", "[delete]", "[skip  ]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing tag %q in %q", want, got)
		}
	}
}

// TestCancelPendingClearsState verifies the exit path used on any keystroke
// other than "y"/Esc after a stage: pending drops back to nil so the next
// /undo starts fresh, and a system block records the cancellation so the
// operator sees why nothing happened. Cheap guard against a future refactor
// forgetting to null out the pointer.
func TestCancelPendingClearsState(t *testing.T) {
	cfg := &config.Config{}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.pending = &pendingRevert{marker: "abc", label: "last turn"}
	before := len(m.blocks)
	m.cancelPending()
	if m.pending != nil {
		t.Errorf("cancelPending must clear m.pending, got %+v", m.pending)
	}
	if len(m.blocks) != before+1 {
		t.Errorf("expected one system block after cancel, got %d new", len(m.blocks)-before)
	}
	// Idempotent: calling again on a cleared state must not append another
	// noise block or panic.
	m.cancelPending()
	if len(m.blocks) != before+1 {
		t.Errorf("second cancel should be a no-op, got %d new blocks", len(m.blocks)-before)
	}
}

// TestUndoRevertCommandsRegistered guards against a future refactor dropping
// the /undo or /revert entry from the command palette. The feature is
// otherwise gated only by these two rows, and a missing row would silently
// ship a TUI that has no way to reach the code below.
func TestUndoRevertCommandsRegistered(t *testing.T) {
	for _, name := range []string{"undo", "revert"} {
		if !commandExists(name) {
			t.Errorf("expected /%s to be registered in the command palette", name)
		}
	}
}
