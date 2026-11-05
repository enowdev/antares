package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

// TestWelcomeRender draws the home screen mid-opening at a small size: exactly
// the window's rows, none wider than it.
func TestWelcomeRender(t *testing.T) {
	m := sized(96, 26)
	m.blocks, m.title, m.sessionID = nil, "", ""
	m.welcomeFrame = 4
	plain := stripANSITest(m.View())
	lines := strings.Split(plain, "\n")
	if len(lines) != 26 {
		t.Fatalf("want 26 rows, got %d", len(lines))
	}
	for i, ln := range lines {
		if w := len([]rune(ln)); w > 96 {
			t.Fatalf("row %d width %d exceeds 96", i, w)
		}
	}
}

// TestCommandOutputShowsOnEmptyTranscript guards the bug where a command's
// system reply (e.g. /help) on an empty chat was hidden behind the welcome splash.
func TestCommandOutputShowsOnEmptyTranscript(t *testing.T) {
	m := &Model{themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 20)
	if !m.showWelcome() {
		t.Fatal("empty transcript should show the welcome")
	}
	m.pushSystem("hello from /help")
	if m.showWelcome() {
		t.Fatal("a pushed system block must replace the welcome")
	}
	out := stripANSITest(m.renderBlocks())
	if !strings.Contains(out, "hello from /help") {
		t.Fatalf("system block not rendered: %q", out)
	}
	if strings.Contains(out, "ANTARES") {
		t.Fatal("welcome banner still shown behind command output")
	}
}

func stripANSITest(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
