package tui

// chrome.go is the layout: one place works out where every box goes (newGrid),
// and the primitives every box is drawn with (drawBox, edgeLine, paint).
//
// The screen is a grid of rounded boxes on the canvas. The main column holds
// the chat box, the command palette while a /command is typed, a QUESTION or
// APPROVAL box while the turn waits on the person, and the composer. The side
// column on the right holds the SESSION card and the tabbed detail card. The
// status bar is the last row. Both columns end on the row above it.
//
// Inside every box the border is column 0, then two columns and one row of
// padding (padX, padY). Markers sit on column 3 and text starts on column 5
// in the transcript, the composer, the palette and every overlay.

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	// padX and padY are the columns and rows between a box's border and its
	// content, on each side.
	padX = 2
	padY = 1

	// The side column's width at each breakpoint. Stepped rather than
	// proportional, so the figures inside its cards sit on the same columns
	// from one terminal width to the next.
	sideRegular = 40
	sideWide    = 52
	// Narrower than this and the side column cannot sit beside a readable
	// chat; its figures move to the status bar.
	sideMinWindow = 100
	wideWindow    = 160
	// Shorter than this and the side column is dropped altogether.
	sideMinHeight = 12
	// Shorter than this and the SESSION card goes, so the detail card keeps
	// enough rows to be worth having; its figures join the status bar.
	sessionMinHeight = 18

	// composerLeft is the columns from the composer's left edge to its text:
	// the border, the padding, the ❯ and a space.
	composerLeft = 1 + padX + 2
	// composerMaxRows is the most rows of text the composer grows to.
	composerMaxRows = 8
)

// rect is a region of the screen, in cells.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// grid is where every region sits, worked out once from the window size.
type grid struct {
	main   rect // chat box, palette, question, composer, stacked
	side   rect // zero width when the side column is hidden
	status rect // the last row, full width
	// sessionCard reports whether the side column leads with the SESSION card.
	sessionCard bool
}

// newGrid is the only code that decides where a region goes, so no two boxes
// can disagree about an edge.
func newGrid(w, h int, showSide bool) grid {
	if w < 1 {
		w = 1
	}
	if h < 2 {
		h = 2
	}
	status := rect{0, h - 1, w, 1}
	body := rect{0, 0, w, h - 1}
	if !showSide || w < sideMinWindow || h < sideMinHeight {
		return grid{main: body, status: status}
	}
	sw := sideRegular
	if w >= wideWindow {
		sw = sideWide
	}
	// One blank column between the columns: the boxes' own edges already
	// separate them.
	return grid{
		main:        rect{0, 0, w - sw - 1, h - 1},
		side:        rect{w - sw, 0, sw, h - 1},
		status:      status,
		sessionCard: h >= sessionMinHeight,
	}
}

func (m *Model) grid() grid { return newGrid(m.width, m.height, !m.chrome.sideHidden) }

// padFor is the padding inside a box whose inner area (the box minus its
// border) is innerW × innerH. A box too small to afford it gives up the
// padding rather than its content.
func padFor(innerW, innerH int, vertical bool) (px, py int) {
	switch {
	case innerW >= 24:
		px = padX
	case innerW >= 6:
		px = 1
	}
	if vertical && innerH >= 2*padY+3 {
		py = padY
	}
	return px, py
}

// ---- painting ----------------------------------------------------------------

var bgSeqCache sync.Map // lipgloss.AdaptiveColor → escape sequence

// bgSeq is the escape sequence that sets c as the background, or "" when the
// terminal has no colour (NO_COLOR, a pipe, a test).
func bgSeq(c lipgloss.AdaptiveColor) string {
	if v, ok := bgSeqCache.Load(c); ok {
		return v.(string)
	}
	s := lipgloss.NewStyle().Background(c).Render("x")
	seq := ""
	if i := strings.IndexByte(s, 'x'); i > 0 {
		seq = s[:i]
	}
	bgSeqCache.Store(c, seq)
	return seq
}

// fit truncates or pads a one-line string to exactly w cells. Tabs become
// spaces first: they have no width of their own and would break the columns.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if strings.IndexByte(s, '\t') >= 0 {
		s = strings.ReplaceAll(s, "\t", "    ")
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		s += strings.Repeat(" ", w-sw)
	}
	return s
}

