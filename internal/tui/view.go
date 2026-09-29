package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/version"
)

const minSidebar = 74 // hide the sidebar below this width

// layout (re)sizes every component from the current window size. Everything is
// clamped so a tiny or huge terminal never breaks the render.
func (m *Model) layout() {
	sidebarW := m.sidebarWidth()
	contentW := m.width - sidebarW
	if contentW < 24 {
		contentW = maxi(m.width, 24)
	}
	innerW := contentW - 2 // main column horizontal padding
	if innerW < 12 {
		innerW = 12
	}

	if m.vp.Width == 0 {
		m.vp = viewport.New(innerW, 1)
	} else {
		m.vp.Width = innerW
	}
	m.resizeViewport()

	// The input field is `❯ ` (2 cols) + the textarea, sitting inside a bordered,
	// horizontally-padded box (border 2 + padding 2 = 4). Size the textarea so the
	// field fits its box exactly — otherwise it wraps, the input grows past its
	// reserved height, and the whole view spills off screen.
	taW := innerW - 6
	if taW < 8 {
		taW = 8
	}
	m.ta.SetWidth(taW)

	m.buildRenderer(innerW - 4)
	m.cache = map[string]string{} // width changed — drop cached renders
}

// chromeHeight is the number of rows taken by everything around the transcript:
// the header (title + rule), the bordered input box, the status line, and the
// command palette when it is open. The transcript viewport gets whatever is left.
func (m *Model) chromeHeight() int {
	h := 2 + 3 + 1 // header + input box + status
	if len(m.palette) > 0 {
		lo, hi := paletteWindow(len(m.palette), m.paletteSel, m.paletteRows())
		h += hi - lo + 2 // palette rows + its border
	}
	return h
}

// paletteRows caps how many palette rows show at once. With the shared
// registry the palette holds about sixty commands, which would otherwise push
// the transcript off a normal terminal.
func (m *Model) paletteRows() int {
	rows := (m.height - 2 - 3 - 1 - 2) / 2
	if rows > 10 {
		rows = 10
	}
	if rows < 3 {
		rows = 3
	}
	return rows
}

// paletteWindow is the [lo, hi) slice of n palette rows to show so that sel
// stays visible, at most rows tall.
func paletteWindow(n, sel, rows int) (lo, hi int) {
	if n <= rows {
		return 0, n
	}
	lo = sel - rows/2
	if lo < 0 {
		lo = 0
	}
	if lo+rows > n {
		lo = n - rows
	}
	return lo, lo + rows
}

// resizeViewport gives the transcript exactly the rows left after the fixed
// chrome, so the composed view is always exactly the terminal height. This is
// what keeps the sidebar, header, and input sticky: only the viewport scrolls.
func (m *Model) resizeViewport() {
	vpH := m.height - m.chromeHeight()
	if vpH < 1 {
		vpH = 1
	}
	m.vp.Height = vpH
}

// buildRenderer (re)creates the Markdown renderer for the current theme + width.
func (m *Model) buildRenderer(wrapAt int) {
	if wrapAt < 8 {
		wrapAt = 8
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamourStyle(themeByName(m.themeName), lipgloss.HasDarkBackground())),
		glamour.WithWordWrap(wrapAt),
	)
	if err == nil {
		m.renderer = r
	}
}

func (m *Model) sidebarWidth() int {
	if m.width < minSidebar {
		return 0
	}
	return 28
}

func (m *Model) View() string {
	if !m.ready {
		return "starting antares…"
	}
	base := m.composeView()
	if m.input.active {
		return m.renderInputModal(base)
	}
	if m.picker.active {
		return m.renderPickerModal(base)
	}
	return clampHeight(base, m.height)
}

// composeView is the normal (no-modal) screen: sidebar + main column.
func (m *Model) composeView() string {
	main := m.mainColumn()
	if m.sidebarWidth() == 0 {
		return main
	}
	side := m.st.sidebar.Width(m.sidebarWidth() - 1).Height(m.height).Render(m.sidebar())
	return lipgloss.JoinHorizontal(lipgloss.Top, side, main)
}

