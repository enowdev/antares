package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/enowdev/antares/internal/agent"
)

// sized is the demo model laid out at w × h, in the chat layout.
func sized(w, h int) *Model {
	m := NewDemo()
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	return m
}

// screen is the model's view as plain rows.
func screen(m *Model) []string {
	return strings.Split(ansi.Strip(m.View()), "\n")
}

// cell is the character in column x, counted in cells (a wide character
// takes two).
func cell(row string, x int) string {
	return ansi.Cut(row, x, x+1)
}

// TestGridBreakpoints pins where the side column goes and how wide it is.
func TestGridBreakpoints(t *testing.T) {
	cases := []struct {
		w, h    int
		sideW   int
		session bool
	}{
		{90, 40, 0, false},
		{99, 40, 0, false},
		{100, 40, 40, true},
		{159, 40, 40, true},
		{160, 40, 52, true},
		{220, 60, 52, true},
		{120, 11, 0, false}, // too short for a side column
		{120, 12, 40, false},
		{120, 17, 40, false}, // too short for the SESSION card
		{120, 18, 40, true},
	}
	for _, c := range cases {
		g := newGrid(c.w, c.h, true)
		if g.side.w != c.sideW || g.sessionCard != c.session {
			t.Errorf("%dx%d: side %d session %v, want %d %v", c.w, c.h, g.side.w, g.sessionCard, c.sideW, c.session)
		}
		if g.status.y != c.h-1 || g.status.w != c.w {
			t.Errorf("%dx%d: status bar at row %d width %d", c.w, c.h, g.status.y, g.status.w)
		}
		if g.main.h != c.h-1 || (g.side.w > 0 && g.side.h != c.h-1) {
			t.Errorf("%dx%d: columns must end above the status bar", c.w, c.h)
		}
		if g.side.w > 0 && g.main.w+1+g.side.w != c.w {
			t.Errorf("%dx%d: main %d + gap + side %d != width", c.w, c.h, g.main.w, g.side.w)
		}
	}
	if g := newGrid(200, 50, false); g.side.w != 0 || g.main.w != 200 {
		t.Errorf("a hidden side column must give the main column the width: %+v", g)
	}
}

// TestWallsUnbroken renders the chat layout at several sizes and checks every
// row is the window's width, both columns' walls run from their top corner to
// their bottom corner, and nothing but the status bar is on the last row.
func TestWallsUnbroken(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {140, 38}, {170, 45}, {100, 14}} {
		w, h := size[0], size[1]
		m := sized(w, h)
		rows := screen(m)
		if len(rows) != h {
			t.Fatalf("%dx%d: %d rows", w, h, len(rows))
		}
		for i, r := range rows {
			if n := ansi.StringWidth(r); n != w {
				t.Fatalf("%dx%d: row %d is %d wide: %q", w, h, i, n, r)
			}
		}
		g := m.grid()
		cols := []int{0, g.main.w - 1}
		if g.side.w > 0 {
			cols = append(cols, g.side.x, g.side.x+g.side.w-1)
		}
		for _, x := range cols {
			for y := 0; y < h-1; y++ {
				if c := cell(rows[y], x); !strings.Contains("╭╮╰╯│", c) {
					t.Fatalf("%dx%d: wall broken at column %d row %d: %q\n%s", w, h, x, y, c, strings.Join(rows, "\n"))
				}
			}
			if c := cell(rows[h-2], x); c != "╰" && c != "╯" {
				t.Errorf("%dx%d: column at %d does not end on the row above the status bar (%q)", w, h, x, c)
			}
		}
		if strings.ContainsAny(rows[h-1], "│╭╮╰╯") {
			t.Errorf("%dx%d: a box runs into the status bar: %q", w, h, rows[h-1])
		}
	}
}

// TestSideColumnHiddenWhenNarrow moves the figures to the status bar.
func TestSideColumnHiddenWhenNarrow(t *testing.T) {
	m := sized(90, 30)
	m.ctxWindow, m.ctxUsed = 200000, 50000
	rows := screen(m)
	for _, r := range rows {
		if strings.Contains(r, "SESSION") || strings.Contains(r, "Agents") {
			t.Fatalf("side column drawn at 90 columns: %q", r)
		}
	}
	if !strings.Contains(rows[len(rows)-1], "ctx 25%") {
		t.Errorf("status bar should carry the context figure: %q", rows[len(rows)-1])
	}
}