// paint fits s to w cells and lays bg under all of it. Styled spans inside
// end in a reset, which would clear the background too, so bg is set again
// after every reset.
func paint(s string, w int, bg lipgloss.AdaptiveColor) string {
	s = fit(s, w)
	seq := bgSeq(bg)
	if seq == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+seq)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+seq)
	s = strings.ReplaceAll(s, "\x1b[49m", seq)
	return seq + s + "\x1b[0m"
}

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// ---- boxes -------------------------------------------------------------------

// edgeLine is a box's top or bottom edge: the corners l and r, a label set
// into it after one run of the rule (`╭─ TITLE ─`), and a tail set in before
// the last run (`─ 1/2 ─╯`). Labels arrive styled; the spaces around them are
// added here.
func edgeLine(w int, l, r, label, tail string, border lipgloss.Style) string {
	if w < 2 {
		return border.Render(strings.Repeat("─", maxi(w, 0)))
	}
	inner := w - 2
	if w <= 7 {
		label, tail = "", ""
	}
	var lw, tw int
	if label != "" {
		label = " " + label + " "
		if room := w - 5; ansi.StringWidth(label) > room {
			label = ansi.Truncate(label, room, "")
		}
		lw = ansi.StringWidth(label)
	}
	if tail != "" {
		tail = " " + tail + " "
		tw = ansi.StringWidth(tail)
		if 1+lw+tw+1 > inner {
			tail, tw = "", 0
		}
	}
	if lw == 0 && tw == 0 {
		return border.Render(l + strings.Repeat("─", inner) + r)
	}
	var b strings.Builder
	b.WriteString(border.Render(l + "─"))
	b.WriteString(label)
	rest := inner - 1 - lw - tw
	if tw > 0 {
		rest--
	}
	b.WriteString(border.Render(strings.Repeat("─", maxi(rest, 0))))
	if tw > 0 {
		b.WriteString(tail)
		b.WriteString(border.Render("─"))
	}
	b.WriteString(border.Render(r))
	return b.String()
}

// box is one rounded box: every box on screen is drawn by drawBox, so they
// share one border, one fill and one corner.
type box struct {
	w, h   int
	border lipgloss.TerminalColor
	fill   lipgloss.AdaptiveColor
	// title is set into the top edge, hint into the bottom edge on the left,
	// pager into the bottom edge on the right. All arrive styled.
	title, hint, pager string
	// top replaces the whole top edge when set (the detail card's tabs).
	top string
	// body is the content, one line per row, placed inside the padding.
	body []string
	// vpad gives the content a row of padding above and below.
	vpad bool
	// raw places body rows straight inside the border, without the side
	// padding, for boxes that lay out their own columns (the composer).
	raw bool
}

// contentSize is the padded area inside a box of w × h.
func contentSize(w, h int, vpad bool) (cw, ch, px, py int) {
	px, py = padFor(w-2, h-2, vpad)
	return maxi(w-2-2*px, 0), maxi(h-2-2*py, 0), px, py
}

// drawBox renders b as exactly b.h lines of b.w cells.
func drawBox(b box) []string {
	if b.w < 2 || b.h < 2 {
		out := make([]string, maxi(b.h, 0))
		for i := range out {
			out[i] = paint("", b.w, b.fill)
		}
		return out
	}
	bs := fg(b.border)
	out := make([]string, 0, b.h)
	top := b.top
	if top == "" {
		top = edgeLine(b.w, "╭", "╮", b.title, "", bs)
	}
	out = append(out, paint(top, b.w, b.fill))

	cw, ch, px, py := contentSize(b.w, b.h, b.vpad)
	if b.raw {
		cw, ch, px, py = b.w-2, b.h-2, 0, 0
	}
	wall := bs.Render("│")
	blank := wall + strings.Repeat(" ", b.w-2) + wall
	side := strings.Repeat(" ", px)
	for i := 0; i < b.h-2; i++ {
		row := i - py
		if row < 0 || row >= ch {
			out = append(out, paint(blank, b.w, b.fill))
			continue
		}
		line := ""
		if row < len(b.body) {
			line = b.body[row]
		}
		out = append(out, paint(wall+side+fit(line, cw)+side+wall, b.w, b.fill))
	}
	out = append(out, paint(edgeLine(b.w, "╰", "╯", b.hint, b.pager, bs), b.w, b.fill))
	return out
}