// mainColumn stacks header, transcript, input, and status.
func (m *Model) mainColumn() string {
	pad := lipgloss.NewStyle().Padding(0, 1)
	w := m.vp.Width

	// Header bar: title on the left, a context pill on the right.
	title := m.title
	if title == "" {
		title = "New conversation"
	}
	left := m.st.header.Render(title)
	if m.sessionID != "" {
		left += " " + m.st.headerDim.Render(shortID(m.sessionID))
	}
	right := m.st.headerDim.Render(m.headerUsage())
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
		right = ""
	}
	headerLine := left + strings.Repeat(" ", gap) + right
	header := pad.Render(headerLine) + "\n" + pad.Render(m.st.rule.Render(strings.Repeat("─", maxi(w, 1))))

	body := pad.Render(m.vp.View())
	input := m.inputView()
	status := pad.Render(m.statusBar())

	if len(m.palette) > 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, body, pad.Render(m.paletteView()), input, status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, input, status)
}

func (m *Model) inputView() string {
	box := m.st.inputBox
	if !m.busy {
		box = m.st.inputFocus
	}
	field := lipgloss.JoinHorizontal(lipgloss.Top, m.st.prompt.Render("❯ "), m.ta.View())
	return lipgloss.NewStyle().Padding(0, 1).Render(box.Width(m.vp.Width).Render(field))
}

func (m *Model) sidebar() string {
	var b strings.Builder
	b.WriteString(m.st.logo.Render("◆ antares"))
	b.WriteString("  " + m.st.headerDim.Render(version.Version) + "\n\n")

	// live status dot
	if m.busy {
		b.WriteString(m.st.stRunning.Render("● ") + m.st.sideValue.Render("working") + "\n\n")
	} else {
		b.WriteString(m.st.stDone.Render("● ") + m.st.sideValue.Render("ready") + "\n\n")
	}

	section := func(label, value string) {
		b.WriteString(m.st.sideLabel.Render(strings.ToUpper(label)) + "\n")
		b.WriteString(m.st.sideValue.Render(value) + "\n\n")
	}
	model, provider := "—", ""
	if m.cfg != nil {
		model, provider = firstNon(m.cfg.Model.Default, "—"), m.cfg.Model.Provider
	}
	section("Model", truncate(model, 22))
	if provider != "" {
		section("Provider", provider)
	}
	sess := "new"
	if m.title != "" {
		sess = m.title
	}
	section("Session", truncate(sess, 22))
	section("Tokens", fmt.Sprintf("%d in / %d out", m.tokensIn, m.tokensOut))
	// Prefer the live event's window (arrives once a turn runs), fall back
	// to the metadata cascade for the active model so a fresh session still
	// shows what the model's budget will be.
	window := m.ctxWindow
	if window == 0 && m.cfg != nil {
		window = providerModelWindow(m.cfg, m.cfg.Model.Provider, m.cfg.Model.Default)
	}
	if window > 0 {
		pct := 0
		if m.ctxUsed > 0 {
			pct = m.ctxUsed * 100 / window
		}
		section("Context", fmt.Sprintf("%dK / %dK (%d%%)", m.ctxUsed/1000, window/1000, pct))
	}

	reason := m.st.stErr.Render("off")
	if m.showReasoning {
		reason = m.st.stDone.Render("on")
	}
	b.WriteString(m.st.sideLabel.Render("REASONING") + "\n" + reason + "\n\n")

	// Shortcuts (trimmed on short terminals).
	b.WriteString(m.st.sideLabel.Render("SHORTCUTS") + "\n")
	rows := [][2]string{
		{"Enter", "send"}, {"Ctrl+J", "newline"}, {"/", "commands"},
		{"Ctrl+R", "reasoning"}, {"Ctrl+T", "theme"}, {"Ctrl+L", "clear"},
		{"PgUp/Dn", "scroll"}, {"Ctrl+C", "quit"},
	}
	if m.height < 24 {
		rows = rows[:4]
	}
	for _, s := range rows {
		b.WriteString(m.st.statusKey.Render(pad(s[0], 8)) + m.st.sideLabel.Render(s[1]) + "\n")
	}
	return b.String()
}