// TestSessionCardDroppedWhenShort keeps the detail card on a short window.
func TestSessionCardDroppedWhenShort(t *testing.T) {
	short := strings.Join(screen(sized(120, 16)), "\n")
	if strings.Contains(short, "SESSION") {
		t.Error("SESSION card drawn below 18 rows")
	}
	if !strings.Contains(short, "Agents") {
		t.Error("detail card missing on a short window")
	}
	tall := strings.Join(screen(sized(120, 30)), "\n")
	if !strings.Contains(tall, "╭─ SESSION ─") {
		t.Error("SESSION card missing at 30 rows")
	}
}

// TestChatBoxTitleAndViewport checks the title sits in the top edge and the
// transcript viewport is the chat box's padded inner area.
func TestChatBoxTitleAndViewport(t *testing.T) {
	m := sized(140, 38)
	rows := screen(m)
	if !strings.HasPrefix(rows[0], "╭─ ") || !strings.Contains(rows[0], "Auth middleware refactor") {
		t.Errorf("chat box title not in the top edge: %q", rows[0])
	}
	g := m.grid()
	if want := g.main.w - 2 - 2*padX; m.vp.Width != want {
		t.Errorf("viewport width %d, want %d", m.vp.Width, want)
	}
	s := m.splitMain(g.main)
	if want := s.chat - 2 - 2*padY; m.vp.Height != want {
		t.Errorf("viewport height %d, want %d", m.vp.Height, want)
	}
	if c := m.chrome.chatInner; c.x != 3 || c.y != 2 {
		t.Errorf("transcript origin %d,%d, want 3,2", c.x, c.y)
	}
}

// TestCtrlBTogglesSideColumn and the tab keys.
func TestSideColumnKeys(t *testing.T) {
	m := sized(140, 38)
	m.onKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.grid().side.w != 0 || m.vp.Width != 140-2-2*padX {
		t.Fatalf("Ctrl+B should hide the side column and widen the chat (vp %d)", m.vp.Width)
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.grid().side.w != 40 {
		t.Fatal("Ctrl+B should bring the side column back")
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyF2})
	if m.chrome.tab != tabTools {
		t.Fatalf("F2 should select Tools, got %d", m.chrome.tab)
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}, Alt: true})
	if m.chrome.tab != tabLog {
		t.Fatalf("Alt+4 should select Log, got %d", m.chrome.tab)
	}
	if m.ta.Value() != "" {
		t.Fatalf("tab keys must not type: %q", m.ta.Value())
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyF2})
	out := strings.Join(screen(m), "\n")
	// Tests draw without colour, so the selected tab carries its marker.
	if !strings.Contains(out, "·›Tools ·") {
		t.Errorf("the selected tab should be marked without colour:\n%s", out)
	}
	if !strings.Contains(out, "✓ terminal") {
		t.Errorf("Tools tab should list the session's calls:\n%s", out)
	}
}

// TestPaletteBoxOpensBetweenChatAndComposer.
func TestPaletteBox(t *testing.T) {
	m := sized(120, 36)
	before := m.vp.Height
	m.ta.SetValue("/th")
	m.updatePalette()
	m.refreshTranscript()
	if len(m.palette) == 0 {
		t.Skip("no /th commands")
	}
	if m.vp.Height >= before {
		t.Errorf("palette should shorten the chat box (%d -> %d)", before, m.vp.Height)
	}
	out := strings.Join(screen(m), "\n")
	if !strings.Contains(out, "╭─ COMMANDS ─") || !strings.Contains(out, "› /theme") {
		t.Errorf("palette box missing:\n%s", out)
	}
}