// splitLine lays left and right on one line of w cells. The right side is
// dropped rather than cut when both do not fit; the left is truncated.
func splitLine(left, right string, w int) string {
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if right != "" && rw+lesser(lw, 20)+2 > w {
		right, rw = "", 0
	}
	room := w
	if rw > 0 {
		room = w - rw - 2
	}
	if lw > room {
		left = ansi.Truncate(left, maxi(room, 0), "…")
		lw = ansi.StringWidth(left)
	}
	return left + strings.Repeat(" ", maxi(w-lw-rw, 0)) + right
}

// splice lays the lines of an overlay over base at (x, y). base keeps its
// colours on both sides of the overlay.
func splice(base, over []string, x, y int) []string {
	out := append([]string(nil), base...)
	for i, ln := range over {
		row := y + i
		if row < 0 || row >= len(out) {
			continue
		}
		b := out[row]
		ow := ansi.StringWidth(ln)
		left := fit(ansi.Truncate(b, x, ""), x)
		right := ansi.TruncateLeft(b, x+ow, "")
		out[row] = left + "\x1b[0m" + ln + "\x1b[0m" + right
	}
	return out
}

// ---- the screen ----------------------------------------------------------------

// layout (re)sizes every component from the window size. The transcript
// viewport is the chat box's padded inner area, so what renders into
// m.vp.Width lands on the text column.
func (m *Model) layout() {
	g := m.grid()
	vw, _, _, _ := contentSize(g.main.w, 8, true)
	if vw < 8 {
		vw = 8
	}
	if m.vp.Width == 0 {
		m.vp = viewport.New(vw, 1)
	} else {
		m.vp.Width = vw
	}
	m.styleComposer()
	m.resizeViewport()
	m.cache = map[string]string{} // width changed — drop cached renders
}

// styleComposer strips the textarea's own chrome: the box it sits in draws
// the fill, and the default cursor line has a background of its own.
func (m *Model) styleComposer() {
	t := themeByName(m.themeName)
	m.ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.ta.FocusedStyle.Base = lipgloss.NewStyle()
	m.ta.FocusedStyle.Text = fg(t.Text)
	m.ta.FocusedStyle.Placeholder = fg(t.Faint)
	m.ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	m.ta.BlurredStyle = m.ta.FocusedStyle
	// The textarea keeps a pointer to its active style, and New copied the
	// textarea after focusing it, so the pointer still aims at the copy's
	// styles. Focusing again points it at these. The composer always has
	// focus, so this changes nothing else.
	if m.ta.Focused() {
		m.ta.Focus()
	}
}

// mainSplit is how the main column's rows are shared out.
type mainSplit struct {
	chat, palette, pause, composer int
	rows                           int // text rows in the composer
}

func (m *Model) splitMain(r rect) mainSplit {
	fieldW := maxi(r.w-composerLeft-1-padX, 1)
	rows := m.composerRows(fieldW)
	// One row of text by default, growing to eight, never past half the
	// column so the chat keeps its space.
	capH := clampi(r.h/2, 3, composerMaxRows+2)
	ih := lesser(rows+2, capH)
	var s mainSplit
	s.composer, s.rows = ih, ih-2
	if n := len(m.palette); n > 0 {
		wanted := lesser(n, 10) + 2
		if room := r.h - ih - 3; room > 2 {
			s.palette = lesser(wanted, room)
		}
	}
	if want := m.pauseHeight(r.w); want > 0 {
		if room := r.h - ih - s.palette - 3; room >= 5 {
			s.pause = lesser(want, room)
		}
	}
	s.chat = maxi(r.h-ih-s.palette-s.pause, 0)
	return s
}

// composerRows is how many rows the typed text takes at fieldW columns.
func (m *Model) composerRows(fieldW int) int {
	v := m.ta.Value()
	if v == "" {
		return 1
	}
	n := 0
	for _, ln := range strings.Split(v, "\n") {
		w := ansi.StringWidth(ln)
		n += maxi(1, (w+fieldW)/fieldW)
		if n >= composerMaxRows {
			return composerMaxRows
		}
	}
	return clampi(n, 1, composerMaxRows)
}

// resizeViewport gives the transcript exactly the chat box's padded inner
// rows, so the composed view is always exactly the terminal height. Only the
// viewport scrolls; every box around it stays put.
func (m *Model) resizeViewport() {
	g := m.grid()
	s := m.splitMain(g.main)
	m.ta.SetWidth(maxi(g.main.w-composerLeft-1-padX, 1))
	m.ta.SetHeight(maxi(s.rows, 1))
	_, ch, px, py := contentSize(g.main.w, s.chat, true)
	if ch < 1 {
		ch = 1
	}
	m.vp.Height = ch
	m.chrome.chatInner = rect{g.main.x + 1 + px, g.main.y + 1 + py, m.vp.Width, ch}
}