func (m *Model) statusBar() string {
	sep := m.st.statusSep.Render("  ")
	var parts []string
	if m.busy {
		parts = append(parts, m.spin.View()+m.st.status.Render("working"))
	} else {
		parts = append(parts, m.st.stDone.Render("●")+m.st.status.Render(" ready"))
	}
	// The options the next turn goes with, when any differ from the default.
	if m.role != "" {
		parts = append(parts, m.st.accent.Render("role "+m.role))
	}
	if m.effort != "" {
		parts = append(parts, m.st.status.Render("effort "+m.effort))
	}
	if m.projectDir != "" {
		parts = append(parts, m.st.status.Render("project "+filepath.Base(m.projectDir)))
	}
	if n := len(m.attachments); n > 0 {
		parts = append(parts, m.st.accent.Render(fmt.Sprintf("%d attached", n)))
	}
	if n := len(m.approvals); n > 0 {
		parts = append(parts, m.st.accent.Render(fmt.Sprintf("approve? y/n (%d)", n)))
	} else if m.ask != nil {
		parts = append(parts, m.st.accent.Render("question waiting"))
	}
	if m.status != "" {
		parts = append(parts, m.st.statusSep.Render("·")+" "+m.st.status.Render(m.status))
	}
	if m.vp.TotalLineCount() > m.vp.Height && !m.vp.AtBottom() {
		parts = append(parts, m.st.accent.Render(fmt.Sprintf("↑ %d%%", int(m.vp.ScrollPercent()*100))))
	}
	line := strings.Join(parts, sep)
	hint := m.st.statusKey.Render("/") + m.st.status.Render("help")
	gap := m.vp.Width - lipgloss.Width(line) - lipgloss.Width(hint)
	if gap < 1 {
		return truncate(line, maxi(m.vp.Width, 1))
	}
	return line + strings.Repeat(" ", gap) + hint
}

func (m *Model) paletteView() string {
	var rows []string
	lo, hi := paletteWindow(len(m.palette), m.paletteSel, m.paletteRows())
	for i := lo; i < hi; i++ {
		c := m.palette[i]
		marker := "  "
		name := m.st.paletteName.Render("/" + c.Name)
		if i == m.paletteSel {
			marker = m.st.paletteSel.Render("❯ ")
			name = m.st.paletteSel.Render("/" + c.Name)
		}
		rows = append(rows, marker+name+"  "+m.st.paletteDesc.Render(c.Summary))
	}
	return m.st.paletteBox.Width(m.vp.Width).Render(strings.Join(rows, "\n"))
}

// refreshTranscript rebuilds the viewport content, keeping it pinned to the
// newest line unless the user scrolled up.
func (m *Model) refreshTranscript() {
	if !m.ready {
		return
	}
	atBottom := m.vp.AtBottom()
	m.resizeViewport()
	m.vp.SetContent(m.renderBlocks())
	if atBottom || m.busy {
		m.vp.GotoBottom()
	}
}

// ---- helpers ----------------------------------------------------------------

func wrap(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// clampHeight caps the composed view at the terminal height so a degenerate
// tiny terminal can never push content past the bottom and make the alt-screen
// scroll. In normal sizes the view is already exactly h rows, so this is a no-op.
func clampHeight(s string, h int) string {
	if h < 1 {
		h = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func clampLines(s string, maxLines, w int) string {
	s = wrap(s, w)
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		return strings.Join(lines, "\n") + "\n…"
	}
	return strings.Join(lines, "\n")
}

func pad(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func firstNon(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// headerUsage renders the token counters and — when the model's context
// window is known (from the live usage event or, before any turn, the
// metadata cascade) — a "used/window" ratio next to them.
func (m *Model) headerUsage() string {
	base := fmt.Sprintf("%d↑ %d↓", m.tokensIn, m.tokensOut)
	window := m.ctxWindow
	if window == 0 && m.cfg != nil {
		window = providerModelWindow(m.cfg, m.cfg.Model.Provider, m.cfg.Model.Default)
	}
	if window <= 0 {
		return base
	}
	pct := 0
	if m.ctxUsed > 0 {
		pct = m.ctxUsed * 100 / window
	}
	return fmt.Sprintf("%s · %dK/%dK (%d%%)", base, m.ctxUsed/1000, window/1000, pct)
}