// TestHomeScreen shows the wordmark and the centred composer, no side column,
// and gives way to the grid once there is a block.
func TestHomeScreen(t *testing.T) {
	m := sized(100, 30)
	m.blocks, m.title, m.sessionID = nil, "", ""
	m.welcomeFrame = 100 // past the opening
	if !m.isHome() {
		t.Fatal("no session and no blocks should be home")
	}
	rows := screen(m)
	out := strings.Join(rows, "\n")
	if len(rows) != 30 {
		t.Fatalf("home is %d rows", len(rows))
	}
	for _, want := range []string{"▀", "Ask, or type / for commands", "READY", "General"} {
		if !strings.Contains(out, want) {
			t.Errorf("home screen missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SESSION") {
		t.Error("home screen must not draw the side column")
	}
	if !strings.Contains(rows[len(rows)-1], "/ commands") {
		t.Errorf("home status bar keeps its keys: %q", rows[len(rows)-1])
	}
	for i, r := range rows {
		if n := ansi.StringWidth(r); n != 100 {
			t.Fatalf("home row %d is %d wide", i, n)
		}
	}
	m.pushSystem("hello")
	if m.isHome() {
		t.Fatal("a block should bring the grid")
	}
}

// TestWordmarkIsOneRectangle guards the pixel art.
func TestWordmarkIsOneRectangle(t *testing.T) {
	for _, row := range wordmark {
		if len(row) != logoW {
			t.Fatalf("wordmark row %q is %d wide, want %d", row, len(row), logoW)
		}
	}
}

// TestQuestionBox opens between the chat and the composer, and ↑↓ Enter
// answer with the highlighted option.
func TestQuestionBox(t *testing.T) {
	m := sized(120, 36)
	m.ask = &pendingAsk{id: "q1", questions: []agent.AskQuestion{{Question: "Which page?", Options: []string{"Home", "About"}}}}
	m.refreshTranscript()
	out := strings.Join(screen(m), "\n")
	for _, want := range []string{"╭─ QUESTION ─", "? Which page?", "› 1  Home", "3  Other", "Answer the question above", "QUESTION "} {
		if !strings.Contains(out, want) {
			t.Errorf("question box missing %q:\n%s", want, out)
		}
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.chrome.askSel != 1 {
		t.Fatalf("↓ should move to the second option, got %d", m.chrome.askSel)
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask != nil {
		t.Fatal("Enter should answer the question")
	}
	if b := lastUserBlock(m); b != "About" {
		t.Errorf("answered %q, want About", b)
	}
}

func lastUserBlock(m *Model) string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockUser {
			return m.blocks[i].text
		}
	}
	return ""
}

// TestApprovalBox shows the tool, its target and the risk.
func TestApprovalBox(t *testing.T) {
	m := sized(120, 36)
	m.approvals = []pendingApproval{{id: "a1", tool: "terminal", summary: "rm -rf build", reason: "deletes files"}}
	m.refreshTranscript()
	out := strings.Join(screen(m), "\n")
	for _, want := range []string{"╭─ APPROVAL ─", "terminal", "rm -rf build", "deletes files", "y allow", "APPROVAL "} {
		if !strings.Contains(out, want) {
			t.Errorf("approval box missing %q:\n%s", want, out)
		}
	}
}

// TestPickerOverlay is centred, titled in its edge, and hit-tests its rows.
func TestPickerOverlay(t *testing.T) {
	m := sized(120, 36)
	m.openThemePicker()
	rows := screen(m)
	out := strings.Join(rows, "\n")
	if !strings.Contains(out, "─ SELECT A THEME ─") || !strings.Contains(out, "search") {
		t.Fatalf("picker overlay missing its title or search:\n%s", out)
	}
	pk := m.picker
	if row := rows[pk.rowY0+pk.cursor-pk.viewStart]; !strings.Contains(row, "›") {
		t.Errorf("hit-test row %d is not the selected row: %q", pk.rowY0, row)
	}
	for i, r := range rows {
		if n := ansi.StringWidth(r); n != 120 {
			t.Fatalf("row %d is %d wide with the overlay open", i, n)
		}
	}
}

// TestDumpFrames prints frames for eyeballing: TUI_DUMP=1 go test -run Dump -v.
func TestDumpFrames(t *testing.T) {
	if os.Getenv("TUI_DUMP") == "" {
		t.Skip("set TUI_DUMP=1")
	}
	for _, s := range [][2]int{{140, 38}, {100, 30}, {90, 24}} {
		t.Logf("\n%s", strings.Join(screen(sized(s[0], s[1])), "\n"))
	}
	h := sized(100, 30)
	h.blocks, h.title, h.sessionID = nil, "", ""
	h.welcomeFrame = 100
	t.Logf("\n%s", strings.Join(screen(h), "\n"))
}