// isHome reports whether the home screen shows: no session and nothing in
// the transcript — at launch, and after /new.
func (m *Model) isHome() bool { return m.showWelcome() && m.sessionID == "" }

func (m *Model) View() string {
	if !m.ready {
		return "starting antares…"
	}
	var lines []string
	if m.isHome() {
		lines = m.homeLines()
	} else {
		lines = m.gridLines()
	}
	switch {
	case m.input.active:
		lines = m.renderInputModal(lines)
	case m.picker.active:
		lines = m.renderPickerModal(lines)
	}
	if len(lines) > m.height {
		lines = lines[:maxi(m.height, 1)]
	}
	return strings.Join(lines, "\n")
}

// gridLines is the chat layout: the main column, the side column beside it,
// and the status bar under both.
func (m *Model) gridLines() []string {
	t := themeByName(m.themeName)
	g := m.grid()
	m.syncComposer()
	mainL := m.mainColumnLines(g.main)
	var sideL []string
	m.chrome.sideRect = g.side
	if g.side.w > 0 {
		sideL = m.sideLines(g.side, g.sessionCard)
	}
	gap := paint("", 1, t.Canvas)
	rows := make([]string, 0, m.height)
	for y := 0; y < g.main.h; y++ {
		row := ""
		if y < len(mainL) {
			row = mainL[y]
		} else {
			row = paint("", g.main.w, t.Canvas)
		}
		if sideL != nil {
			row += gap
			if y < len(sideL) {
				row += sideL[y]
			} else {
				row += paint("", g.side.w, t.Canvas)
			}
		}
		rows = append(rows, row)
	}
	figures := !(g.side.w > 0 && g.sessionCard)
	rows = append(rows, m.statusLine(g.status.w, figures, false))
	return rows
}

// mainColumnLines stacks the chat box, the palette, the question or approval
// box and the composer on the same two edges.
func (m *Model) mainColumnLines(r rect) []string {
	s := m.splitMain(r)
	out := make([]string, 0, r.h)
	out = append(out, m.chatLines(r.w, s.chat)...)
	if s.palette > 0 {
		out = append(out, m.paletteLines(r.w, s.palette)...)
	}
	if s.pause > 0 {
		out = append(out, m.pauseLines(r.w, s.pause)...)
	}
	t := themeByName(m.themeName)
	out = append(out, m.composerLines(r.w, s.composer, t.Subtle)...)
	return out
}

// chatTitle is the project directory and the session, or "New conversation".
func (m *Model) chatTitle() string {
	t := themeByName(m.themeName)
	project := ""
	switch {
	case m.projectDir != "":
		project = filepath.Base(m.projectDir)
	case m.cfg != nil && m.cfg.Agent.Workspace != "":
		project = filepath.Base(m.cfg.Agent.Workspace)
	}
	title := m.title
	if title == "" {
		title = "New conversation"
	}
	if project == "" {
		return fg(t.Text).Bold(true).Render(title)
	}
	return fg(t.Text).Bold(true).Render(project) + fg(t.Faint).Render(" · ") + fg(t.Muted).Render(title)
}

func (m *Model) chatLines(w, h int) []string {
	if h <= 0 {
		return nil
	}
	t := themeByName(m.themeName)
	body := strings.Split(m.vp.View(), "\n")
	pager := ""
	if m.vp.TotalLineCount() > m.vp.Height && !m.vp.AtBottom() {
		pager = fg(t.Accent).Render(fmt.Sprintf("↑ %d%%", int(m.vp.ScrollPercent()*100)))
	}
	return drawBox(box{
		w: w, h: h, border: t.Border, fill: t.Panel,
		title: m.chatTitle(), pager: pager, body: body, vpad: true,
	})
}

// paletteLines is the inline command list, a box of its own between the chat
// and the composer, so it shortens the chat box rather than covering it.
func (m *Model) paletteLines(w, h int) []string {
	t := themeByName(m.themeName)
	rows := maxi(h-2, 1)
	lo, hi := paletteWindow(len(m.palette), m.paletteSel, rows)
	nameW := 0
	for i := lo; i < hi; i++ {
		nameW = maxi(nameW, ansi.StringWidth("/"+m.palette[i].Name))
	}
	body := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		c := m.palette[i]
		marker, name := " ", fg(t.Text).Render(padRight("/"+c.Name, nameW))
		if i == m.paletteSel {
			marker = fg(t.Accent).Bold(true).Render("›")
			name = fg(t.Accent).Bold(true).Render(padRight("/"+c.Name, nameW))
		}
		body = append(body, marker+" "+name+"  "+fg(t.Muted).Render(c.Summary))
	}
	pager := ""
	if len(m.palette) > rows {
		pager = fg(t.Muted).Render(fmt.Sprintf("%d/%d", m.paletteSel+1, len(m.palette)))
	}
	return drawBox(box{
		w: w, h: h, border: t.Border, fill: t.Panel,
		title: fg(t.Muted).Bold(true).Render("COMMANDS"), pager: pager, body: body,
	})
}

// syncComposer sets what the empty composer says it is for.
func (m *Model) syncComposer() {
	m.styleComposer() // the theme may have changed since the last frame
	switch {
	case len(m.approvals) > 0:
		m.ta.Placeholder = "Allow the tool above? y or n"
	case m.ask != nil && !m.ask.aside:
		m.ta.Placeholder = "Answer the question above"
	default:
		m.ta.Placeholder = "Ask, or type / for commands"
	}
}

// composerLines draws the composer: `❯` on column 3, the text from column 5.
// Its border takes the second accent while a command is typed and yellow
// while a turn runs, so the difference shows before Enter decides it.
func (m *Model) composerLines(w, h int, fill lipgloss.AdaptiveColor) []string {
	t := themeByName(m.themeName)
	command := strings.HasPrefix(m.ta.Value(), "/")
	border := t.Border
	switch {
	case command:
		border = t.Accent2
	case m.busy:
		border = t.Yellow
	}
	title := ""
	if n := len(m.attachments); n > 0 && w > 30 {
		title = fg(t.Accent2).Bold(true).Render(fmt.Sprintf("%d ATTACHED", n))
	}
	markColour := t.Accent
	if command {
		markColour = t.Accent2
	}
	fieldW := maxi(w-composerLeft-1-padX, 1)
	lines := strings.Split(m.ta.View(), "\n")
	body := make([]string, 0, h-2)
	for i := 0; i < h-2; i++ {
		mark := " "
		if i == 0 {
			mark = fg(markColour).Bold(true).Render("❯")
		}
		ln := ""
		if i < len(lines) {
			ln = lines[i]
		}
		body = append(body, strings.Repeat(" ", padX)+mark+" "+fit(ln, fieldW)+strings.Repeat(" ", padX))
	}
	return drawBox(box{w: w, h: h, border: border, fill: fill, title: title, body: body, raw: true})
}

// ---- status bar ------------------------------------------------------------------

// roleLabel is the agent answering: the session's role, or the general
// assistant.
func (m *Model) roleLabel() string {
	if m.role == "" {
		return "General"
	}
	if m.ag != nil && m.ag.Roles() != nil {
		if r, ok := m.ag.Roles().Get(m.role); ok && r.Title != "" {
			return r.Title
		}
	}
	return strings.ToUpper(m.role[:1]) + m.role[1:]
}

func (m *Model) modelLabel() string {
	if m.cfg == nil || strings.TrimSpace(m.cfg.Model.Default) == "" {
		return "no model"
	}
	return m.cfg.Model.Default
}

// stateBadge is the coloured state at the head of the status bar. Its word
// carries the state on its own, so NO_COLOR loses nothing.
func (m *Model) stateBadge() string {
	t := themeByName(m.themeName)
	label, colour := "READY", t.Green
	switch {
	case len(m.approvals) > 0:
		label, colour = "APPROVAL", t.Red
	case m.ask != nil && !m.ask.aside:
		label, colour = "QUESTION", t.Accent2
	case m.busy:
		label, colour = "WORKING", t.Yellow
	}
	return lipgloss.NewStyle().Foreground(t.Canvas).Background(colour).Bold(true).Render(" " + label + " ")
}

// statusLeft is the state, the agent and its model, and the last message: the
// status bar's left side, and the line under the home screen's composer.
func (m *Model) statusLeft() string {
	t := themeByName(m.themeName)
	s := m.stateBadge() + " " + fg(t.Accent).Bold(true).Render(m.roleLabel()) + " " + fg(t.Muted).Render("· "+m.modelLabel())
	if m.effort != "" {
		s += fg(t.Muted).Render(" · effort " + m.effort)
	}
	if m.busy {
		s += "  " + fg(t.Yellow).Render(strings.TrimSpace(m.spin.View()))
	}
	if m.status != "" {
		s += "  " + fg(t.Muted).Render(truncate(m.status, 48))
	}
	return s
}

// statusRight is the session's figures, when no SESSION card shows them, and
// the keys that apply right now.
func (m *Model) statusRight(figures bool) (figs, keys []string) {
	t := themeByName(m.themeName)
	if figures {
		if pct, ok := m.contextPercent(); ok {
			col := t.Muted
			if pct >= 85 {
				col = t.Red
			}
			figs = append(figs, fg(col).Render(fmt.Sprintf("ctx %d%%", pct)))
		}
		if cost, ok := m.sessionCost(); ok {
			figs = append(figs, fg(t.Muted).Render(formatCost(cost)))
		}
	}
	var pairs [][2]string
	switch {
	case len(m.approvals) > 0:
		pairs = [][2]string{{"y", "allow"}, {"n", "refuse"}}
	case m.pending != nil || m.confirm != nil:
		pairs = [][2]string{{"y", "confirm"}, {"Esc", "cancel"}}
	case m.ask != nil && !m.ask.aside:
		pairs = [][2]string{{"Enter", "answer"}, {"Esc", "later"}}
	case m.busy:
		pairs = [][2]string{{"Ctrl+O", "tool output"}, {"Ctrl+C", "stop"}}
	case len(m.palette) > 0:
		pairs = [][2]string{{"Enter", "run"}, {"Tab", "complete"}, {"Esc", "close"}}
	case m.isHome():
		pairs = [][2]string{{"/", "commands"}, {"Ctrl+P", "settings"}, {"Ctrl+J", "newline"}, {"Ctrl+C", "quit"}}
	default:
		pairs = [][2]string{{"/", "commands"}, {"Ctrl+P", "settings"}, {"Ctrl+O", "tool output"}, {"Ctrl+B", "side"}, {"Ctrl+C", "quit"}}
	}
	for _, k := range pairs {
		keys = append(keys, fg(t.Muted).Bold(true).Render(k[0])+fg(t.Faint).Render(" "+k[1]))
	}
	return figs, keys
}

// statusLine is the status bar: inset by a border and the padding, so its
// first and last characters sit on the columns of the markers above. keysOnly
// is the home screen's, which shows the rest under its composer. On a narrow
// window the keys between the first and the last go first, then the rest of
// the keys but one, then the cost; the context figure stays longest.
func (m *Model) statusLine(w int, figures, keysOnly bool) string {
	t := themeByName(m.themeName)
	inset := 1 + padX
	if w < 2*inset+4 {
		return paint("", w, t.Canvas)
	}
	room := w - 2*inset
	left := ""
	if !keysOnly {
		left = m.statusLeft()
	}
	figs, keys := m.statusRight(figures)
	lw := lesser(ansi.StringWidth(left), 20)
	right := func() string {
		gap := ""
		if len(figs) > 0 && len(keys) > 0 {
			gap = "   "
		}
		return strings.Join(figs, "  ") + gap + strings.Join(keys, "   ")
	}
	for ansi.StringWidth(right())+lw+2 > room {
		switch {
		case len(keys) > 2:
			keys = append(keys[:len(keys)-2], keys[len(keys)-1])
		case len(keys) > 1:
			keys = keys[:1]
		case len(figs) > 1:
			figs = figs[:1]
		case len(keys) > 0:
			keys = nil
		default:
			figs = nil
		}
		if len(figs) == 0 && len(keys) == 0 {
			break
		}
	}
	line := splitLine(left, right(), room)
	pad := strings.Repeat(" ", inset)
	return paint(pad+line+pad, w, t.Canvas)
}

// contextPercent is how full the context window is, when the window is known.
func (m *Model) contextPercent() (int, bool) {
	window := m.contextWindow()
	if window <= 0 {
		return 0, false
	}
	return lesser(m.ctxUsed*100/window, 999), true
}

// contextWindow prefers the live event's window (it arrives once a turn
// runs), then the metadata cascade for the active model, so a fresh session
// still shows what the model's budget will be.
func (m *Model) contextWindow() int {
	if m.ctxWindow > 0 {
		return m.ctxWindow
	}
	if m.cfg != nil {
		return providerModelWindow(m.cfg, m.cfg.Model.Provider, m.cfg.Model.Default)
	}
	return 0
}

// ---- small helpers -----------------------------------------------------------------

func lesser(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// thousands formats n with comma separators.
func thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
